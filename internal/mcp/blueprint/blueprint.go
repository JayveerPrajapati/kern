// Package blueprint owns the blueprint change-firewall MCP tool bodies
// (kern_validate_staged, kern_validate_proposed, kern_explain_finding,
// kern_repair_guidance) bridged into the kern catalog, as plain functions.
// The handler bodies are Server-independent; only the bpmcp tool handlers
// (internal/bpcli/mcp) and the rooted-path confinement helper are used.
package blueprint

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	bpmcp "github.com/JayveerPrajapati/kern/internal/bpcli/mcp"
)

// jsonPayloadArgs are parameters the kern catalog declares as JSON-encoded
// strings (matching the CLI flags and plugin schemas) while the blueprint
// handlers decode them as typed values (arrays/objects). Without decoding,
// a schema-compliant string payload fails the handler's json.Unmarshal and
// the tool is uncallable through the MCP surface.
var jsonPayloadArgs = map[string]string{
	"files":   "array of {path, content, op} objects",
	"finding": "object with rule_id, severity, category, file, line, message",
}

// runBlueprintHandler adapts a blueprint ToolHandler (raw-JSON args,
// ToolResult) to the kern handler shape (map args, text/error).
func runBlueprintHandler(h bpmcp.ToolHandler, args map[string]any) (string, error) {
	// kern callers say "root"; the blueprint handlers say "repo".
	if root, ok := args["root"]; ok {
		args["repo"] = root
		delete(args, "root")
	}
	// Decode JSON-string payloads (e.g. files / finding) into their typed
	// values so the blueprint handlers receive the structures they expect.
	for key, shape := range jsonPayloadArgs {
		s, ok := args[key].(string)
		if !ok || strings.TrimSpace(s) == "" {
			continue
		}
		var decoded any
		if err := json.Unmarshal([]byte(s), &decoded); err != nil {
			return "", fmt.Errorf("kern_blueprint: argument %q must be a JSON-encoded %s: %w", key, shape, err)
		}
		args[key] = decoded
	}
	// Re-apply root confinement to the decoded payloads. The confinement
	// gate (dispatch, server.go) ran on the raw string arguments, where
	// "files" and "finding" are not path-typed keys — isPathKey("files") is
	// false — so any path nested inside those JSON-encoded payloads escaped
	// root confinement. Walk the decoded path-bearing fields and confine
	// them with the same semantics rootedPath uses (absolute paths outside
	// the root are rejected; relative paths are joined to root/cwd). The
	// error matches the gate's wording but never discloses the allowed
	// roots. Non-path fields are passed through unchanged.
	repo, _ := args["repo"].(string)
	for key := range jsonPayloadArgs {
		if err := confineDecodedBlueprintPaths(repo, key, args[key]); err != nil {
			return "", fmt.Errorf("kern_blueprint: %w", err)
		}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("kern_blueprint: invalid arguments: %w", err)
	}
	res := h.Handle(context.Background(), raw)
	var texts []string
	for _, c := range res.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		}
	}
	out := strings.TrimSpace(strings.Join(texts, "\n"))
	if res.IsError {
		if out == "" {
			out = "blueprint gate error"
		}
		return out, fmt.Errorf("%s", out)
	}
	return out, nil
}

// confineDecodedBlueprintPaths re-applies root confinement to the decoded
// JSON-string payloads of the blueprint tools ("files" / "finding"). The
// gate only ever saw the raw string values, so paths nested inside the JSON
// were not confined; this walk confines every decoded path-bearing field
// with the same semantics rootedPath uses for the given repo root (or the
// process cwd when no root was supplied). Decoded shapes:
//
//	files:   []any of map[string]any — path-bearing field: "path"
//	finding: map[string]any          — path-bearing field: "file"
//
// A payload that is not one of the known shapes is passed through unchanged
// and left to the handler's own validation. The returned error names the
// offending key and value in the gate's style but never the allowed roots.
func confineDecodedBlueprintPaths(repo, key string, decoded any) error {
	confine := func(what, p string) error {
		if p == "" {
			return nil // empty paths are the handler's concern, not the gate's
		}
		if _, err := rootedPath(repo, p); err != nil {
			return fmt.Errorf("path outside allowed roots: %s=%q", what, p)
		}
		return nil
	}
	switch key {
	case "files":
		arr, ok := decoded.([]any)
		if !ok {
			return nil
		}
		for i, item := range arr {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if p, ok := m["path"].(string); ok {
				if err := confine(fmt.Sprintf("files[%d].path", i), p); err != nil {
					return err
				}
			}
		}
	case "finding":
		m, ok := decoded.(map[string]any)
		if !ok {
			return nil
		}
		if p, ok := m["file"].(string); ok {
			return confine("finding.file", p)
		}
	}
	return nil
}

// ValidateStaged runs the blueprint validate-staged change firewall.
func ValidateStaged(ctx context.Context, args map[string]any) (string, error) {
	return runBlueprintHandler(bpmcp.ValidateStagedHandler{}, args)
}

// ValidateProposed runs the blueprint validate-proposed change firewall.
func ValidateProposed(ctx context.Context, args map[string]any) (string, error) {
	return runBlueprintHandler(bpmcp.ValidateProposedHandler{}, args)
}

// ExplainFinding explains a blueprint finding.
func ExplainFinding(ctx context.Context, args map[string]any) (string, error) {
	return runBlueprintHandler(bpmcp.ExplainFindingHandler{}, args)
}

// RepairGuidance returns repair guidance for a blueprint finding.
func RepairGuidance(ctx context.Context, args map[string]any) (string, error) {
	return runBlueprintHandler(bpmcp.RepairGuidanceHandler{}, args)
}

// rootedPath resolves p for a file-reading tool. When root is given, the path
// must stay inside it (rejecting "..", absolute paths outside, and symlink
// escapes). A rootless call may only reference a path relative to the current
// working directory: an absolute path is rejected outright, since otherwise a
// caller could pass e.g. path=/etc/shadow and read any file on the system
// outside the confined workspace. Mirrors the mcp adapter's rootedPath.
func rootedPath(root, p string) (string, error) {
	if root == "" {
		if filepath.IsAbs(p) {
			return "", fmt.Errorf("absolute path requires root argument")
		}
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return withinRoot(cwd, p)
	}
	return withinRoot(root, p)
}

// withinRoot resolves file against root (absolute paths are used as-is) and
// requires the result to stay inside root, rejecting `..` escapes, absolute
// paths that point outside the project boundary, and symlink escapes (a
// symlink inside the project that points outside). It returns the resolved
// absolute path. Mirrors the mcp adapter's withinRoot.
func withinRoot(root, file string) (string, error) {
	var abs string
	if filepath.IsAbs(file) {
		abs = filepath.Clean(file)
	} else {
		abs = filepath.Join(root, file)
	}
	// Resolve symlinks on both the root and the candidate so a symlink inside
	// the project that points outside cannot read/escape the project boundary.
	// A candidate that does not exist yet (e.g. a file about to be written)
	// cannot be resolved directly, so resolve the NEAREST EXISTING ANCESTOR
	// and re-append the remaining components: a symlinked parent directory
	// (root/link -> /etc) is then judged by its real location instead of its
	// lexical text, closing the escape where the old pure-lexical fallback
	// let root/link/newfile land in /etc.
	rRoot, rerr := filepath.EvalSymlinks(root)
	if rerr != nil {
		rRoot = root
	}
	real := abs
	var rem []string
	probe := abs
	for {
		if r, err := filepath.EvalSymlinks(probe); err == nil {
			real = r
			if len(rem) > 0 {
				real = filepath.Join(append([]string{r}, rem...)...)
			}
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			// Nothing resolvable up to the filesystem root: fall back to the
			// lexical Clean+Rel check rather than denying an unresolvable path.
			real = abs
			break
		}
		rem = append([]string{filepath.Base(probe)}, rem...)
		probe = parent
	}
	rel, err := filepath.Rel(rRoot, real)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", file, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %s escapes project root %s", abs, root)
	}
	return abs, nil
}
