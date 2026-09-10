package cli

import (
	"reflect"
	"testing"
	"time"

	bdomain "github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// E2: internal/blueprint/cli had zero tests across 12 files. These pin the
// pure diff-parse and format helpers that the check/ci commands depend on.

func TestCheckOutputFormat(t *testing.T) {
	cases := []struct {
		name     string
		jsonOut  bool
		format   string
		wantJSON bool
		wantCode int
	}{
		{name: "default terminal", wantJSON: false, wantCode: 0},
		{name: "--json shorthand", jsonOut: true, wantJSON: true, wantCode: 0},
		{name: "format json", format: "json", wantJSON: true, wantCode: 0},
		{name: "format terminal", format: "terminal", wantJSON: false, wantCode: 0},
		{name: "json beats default", jsonOut: true, format: "", wantJSON: true, wantCode: 0},
		{name: "unknown format rejected", format: "xml", wantJSON: false, wantCode: 2},
		{name: "unknown format beats --json", jsonOut: true, format: "xml", wantJSON: false, wantCode: 2},
	}
	for _, tc := range cases {
		gotJSON, gotCode := checkOutputFormat(tc.jsonOut, tc.format)
		if gotJSON != tc.wantJSON || gotCode != tc.wantCode {
			t.Errorf("%s: checkOutputFormat(%v, %q) = (%v, %d), want (%v, %d)",
				tc.name, tc.jsonOut, tc.format, gotJSON, gotCode, tc.wantJSON, tc.wantCode)
		}
	}
}

func TestSplitDiffBlocks(t *testing.T) {
	diff := `diff --git a/a.go b/a.go
index 111..222 100644
--- a/a.go
+++ b/a.go
@@ -1 +1 @@
-old
+new
diff --git a/b/b.go b/b/b.go
index 333..444 100644
--- a/b/b.go
+++ b/b/b.go
@@ -5 +5 @@
-x
+y
`
	blocks := SplitDiffBlocks(diff)
	if len(blocks) != 2 {
		t.Fatalf("SplitDiffBlocks = %d blocks, want 2: %v", len(blocks), blocks)
	}
	if _, ok := blocks["a.go"]; !ok {
		t.Errorf("missing a.go block, got keys %v", keysOf(blocks))
	}
	if _, ok := blocks["b/b.go"]; !ok {
		t.Errorf("missing b/b.go block, got keys %v", keysOf(blocks))
	}
	if got := SplitDiffBlocks("no diff here"); len(got) != 0 {
		t.Errorf("non-diff input = %d blocks, want 0", len(got))
	}
}

func TestIsBinaryDiffBlock(t *testing.T) {
	if !IsBinaryDiffBlock("Binary files a/x.png and b/x.png differ") {
		t.Error("binary marker not detected")
	}
	if IsBinaryDiffBlock("@@ -1 +1 @@\n-a\n+b") {
		t.Error("text hunk misdetected as binary")
	}
}

func TestParseDiffLineNumbers(t *testing.T) {
	// Pure addition hunk (-0,0 semantics: `@@ -1,0 +1,3 @@`).
	added, removed := ParseDiffLineNumbers("@@ -1,0 +1,3 @@\n+a\n+b\n+c")
	if !reflect.DeepEqual(added, []string{"1", "2", "3"}) {
		t.Errorf("addition added = %v, want [1 2 3]", added)
	}
	if len(removed) != 0 {
		t.Errorf("addition removed = %v, want empty", removed)
	}
	// Pure deletion hunk (`@@ -1,3 +1,0 @@`).
	added, removed = ParseDiffLineNumbers("@@ -1,3 +1,0 @@\n-a\n-b\n-c")
	if len(added) != 0 {
		t.Errorf("deletion added = %v, want empty", added)
	}
	if !reflect.DeepEqual(removed, []string{"1", "2", "3"}) {
		t.Errorf("deletion removed = %v, want [1 2 3]", removed)
	}
	// Omitted counts mean 1; multi-hunk diffs accumulate.
	added, removed = ParseDiffLineNumbers("@@ -1 +1 @@\n-a\n+b\n@@ -5 +6 @@\n-c\n+d")
	if !reflect.DeepEqual(added, []string{"1", "6"}) {
		t.Errorf("multi-hunk added = %v, want [1 6]", added)
	}
	if !reflect.DeepEqual(removed, []string{"1", "5"}) {
		t.Errorf("multi-hunk removed = %v, want [1 5]", removed)
	}
	// No hunks at all.
	added, removed = ParseDiffLineNumbers("plain text")
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("no-hunk added/removed = %v/%v, want empty", added, removed)
	}
}

func TestTimeoutDuration(t *testing.T) {
	// Non-positive values fall back to the 2-minute default.
	if d := timeoutDuration(0); d != 120*time.Second {
		t.Errorf("timeoutDuration(0) = %v, want 2m default", d)
	}
	if d := timeoutDuration(-5); d != 120*time.Second {
		t.Errorf("timeoutDuration(-5) = %v, want 2m default", d)
	}
	if d := timeoutDuration(30); d != 30*time.Second {
		t.Errorf("timeoutDuration(30) = %v, want 30s", d)
	}
}

func TestBuildCheckRequestCarriesIdentity(t *testing.T) {
	req := buildCheckRequest("/repo", string(bdomain.SourceAgent), []bdomain.FileChange{{Path: "a.go"}}, "agent-7", "appr-1", "fix the bug", "task-1")
	if req.RepositoryRoot != "/repo" {
		t.Errorf("RepositoryRoot = %q, want /repo", req.RepositoryRoot)
	}
	if req.AgentID != "agent-7" {
		t.Errorf("AgentID = %q, want agent-7", req.AgentID)
	}
	if req.Metadata["task"] != "task-1" || req.Metadata["intent"] != "fix the bug" || req.Metadata["approval-id"] != "appr-1" {
		t.Errorf("Metadata = %v, want approval-id/intent/task carried", req.Metadata)
	}
	if len(req.Files) != 1 || req.Files[0].Path != "a.go" {
		t.Errorf("Files = %v, want [a.go]", req.Files)
	}
	// Agent-sourced changes with no explicit agent fall back to the stable
	// default identity (P0.4 authz).
	req2 := buildCheckRequest("/repo", string(bdomain.SourceAgent), nil, "", "", "", "")
	if req2.AgentID == "" {
		t.Error("agent-sourced request must carry a default AgentID")
	}
	// Non-agent sources stay anonymous.
	req3 := buildCheckRequest("/repo", string(bdomain.SourceHuman), nil, "", "", "", "")
	if req3.AgentID != "" {
		t.Errorf("non-agent source AgentID = %q, want empty", req3.AgentID)
	}
	if req3.Metadata != nil {
		t.Errorf("Metadata = %v, want nil when no approval/intent/task given", req3.Metadata)
	}
}

func keysOf(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
