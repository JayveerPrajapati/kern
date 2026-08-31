package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/rename"
	"os"
	"strings"
)

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

func (s *Server) handleLock(ctx context.Context, args map[string]any) (string, error) {
	{
		scope := argString(args, "scope")
		if scope == "" {
			return "", fmt.Errorf("scope is required")
		}
		if !validScope(scope) {
			return "", fmt.Errorf("invalid lock scope %q: must contain only [A-Za-z0-9._-] and not start with '.'", scope)
		}
		root := argString(args, "root")
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
		s.mu.Lock()
		if s.locks == nil {
			s.locks = map[string]*lock.Lock{}
		}
		if prev := s.locks[scope]; prev != nil {
			_ = prev.Release()
		}
		s.locks[scope] = lk
		s.mu.Unlock()
		return fmt.Sprintf("lock acquired: %s (pid %d)", scope, os.Getpid()), nil

	}
}

func (s *Server) handleUnlock(ctx context.Context, args map[string]any) (string, error) {
	{
		scope := argString(args, "scope")
		if scope == "" {
			return "", fmt.Errorf("scope is required")
		}
		if !validScope(scope) {
			return "", fmt.Errorf("invalid lock scope %q: must contain only [A-Za-z0-9._-] and not start with '.'", scope)
		}
		s.mu.Lock()
		lk := s.locks[scope]
		delete(s.locks, scope)
		s.mu.Unlock()
		if lk != nil {
			if err := lk.Release(); err != nil {
				return "", err
			}
			return "lock released: " + scope, nil
		}
		return "", fmt.Errorf("lock %q is not held by this server", scope)

	}
}

func (s *Server) handleLockStatus(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
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

func (s *Server) handleUsageGuide(ctx context.Context, args map[string]any) (string, error) {
	{
		return Guide(), nil

	}
}

func (s *Server) handleRename(ctx context.Context, args map[string]any) (string, error) {
	{
		root := resolveRoot(argString(args, "root"))
		ix, err := s.loadIndex(ctx, root)
		if err != nil {
			return "", err
		}
		oldName := argString(args, "symbol")
		newName := argString(args, "new_name")
		rep, err := rename.Rename(ix, oldName, newName)
		if err != nil {
			return "", err
		}
		if argString(args, "apply") == "true" || argString(args, "apply") == "1" {
			if _, err := rename.Apply(root, rep); err != nil {
				return "", fmt.Errorf("apply failed (files restored): %w", err)
			}
		}
		return rename.Render(rep), nil

	}
}

// handleAuthorizeContext implements the kern_authorize_context tool (P0.1):
// the authorized-context primitive. It computes the symbols and call edges an
// agent may legally read for a task, scoped by the agent's firewall identity
// and an optional task scope, and returns the permitted scope plus an
// auditable authorization proof. On denial it returns both the proof JSON and
// an error so the denial itself is auditable. The firewall is built per call
// (the MCP server holds no global firewall state).
func (s *Server) handleAuthorizeContext(ctx context.Context, args map[string]any) (string, error) {
	{
		agentID := argString(args, "agent_id")
		if agentID == "" {
			return "", fmt.Errorf("agent_id is required")
		}
		task := argString(args, "task")
		if task == "" {
			return "", fmt.Errorf("task is required")
		}
		root := resolveRoot(argString(args, "root"))
		ix, err := s.loadIndex(ctx, root)
		if err != nil {
			return "", err
		}

		// Per-call firewall: resolve the agent into it when registered; an
		// unregistered agent is denied at the authentication stage by the
		// primitive itself.
		fw := governance.NewFirewall()
		if agent, aerr := governance.GetAgent(agentID); aerr == nil {
			fw = fw.WithAgents(agent)
		}

		// Optional task scope object: {paths, denied_paths, services, envs,
		// artifacts}. Absent scope = permissive default.
		scope := taskScopeFromArgs(args, task)

		req := governance.Request{
			Task:         task,
			AgentID:      agentID,
			Scope:        scope,
			Root:         root,
			SymbolFilter: argString(args, "symbol_filter"),
		}
		resp, aerr := governance.AuthorizeContext(req, ix, fw)
		// Structured provenance on the envelope: the symbols are the allowed
		// scope — exactly what the response returns. toolCallResponse appends
		// the one-line index summary derived from this same field.
		policySource := policySourceDefaultScoped
		if scope != nil {
			policySource = policySourceTaskScope
		}
		syms := make([]SymbolProvenance, 0, len(resp.Scope.Symbols))
		for _, r := range resp.Scope.Symbols {
			syms = append(syms, SymbolProvenance{Name: r.Name, Qualified: r.Qualified, File: r.File, Line: r.Line})
		}
		s.stampProvenance(ctx, s.governedProvenance(ix, policySource, resp.Proof, syms))
		b, merr := json.MarshalIndent(resp, "", "  ")
		if merr != nil {
			return "", merr
		}
		out := string(b)
		if aerr != nil {
			// Denial: return both the auditable proof and the error.
			return out, fmt.Errorf("authorize-context denied: %w", aerr)
		}
		return out, nil
	}
}
