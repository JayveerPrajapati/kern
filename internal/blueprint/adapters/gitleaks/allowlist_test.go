package gitleaks

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// gitleaksReportForFiles builds a canned gitleaks JSON report with one
// generic-api-key finding per file. Paths are written relative to
// {{WORKDIR}} (the scan dir) so the check maps them back to repo-relative
// paths, exactly like the captured 8.30.1 report in contract_test.go.
func gitleaksReportForFiles(files ...string) string {
	var b strings.Builder
	b.WriteString("[")
	for i, f := range files {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b,
			`{"RuleID":"generic-api-key","Description":"Detected a Generic API Key","StartLine":3,"EndLine":3,"StartColumn":8,"EndColumn":38,"Match":"m","Secret":"s","File":"{{WORKDIR}}/%s","SymlinkFile":"","Commit":"","Entropy":4.0,"Author":"","Email":"","Date":"","Message":"","Tags":[],"Fingerprint":"f%d"}`,
			f, i)
	}
	b.WriteString("]")
	return b.String()
}

// TestGitleaksAllowlistSuppressesTestFixtures: a placeholder secret in a
// _test.go file must be suppressed, while the same content in a non-test file
// still blocks (matches the in-house kern secret check's DefaultAllowlist).
func TestGitleaksAllowlistSuppressesTestFixtures(t *testing.T) {
	chk := NewCheck(nil, WithBinary("gitleaks"), WithRunner(fakeGitleaksRunner(gitleaksReportForFiles("fixture_test.go", "config.go"))))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files: []domain.FileChange{
			{Path: "fixture_test.go", Op: domain.OpWrite, Content: "const secret = \"AKIA1234567890ABCDEF\"\n"},
			{Path: "config.go", Op: domain.OpWrite, Content: "const AWSKey = \"AKIA1234567890ABCDEF\"\n"},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want %q (non-test file must still block)", cr.Status, domain.StatusBlock)
	}
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1 (only the non-test file)", len(cr.Findings))
	}
	if got := cr.Findings[0].File; got != "config.go" {
		t.Errorf("File = %q, want config.go (test fixture suppressed)", got)
	}
}

// TestGitleaksAllowlistOnlyTestFixturePasses: a staged _test.go file whose
// only "secret" is a placeholder must not block at all — this is the exact
// production gap (e.g. internal/blueprint/mcp/g15_test.go).
func TestGitleaksAllowlistOnlyTestFixturePasses(t *testing.T) {
	chk := NewCheck(nil, WithBinary("gitleaks"), WithRunner(fakeGitleaksRunner(gitleaksReportForFiles("internal/blueprint/mcp/g15_test.go"))))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files: []domain.FileChange{
			{Path: "internal/blueprint/mcp/g15_test.go", Op: domain.OpWrite, Content: "const secret = \"AKIA1234567890ABCDEF\"\n"},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want %q (test fixture suppressed)", cr.Status, domain.StatusPass)
	}
	if len(cr.Findings) != 0 {
		t.Fatalf("Findings = %d, want 0", len(cr.Findings))
	}
}

// TestGitleaksAllowlistTestdataAndJSTestFixtures: the other DefaultAllowlist
// semantics (testdata/ dirs, *.test.js) suppress findings too.
func TestGitleaksAllowlistTestdataAndJSTestFixtures(t *testing.T) {
	chk := NewCheck(nil, WithBinary("gitleaks"), WithRunner(fakeGitleaksRunner(gitleaksReportForFiles("testdata/fixture.go", "app.test.js"))))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files: []domain.FileChange{
			{Path: "testdata/fixture.go", Op: domain.OpWrite, Content: "const key = \"AKIA1234567890ABCDEF\"\n"},
			{Path: "app.test.js", Op: domain.OpWrite, Content: "const KEY = 'AKIA1234567890ABCDEF'\n"},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Fatalf("Status = %q, want %q (testdata + .test.js suppressed)", cr.Status, domain.StatusPass)
	}
	if len(cr.Findings) != 0 {
		t.Fatalf("Findings = %d, want 0", len(cr.Findings))
	}
}
