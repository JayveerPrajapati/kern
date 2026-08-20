package architecture

import (
	"strings"
	"testing"
)

func TestValidateProjectNoConfigPasses(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	rep, err := ValidateProject(root)
	if err != nil {
		t.Fatalf("ValidateProject: %v", err)
	}
	if !rep.OK || len(rep.Violations) != 0 {
		t.Fatalf("expected passing report, got %+v", rep)
	}
}

func TestValidateProjectFailsOnForbid(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", `version: "1"
rules:
  - id: forbid-web-db
    from: web
    to: db
    action: forbid
`)
	rep, err := ValidateProject(root)
	if err != nil {
		t.Fatalf("ValidateProject: %v", err)
	}
	if rep.OK {
		t.Fatal("expected non-OK report")
	}
	if rep.ErrorCount != 1 {
		t.Fatalf("expected 1 error, got %d", rep.ErrorCount)
	}
	if len(rep.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(rep.Violations))
	}
}

func TestValidateDiffScopedToChangedFiles(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", `version: "1"
rules:
  - id: forbid-web-db
    from: web
    to: db
    action: forbid
`)
	// Only db changed -> no violation (web untouched).
	rep, err := ValidateDiff(root, []string{"db/db.go"})
	if err != nil {
		t.Fatalf("ValidateDiff: %v", err)
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("expected no violations for db-only diff, got %+v", rep.Violations)
	}
	// web changed -> the forbidden web->db edge fires.
	rep2, err := ValidateDiff(root, []string{"web/web.go"})
	if err != nil {
		t.Fatalf("ValidateDiff: %v", err)
	}
	if len(rep2.Violations) != 1 {
		t.Fatalf("expected 1 violation for web diff, got %+v", rep2.Violations)
	}
}

func TestRenderReportWithCounts(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", `version: "1"
rules:
  - id: forbid-web-db
    from: web
    to: db
    action: forbid
    severity: warning
`)
	rep, err := ValidateProject(root)
	if err != nil {
		t.Fatalf("ValidateProject: %v", err)
	}
	text := Render(rep)
	if !strings.Contains(text, "0 errors, 1 warnings") {
		t.Fatalf("render missing summary, got:\n%s", text)
	}
	if !strings.Contains(text, "REJECT") {
		t.Fatalf("render missing base violation lines, got:\n%s", text)
	}
}

func TestRenderEmptyReport(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	rep, _ := ValidateProject(root)
	text := Render(rep)
	if !strings.Contains(text, "0 errors, 0 warnings") {
		t.Fatalf("render empty summary wrong:\n%s", text)
	}
}
