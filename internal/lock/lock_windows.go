//go:build windows

package lock

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The Windows implementation uses exclusive file creation as the advisory
// lock primitive: creating a scope.lock file with O_EXCL succeeds only when no
// other process holds that scope. The file is removed on release, so on
// Windows a lock file's existence means it is currently held.

// Acquire takes a non-blocking advisory lock on scope. When the lock is already
// held, it returns ErrLocked. The returned Lock is held until Release (or the
// process exits).
func Acquire(root, scope string) (*Lock, error) {
	if scope == "" {
		return nil, ErrScopeRequired
	}
	if err := os.MkdirAll(dir(root), 0o755); err != nil {
		return nil, err
	}
	p := pathFor(root, scope)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrLocked
		}
		return nil, err
	}
	h := holder{Scope: scope, PID: os.Getpid(), AcquiredAt: time.Now().UTC()}
	if data, err := json.Marshal(h); err == nil {
		_, _ = f.Write(data)
	}
	return &Lock{f: f, path: p, Scope: scope, Root: root}, nil
}

// Release releases a held lock. It is safe to call multiple times and after a
// process exit.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	if l.path != "" {
		_ = os.Remove(l.path)
	}
	return err
}

// Held reports whether scope is currently locked and, when it is, by which
// PID. Probing with O_EXCL is atomic; a probe file that was created is removed
// immediately.
func Held(root, scope string) (bool, int, error) {
	p := pathFor(root, scope)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false, 0, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		_ = f.Close()
		_ = os.Remove(p)
		return false, 0, nil
	}
	if os.IsExist(err) {
		return true, holderPID(p), nil
	}
	return false, 0, err
}

// List reports every lock scope currently held in the workspace and by whom.
// On Windows only held locks have files, so existence is the held signal.
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
		s := Status{Scope: strings.TrimSuffix(e.Name(), ".lock"), Path: p, Held: true}
		s.PID, s.AcquiredAt = readHolder(p)
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
			_ = f.Close()
			_ = os.Remove(p)
			s.Held = false
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scope < out[j].Scope })
	return out, nil
}
