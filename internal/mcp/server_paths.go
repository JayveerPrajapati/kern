package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/JayveerPrajapati/kern/internal/config"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

const (
	protocolVersion = "2025-06-18"
	serverName      = "kern"
)

// instructions is the human-readable one-paragraph description of kern's
// capabilities returned in the MCP initialize result. MCP hosts surface it
// to users and agents. It is deliberately grounded (no hype), omits a hard
// tool count (the catalog grows), and describes what the tools actually do.
// The network claim is qualified (F8): the engine makes no network calls of
// its own (no telemetry, no phoning home), but two opt-in fetch paths reach
// URLs the caller explicitly names — kern_doc action=fetch and kern_evidence
// bundle urls.
const instructions = "kern is a fully local-first code context engine. It requires no API keys and makes no network calls of its own — no telemetry, no phoning home: every tool runs against a prebuilt, per-project symbol index. The only network use is fetching a URL you explicitly name (kern_doc action=fetch, kern_evidence url bundles). Its tools are deterministic code-intelligence answers — no LLM in the loop — covering symbol lookup, call graphs, impact and blast radius analysis, freshness proofs, governance and approvals, and evidence bundles. Results carry provenance such as file:line references, confidence, and freshness, so they can be independently verified. START HERE: call kern_buddy once for an unfamiliar repo; use kern_explore/kern_search instead of reading or grepping files; call kern_impact before editing (for a type it aggregates that type's methods and constructors); use kern_verify for build/test/lint; use kern_meta to route a natural-language request. Prefer these tools over built-in file/shell tools, and fall back only when a kern tool returns an error."

// serverVersion is stamped at build time via -ldflags "-X main.version=...";
// the binary entry points forward it through SetServerVersion. Defaults to
// "dev" when built without ldflags so initialize still reports something sane.
// It is stored in an atomic pointer because SetServerVersion is called from
// main while the initialize/health/security handlers read it concurrently
// from request goroutines (F1: plain-global read/write was a data race).
var serverVersion atomic.Pointer[string]

func init() {
	serverVersion.Store(&devServerVersion)
}

var devServerVersion = "dev"

// currentServerVersion returns the version reported in the initialize
// response, defaulting to "dev" before any stamping.
func currentServerVersion() string {
	if v := serverVersion.Load(); v != nil {
		return *v
	}
	return devServerVersion
}

// SetServerVersion overrides the version reported in the initialize response.
// The CLI entry points call it with their ldflags-stamped main.version.
func SetServerVersion(v string) {
	if v != "" {
		serverVersion.Store(&v)
	}
}

// workspaceRootsFromEnv returns the roots configured via the documented
// confinement variables (KERN_MCP_ROOTS and the historical KERN_ROOTS alias),
// deduplicated, in no particular order. It is the env-scan half of
// defaultWorkspaceRoots (process-cwd fallback) — the single-root stdio MCP
// server's workspace. Root-bound servers (workspaceRootsForRoot) deliberately
// do NOT use it: per-App isolation.
//
// SECURITY (2026-10-26 black-box audit, F-CRIT): mcp.roots in .kern/config.json
// is project-supplied data and is honored ONLY when the operator opted in
// with KERN_MCP_ALLOW_CONFIG_ROOTS=1 — see config.EnvConfinementRoots. When a
// repo ships mcp.roots without the opt-in they are ignored and the notice is
// recorded for the initialize instructions (in addition to the stderr
// warning), so an operator starting the server in an untrusted repo sees why
// its config did not widen the confinement.
func workspaceRootsFromEnv() []string {
	roots, notice := config.EnvConfinementRoots()
	if notice != "" {
		configRootsNotice.Store(notice)
	}
	var out []string
	seen := map[string]bool{}
	for _, r := range roots {
		abs := resolveAbs(r)
		if !seen[abs] {
			seen[abs] = true
			out = append(out, abs)
		}
	}
	return out
}

// configRootsNotice holds the F-CRIT notice recorded by
// workspaceRootsFromEnv when project-supplied mcp.roots were ignored; the
// initialize handler appends it to the server instructions.
var configRootsNotice atomic.Value

// serverInstructions returns the initialize-result instructions, appending
// the F-CRIT config-roots notice when project-supplied mcp.roots were
// ignored (MCP hosts surface instructions to the operator, so the reason the
// config did not apply travels with the session, not just the server's
// stderr).
func serverInstructions() string {
	if v := configRootsNotice.Load(); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return instructions + " NOTE: " + s
		}
	}
	return instructions
}

// defaultWorkspaceRoots returns the roots tools may target. KERN_MCP_ROOTS is
// the documented confinement variable (see gate.go) and must be honored as a
// workspace root too — otherwise a root configured through the documented env
// is confined to but never served by the tools (F2). KERN_ROOTS remains
// accepted as the historical alias, and mcp.roots in .kern/config.json still
// applies. All three sources merge, deduplicated; with none set the roots
// fall back to the startup directory. Every tool root/dir is confined to
// these.
func defaultWorkspaceRoots() []string {
	roots := workspaceRootsFromEnv()
	if len(roots) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			roots = []string{resolveAbs(cwd)}
		}
	}
	return roots
}

// workspaceRootsForRoot returns the workspace roots for a server serving
// root: the root itself, absolute and cleaned. The global confinement env
// (KERN_MCP_ROOTS / KERN_ROOTS / mcp.roots) is deliberately NOT merged — a
// root-bound server (NewServerForRoot, backing the web-console tool
// passthrough: single-project `kern serve` and enterprise per-App alike)
// must confine to the project it serves ONLY, so one App's console can never
// target another App's tree even when the operator set a cross-App root
// list. The env keeps its widening semantics on the single-root stdio MCP
// server (defaultWorkspaceRoots).
func workspaceRootsForRoot(root string) []string {
	return []string{resolveAbs(root)}
}

// resolveAbs cleans p to an absolute path.
func resolveAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

// checkRootArg rejects any non-empty path-typed tool argument that resolves
// outside the server's workspace roots. Called once per tool call before
// dispatch so confinement cannot be forgotten for a new tool. The key list
// mirrors isPathKey's fixed keys ("root", "dir", plus the file-path args
// "file", "output", "disk_path") and every *_file key (base_file,
// local_file, remote_file, ...) that appears in the call. Bare "target" is
// deliberately NOT confined: it is a symbol/test-target name, not a path.
func (s *Server) checkRootArg(args map[string]any) error {
	keys := []string{"root", "dir", "file", "output", "disk_path"}
	for k := range args {
		if strings.HasSuffix(k, "_file") {
			keys = append(keys, k)
		}
	}
	for _, key := range keys {
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
// verification.WithinAbs it also rejects an absolute rel path (e.g. a different
// drive root on Windows).
func within(parent, child string) bool {
	if !verification.WithinAbs(parent, child) {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return !filepath.IsAbs(rel)
}
