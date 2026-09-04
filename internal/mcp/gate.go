package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// NewGateFromEnv builds a Gate from the KERN_MCP_ROOTS environment variable: a
// comma-separated list of directories. Entries are trimmed of surrounding
// spaces and empty entries are skipped. Roots are expected absolute; a
// relative entry is resolved against the process working directory
// (documented behavior). When the variable is unset or names no usable roots,
// the gate defaults to the process working directory — the gate is always
// enabled unless KERN_MCP_PERMISSIVE=1 opts out of confinement.
func NewGateFromEnv() *Gate {
	g := &Gate{}
	for _, r := range strings.Split(os.Getenv("KERN_MCP_ROOTS"), ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		// Resolve each root's real location once (mirroring blueprint's
		// per-call EvalSymlinks) so a root reached through a symlink — e.g.
		// /var -> /private/var on macOS — is compared on its real path. An
		// unresolvable root is kept as-is (it may be created after startup).
		g.roots = append(g.roots, symlinkOrSelf(abs))
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

// Check applies the gate to one tool call. A disabled gate allows everything.
// Path-typed arguments are walked recursively — nested maps and arrays of maps
// (e.g. files[].path) are confined the same way as top-level ones — and each
// string value is resolved to its real location (absolute + symlinks
// evaluated). A value whose real location is not inside at least one root is
// rejected with an error naming the key, the value and the allowed roots; the
// handler must not run for a rejected call.
func (g *Gate) Check(toolName string, args map[string]any) error {
	if !g.enabled {
		return nil
	}
	if err := g.confineMap(args); err != nil {
		if toolName != "" {
			return fmt.Errorf("tool %s: %w", toolName, err)
		}
		return err
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
			if err := g.confineSlice(v); err != nil {
				return err
			}
		}
	}
	return nil
}

// confineSlice confines every map element of a nested array and recurses into
// deeper arrays, mirroring blueprint's files[].path handling.
func (g *Gate) confineSlice(vals []any) error {
	for _, v := range vals {
		switch item := v.(type) {
		case map[string]any:
			if err := g.confineMap(item); err != nil {
				return err
			}
		case []any:
			if err := g.confineSlice(item); err != nil {
				return err
			}
		}
	}
	return nil
}

// isPathKey reports whether a tool-call argument key is path-typed: the
// explicit "root" and "dir" keys plus any key containing "path"
// (case-insensitive, so "targetPath" is caught too). This mirrors blueprint's
// key detection adapted to kern's argument names — kern tools use "root" where
// blueprint uses "repo".
func isPathKey(key string) bool {
	return key == "root" || key == "dir" || strings.Contains(strings.ToLower(key), "path")
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
		return fmt.Errorf("path %q for %q cannot be resolved", p, key)
	}
	for _, root := range g.roots {
		if RootContains(root, resolved) {
			return nil // inside an allowed root
		}
	}
	return fmt.Errorf("path outside allowed roots: %s=%q (allowed: %s)", key, p, strings.Join(g.roots, ", "))
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
