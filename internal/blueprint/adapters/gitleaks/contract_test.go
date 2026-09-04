package gitleaks

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// Contract tests (mirroring the kern G14 gate): the gitleaks adapter must
// handle valid incumbent JSON output, empty output, and malformed output —
// failing closed (StatusError) on anything it cannot parse, never silently
// passing. They use a fake runner (contract_test.go's fakeGitleaksRunner) so
// no real gitleaks binary is required; the runner writes the report file at
// the --report-path the check hands it, exactly like the real binary.

// fakeGitleaksRunner is a commandRunner that answers `version` probes with
// "8.30.1" and writes reportTemplate to the --report-path the check passes.
// The {{WORKDIR}} placeholder is replaced with the runner's workdir (the
// scan dir) so canned findings can carry the real scan-dir-prefixed File
// path. Exit code follows the real binary: 0 when the report is empty ([]),
// 1 when it contains findings.
func fakeGitleaksRunner(reportTemplate string) commandRunner {
	return func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		if len(args) > 0 && args[0] == "version" {
			return "8.30.1", "", 0, nil
		}
		report := strings.ReplaceAll(reportTemplate, "{{WORKDIR}}", workdir)
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "--report-path" {
				if err := os.WriteFile(args[i+1], []byte(report), 0o644); err != nil {
					return "", err.Error(), -1, err
				}
			}
		}
		if strings.TrimSpace(report) == "[]" {
			return "", "", 0, nil
		}
		return "", "", 1, nil
	}
}

// gitleaksFindingJSON is a real 8.30.1 report element (captured schema). The
// File path is written relative to {{WORKDIR}} (the scan dir) so the check
// maps it back to the repo-relative path.
const gitleaksFindingJSON = `[
{
  "RuleID": "generic-api-key",
  "Description": "Detected a Generic API Key, potentially exposing access to various services and sensitive operations.",
  "StartLine": 3,
  "EndLine": 3,
  "StartColumn": 8,
  "EndColumn": 38,
  "Match": "AWSKey = \"AKIA1234567890ABCDEF\"",
  "Secret": "AKIA1234567890ABCDEF",
  "File": "{{WORKDIR}}/config.go",
  "SymlinkFile": "",
  "Commit": "",
  "Entropy": 4.0841837,
  "Author": "",
  "Email": "",
  "Date": "",
  "Message": "",
  "Tags": [],
  "Fingerprint": "{{WORKDIR}}/config.go:generic-api-key:3"
}
]`

func gitleaksCheck(t *testing.T, reportTemplate string) domain.CheckResult {
	t.Helper()
	chk := NewCheck(nil, WithBinary("gitleaks"), WithRunner(fakeGitleaksRunner(reportTemplate)))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files: []domain.FileChange{
			{Path: "config.go", Op: domain.OpWrite, Content: "const AWSKey = \"AKIA1234567890ABCDEF\"\n"},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	return cr
}

func TestContractGitleaksValidOutput(t *testing.T) {
	cr := gitleaksCheck(t, gitleaksFindingJSON)

	if cr.Name != "secret:gitleaks" {
		t.Errorf("Name = %q, want secret:gitleaks", cr.Name)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusBlock)
	}
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1", len(cr.Findings))
	}

	f := cr.Findings[0]
	if f.RuleID != "secret:gitleaks:generic-api-key" {
		t.Errorf("RuleID = %q, want secret:gitleaks:generic-api-key", f.RuleID)
	}
	if f.Severity != domain.SeverityBlock {
		t.Errorf("Severity = %q, want %q", f.Severity, domain.SeverityBlock)
	}
	if f.Category != domain.CategorySecret {
		t.Errorf("Category = %q, want %q", f.Category, domain.CategorySecret)
	}
	if f.File != "config.go" {
		t.Errorf("File = %q, want config.go", f.File)
	}
	if f.Line != 3 {
		t.Errorf("Line = %d, want 3", f.Line)
	}
	if !strings.Contains(f.Message, "generic-api-key") {
		t.Errorf("Message = %q, want mention of the gitleaks rule id", f.Message)
	}
	if !strings.Contains(f.Explanation, "Generic API Key") {
		t.Errorf("Explanation = %q, want gitleaks' rule description", f.Explanation)
	}
	if !f.Redacted {
		t.Error("Redacted = false, want true (secrets must be marked redacted)")
	}
	if f.RuleVersion != "8.30.1" {
		t.Errorf("RuleVersion = %q, want 8.30.1", f.RuleVersion)
	}
	if f.Confidence != 1.0 {
		t.Errorf("Confidence = %v, want 1.0 (gitleaks is deterministic)", f.Confidence)
	}
	if len(f.Evidence) != 1 {
		t.Fatalf("Evidence = %d, want 1", len(f.Evidence))
	}
	if f.Evidence[0].Kind != "gitleaks" {
		t.Errorf("Evidence.Kind = %q, want gitleaks", f.Evidence[0].Kind)
	}
	if f.Evidence[0].Location != "config.go:3" {
		t.Errorf("Evidence.Location = %q, want config.go:3", f.Evidence[0].Location)
	}
}

func TestContractGitleaksEmptyOutput(t *testing.T) {
	cr := gitleaksCheck(t, `[]`)

	if cr.Status != domain.StatusPass {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusPass)
	}
	if len(cr.Findings) != 0 {
		t.Errorf("Findings = %d, want 0", len(cr.Findings))
	}
}

func TestContractGitleaksMalformedOutput(t *testing.T) {
	cr := gitleaksCheck(t, `not json`)

	// Fail closed: a report we cannot parse is a tool failure, never a
	// silent pass (mirrors G14's contract).
	if cr.Status != domain.StatusError {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
	if !strings.Contains(cr.Error, "parse") {
		t.Errorf("Error = %q, want mention of parse failure", cr.Error)
	}
}

func TestContractGitleaksRedaction(t *testing.T) {
	cr := gitleaksCheck(t, gitleaksFindingJSON)

	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1", len(cr.Findings))
	}
	f := cr.Findings[0]

	// The canned report carried the raw secret and match; neither may
	// appear anywhere in the finding's user-visible fields.
	blob := f.Message + "\n" + f.Explanation + "\n" + f.SuggestedFix
	for _, ev := range f.Evidence {
		blob += "\n" + ev.Description + "\n" + ev.Location
	}
	for _, forbidden := range []string{"AKIA1234567890ABCDEF", "AWSKey = \"AKIA1234567890ABCDEF\"", "supersecret"} {
		if strings.Contains(blob, forbidden) {
			t.Errorf("finding leaked raw secret material %q:\n%s", forbidden, blob)
		}
	}
	if !f.Redacted {
		t.Error("Redacted = false, want true")
	}
}

// TestContractGitleaksExit2IsToolError: gitleaks uses exit code 2 for tool
// errors; that must surface as StatusError, not as an empty result.
func TestContractGitleaksExit2IsToolError(t *testing.T) {
	runner := func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		if len(args) > 0 && args[0] == "version" {
			return "8.30.1", "", 0, nil
		}
		return "", "gitleaks: failed to open source", 2, nil
	}
	chk := NewCheck(nil, WithBinary("gitleaks"), WithRunner(runner))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "config.go", Op: domain.OpWrite, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
	if !strings.Contains(cr.Error, "exit 2") {
		t.Errorf("Error = %q, want mention of exit 2", cr.Error)
	}
}

// TestContractGitleaksReportFileMissing: the binary claimed success but wrote
// no report file — fail closed rather than passing silently.
func TestContractGitleaksReportFileMissing(t *testing.T) {
	runner := func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		return "", "", 0, nil // never writes the report
	}
	chk := NewCheck(nil, WithBinary("gitleaks"), WithRunner(runner))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "config.go", Op: domain.OpWrite, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Fatalf("Status = %q, want %q (missing report must not pass silently)", cr.Status, domain.StatusError)
	}
}

// TestContractGitleaksFileMapping verifies the repo-relative path mapping:
// gitleaks reports the absolute scan-dir path; the finding must carry the
// repo-relative path, even for nested files.
func TestContractGitleaksFileMapping(t *testing.T) {
	nested := strings.ReplaceAll(gitleaksFindingJSON, "config.go", "cmd/app/config.go")
	chk := NewCheck(nil, WithBinary("gitleaks"), WithRunner(fakeGitleaksRunner(nested)))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files: []domain.FileChange{
			{Path: "cmd/app/config.go", Op: domain.OpWrite, Content: "const AWSKey = \"AKIA1234567890ABCDEF\"\n"},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusBlock)
	}
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1", len(cr.Findings))
	}
	if got := cr.Findings[0].File; got != "cmd/app/config.go" {
		t.Errorf("File = %q, want cmd/app/config.go", got)
	}
}

// TestContractGitleaksFiltersNonChangedFiles: a secret in a file that is not
// part of the change must not be reported (new-change principle).
func TestContractGitleaksFiltersNonChangedFiles(t *testing.T) {
	// Report carries config.go (changed) and other.go (not in the change).
	const both = `[
  {"RuleID": "generic-api-key", "Description": "Detected a Generic API Key", "StartLine": 3, "EndLine": 3, "StartColumn": 8, "EndColumn": 38, "Match": "m", "Secret": "s1", "File": "{{WORKDIR}}/config.go", "SymlinkFile": "", "Commit": "", "Entropy": 1, "Author": "", "Email": "", "Date": "", "Message": "", "Tags": [], "Fingerprint": "f1"},
  {"RuleID": "slack-bot-token", "Description": "Identified a Slack Bot token", "StartLine": 5, "EndLine": 5, "StartColumn": 1, "EndColumn": 10, "Match": "m", "Secret": "s2", "File": "{{WORKDIR}}/other.go", "SymlinkFile": "", "Commit": "", "Entropy": 1, "Author": "", "Email": "", "Date": "", "Message": "", "Tags": [], "Fingerprint": "f2"}
]`
	cr := gitleaksCheck(t, both)

	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1 (only the changed file's secret)", len(cr.Findings))
	}
	if cr.Findings[0].File != "config.go" {
		t.Errorf("File = %q, want config.go", cr.Findings[0].File)
	}
}

// TestContractGitleaksLaunchFailure: a runner launch failure (missing binary
// at the pinned path) is a tool failure, not a fallback or a pass.
func TestContractGitleaksLaunchFailure(t *testing.T) {
	runner := func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		return "", "", -1, os.ErrNotExist
	}
	chk := NewCheck(nil, WithBinary("/nonexistent/gitleaks"), WithRunner(runner))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "config.go", Op: domain.OpWrite, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
	if cr.Error == "" {
		t.Error("Error is empty")
	}
}
