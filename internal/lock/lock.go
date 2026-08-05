// Package lock provides advisory workspace locks for multi-agent
// coordination. Locks are OS-native advisory locks (flock on Unix, exclusive
// create on Windows — zero dependencies) and live in .kern/locks/ so every
// agent working on the same workspace shares them. A lock is held by the
// process that acquired it; it is released explicitly or automatically when
// that process exits, so a crashed agent never deadlocks the workspace.
package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrLocked is returned when a scope is already held by another process.
var ErrLocked = errors.New("lock is held by another process")

// ErrScopeRequired is returned when Acquire is called with an empty scope.
var ErrScopeRequired = errors.New("lock scope is required")

// Lock is a held advisory lock. Release it when the guarded region ends; the
// operating system releases it automatically on process exit.
type Lock struct {
	f     *os.File
	path  string
	Scope string
	Root  string
}

type holder struct {
	Scope      string    `json:"scope"`
	PID        int       `json:"pid"`
	AcquiredAt time.Time `json:"acquired_at"`
}

// Status describes one lock file.
type Status struct {
	Scope      string    `json:"scope"`
	Path       string    `json:"path"`
	PID        int       `json:"pid"`
	AcquiredAt time.Time `json:"acquired_at"`
	Held       bool      `json:"held"`
}

func dir(root string) string {
	return filepath.Join(root, ".kern", "locks")
}

func pathFor(root, scope string) string {
	return filepath.Join(dir(root), scope+".lock")
}

// Remove deletes the lock file for scope (a stale file whose holder has
// exited). It refuses to remove a lock that is still held.
func Remove(root, scope string) error {
	held, pid, err := Held(root, scope)
	if err != nil {
		return err
	}
	if held {
		return fmt.Errorf("lock %q is still held (pid %d); release it before removing", scope, pid)
	}
	return os.Remove(pathFor(root, scope))
}

// holderPID reads the recorded holder PID from a lock file path.
func holderPID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var h holder
	if json.Unmarshal(data, &h) != nil {
		return 0
	}
	return h.PID
}

// readHolder fills PID and AcquiredAt for a lock file path.
func readHolder(path string) (int, time.Time) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, time.Time{}
	}
	var h holder
	if json.Unmarshal(data, &h) != nil {
		return 0, time.Time{}
	}
	return h.PID, h.AcquiredAt
}
