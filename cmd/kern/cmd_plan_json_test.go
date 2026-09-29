package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planJSONFixture writes a tiny Go module with two packages: an "Add" symbol
// (so the net-new intent "Add a Greet function to internal/strutil/strutil.go"
// resolves to a real symbol, exactly like the repo's memory.Add) and the
// internal/strutil/ target package.
func planJSONFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                      "module planjsonfixture\n\ngo 1.20\n",
		"internal/add/add.go":         "package add\n\n// Add returns x.\nfunc Add(x int) int { return x }\n",
		"internal/strutil/strutil.go": "package strutil\n\n// Trim trims s.\nfunc Trim(s string) string { return s }\n",
	}
	for rel, content := range files {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir
}

// TestPlanJSONHonored: `kern plan --json` must emit the structured
// domain.Plan as valid JSON (the help text advertises --json, so the flag must
// be honored, not rejected), with target-scoped risk and grounded steps.
func TestPlanJSONHonored(t *testing.T) {
	if testing.Short() {
		t.Skip("plan --json: builds a fixture index; skipped with -short")
	}
	dir := planJSONFixture(t)
	change := "Add a Greet function to internal/strutil/strutil.go"
	out := captureStdout(t, func() {
		runAnalyze("plan", []string{"--json", "--root", dir, change})
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("plan --json output is not valid JSON: %v\n%s", err, out)
	}
	plan, ok := payload["plan"].(map[string]any)
	if !ok {
		t.Fatalf("plan --json missing \"plan\" object, got:\n%s", out)
	}
	if plan["objective"] != change {
		t.Errorf("plan.objective = %v, want %q", plan["objective"], change)
	}
	if plan["risk"] != "low" {
		t.Errorf("plan.risk = %v, want \"low\" (target-scoped: strutil not in packet scope)", plan["risk"])
	}
	steps, ok := plan["implementation_steps"].([]any)
	if !ok {
		t.Fatalf("plan.implementation_steps missing, got:\n%s", out)
	}
	joined := ""
	for _, s := range steps {
		joined += s.(string) + "\n"
	}
	for _, want := range []string{"internal/strutil/strutil.go", "internal/strutil/strutil_test.go"} {
		if !strings.Contains(joined, want) {
			t.Errorf("grounded plan step missing %q, got:\n%s", want, joined)
		}
	}
}
