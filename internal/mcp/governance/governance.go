// Package governance owns the governance-family MCP tool bodies
// (kern_lock action=acquire|release|status, kern_usage_guide,
// kern_rename) as plain functions.
package governance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
	"github.com/JayveerPrajapati/kern/internal/mcp/root"
	"github.com/JayveerPrajapati/kern/internal/rename"
)

// Hooks provides dependencies from the owning MCP server.
type Hooks struct {
	// Mu guards Locks; it is the server's write mutex (s.mu).
	Mu *sync.Mutex
	// Locks is the server's held-lock registry keyed by scope (s.locks).
	Locks map[string]*lock.Lock
	// LoadIndex builds (or loads) the project index for root (s.loadIndex).
	LoadIndex func(ctx context.Context, root string) (*index.Index, error)
	// Guide renders the tool usage guide (root Guide()).
	Guide func() string
	// StampGoverned records governed provenance for the call
	// (s.stampProvenance over s.governedProvenance).
	StampGoverned func(ctx context.Context, ix *index.Index, policySource string, proof governance.AuthorizationProof, symbols []provenance.SymbolProvenance)
}

// validScope reports whether scope is safe to embed directly into a filesystem
// path. It rejects empty values, path separators, and any character outside
// [A-Za-z0-9._-], and forbids a leading dot (which would allow "." / ".." /
// dotfile tricks). This prevents scope/namespace values from escaping their
// intended directory (e.g. `../../../../tmp/pwn` turning an O_CREATE lock write
// into an arbitrary file create/truncate/overwrite primitive).
func validScope(scope string) bool {
	if scope == "" {
		return false
	}
	if strings.HasPrefix(scope, ".") {
		return false
	}
	for _, r := range scope {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// LockTool is the consolidated kern_lock dispatcher: the action argument
// selects the acquire/release/status body.
func LockTool(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	action := mcpargs.ArgString(args, "action")
	if action == "" {
		return "", fmt.Errorf("kern_lock: 'action' is required")
	}
	switch action {
	case "acquire":
		return Lock(ctx, h, args)
	case "release":
		return Unlock(ctx, h, args)
	case "status":
		return LockStatus(ctx, h, args)
	default:
		return "", fmt.Errorf("kern_lock: unknown action %q (want acquire|release|status)", action)
	}
}

// Lock implements the kern_lock action=acquire body: acquires an advisory
// workspace-scoped lock, registering it on the server so release/cancel can
// release it.
func Lock(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	{
		scope := mcpargs.ArgString(args, "scope")
		if scope == "" {
			return "", fmt.Errorf("scope is required")
		}
		if !validScope(scope) {
			return "", fmt.Errorf("invalid lock scope %q: must contain only [A-Za-z0-9._-] and not start with '.'", scope)
		}
		root := mcpargs.ArgString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		lk, err := lock.Acquire(root, scope)
		if err != nil {
			// Only genuine contention reports "held (pid N)"; other Acquire
			// failures (bad scope, unwritable root) are returned as-is.
			if errors.Is(err, lock.ErrLocked) {
				_, pid, _ := lock.Held(root, scope)
				return "", fmt.Errorf("lock %q is held (pid %d)", scope, pid)
			}
			return "", err
		}
		h.Mu.Lock()
		if h.Locks == nil {
			h.Locks = map[string]*lock.Lock{}
		}
		if prev := h.Locks[scope]; prev != nil {
			_ = prev.Release()
		}
		h.Locks[scope] = lk
		h.Mu.Unlock()
		return fmt.Sprintf("lock acquired: %s (pid %d)", scope, os.Getpid()), nil

	}
}

// Unlock implements the kern_lock action=release body: releases a lock held
// by this server, reporting "not held" for scopes it never acquired.
func Unlock(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	{
		scope := mcpargs.ArgString(args, "scope")
		if scope == "" {
			return "", fmt.Errorf("scope is required")
		}
		if !validScope(scope) {
			return "", fmt.Errorf("invalid lock scope %q: must contain only [A-Za-z0-9._-] and not start with '.'", scope)
		}
		h.Mu.Lock()
		lk := h.Locks[scope]
		delete(h.Locks, scope)
		h.Mu.Unlock()
		if lk != nil {
			if err := lk.Release(); err != nil {
				return "", err
			}
			return "lock released: " + scope, nil
		}
		return "", fmt.Errorf("lock %q is not held by this server", scope)

	}
}

// LockStatus implements the kern_lock action=status body: lists every lock
// file in the workspace with its holder state.
func LockStatus(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	{
		root := mcpargs.ArgString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		sts, err := lock.List(root)
		if err != nil {
			return "", err
		}
		if len(sts) == 0 {
			return "no locks in workspace", nil
		}
		var b strings.Builder
		for _, s := range sts {
			state := "free"
			if s.Held {
				state = "HELD"
			}
			holder := ""
			if s.PID > 0 {
				holder = fmt.Sprintf(" (pid %d)", s.PID)
			}
			fmt.Fprintf(&b, "%s %s%s\n", s.Scope, state, holder)
		}
		return strings.TrimSuffix(b.String(), "\n"), nil

	}
}

// UsageGuide implements the kern_usage_guide tool: returns the categorized
// tool usage guide.
func UsageGuide(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	{
		return h.Guide(), nil

	}
}

// Rename implements the kern_rename tool: AST-scoped structural rename
// preview, with an optional apply that rewrites references (gated by the
// edit-risk verdict unless force is set).
func Rename(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	{
		root := root.ResolveRoot(mcpargs.ArgString(args, "root"))
		ix, err := h.LoadIndex(ctx, root)
		if err != nil {
			return "", err
		}
		oldName := mcpargs.ArgString(args, "symbol")
		newName := mcpargs.ArgString(args, "new_name")
		rep, err := rename.Rename(ix, oldName, newName)
		if err != nil {
			return "", err
		}
		if mcpargs.ArgString(args, "apply") == "true" || mcpargs.ArgString(args, "apply") == "1" {
			// P2 mutation gate: applying rewrites every reference; HIGH
			// verdict blocks unless force is set.
			if !mcpargs.ArgBool(args, "force") {
				if msg := intel.AssessEditRisk(ix, "", oldName).Refusal("rename apply of " + oldName); msg != "" {
					return "", fmt.Errorf("%s", msg)
				}
			}
			if _, err := rename.Apply(root, rep); err != nil {
				return "", fmt.Errorf("apply failed (files restored): %w", err)
			}
		}
		return rename.Render(rep), nil

	}
}
