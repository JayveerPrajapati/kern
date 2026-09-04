package jscpd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// Contract tests (mirroring the kern G14 gate): the jscpd adapter must
// handle valid incumbent JSON output, empty output, and malformed output —
// failing closed (StatusError) on anything it cannot parse, never silently
// passing. They use a fake runner (fakeJSCPDRunner) so no real jscpd binary
// is required; the runner writes the report file into the --output dir the
// check hands it, exactly like the real binary.

// fakeJSCPDRunner is a commandRunner that answers `--version` probes with
// "cpd 5.0.16" and writes reportJSON to <--output>/jscpd-report.json.
func fakeJSCPDRunner(reportJSON string) commandRunner {
	return func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		if len(args) > 0 && args[0] == "--version" {
			return "cpd 5.0.16", "", 0, nil
		}
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "--output" {
				if err := os.MkdirAll(args[i+1], 0o755); err != nil {
					return "", err.Error(), -1, err
				}
				if err := os.WriteFile(filepath.Join(args[i+1], "jscpd-report.json"), []byte(reportJSON), 0o644); err != nil {
					return "", err.Error(), -1, err
				}
			}
		}
		return "", "", 0, nil
	}
}

// jscpdCloneJSON is a real 5.0.16 report element (captured schema): one
// clone between sub1/a.js and sub2/b.js, 32 tokens / 8 lines.
const jscpdCloneJSON = `{
"duplicates": [
{
"firstFile": {"end": 8, "endLoc": {"column": 1, "line": 8, "position": 128}, "name": "sub1/a.js", "start": 1, "startLoc": {"column": 0, "line": 1, "position": 0}},
"format": "javascript",
"fragment": "function process(data) {\n  let result = \"\";\n  for (const c of data) {\n    result += c;\n    result += \",\";\n  }\n  return result;\n}",
"lines": 8,
"secondFile": {"end": 8, "endLoc": {"column": 1, "line": 8, "position": 128}, "name": "sub2/b.js", "start": 1, "startLoc": {"column": 0, "line": 1, "position": 0}},
"tokens": 32
}
],
"statistics": {"detectionDate": "2026-08-28T07:46:45.295Z", "formats": {}, "total": {"clones": 1, "duplicatedLines": 7, "duplicatedTokens": 32, "lines": 16, "newClones": 0, "newDuplicatedLines": 0, "percentage": 43.75, "percentageTokens": 50.0, "sources": 2, "tokens": 64}}
}`

// jscpdEmptyJSON is the real 5.0.16 clean report shape.
const jscpdEmptyJSON = `{"duplicates":[],"statistics":{"detectionDate":"2026-08-28T07:46:45.295Z","formats":{},"total":{"clones":0,"duplicatedLines":0,"duplicatedTokens":0,"lines":0,"newClones":0,"newDuplicatedLines":0,"percentage":0.0,"percentageTokens":0.0,"sources":0,"tokens":0}}}`

func jscpdCheck(t *testing.T, reportJSON string) domain.CheckResult {
	t.Helper()
	chk := NewCheck(nil, WithBinary("jscpd"), WithRunner(fakeJSCPDRunner(reportJSON)))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files: []domain.FileChange{
			{Path: "sub1/a.js", Op: domain.OpWrite, Content: "function process(data) { return data.join(','); }"},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	return cr
}

func TestContractJSCPDValidOutput(t *testing.T) {
	cr := jscpdCheck(t, jscpdCloneJSON)

	if cr.Name != "duplication:jscpd" {
		t.Errorf("Name = %q, want duplication:jscpd", cr.Name)
	}
	if cr.Status != domain.StatusWarn {
		t.Fatalf("Status = %q, want %q (duplication never blocks)", cr.Status, domain.StatusWarn)
	}
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1", len(cr.Findings))
	}

	f := cr.Findings[0]
	if f.RuleID != "duplication:jscpd:clone" {
		t.Errorf("RuleID = %q, want duplication:jscpd:clone", f.RuleID)
	}
	if f.Severity != domain.SeverityWarn {
		t.Errorf("Severity = %q, want %q (duplication is probabilistic)", f.Severity, domain.SeverityWarn)
	}
	if f.Category != domain.CategoryDuplication {
		t.Errorf("Category = %q, want %q", f.Category, domain.CategoryDuplication)
	}
	if f.File != "sub1/a.js" {
		t.Errorf("File = %q, want sub1/a.js (first file of the pair)", f.File)
	}
	if f.Line != 1 {
		t.Errorf("Line = %d, want 1 (first file start line)", f.Line)
	}
	wantMsg := "Duplicated code block (32 tokens, 8 lines) also found in sub2/b.js:1"
	if f.Message != wantMsg {
		t.Errorf("Message = %q, want %q", f.Message, wantMsg)
	}
	if !strings.Contains(f.Explanation, "javascript") {
		t.Errorf("Explanation = %q, want mention of the clone format", f.Explanation)
	}
	if f.RuleVersion != "5.0.16" {
		t.Errorf("RuleVersion = %q, want 5.0.16", f.RuleVersion)
	}
	if got, want := f.Confidence, cloneConfidence(32); got != want {
		t.Errorf("Confidence = %v, want %v (derived from token count)", got, want)
	}
	if len(f.Evidence) != 1 {
		t.Fatalf("Evidence = %d, want 1", len(f.Evidence))
	}
	if f.Evidence[0].Kind != "jscpd-clone" {
		t.Errorf("Evidence.Kind = %q, want jscpd-clone", f.Evidence[0].Kind)
	}
	if !strings.Contains(f.Evidence[0].Description, "function process(data)") {
		t.Errorf("Evidence.Description = %q, want the duplicated fragment", f.Evidence[0].Description)
	}
	if f.Evidence[0].Location != "sub1/a.js:1 vs sub2/b.js:1" {
		t.Errorf("Evidence.Location = %q, want sub1/a.js:1 vs sub2/b.js:1", f.Evidence[0].Location)
	}
}

func TestContractJSCPDEmptyOutput(t *testing.T) {
	cr := jscpdCheck(t, jscpdEmptyJSON)

	if cr.Status != domain.StatusPass {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusPass)
	}
	if len(cr.Findings) != 0 {
		t.Errorf("Findings = %d, want 0", len(cr.Findings))
	}
}

func TestContractJSCPDMalformedOutput(t *testing.T) {
	cr := jscpdCheck(t, `not json`)

	// Fail closed: a report we cannot parse is a tool failure, never a
	// silent pass (mirrors G14's contract).
	if cr.Status != domain.StatusError {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
	if !strings.Contains(cr.Error, "parse") {
		t.Errorf("Error = %q, want mention of parse failure", cr.Error)
	}
}

// TestContractJSCPDFiltersExistingOnly: a clone entirely between existing
// (non-changed) files is pre-existing repo-internal duplication — not this
// change's signal — and must not be reported (new-change principle).
func TestContractJSCPDFiltersExistingOnly(t *testing.T) {
	chk := NewCheck(nil, WithBinary("jscpd"), WithRunner(fakeJSCPDRunner(jscpdCloneJSON)))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "other.go", Op: domain.OpWrite, Content: "package x"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Errorf("Status = %q, want %q (clone does not involve the changed file)", cr.Status, domain.StatusPass)
	}
	if len(cr.Findings) != 0 {
		t.Errorf("Findings = %d, want 0", len(cr.Findings))
	}
}

// TestContractJSCPDSeveralClonePairs: a changed file duplicated against two
// existing files yields one finding per distinct counterpart.
func TestContractJSCPDSeveralClonePairs(t *testing.T) {
	multi := `{
"duplicates": [
{"firstFile": {"name": "sub1/a.js", "start": 1, "end": 8, "startLoc": {"column": 0, "line": 1, "position": 0}, "endLoc": {"column": 1, "line": 8, "position": 128}}, "format": "javascript", "fragment": "frag1", "lines": 8, "secondFile": {"name": "sub2/b.js", "start": 1, "end": 8, "startLoc": {"column": 0, "line": 1, "position": 0}, "endLoc": {"column": 1, "line": 8, "position": 128}}, "tokens": 32},
{"firstFile": {"name": "sub1/a.js", "start": 1, "end": 8, "startLoc": {"column": 0, "line": 1, "position": 0}, "endLoc": {"column": 1, "line": 8, "position": 128}}, "format": "javascript", "fragment": "frag2", "lines": 8, "secondFile": {"name": "sub3/c.js", "start": 1, "end": 8, "startLoc": {"column": 0, "line": 1, "position": 0}, "endLoc": {"column": 1, "line": 8, "position": 128}}, "tokens": 60}
],
"statistics": {"detectionDate": "2026-08-28T07:46:45.295Z", "formats": {}, "total": {"clones": 2, "duplicatedLines": 14, "duplicatedTokens": 92, "lines": 24, "newClones": 0, "newDuplicatedLines": 0, "percentage": 58.3, "percentageTokens": 71.9, "sources": 3, "tokens": 128}}
}`
	cr := jscpdCheck(t, multi)

	if len(cr.Findings) != 2 {
		t.Fatalf("Findings = %d, want 2 (one per distinct counterpart)", len(cr.Findings))
	}
	if cr.Findings[0].Message != "Duplicated code block (32 tokens, 8 lines) also found in sub2/b.js:1" {
		t.Errorf("first finding Message = %q", cr.Findings[0].Message)
	}
	if cr.Findings[1].Message != "Duplicated code block (60 tokens, 8 lines) also found in sub3/c.js:1" {
		t.Errorf("second finding Message = %q", cr.Findings[1].Message)
	}
}

// TestContractJSCPDReportFileMissing: the binary claimed success but wrote no
// report file — fail closed rather than passing silently.
func TestContractJSCPDReportFileMissing(t *testing.T) {
	runner := func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		return "", "", 0, nil // never writes the report
	}
	chk := NewCheck(nil, WithBinary("jscpd"), WithRunner(runner))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "sub1/a.js", Op: domain.OpWrite, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Fatalf("Status = %q, want %q (missing report must not pass silently)", cr.Status, domain.StatusError)
	}
}

// TestContractJSCPDToolError: a non-zero jscpd exit is a tool failure.
func TestContractJSCPDToolError(t *testing.T) {
	runner := func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		if len(args) > 0 && args[0] == "--version" {
			return "cpd 5.0.16", "", 0, nil
		}
		return "", "jscpd: cannot tokenize", 1, nil
	}
	chk := NewCheck(nil, WithBinary("jscpd"), WithRunner(runner))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "sub1/a.js", Op: domain.OpWrite, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
	if !strings.Contains(cr.Error, "exit 1") {
		t.Errorf("Error = %q, want mention of exit 1", cr.Error)
	}
}
