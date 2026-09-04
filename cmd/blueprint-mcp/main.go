// Package main is the Blueprint MCP server entry point. It runs a minimal MCP
// server over stdio that exposes Blueprint's validation tools to agents.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/mcp"
	blueprintversion "github.com/JayveerPrajapati/kern/internal/blueprint/version"
)

func main() {
	// Honor -h/--help/help before anything else: without this the server
	// silently starts (and exits 0 with stdin closed) instead of showing usage.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help", "help":
			usage(os.Stdout)
			return
		}
		if strings.HasPrefix(os.Args[1], "-") {
			fmt.Fprintf(os.Stderr, "blueprint-mcp: unknown flag %q\n\n", os.Args[1])
			usage(os.Stderr)
			os.Exit(2)
		}
		// Any remaining first argument is positional — the server takes no
		// positional arguments, so reject it instead of silently starting.
		fmt.Fprintf(os.Stderr, "blueprint-mcp: unexpected argument '%s'; this command takes no positional arguments\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}

	server := mcp.NewServer("blueprint", blueprintversion.Version)

	// Register Blueprint's MCP tools (spec Phase 5).
	server.RegisterTool(mcp.ValidateStagedHandler{})
	server.RegisterTool(mcp.ValidateProposedHandler{})
	server.RegisterTool(mcp.ExplainFindingHandler{})
	server.RegisterTool(mcp.RepairGuidanceHandler{})

	// Pre-tool-use gate (spec Phase 5, "PreToolUse behavior"): confine every
	// tool call to the workspace roots so an agent cannot point Blueprint's
	// validation at arbitrary filesystem paths. Roots come from BLUEPRINT_ROOTS
	// (colon/comma separated) and default to the process working directory —
	// the same confinement model kern's MCP server uses (KERN_ROOTS).
	server.WithPreToolHook(confinementGate())

	if err := server.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "blueprint-mcp: %v\n", err)
		os.Exit(1)
	}
}

// confinementGate returns a pre-tool-use hook that denies any tool call whose
// path-typed arguments reference a location outside the allowed roots. It
// returns nil for every other call (no-op) so the gate is purely additive.
//
// Symlinks are resolved BEFORE containment: a link that lives inside a root
// but points outside is judged by its real location and denied.
func confinementGate() func(name string, args map[string]any) error {
	roots := workspaceRoots()
	return func(name string, args map[string]any) error {
		return confineArgs(roots, args)
	}
}

// confineArgs recursively walks tool-call arguments and applies the path gate
// to every string value whose key is path-typed (isPathArg): the top-level
// repo/dir/*path* keys AND the same keys nested inside maps and arrays of maps
// (e.g. files[].path in blueprint_validate_proposed), so nested path arguments
// are confined to the same workspace roots. Non-string values — including file
// content strings under non-path keys — are never treated as paths; only keys
// matching isPathArg are checked. The top-level behavior is identical to the
// original gate.
func confineArgs(roots []string, args map[string]any) error {
	return confineArgsNested(roots, args, false)
}

// confineArgsNested is confineArgs with an allowMissing flag threaded through
// the recursion. When allowMissing is true — inside a "files" subtree, the
// proposed-file list of blueprint_validate_proposed — path arguments may name
// files that are not on disk yet, so they are judged with gatePathAllowMissing
// instead of the strict gatePath. The flag propagates unchanged through maps
// and nested arrays; only descending into an array under the key "files"
// (case-insensitive) turns it on. Everywhere else the strict gate applies.
func confineArgsNested(roots []string, args map[string]any, allowMissing bool) error {
	for key, val := range args {
		switch v := val.(type) {
		case string:
			if v == "" || !isPathArg(key) {
				continue
			}
			if allowMissing {
				if err := gatePathAllowMissing(roots, v); err != nil {
					return err
				}
			} else {
				if err := gatePath(roots, v); err != nil {
					return err
				}
			}
		case map[string]any:
			if err := confineArgsNested(roots, v, allowMissing); err != nil {
				return err
			}
		case []any:
			subAllowMissing := allowMissing
			if strings.EqualFold(key, "files") {
				// A "files" array carries proposed (not yet on-disk) file
				// paths, so missing paths inside it must be allowed.
				subAllowMissing = true
			}
			if err := confineSliceNested(roots, v, subAllowMissing); err != nil {
				return err
			}
		}
	}
	return nil
}

// confineSlice applies confineArgs to every map element of a nested array and
// recurses into nested arrays, so files[].path and deeper structures are
// confined the same way as top-level path arguments.
func confineSlice(roots []string, vals []any) error {
	return confineSliceNested(roots, vals, false)
}

// confineSliceNested is confineSlice with the allowMissing flag threaded
// through (see confineArgsNested): it applies confineArgsNested to every map
// element of a nested array and recurses into nested arrays, carrying the flag
// so the files subtree keeps permitting not-yet-written paths.
func confineSliceNested(roots []string, vals []any, allowMissing bool) error {
	for _, v := range vals {
		switch item := v.(type) {
		case map[string]any:
			if err := confineArgsNested(roots, item, allowMissing); err != nil {
				return err
			}
		case []any:
			if err := confineSliceNested(roots, item, allowMissing); err != nil {
				return err
			}
		}
	}
	return nil
}

// isPathArg reports whether a tool-call argument is path-typed and therefore
// confined by the gate: the repository argument explicitly, plus any key that
// contains "path" (case-insensitive, so "targetPath" is caught), or equals
// "dir". The `file` key inside a finding object is NOT path-typed from the
// server's perspective — it is metadata describing where a violation occurred
// in the target repo, not a path the MCP server reads from disk. The
// repair-guidance and explain-finding handlers never open finding.file, so
// confining it would reject legitimate findings whose file paths are relative
// to the target repo (not the server's CWD). Future tools with real path
// arguments are covered by the "path" substring rule without modifying the gate.
func isPathArg(key string) bool {
	if key == "repo" || key == "dir" {
		return true
	}
	return strings.Contains(strings.ToLower(key), "path")
}

// gatePath confines a single path argument to the allowed workspace roots.
// The candidate is resolved to an absolute path and symlinks are evaluated
// first — containment is judged on the path's real location, so a symlink
// inside a root that points outside is denied rather than allowed. The
// allowed roots are resolved the same way, so a root reached through a
// symlink (e.g. /var -> /private/var on macOS) still matches.
func gatePath(allowed []string, p string) error {
	abs, err := filepath.Abs(p)
	if err != nil {
		return fmt.Errorf("invalid path %q", p)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return fmt.Errorf("path %q cannot be resolved: %v", p, err)
	}
	for _, root := range allowed {
		if withinRoot(root, resolved) {
			return nil // inside an allowed root
		}
	}
	return fmt.Errorf("path %q is outside the allowed workspace roots %v", resolved, allowed)
}

// gatePathAllowMissing confines a path argument like gatePath, except that a
// path that does not exist yet (a proposed new file) is still judged for
// containment instead of being denied outright with "cannot be resolved". The
// existing portion is symlink-resolved: on EvalSymlinks failure for the full
// path, the walk climbs the directory chain to the longest ancestor it can
// resolve, then joins the unresolved tail lexically and applies the same
// withinRoot check. This keeps the symlink-escape defense for the existing
// prefix — a link inside a root pointing outside still resolves outside and is
// denied — while allowing paths whose missing components cannot carry
// symlinks. Termination is guaranteed: EvalSymlinks on the filesystem root
// always succeeds.
func gatePathAllowMissing(allowed []string, p string) error {
	abs, err := filepath.Abs(p)
	if err != nil {
		return fmt.Errorf("invalid path %q", p)
	}

	// Existing path: behave exactly like gatePath, judging containment on the
	// fully symlink-resolved location.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		for _, root := range allowed {
			if withinRoot(root, resolved) {
				return nil // inside an allowed root
			}
		}
		return fmt.Errorf("path %q is outside the allowed workspace roots %v", resolved, allowed)
	}

	// Missing path (a proposed file): walk up to the longest existing
	// ancestor, remembering the unresolved tail, then judge the joined result.
	resolvedPrefix, tail := abs, []string{}
	for {
		r, err := filepath.EvalSymlinks(resolvedPrefix)
		if err == nil {
			resolvedPrefix = r
			break
		}
		parent := filepath.Dir(resolvedPrefix)
		if parent == resolvedPrefix {
			// Unreachable in practice: EvalSymlinks("/") always succeeds.
			return fmt.Errorf("path %q cannot be resolved: %v", p, err)
		}
		tail = append([]string{filepath.Base(resolvedPrefix)}, tail...)
		resolvedPrefix = parent
	}
	candidate := filepath.Join(append([]string{resolvedPrefix}, tail...)...)
	for _, root := range allowed {
		if withinRoot(root, candidate) {
			return nil // the existing prefix is inside an allowed root
		}
	}
	return fmt.Errorf("path %q is outside the allowed workspace roots %v", candidate, allowed)
}

// withinRoot reports whether the symlink-resolved path lies inside the given
// allowed root. The root itself is resolved first so both sides are compared
// on their real locations.
func withinRoot(root, resolved string) bool {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		r = root
	}
	rel, err := filepath.Rel(r, resolved)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// workspaceRoots returns the absolute workspace roots the MCP server may
// target: BLUEPRINT_ROOTS when set, else the process working directory.
func workspaceRoots() []string {
	var roots []string
	if env := os.Getenv("BLUEPRINT_ROOTS"); env != "" {
		for _, r := range strings.FieldsFunc(env, func(r rune) bool { return r == ':' || r == ',' }) {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			if abs, err := filepath.Abs(r); err == nil {
				roots = append(roots, abs)
			}
		}
	}
	if len(roots) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			roots = []string{cwd}
		}
	}
	return roots
}

// usage prints the blueprint-mcp help text to w.
func usage(w *os.File) {
	fmt.Fprintln(w, `blueprint-mcp — Blueprint MCP server (stdio)

Runs a minimal MCP server over stdin/stdout that exposes Blueprint's
validation tools (validate-staged, validate-proposed, explain-finding, repair-guidance) to agents.

Environment:
  BLUEPRINT_ROOTS   Colon/comma-separated workspace roots the server may
                    target (default: the process working directory). Tool
                    calls referencing paths outside these roots are denied.

Usage:
  blueprint-mcp                 Start the stdio MCP server (default)
  blueprint-mcp -h | --help     Show this help
  blueprint-mcp help            Show this help`)
}
