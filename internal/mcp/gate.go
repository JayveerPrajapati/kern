package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/config"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// Gate confines every tool call's path-typed arguments to a set of allowed
// workspace roots. It is kern's pre-tool-use confinement gate (the E-1
// backport of blueprint's BLUEPRINT_ROOTS gate): a tool call whose root, dir
// or *path* arguments resolve outside those roots is REJECTED before its
// handler runs. The gate is always enabled: when KERN_MCP_ROOTS is unset it
// fails closed to the process working directory (the server root), so
// zero-config deployments are confined to the workspace instead of trusted
// unconditionally. KERN_MCP_PERMISSIVE=1 is the explicit opt-out that
// restores the old allow-all behavior.
type Gate struct {
	roots   []string // allowed roots: absolute, cleaned, symlink-resolved
	enabled bool
}

// newGate builds a Gate from the KERN_MCP_ROOTS environment variable (or
// mcp.roots in .kern/config.json) merged with the caller-supplied roots —
// unless fromEnv is false, in which case ONLY the caller's roots confine
// (per-App servers must never be widened by the global env). Entries are
// trimmed of surrounding spaces and empty entries are skipped. Roots are
// expected absolute; a relative entry is resolved against the process
// working directory (documented behavior). When nothing is configured or no
// usable roots are named, the gate defaults to the process working
// directory — the gate is always enabled unless KERN_MCP_PERMISSIVE=1 opts
// out of confinement.
func newGate(extraRoots []string, fromEnv bool) *Gate {
	g := &Gate{}
	seen := map[string]bool{}
	add := func(r string) {
		r = strings.TrimSpace(r)
		if r == "" {
			return
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			return
		}
		abs = filepath.Clean(abs)
		// Resolve each root's real location once (mirroring blueprint's
		// per-call EvalSymlinks) so a root reached through a symlink — e.g.
		// /var -> /private/var on macOS — is compared on its real path. An
		// unresolvable root is kept as-is (it may be created after startup).
		abs = symlinkOrSelf(abs)
		if seen[abs] {
			return
		}
		seen[abs] = true
		g.roots = append(g.roots, abs)
	}
	if fromEnv {
		for _, r := range config.Strings("", "KERN_MCP_ROOTS", "mcp.roots", nil) {
			add(r)
		}
	}
	for _, r := range extraRoots {
		add(r)
	}
	// Fail-closed default: no configured roots means confine to the server's
	// working directory rather than disabling confinement.
	if len(g.roots) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			g.roots = append(g.roots, symlinkOrSelf(cwd))
		}
	}
	// The gate is enabled unless permissive mode explicitly opts out.
	g.enabled = !governance.PermissiveMode() && len(g.roots) > 0
	return g
}

// NewGateFromEnv builds a Gate from the KERN_MCP_ROOTS environment variable
// (or mcp.roots in .kern/config.json), defaulting to the process working
// directory when nothing is configured. The env semantics belong to the
// single-root stdio MCP server (kern-mcp / kern serve's in-process server
// without a project root).
func NewGateFromEnv() *Gate {
	return newGate(nil, true)
}

// NewGateForRoots builds a Gate confined to EXACTLY the given roots: the
// KERN_MCP_ROOTS / mcp.roots env is deliberately NOT merged (per-App
// isolation, finding: cross-App root targeting). Unlike NewGateFromEnv the
// fail-closed default is the caller's roots, never the process cwd — used by
// the root-aware web-console tool servers (NewServerForRoot) so a server
// serving project A confines every tool call to project A's tree even when
// the process runs from another directory, and a KERN_MCP_ROOTS value naming
// project B can never widen project A's console.
func NewGateForRoots(roots []string) *Gate {
	return newGate(roots, false)
}

// Check applies the gate to one tool call. A disabled gate allows everything.
// Path-typed arguments are walked recursively — nested maps and arrays of maps
// (e.g. files[].path) are confined the same way as top-level ones — and each
// string value is resolved to its real location (absolute + symlinks
// evaluated). A value whose real location is not inside at least one root is
// rejected with an error naming the denied key and generic guidance — the
// allowed roots are never disclosed to the client (audit A6); the handler
// must not run for a rejected call.
func (g *Gate) Check(toolName string, args map[string]any) error {
	if !g.enabled {
		return nil
	}
	if err := g.confineMap(args); err != nil {
		// Classify the denial as domain.ErrToolDenied so every consumer of
		// the governed path (REST passthrough, sdk catalog client) maps the
		// whole pre-execution-deny class with errors.Is (finding 6).
		if toolName != "" {
			return fmt.Errorf("tool %s: %w: %w", toolName, domain.ErrToolDenied, err)
		}
		return fmt.Errorf("%w: %w", domain.ErrToolDenied, err)
	}
	return nil
}

// confineMap walks one argument map: path-typed string values are confined and
// nested maps and slices are recursed into, so nested path arguments cannot
// bypass the gate.
func (g *Gate) confineMap(args map[string]any) error {
	for key, val := range args {
		switch v := val.(type) {
		case string:
			if v == "" || !isPathKey(key) {
				continue
			}
			if err := g.gatePath(key, v); err != nil {
				return err
			}
		case map[string]any:
			if err := g.confineMap(v); err != nil {
				return err
			}
		case []any:
			if err := g.confineSlice(key, v); err != nil {
				return err
			}
		case []string:
			// A Go-constructed plain string list under a path-typed key: each
			// element is a path candidate (e.g. a files list). JSON-decoded
			// arguments arrive as []any and are handled in confineSlice.
			if !isPathKey(key) {
				continue
			}
			for _, p := range v {
				if p == "" {
					continue
				}
				if err := g.gatePath(key, p); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// confineSlice confines every map element of a nested array and recurses into
// deeper arrays, mirroring blueprint's files[].path handling. Plain string
// elements (JSON arrays of paths under a path-typed key) and []string values
// are each treated as a path candidate, so a string list can never bypass the
// gate by arriving as an array.
func (g *Gate) confineSlice(key string, vals []any) error {
	pathTyped := isPathKey(key)
	for _, v := range vals {
		switch item := v.(type) {
		case map[string]any:
			if err := g.confineMap(item); err != nil {
				return err
			}
		case []any:
			if err := g.confineSlice(key, item); err != nil {
				return err
			}
		case string:
			if !pathTyped || item == "" {
				continue
			}
			if err := g.gatePath(key, item); err != nil {
				return err
			}
		case []string:
			if !pathTyped {
				continue
			}
			for _, p := range item {
				if p == "" {
					continue
				}
				if err := g.gatePath(key, p); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// isPathKey reports whether a tool-call argument key is path-typed: the
// explicit "root", "dir", "repo", "file", "output" and "disk_path" keys plus
// any key containing "path" (case-insensitive, so "targetPath" is caught too)
// and any key with the "_file" suffix (covers base_file/local_file/remote_file
// and future tools). "repo" is the argument blueprint tools use for the
// project root (audit R3): leaving it out let a client pass `repo` directly
// and bypass raw-arg confinement, after which the decoded-path confinement
// used an attacker-chosen root. "file" and the *_file keys are the file-path
// arguments of the write-capable tools (kern_semantic_merge, kern_ast_transform,
// kern_synthesize_test, kern_pre_edit, ...): leaving them out let a client pass
// a raw absolute path or a ".."-escape that the handlers used unvalidated.
// "files" is the path-list argument of the validate-proposed tools (and a
// future-proof key for any tool taking a list of file paths); "linked_repos"
// is the dead-schema path list of kern_cross_repo_impact (its handler ignores
// it, so confining it is harmless) — both make plain string lists under those
// keys subject to confinement. Bare "target" is deliberately NOT path-typed:
// it is a symbol/test-target name in some tools, not a filesystem path.
func isPathKey(key string) bool {
	if key == "root" || key == "dir" || key == "repo" || key == "file" || key == "files" || key == "linked_repos" || key == "output" || key == "disk_path" {
		return true
	}
	return strings.Contains(strings.ToLower(key), "path") || strings.HasSuffix(key, "_file")
}

// gatePath confines a single path value to the allowed roots. The value is
// resolved to an absolute path and symlinks are evaluated BEFORE containment,
// so a symlink that lives inside a root but points outside is judged by its
// real location and denied. Each root is resolved the same way before
// comparison (mirroring blueprint's withinRoot), so a root reached through a
// symlink — e.g. /var -> /private/var on macOS — still matches its real
// children. A value that cannot be resolved is also denied — an unresolvable
// path is not trustworthy. The first root that contains the real location
// admits the value.
func (g *Gate) gatePath(key, p string) error {
	abs, err := filepath.Abs(p)
	if err != nil {
		return fmt.Errorf("invalid path %q for %q", p, key)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		resolved = abs
	}
	for _, root := range g.roots {
		if RootContains(root, resolved) {
			return nil // inside an allowed root
		}
	}
	// Audit A6: the denial must NOT disclose the server's allowed roots —
	// they are server configuration a client must not learn from a denial.
	// Name only the denied key with generic guidance.
	return fmt.Errorf("path outside allowed roots for key %q", key)
}

// RootContains reports whether the symlink-resolved path resolved lies inside
// the given root. The root itself is resolved first (falling back to the raw
// root when it cannot be resolved yet) so both sides are compared on their
// real locations — a symlink inside a root that points outside is denied, and
// a root reached through a symlink (e.g. /var -> /private/var on macOS) still
// matches its real children. This is the single shared containment primitive
// used by both kern's MCP gate and Blueprint's MCP gate (DRY): Blueprint's
// validate-proposed flow additionally handles not-yet-on-disk paths, but every
// on-disk containment decision funnels through this one function.
func RootContains(root, resolved string) bool {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		r = root
	}
	rel, err := filepath.Rel(r, resolved)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// symlinkOrSelf resolves p's real location, falling back to p itself when the
// path does not exist yet (a root may be created after the server starts).
func symlinkOrSelf(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}
