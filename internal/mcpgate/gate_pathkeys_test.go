package mcpgate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
)

// nonPathArgs are catalog arguments whose schema text mentions a file or
// path-like word but which are NOT filesystem locations the gate should
// confine. Every entry needs a reason; a tool-scoped key ("tool.arg") is
// preferred over a bare arg name.
var nonPathArgs = map[string]string{
	"profile":                "a named compaction/analysis profile, not a file (the name merely contains \"file\")",
	"max_files":              "an integer cap on how many files to list",
	"kern_explain.target":    "a symbol or finding name to explain, not a filesystem path",
	"kern_policy_dsl.policy": "inline policy YAML/JSON text (the default .kern/policy.yaml is read under the already-confined root)",
	"kern_semantic.base":     "inline source text or a path; the handler confines any path via fsutil.ConfinePath before reading",
	"kern_semantic.local":    "inline source text or a path; the handler confines any path via fsutil.ConfinePath before reading",
	"kern_semantic.remote":   "inline source text or a path; the handler confines any path via fsutil.ConfinePath before reading",
}

func TestGateConfinesDiffFilesArgsPerTool(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"x.go", "y.go"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g := NewGateForRoots([]string{root})

	if err := g.Check("kern_diff_files", map[string]any{"a": "/etc/passwd", "b": "x.go"}); err == nil {
		t.Error("kern_diff_files a=/etc/passwd must be denied by the gate")
	}
	if err := g.Check("kern_diff_files", map[string]any{"a": "x.go", "b": "/etc/hosts"}); err == nil {
		t.Error("kern_diff_files b=/etc/hosts must be denied by the gate")
	}
	if err := g.Check("kern_diff_files", map[string]any{"a": root + "/x.go", "b": root + "/y.go"}); err != nil {
		t.Errorf("kern_diff_files with in-root files must pass: %v", err)
	}
	// The per-tool keys must not leak onto other tools, whose "a" is not a path.
	if err := g.Check("kern_search", map[string]any{"a": "/etc/passwd"}); err != nil {
		t.Errorf("tool-scoped keys must not apply to other tools: %v", err)
	}
}

// Every tool-scoped path argument in the catalog schema must be registered
// in toolPathArgs in gate.go.
func TestToolScopedPathArgsAreRegisteredWithTheGate(t *testing.T) {
	for _, tool := range catalog.All {
		if tool.Name != "kern_diff_files" {
			continue
		}
		walkSchemaProps(tool.InputSchema, func(name, _ string) {
			if name != "a" && name != "b" {
				return
			}
			found := false
			for _, k := range toolPathArgs[tool.Name] {
				if k == name {
					found = true
				}
			}
			if !found {
				t.Errorf("%s.%s is a path argument but is missing from toolPathArgs", tool.Name, name)
			}
		})
	}
}

// looksPathLike reports whether a schema property reads as a filesystem
// location: its name or description says so.
func looksPathLike(name, desc string) bool {
	n := strings.ToLower(name)
	for _, w := range []string{"path", "dir", "folder", "file", "location", "cwd", "workspace"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	d := strings.ToLower(desc)
	for _, w := range []string{"file path", "filepath", "path to", "directory", "absolute path", "relative path", "on disk", "file to "} {
		if strings.Contains(d, w) {
			return true
		}
	}
	return false
}

// walkSchemaProps calls fn for every named property in a JSON-schema fragment,
// recursing through nested objects and array items.
func walkSchemaProps(schema map[string]any, fn func(name, desc string)) {
	if props, ok := schema["properties"].(map[string]any); ok {
		for name, raw := range props {
			p, _ := raw.(map[string]any)
			desc, _ := p["description"].(string)
			fn(name, desc)
			if p != nil {
				walkSchemaProps(p, fn)
			}
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		walkSchemaProps(items, fn)
	}
}

// TestEveryPathLikeToolArgIsConfined is the drift guard for the gate's
// name-based confinement: a catalog argument that reads as a filesystem path
// must be matched by isPathKey (so Gate.Check confines it) or be listed in
// nonPathArgs with a reason. A new tool with a path under an unconventional
// key fails here instead of shipping unconfined.
func TestEveryPathLikeToolArgIsConfined(t *testing.T) {
	var offenders []string
	for _, tool := range catalog.All {
		walkSchemaProps(tool.InputSchema, func(name, desc string) {
			if isPathKey(name) || !looksPathLike(name, desc) {
				return
			}
			for _, k := range toolPathArgs[tool.Name] {
				if k == name {
					return
				}
			}
			if _, ok := nonPathArgs[tool.Name+"."+name]; ok {
				return
			}
			if _, ok := nonPathArgs[name]; ok {
				return
			}
			offenders = append(offenders, fmt.Sprintf("%s.%s", tool.Name, name))
		})
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("path-like tool arguments not matched by isPathKey (confine them in gate.go or list them in nonPathArgs with a reason):\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
