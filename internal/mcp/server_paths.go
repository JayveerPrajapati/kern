package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/config"
	"github.com/JayveerPrajapati/kern/internal/verify"
)

const (
	protocolVersion = "2025-06-18"
	serverName      = "kern"
)

// instructions is the human-readable one-paragraph description of kern's
// capabilities returned in the MCP initialize result. MCP hosts surface it
// to users and agents. It is deliberately grounded (no hype), omits a hard
// tool count (the catalog grows), and describes what the tools actually do.
const instructions = "kern is a fully local-first code context engine. It makes no network calls and requires no API keys: every tool runs against a prebuilt, per-project symbol index. Its tools are deterministic code-intelligence answers — no LLM in the loop — covering symbol lookup, call graphs, impact and blast radius analysis, freshness proofs, governance and approvals, and evidence bundles. Results carry provenance such as file:line references, confidence, and freshness, so they can be independently verified."

// serverVersion is stamped at build time via -ldflags "-X main.version=...";
// the binary entry points forward it through SetServerVersion. Defaults to
// "dev" when built without ldflags so initialize still reports something sane.
var serverVersion = "dev"

// SetServerVersion overrides the version reported in the initialize response.
// The CLI entry points call it with their ldflags-stamped main.version.
func SetServerVersion(v string) {
	if v != "" {
		serverVersion = v
	}
}

// defaultWorkspaceRoots returns the roots tools may target: KERN_ROOTS (or
// mcp.roots in .kern/config.json) when set, else the startup directory. Every
// tool root/dir is confined to these.
func defaultWorkspaceRoots() []string {
	var roots []string
	for _, r := range config.StringsSplit("", "KERN_ROOTS", "mcp.roots", nil, func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool { return r == ':' || r == ',' })
	}) {
		r = strings.TrimSpace(r)
		if r != "" {
			roots = append(roots, resolveAbs(r))
		}
	}
	if len(roots) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			roots = []string{resolveAbs(cwd)}
		}
	}
	return roots
}

// resolveAbs cleans p to an absolute path.
func resolveAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

// checkRootArg rejects any non-empty root/dir tool argument that resolves
// outside the server's workspace roots. Called once per tool call before
// dispatch so confinement cannot be forgotten for a new tool.
func (s *Server) checkRootArg(args map[string]any) error {
	for _, key := range []string{"root", "dir"} {
		v := argString(args, key)
		if v == "" {
			continue
		}
		if err := s.checkWithinWorkspace(v); err != nil {
			return fmt.Errorf("%s %q: %w", key, v, err)
		}
	}
	return nil
}

// checkWithinWorkspace reports whether p (absolute or relative) resolves
// inside one of the workspace roots, following symlinks. The resolved target
// must be the root itself or a descendant; a symlink pointing outside is
// rejected even though its text lives inside.
func (s *Server) checkWithinWorkspace(p string) error {
	real, err := realPath(p)
	if err != nil {
		return err
	}
	for _, r := range s.roots {
		rr, err := realPath(r)
		if err != nil {
			return err
		}
		if real == rr || within(rr, real) {
			return nil
		}
	}
	var roots []string
	for _, r := range s.roots {
		roots = append(roots, resolveAbs(r))
	}
	return fmt.Errorf("outside the allowed workspace (roots: %s)", strings.Join(roots, ", "))
}

// realPath resolves p to an absolute, symlink-resolved path. For paths that do
// not exist yet it resolves the nearest existing ancestor and re-appends the
// remaining components.
func realPath(p string) (string, error) {
	abs := resolveAbs(p)
	var rem []string
	probe := abs
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if len(rem) == 0 {
				return real, nil
			}
			return filepath.Join(append([]string{real}, rem...)...), nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return abs, fmt.Errorf("cannot resolve %q", p)
		}
		rem = append([]string{filepath.Base(probe)}, rem...)
		probe = parent
	}
}

// within reports whether child is parent or a descendant of parent. Unlike
// verify.WithinAbs it also rejects an absolute rel path (e.g. a different
// drive root on Windows).
func within(parent, child string) bool {
	if !verify.WithinAbs(parent, child) {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return !filepath.IsAbs(rel)
}
