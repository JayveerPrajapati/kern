package jscpd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// fakeKernFingerprintRunner is a commandRunner for the in-house kern client
// used in fallback tests: it answers `version` probes and returns an empty
// `kern fingerprint` contract (no functions => the in-house duplication check
// finds nothing).
func fakeKernFingerprintRunner() kern.CommandRunner {
	return func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		if len(args) > 0 && args[0] == "version" {
			return "kern dev", "", 0, nil
		}
		if len(args) > 0 && args[0] == "fingerprint" {
			return `{"schema_version":2,"fingerprints":[]}`, "", 0, nil
		}
		return `{"schema_version":2}`, "", 0, nil
	}
}

func TestJSCPDCheckEmptyChange(t *testing.T) {
	chk := NewCheck(nil, WithBinary("jscpd"), WithRunner(fakeJSCPDRunner(jscpdCloneJSON)))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{RepositoryRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Errorf("Status = %q, want %q (no files => nothing to compare)", cr.Status, domain.StatusPass)
	}
	if len(cr.Findings) != 0 {
		t.Errorf("Findings = %d, want 0", len(cr.Findings))
	}
}

func TestJSCPDCheckMissingRepoRoot(t *testing.T) {
	chk := NewCheck(nil, WithBinary("jscpd"), WithRunner(fakeJSCPDRunner(jscpdCloneJSON)))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		Files: []domain.FileChange{{Path: "sub1/a.js", Op: domain.OpWrite, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
	if cr.Error != "repository root required" {
		t.Errorf("Error = %q, want repository root required", cr.Error)
	}
}

// TestJSCPDCheckOnDiskFile: files without proposed content are read from
// disk into the mirror and compared.
func TestJSCPDCheckOnDiskFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub1"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub1", "a.js"), []byte("function process(data) { return data.join(','); }"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	chk := NewCheck(nil, WithBinary("jscpd"), WithRunner(fakeJSCPDRunner(jscpdCloneJSON)))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "sub1/a.js", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusWarn {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusWarn)
	}
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1", len(cr.Findings))
	}
	if cr.Findings[0].File != "sub1/a.js" {
		t.Errorf("File = %q, want sub1/a.js", cr.Findings[0].File)
	}
}

func TestJSCPDCheckPathEscapeRejected(t *testing.T) {
	chk := NewCheck(nil, WithBinary("jscpd"), WithRunner(fakeJSCPDRunner(jscpdEmptyJSON)))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "../../etc/passwd", Op: domain.OpWrite, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
	if !strings.Contains(cr.Error, "invalid path") {
		t.Errorf("Error = %q, want mention of invalid path", cr.Error)
	}
}

func TestJSCPDCheckLaunchFailure(t *testing.T) {
	runner := func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		return "", "", -1, os.ErrNotExist
	}
	chk := NewCheck(nil, WithBinary("/nonexistent/jscpd"), WithRunner(runner))
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
}

// TestJSCPDCheckFragmentTruncated: large duplicated fragments are capped in
// the evidence so findings stay lean.
func TestJSCPDCheckFragmentTruncated(t *testing.T) {
	big := strings.Repeat("x", 500)
	report := `{"duplicates":[{"firstFile":{"name":"sub1/a.js","start":1,"end":1,"startLoc":{"column":0,"line":1,"position":0},"endLoc":{"column":1,"line":1,"position":500}},"format":"text","fragment":"` + big + `","lines":1,"secondFile":{"name":"sub2/b.js","start":1,"end":1,"startLoc":{"column":0,"line":1,"position":0},"endLoc":{"column":1,"line":1,"position":500}},"tokens":100}],"statistics":{"detectionDate":"2026-08-28T07:46:45.295Z","formats":{},"total":{"clones":1,"duplicatedLines":1,"duplicatedTokens":100,"lines":2,"newClones":0,"newDuplicatedLines":0,"percentage":50.0,"percentageTokens":50.0,"sources":2,"tokens":200}}}`
	cr := jscpdCheck(t, report)
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1", len(cr.Findings))
	}
	desc := cr.Findings[0].Evidence[0].Description
	if len(desc) > maxEvidenceFragment+len("format: text, tokens: 100, lines: 1; fragment:\n")+len("...")+1 {
		t.Errorf("evidence description too large: %d chars", len(desc))
	}
	if !strings.HasSuffix(desc, "...") {
		t.Errorf("evidence description = %q, want truncated with ellipsis", desc)
	}
}

// --- Fallback behavior (incumbent binary absent) ---

func TestJSCPDFallbackInHousePassWarns(t *testing.T) {
	client, err := kern.NewKernClient(kern.WithBinary("kern"), kern.WithRunner(fakeKernFingerprintRunner()))
	if err != nil {
		t.Fatalf("NewKernClient: %v", err)
	}

	// WithBinary("") explicitly disables the incumbent => fallback path.
	chk := NewCheck(client, WithBinary(""))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "main.go", Op: domain.OpWrite, Content: "package x\n"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Name != "duplication:jscpd" {
		t.Errorf("Name = %q, want duplication:jscpd", cr.Name)
	}
	if cr.Status != domain.StatusWarn {
		t.Errorf("Status = %q, want %q (clean in-house scan still surfaces the fallback WARN)", cr.Status, domain.StatusWarn)
	}
	if len(cr.Findings) != 1 || cr.Findings[0].RuleID != "duplication:incumbent-unavailable" {
		t.Fatalf("Findings = %+v, want exactly the fallback finding", cr.Findings)
	}
	f := cr.Findings[0]
	if f.Severity != domain.SeverityWarn {
		t.Errorf("fallback Severity = %q, want %q", f.Severity, domain.SeverityWarn)
	}
	if !strings.Contains(f.Message, "jscpd not found") {
		t.Errorf("fallback Message = %q, want mention of jscpd not found", f.Message)
	}
	if f.Category != domain.CategoryDuplication {
		t.Errorf("fallback Category = %q, want %q", f.Category, domain.CategoryDuplication)
	}
}

func TestJSCPDFallbackNoClient(t *testing.T) {
	chk := NewCheck(nil, WithBinary(""))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "main.go", Op: domain.OpWrite, Content: "package x\n"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusWarn {
		t.Errorf("Status = %q, want %q (never a silent pass)", cr.Status, domain.StatusWarn)
	}
	if len(cr.Findings) != 1 || cr.Findings[0].RuleID != "duplication:incumbent-unavailable" {
		t.Fatalf("Findings = %+v, want exactly the fallback finding", cr.Findings)
	}
}

func TestJSCPDFallbackInHouseErrorPreserved(t *testing.T) {
	runner := func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		return "", "kern: not installed", 2, nil
	}
	client, err := kern.NewKernClient(kern.WithBinary("kern"), kern.WithRunner(runner))
	if err != nil {
		t.Fatalf("NewKernClient: %v", err)
	}
	chk := NewCheck(client, WithBinary(""))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "main.go", Op: domain.OpWrite, Content: "package x\n"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Errorf("Status = %q, want %q (in-house ERROR preserved)", cr.Status, domain.StatusError)
	}
}

// --- Two-pass triage (P1.1): in-house structural scan -> jscpd confirmation ---

// twoPassFakeRunner serves kern fingerprint records that make the in-house
// check emit a high-confidence (>0.90) structural finding between a changed
// file (new.go) and an existing file (existing.go): the DoRetry/RetryRequest
// records are structurally identical (score 1.0), while send/send scores 0.68
// (small-function penalty) — a standard advisory. Whole-root scans walk the
// workdir; scoped scans serve the requested files.
func twoPassFakeRunner() kern.CommandRunner {
	retryMain := []string{"errors.New(1)", "send(1)", "time.Sleep(1)"}
	rec := func(file, fn, sig string, params, rets int, calls []string, lits, stmts, line int, cf kern.ControlFlow) kern.FingerprintRecord {
		return kern.FingerprintRecord{
			File: file, Name: fn, SignatureShape: sig, ParamCount: params, ReturnCount: rets,
			CalledSymbols: calls, LiteralCount: lits, StatementCount: stmts,
			Lang: "go", Line: line, ControlFlow: cf,
		}
	}
	records := map[string][]kern.FingerprintRecord{
		"new.go": {
			rec("new.go", "send", "func(1ptr)1err", 1, 1, nil, 0, 1, 10, kern.ControlFlow{Return: 1}),
			rec("new.go", "DoRetry", "func(1ptr)1err", 1, 1, retryMain, 3, 8, 12, kern.ControlFlow{If: 1, For: 1, Return: 2, Assign: 2, Call: 3}),
		},
		"existing.go": {
			rec("existing.go", "send", "func(1ptr)1err", 1, 1, nil, 0, 1, 10, kern.ControlFlow{Return: 1}),
			rec("existing.go", "RetryRequest", "func(1ptr)1err", 1, 1, retryMain, 3, 8, 12, kern.ControlFlow{If: 1, For: 1, Return: 2, Assign: 2, Call: 3}),
		},
	}
	return func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		if len(args) > 0 && args[0] == "version" {
			return "kern 2.0", "", 0, nil
		}
		if len(args) > 0 && args[0] == "fingerprint" {
			var files []string
			for i := 0; i < len(args); i++ {
				if args[i] == "--file" && i+1 < len(args) {
					files = strings.Split(args[i+1], ",")
				}
			}
			var out []kern.FingerprintRecord
			if files == nil {
				_ = filepath.Walk(workdir, func(path string, info os.FileInfo, err error) error {
					if err != nil || info.IsDir() {
						return nil
					}
					if !strings.HasSuffix(path, ".go") {
						return nil
					}
					rel, err := filepath.Rel(workdir, path)
					if err != nil {
						return nil
					}
					out = append(out, records[filepath.ToSlash(rel)]...)
					return nil
				})
			} else {
				for _, f := range files {
					out = append(out, records[f]...)
				}
			}
			payload := struct {
				SchemaVersion int                      `json:"schema_version"`
				Fingerprints  []kern.FingerprintRecord `json:"fingerprints"`
			}{2, out}
			b, err := json.Marshal(payload)
			if err != nil {
				return "", err.Error(), 2, err
			}
			return string(b), "", 0, nil
		}
		return `{"schema_version":2}`, "", 0, nil
	}
}

// twoPassCloneJSON is a jscpd report with one clone between the changed file
// (new.go) and the existing file (existing.go) — the pair the in-house check
// flags at score 1.0.
const twoPassCloneJSON = `{
"duplicates": [
{
"firstFile": {"end": 8, "endLoc": {"column": 1, "line": 8, "position": 128}, "name": "new.go", "start": 1, "startLoc": {"column": 0, "line": 1, "position": 0}},
"format": "go",
"fragment": "func DoRetry(req *Request) error {\n  for i := 0; i < 3; i++ {\n    if send(req) == nil { return nil }\n  }\n  return errors.New(\"max retries\")\n}",
"lines": 8,
"secondFile": {"end": 8, "endLoc": {"column": 1, "line": 8, "position": 128}, "name": "existing.go", "start": 1, "startLoc": {"column": 0, "line": 1, "position": 0}},
"tokens": 32
}
],
"statistics": {"detectionDate": "2026-08-28T07:46:45.295Z", "formats": {}, "total": {"clones": 1, "duplicatedLines": 8, "duplicatedTokens": 32, "lines": 16, "newClones": 1, "newDuplicatedLines": 8, "percentage": 50.0, "percentageTokens": 50.0, "sources": 2, "tokens": 64}}
}`

// twoPassRequest builds a change that adds new.go (proposed content) next to
// the on-disk existing.go.
func twoPassRequest(root string) domain.ChangeRequest {
	return domain.ChangeRequest{
		RepositoryRoot: root,
		Files: []domain.FileChange{
			{Path: "new.go", Op: domain.OpWrite, Content: "package main\n\nfunc DoRetry(req *Request) error { return nil }\n"},
		},
	}
}

// twoPassRepo materializes a repo containing the existing file the in-house
// whole-root scan and the jscpd mirror both need on disk.
func twoPassRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.go"), []byte("package main\n\nfunc RetryRequest(req *Request) error { return nil }\n"), 0o644); err != nil {
		t.Fatalf("write existing.go: %v", err)
	}
	return root
}

// twoPassCheck builds the jscpd check with a kern client backed by
// twoPassFakeRunner and a jscpd runner serving reportJSON.
func twoPassCheck(t *testing.T, reportJSON string, opts ...Option) *Check {
	t.Helper()
	client, err := kern.NewKernClient(kern.WithBinary("kern"), kern.WithRunner(twoPassFakeRunner()))
	if err != nil {
		t.Fatalf("NewKernClient: %v", err)
	}
	all := append([]Option{WithBinary("jscpd"), WithRunner(fakeJSCPDRunner(reportJSON))}, opts...)
	return NewCheck(client, all...)
}

// TestJSCPDTwoPassConfirmedBlock: a >0.90 in-house candidate whose file pair
// jscpd also reports escalates to duplication:confirmed-block (BLOCK). The
// confirmed-block replaces the in-house advisory and the jscpd clone for the
// pair; evidence carries both signals plus the two-pass-confirmed marker.
func TestJSCPDTwoPassConfirmedBlock(t *testing.T) {
	chk := twoPassCheck(t, twoPassCloneJSON)
	cr, err := chk.Run(context.Background(), twoPassRequest(twoPassRepo(t)))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want %q (two-pass confirmed duplicate blocks)", cr.Status, domain.StatusBlock)
	}
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1 (only the confirmed-block; advisory + clone for the pair replaced)", len(cr.Findings))
	}
	f := cr.Findings[0]
	if f.RuleID != "duplication:confirmed-block" {
		t.Errorf("RuleID = %q, want duplication:confirmed-block", f.RuleID)
	}
	if f.Severity != domain.SeverityBlock {
		t.Errorf("Severity = %q, want %q", f.Severity, domain.SeverityBlock)
	}
	if f.Category != domain.CategoryDuplication {
		t.Errorf("Category = %q, want duplication", f.Category)
	}
	if f.File != "new.go" {
		t.Errorf("File = %q, want new.go (the changed file)", f.File)
	}
	if f.Line != 12 {
		t.Errorf("Line = %d, want 12 (DoRetry's line)", f.Line)
	}
	if !strings.Contains(f.Message, "duplicate confirmed") {
		t.Errorf("Message = %q, want mention of confirmed duplicate", f.Message)
	}
	if f.Confidence < 0.99 {
		t.Errorf("Confidence = %v, want >= 0.99 (max of in-house ~1.0 and jscpd 0.58)", f.Confidence)
	}
	if f.RuleVersion != "5.0.16" {
		t.Errorf("RuleVersion = %q, want 5.0.16 (jscpd version)", f.RuleVersion)
	}
	// Evidence: structural-fingerprint + jscpd-clone + two-pass-confirmed.
	if len(f.Evidence) != 3 {
		t.Fatalf("Evidence = %d entries, want 3 (structural + clone + marker)", len(f.Evidence))
	}
	if f.Evidence[0].Kind != "structural-fingerprint" {
		t.Errorf("Evidence[0].Kind = %q, want structural-fingerprint", f.Evidence[0].Kind)
	}
	if f.Evidence[1].Kind != "jscpd-clone" {
		t.Errorf("Evidence[1].Kind = %q, want jscpd-clone", f.Evidence[1].Kind)
	}
	if f.Evidence[2].Kind != "two-pass-confirmed" {
		t.Errorf("Evidence[2].Kind = %q, want two-pass-confirmed", f.Evidence[2].Kind)
	}
}

// TestJSCPDTwoPassNotConfirmedStaysAdvisory: a >0.90 in-house candidate that
// jscpd does NOT report (empty report) stays duplication:advisory WARN — the
// structural signal alone never blocks.
func TestJSCPDTwoPassNotConfirmedStaysAdvisory(t *testing.T) {
	chk := twoPassCheck(t, jscpdEmptyJSON)
	cr, err := chk.Run(context.Background(), twoPassRequest(twoPassRepo(t)))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusWarn {
		t.Fatalf("Status = %q, want %q (unconfirmed candidate stays advisory WARN)", cr.Status, domain.StatusWarn)
	}
	if len(cr.Findings) != 2 {
		t.Fatalf("Findings = %d, want 2 (DoRetry 1.0 + send 0.68 advisories)", len(cr.Findings))
	}
	for _, f := range cr.Findings {
		if f.RuleID != "duplication:advisory" {
			t.Errorf("RuleID = %q, want duplication:advisory", f.RuleID)
		}
		if f.Severity == domain.SeverityBlock {
			t.Errorf("Severity = %q, never blocks without jscpd confirmation", f.Severity)
		}
		// Advisories are WARN, or INFO for the informational bucket (0.60-0.85).
		if f.Severity != domain.SeverityWarn && f.Severity != domain.SeverityInfo {
			t.Errorf("Severity = %q, want warn or info", f.Severity)
		}
	}
}

// TestJSCPDTwoPassMockConfirmer: the WithConfirmer option is the injectable
// Pass-2 oracle. A mock that says yes blocks even with an empty report; a
// mock that says no keeps both signals (advisory + jscpd clone) as WARN.
func TestJSCPDTwoPassMockConfirmer(t *testing.T) {
	t.Run("mock-confirms", func(t *testing.T) {
		chk := twoPassCheck(t, jscpdEmptyJSON, WithConfirmer(func([2]string) bool { return true }))
		cr, err := chk.Run(context.Background(), twoPassRequest(twoPassRepo(t)))
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
		if cr.Status != domain.StatusBlock {
			t.Fatalf("Status = %q, want %q (mock confirmer confirms the pair)", cr.Status, domain.StatusBlock)
		}
		if len(cr.Findings) != 1 || cr.Findings[0].RuleID != "duplication:confirmed-block" {
			t.Fatalf("Findings = %+v, want exactly the confirmed-block", cr.Findings)
		}
		if cr.Findings[0].Severity != domain.SeverityBlock {
			t.Errorf("Severity = %q, want %q", cr.Findings[0].Severity, domain.SeverityBlock)
		}
	})

	t.Run("mock-rejects", func(t *testing.T) {
		chk := twoPassCheck(t, twoPassCloneJSON, WithConfirmer(func([2]string) bool { return false }))
		cr, err := chk.Run(context.Background(), twoPassRequest(twoPassRepo(t)))
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
		if cr.Status != domain.StatusWarn {
			t.Fatalf("Status = %q, want %q (mock rejects the pair, nothing escalates)", cr.Status, domain.StatusWarn)
		}
		// Non-escalated pair: BOTH signals are kept — advisory (DoRetry 1.0,
		// send 0.68) and the jscpd clone.
		var advisory, clone int
		for _, f := range cr.Findings {
			switch f.RuleID {
			case "duplication:advisory":
				advisory++
			case "duplication:jscpd:clone":
				clone++
			}
		}
		if advisory != 2 || clone != 1 {
			t.Errorf("findings = %d advisory + %d clone, want 2 advisory + 1 clone", advisory, clone)
		}
	})
}

// TestJSCPDFallbackAdvisoryStaysWarn: without the jscpd binary, in-house
// findings (including >0.90 candidates) stay advisory WARN — no confirmation
// is possible, so nothing escalates; the fallback flag is added.
func TestJSCPDFallbackAdvisoryStaysWarn(t *testing.T) {
	client, err := kern.NewKernClient(kern.WithBinary("kern"), kern.WithRunner(twoPassFakeRunner()))
	if err != nil {
		t.Fatalf("NewKernClient: %v", err)
	}
	chk := NewCheck(client, WithBinary(""))
	cr, err := chk.Run(context.Background(), twoPassRequest(twoPassRepo(t)))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusWarn {
		t.Fatalf("Status = %q, want %q (fallback is always WARN, never BLOCK)", cr.Status, domain.StatusWarn)
	}
	var advisory, fallback int
	for _, f := range cr.Findings {
		switch f.RuleID {
		case "duplication:advisory":
			advisory++
			if f.Severity == domain.SeverityBlock {
				t.Errorf("advisory Severity = %q, want never BLOCK (no confirmation in fallback)", f.Severity)
			}
		case "duplication:incumbent-unavailable":
			fallback++
		}
	}
	if advisory != 2 || fallback != 1 {
		t.Errorf("findings = %d advisory + %d fallback, want 2 advisory + 1 fallback", advisory, fallback)
	}
}
