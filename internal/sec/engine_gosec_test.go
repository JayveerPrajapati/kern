package sec

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeGosec writes an executable shell script that prints canned JSON to
// stdout and exits with the given code, and returns its path. Tests inject
// it via KERN_GOSEC so the gosec engine is deterministic on machines without
// gosec installed.
func fakeGosec(t *testing.T, exit int, stdout string) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "out.json")
	if err := os.WriteFile(out, []byte(stdout), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "gosec")
	body := "#!/bin/sh\ncat '" + out + "'\nexit " + strconv.Itoa(exit) + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

// gosecJSON wraps a raw issue list in gosec's top-level document shape.
func gosecJSON(issues string) string {
	return `{"Issues":[` + issues + `]}`
}

// TestGosecAbsentBinarySkipped pins the SKIPPED path: a KERN_GOSEC that
// points nowhere reports skipped with the install hint, never a hard error.
func TestGosecAbsentBinarySkipped(t *testing.T) {
	t.Setenv("KERN_GOSEC", filepath.Join(t.TempDir(), "no-gosec-here"))
	res := RunGosec(t.TempDir())
	if res.Status != EngineStatusSkipped {
		t.Fatalf("Status = %q, want %q", res.Status, EngineStatusSkipped)
	}
	if !strings.Contains(res.Detail, "go install github.com/securego/gosec") {
		t.Errorf("Detail = %q, want the install hint", res.Detail)
	}
	if len(res.Findings) != 0 {
		t.Errorf("Findings = %d, want 0", len(res.Findings))
	}
}

// TestGosecFindingsMapped pins the severity mapping (HIGH/MEDIUM → warning,
// LOW → info), the "gosec:" rule prefix, and root-relative file conversion.
func TestGosecFindingsMapped(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "cmd", "main.go")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(
		`{"severity":"HIGH","rule_id":"G104","details":"audit","file":%q,"line":"42"},`+
			`{"severity":"MEDIUM","rule_id":"G110","details":"decompress bomb","file":%q,"line":"7"},`+
			`{"severity":"LOW","rule_id":"G101","details":"hardcoded creds","file":%q,"line":"1"}`,
		src, src, src)
	t.Setenv("KERN_GOSEC", fakeGosec(t, 0, gosecJSON(body)))

	res := RunGosec(root)
	if res.Status != EngineStatusRan {
		t.Fatalf("Status = %q, want %q", res.Status, EngineStatusRan)
	}
	if len(res.Findings) != 3 {
		t.Fatalf("Findings = %d, want 3", len(res.Findings))
	}
	want := []struct {
		rule, sev, file string
		line            int
	}{
		{"gosec:G104", "warning", "cmd/main.go", 42},
		{"gosec:G110", "warning", "cmd/main.go", 7},
		{"gosec:G101", "info", "cmd/main.go", 1},
	}
	for i, w := range want {
		f := res.Findings[i]
		if f.Rule != w.rule || f.Severity != w.sev || f.File != w.file || f.Line != w.line {
			t.Errorf("finding %d = %+v, want rule=%s sev=%s file=%s line=%d", i, f, w.rule, w.sev, w.file, w.line)
		}
	}
}

// TestGosecExitOneStillParses pins gosec's find-issues contract: exit 1 with
// valid stdout still yields parsed findings (only empty-stdout skips).
func TestGosecExitOneStillParses(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "main.go")
	if err := os.WriteFile(src, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"severity":"HIGH","rule_id":"G104","details":"audit","file":%q,"line":"3"}`, src)
	t.Setenv("KERN_GOSEC", fakeGosec(t, 1, gosecJSON(body)))

	res := RunGosec(root)
	if res.Status != EngineStatusRan {
		t.Fatalf("Status = %q, want ran (exit 1 means gosec found issues)", res.Status)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1", len(res.Findings))
	}
	if res.Findings[0].Rule != "gosec:G104" {
		t.Errorf("Rule = %q, want gosec:G104", res.Findings[0].Rule)
	}
}

// TestGosecInvalidJSONSkipped pins the unparseable-output path.
func TestGosecInvalidJSONSkipped(t *testing.T) {
	t.Setenv("KERN_GOSEC", fakeGosec(t, 0, "definitely not json"))
	res := RunGosec(t.TempDir())
	if res.Status != EngineStatusSkipped {
		t.Fatalf("Status = %q, want %q", res.Status, EngineStatusSkipped)
	}
	if !strings.Contains(res.Detail, "could not be parsed") {
		t.Errorf("Detail = %q, want the parse-error message", res.Detail)
	}
}

// TestGosecTimeoutSkipped pins the KERN_GOSEC_TIMEOUT contract: a run that
// exceeds the timeout reports skipped with the timeout detail.
func TestGosecTimeoutSkipped(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.json")
	if err := os.WriteFile(out, []byte(gosecJSON(`{"severity":"HIGH","rule_id":"G104","details":"audit","file":"/x","line":"1"}`)), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "gosec")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 2\ncat '"+out+"'\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KERN_GOSEC", script)
	t.Setenv("KERN_GOSEC_TIMEOUT", "1")

	res := RunGosec(t.TempDir())
	if res.Status != EngineStatusSkipped {
		t.Fatalf("Status = %q, want %q", res.Status, EngineStatusSkipped)
	}
	if !strings.Contains(res.Detail, "timed out after") {
		t.Errorf("Detail = %q, want the timeout message", res.Detail)
	}
}

// TestGosecPromoteMakesError pins KERN_SEC_PROMOTE: a listed rule ID becomes
// error severity while unlisted rules stay at their mapped severity.
func TestGosecPromoteMakesError(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "main.go")
	if err := os.WriteFile(src, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(
		`{"severity":"HIGH","rule_id":"G104","details":"audit","file":%q,"line":"3"},`+
			`{"severity":"MEDIUM","rule_id":"G110","details":"decompress bomb","file":%q,"line":"7"}`,
		src, src)
	t.Setenv("KERN_GOSEC", fakeGosec(t, 1, gosecJSON(body)))
	t.Setenv("KERN_SEC_PROMOTE", "G104")

	res := RunGosec(root)
	if res.Status != EngineStatusRan {
		t.Fatalf("Status = %q, want %q", res.Status, EngineStatusRan)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("Findings = %d, want 2", len(res.Findings))
	}
	if res.Findings[0].Severity != "error" {
		t.Errorf("G104 severity = %q, want error (promoted)", res.Findings[0].Severity)
	}
	if res.Findings[1].Severity != "warning" {
		t.Errorf("G110 severity = %q, want warning (unpromoted)", res.Findings[1].Severity)
	}
}
