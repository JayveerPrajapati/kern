package guardhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRecordsFastPathReasons(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := `{"schema_version":1,"managed":true,"guard":{"enabled":true}}`
	if err := os.WriteFile(filepath.Join(root, ".kern", "config.json"), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	tiny := filepath.Join(root, "tiny.go")
	if err := os.WriteFile(tiny, []byte("package demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", root)
	t.Setenv("KERN_GUARD_ASSUME_KERN", "1")

	cases := []struct {
		name   string
		stdin  string
		reason string
	}{
		{"non-code", `{"tool_name":"read","tool_input":{"file_path":"README.md"}}`, "non-code"},
		{"tiny", fmt.Sprintf(`{"tool_name":"read","tool_input":{"file_path":%q}}`, tiny), "tiny"},
		{"window", fmt.Sprintf(`{"tool_name":"read","tool_input":{"file_path":%q,"start_line":1}}`, tiny), "window"},
		{"missing", fmt.Sprintf(`{"tool_name":"read","tool_input":{"file_path":%q}}`, filepath.Join(root, "missing.go")), "missing"},
		{"text-search", `{"tool_name":"grep","tool_input":{"pattern":"not-a-symbol"}}`, "text-search"},
		{"native-glob", `{"tool_name":"glob","tool_input":{"pattern":"*.go"}}`, "native-glob"},
	}
	for _, tc := range cases {
		var stdout, stderr bytes.Buffer
		if code := Run(strings.NewReader(tc.stdin), &stdout, &stderr); code != 0 {
			t.Fatalf("%s: Run exit = %d, stderr %q", tc.name, code, stderr.String())
		}
	}

	data, err := os.ReadFile(filepath.Join(root, ".kern", "adoption.log"))
	if err != nil {
		t.Fatalf("adoption log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != len(cases) {
		t.Fatalf("got %d events, want %d: %q", len(lines), len(cases), data)
	}
	for i, tc := range cases {
		var ev struct {
			Tool   string `json:"tool"`
			Routed bool   `json:"routed"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(lines[i]), &ev); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if ev.Routed || ev.Reason != tc.reason {
			t.Fatalf("event %d = %+v, want routed=false reason=%s", i, ev, tc.reason)
		}
	}
	if strings.Contains(string(data), root) || strings.Contains(string(data), "tiny.go") {
		t.Fatalf("fast-path telemetry leaked a path: %q", data)
	}
}

func TestRunFastPathTelemetryOptOut(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := `{"schema_version":1,"managed":true,"guard":{"enabled":true}}`
	if err := os.WriteFile(filepath.Join(root, ".kern", "config.json"), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", root)
	t.Setenv("KERN_GUARD_ASSUME_KERN", "1")
	t.Setenv("KERN_ADOPTION_LOG", "0")
	var stdout, stderr bytes.Buffer
	payload := `{"tool_name":"read","tool_input":{"file_path":"README.md"}}`
	if code := Run(strings.NewReader(payload), &stdout, &stderr); code != 0 {
		t.Fatalf("Run exit = %d, stderr %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".kern", "adoption.log")); !os.IsNotExist(err) {
		t.Fatalf("KERN_ADOPTION_LOG=0 still wrote telemetry: %v", err)
	}
}
