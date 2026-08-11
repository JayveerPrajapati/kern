//go:build !windows

package lock

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
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
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrLocked
	}
	h := holder{Scope: scope, PID: os.Getpid(), AcquiredAt: time.Now().UTC()}
	if data, err := json.Marshal(h); err == nil {
		_ = f.Truncate(0)
		_, _ = f.WriteAt(data, 0)
	}
	return &Lock{f: f, path: p, Scope: scope, Root: root}, nil
}

// Release releases a held lock. It is safe to call multiple times and after a
// process exit.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	err := l.f.Close()
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
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return false, 0, err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true, holderPID(p), nil
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
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
		if f, err := os.OpenFile(p, os.O_RDWR, 0); err == nil {
			s.PID, s.AcquiredAt = readHolder(p)
			if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
				s.Held = true
			} else {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			}
			f.Close()
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
