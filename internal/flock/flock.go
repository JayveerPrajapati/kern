// Package flock provides the single cross-process advisory-lock primitive
// used across kern: a blocking (or non-blocking) exclusive flock(2) on a
// persistent lock file, with explicit LOCK_UN release semantics.
//
// All four lock sites in the codebase — governance's audit store
// (internal/governance/auditlock_unix.go), blueprint's audit writer
// (internal/blueprint/audit/flock_unix.go), the JSON-file cache stores
// (internal/cache/filelock_unix.go), and the workspace coordination locks
// (internal/lock/lock_unix.go) — delegate to this package so that lock
// semantics cannot drift between them. The lock file is a sidecar that
// persists after release so lock identity stays stable across processes, and
// the kernel drops the lock automatically if the holder crashes, so a dead
// process never wedges a lock.
//
// On Windows the standard library has no flock(2); the per-package Windows
// implementations (exclusive create / LockFileEx / no-op) are intentionally
// left in place. This package's Windows build is a fail-closed stub.
package flock

import "errors"

// ErrLocked is returned by TryLock when another process holds the lock.
var ErrLocked = errors.New("flock: lock is held by another process")
