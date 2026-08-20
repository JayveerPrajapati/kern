package architecture

import (
	"strings"
	"testing"
)

func TestValidatePRApprovedWhenClean(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	// No governance rules -> everything permitted.
	pv, err := ValidatePR(root, "feature/x", "main", []string{"web/web.go", "db/db.go"})
	if err != nil {
		t.Fatalf("ValidatePR: %v", err)
	}
	if !pv.Approved {
		t.Fatal("expected approved PR when there are no violations")
	}
	if len(pv.Violations) != 0 {
		t.Fatalf("expected no violations, got %+v", pv.Violations)
	}
	if pv.Branch != "feature/x" || pv.Base != "main" {
		t.Fatalf("branch/base wrong: %+v", pv)
	}
}

func TestValidatePRBlockedOnError(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", `version: "1"
rules:
  - id: forbid-web-db
    from: web
    to: db
    action: forbid
`)
	pv, err := ValidatePR(root, "feature/x", "main", []string{"web/web.go"})
	if err != nil {
		t.Fatalf("ValidatePR: %v", err)
	}
	if pv.Approved {
		t.Fatal("expected PR to be blocked by an error-severity violation")
	}
	if len(pv.Violations) == 0 {
		t.Fatal("expected at least one violation")
	}
	if pv.Summary == "" {
		t.Fatal("expected a non-empty summary")
	}
	if !strings.Contains(pv.Summary, "1 errors") {
		t.Fatalf("summary should mention the error count:\n%s", pv.Summary)
	}
}

func TestValidatePRAllowsWarningsOnly(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", `version: "1"
rules:
  - id: warn-web-db
    from: web
    to: db
    action: forbid
    severity: warning
`)
	pv, err := ValidatePR(root, "feature/x", "main", []string{"web/web.go"})
	if err != nil {
		t.Fatalf("ValidatePR: %v", err)
	}
	if !pv.Approved {
		t.Fatal("warning-only violations should not block approval")
	}
}

func TestAgentCheckReturnsReportForProposedFiles(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", `version: "1"
rules:
  - id: forbid-web-db
    from: web
    to: db
    action: forbid
`)
	rep, err := AgentCheck(root, []string{"web/web.go"})
	if err != nil {
		t.Fatalf("AgentCheck: %v", err)
	}
	if rep == nil {
		t.Fatal("expected a report")
	}
	if len(rep.Violations) != 1 {
		t.Fatalf("expected 1 violation for proposed web file, got %+v", rep.Violations)
	}
	if rep.ErrorCount != 1 {
		t.Fatalf("expected 1 error, got %d", rep.ErrorCount)
	}
}

func TestAgentCheckIgnoresNonProposedFiles(t *testing.T) {
	root := scaffoldModule(t, webToDBModule())
	writeConfig(t, root, "architecture.yaml", `version: "1"
rules:
  - id: forbid-web-db
    from: web
    to: db
    action: forbid
`)
	// Agent proposes only db -> no violation.
	rep, err := AgentCheck(root, []string{"db/db.go"})
	if err != nil {
		t.Fatalf("AgentCheck: %v", err)
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("expected no violations for db-only proposal, got %+v", rep.Violations)
	}
}
