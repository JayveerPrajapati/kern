package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	bpmcp "github.com/JayveerPrajapati/kern/internal/bpcli/mcp"
)

// Blueprint change-firewall tools bridged into the kern catalog.

// runBlueprintHandler adapts a blueprint ToolHandler (raw-JSON args,
// ToolResult) to the kern handler shape (map args, text/error).
// jsonPayloadArgs are parameters the kern catalog declares as JSON-encoded
// strings (matching the CLI flags and plugin schemas) while the blueprint
// handlers decode them as typed values (arrays/objects). Without decoding,
// a schema-compliant string payload fails the handler's json.Unmarshal and
// the tool is uncallable through the MCP surface.
var jsonPayloadArgs = map[string]string{
	"files":   "array of {path, content, op} objects",
	"finding": "object with rule_id, severity, category, file, line, message",
}

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
			return "", fmt.Errorf("kern_blueprint: argument %q must be a JSON-encoded %s: %v", key, shape, err)
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

func (s *Server) handleValidateStaged(ctx context.Context, args map[string]any) (string, error) {
	return runBlueprintHandler(bpmcp.ValidateStagedHandler{}, args)
}

func (s *Server) handleValidateProposed(ctx context.Context, args map[string]any) (string, error) {
	return runBlueprintHandler(bpmcp.ValidateProposedHandler{}, args)
}

func (s *Server) handleExplainFinding(ctx context.Context, args map[string]any) (string, error) {
	return runBlueprintHandler(bpmcp.ExplainFindingHandler{}, args)
}

func (s *Server) handleRepairGuidance(ctx context.Context, args map[string]any) (string, error) {
	return runBlueprintHandler(bpmcp.RepairGuidanceHandler{}, args)
}
