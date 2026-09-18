package main

import (
	"context"
	"encoding/json"
	"testing"

	bpmcp "github.com/JayveerPrajapati/kern/internal/bpcli/mcp"
)

// stubBlueprintHandler returns a canned ToolResult (used to exercise
// runBlueprintToolCLI without the real validation pipeline).
type stubBlueprintHandler struct {
	text  string
	isErr bool
}

func (s stubBlueprintHandler) Name() string        { return "stub" }
func (s stubBlueprintHandler) Description() string { return "stub" }
func (s stubBlueprintHandler) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (s stubBlueprintHandler) Handle(ctx context.Context, args json.RawMessage) bpmcp.ToolResult {
	return bpmcp.ToolResult{
		Content: []bpmcp.ToolContent{{Type: "text", Text: s.text}},
		IsError: s.isErr,
	}
}

// TestRunBlueprintToolCLI_VerdictExitMapping pins F6: the CLI exit code must
// mirror the validate-* JSON payload verdict, not just res.IsError. A BLOCK
// verdict arrives as a successful (isError=false) JSON payload and must exit
// 1 — previously it exited 0, silently failing the pipeline.
func TestRunBlueprintToolCLI_VerdictExitMapping(t *testing.T) {
	build := func(root, source, payload string) map[string]any {
		return map[string]any{"repo": root, "source": source, "payload": payload}
	}
	cases := []struct {
		name  string
		text  string
		isErr bool
		want  int
	}{
		{"BLOCK verdict exits 1", `{"status":"BLOCK","exit_code":1,"findings":[]}`, false, 1},
		{"ERROR verdict exits 1", `{"status":"ERROR","exit_code":1,"findings":[]}`, false, 1},
		{"non-zero exit_code exits 1 even on WARN", `{"status":"WARN","exit_code":1}`, false, 1},
		{"PASS exits 0", `{"status":"PASS","exit_code":0,"findings":[]}`, false, 0},
		{"WARN non-blocking exits 0", `{"status":"WARN","exit_code":0}`, false, 0},
		{"SKIP exits 0", `{"status":"SKIP","exit_code":0}`, false, 0},
		{"handler error exits 1", "boom", true, 1},
		{"prose output (explain-finding) stays 0 when no error", "some prose\nwith details", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := stubBlueprintHandler{text: tc.text, isErr: tc.isErr}
			if got := runBlueprintToolCLI([]string{"--files", `[{"path":"x.go","content":"x"}]`}, h, build, "usage"); got != tc.want {
				t.Fatalf("exit = %d, want %d (text=%q isErr=%v)", got, tc.want, tc.text, tc.isErr)
			}
		})
	}
}

// TestBlueprintVerdict unit-tests the payload mapping directly, including
// malformed input falling back to ("", 0).
func TestBlueprintVerdict(t *testing.T) {
	cases := []struct {
		out      string
		status   string
		exitCode int
	}{
		{`{"status":"BLOCK","exit_code":1}`, "BLOCK", 1},
		{`{"status":"pass","exit_code":0}`, "PASS", 0},
		{`{"exit_code":3}`, "", 3},
		{"not json at all", "", 0},
		{`{"status":42}`, "", 0},
	}
	for _, tc := range cases {
		status, code := blueprintVerdict(tc.out)
		if status != tc.status || code != tc.exitCode {
			t.Errorf("blueprintVerdict(%q) = (%q,%d), want (%q,%d)", tc.out, status, code, tc.status, tc.exitCode)
		}
	}
}
