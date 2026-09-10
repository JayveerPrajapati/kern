//go:build !windows

package lock

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/flock"
)

// Acquire takes a non-blocking advisory lock on scope. When the lock is already
// held, it returns ErrLocked. The returned Lock is held until Release (or the
// process exits). Uses flock: the lock file persists after release so status
// listings are stable, and the kernel drops the lock if the holder crashes.
func Acquire(root, scope string) (*Lock, error) {
	if scope == "" {
		return nil, ErrScopeRequired
	}
	if err := os.MkdirAll(dir(root), 0o755); err != nil {
		return nil, err
	}
	p := pathFor(root, scope)
	f, err := flock.TryLock(p)
	if err != nil {
		if hook := ContentionHook; hook != nil {
			hook(scope, holderPID(p))
		}
		return nil, ErrLocked
	}
	h := holder{Scope: scope, PID: os.Getpid(), AcquiredAt: time.Now().UTC()}
	if data, err := json.Marshal(h); err == nil {
		if err := f.Truncate(0); err != nil {
			_ = flock.Release(f) // releases the flock
			return nil, fmt.Errorf("lock %s: stamp truncate: %w", p, err)
		}
		if _, err := f.WriteAt(data, 0); err != nil {
			_ = flock.Release(f) // releases the flock
			return nil, fmt.Errorf("lock %s: stamp write: %w", p, err)
		}
	}
	return &Lock{f: f, path: p, Scope: scope, Root: root}, nil
}

// Release releases a held lock. It is safe to call multiple times and after a
// process exit.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := flock.Release(l.f)
	l.f = nil
	return err
}

// Held reports whether scope is currently locked and, when it is, by which
// PID. The lock file is created if absent so status listings are stable.
func Held(root, scope string) (bool, int, error) {
	p := pathFor(root, scope)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false, 0, err
	}
	f, err := flock.TryLock(p)
	if err != nil {
		return true, holderPID(p), nil
	}
	_ = flock.Release(f)
	return false, 0, nil
}

// List reports every lock scope in the workspace with whether it is held and
// by whom. Used by agents to see what their peers are working on.
func List(root string) ([]Status, error) {
	entries, err := os.ReadDir(dir(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Status
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".lock") {
			continue
		}
		p := filepath.Join(dir(root), e.Name())
		s := Status{Scope: strings.TrimSuffix(e.Name(), ".lock"), Path: p}
		s.PID, s.AcquiredAt = readHolder(p)
		if f, err := flock.TryLock(p); err == nil {
			_ = flock.Release(f)
		} else {
			s.Held = true
		}
		if !s.Held {
			// The stored PID belongs to the last holder, which may have died.
			// A free lock has no live holder, so don't report a stale one.
			s.PID = 0
			s.AcquiredAt = time.Time{}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scope < out[j].Scope })
	return out, nil
}
