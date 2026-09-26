package security

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/sec"
)

type fakeSecSvc struct {
	scanFn   func(ctx context.Context, root string) ([]sec.Finding, error)
	filterFn func(findings []sec.Finding, allow []string) []sec.Finding
	renderFn func(findings []sec.Finding, max int) string
	maskFn   func(ctx context.Context, text string, names []string) (pii.Result, error)
	gotNames []string
}

func (f *fakeSecSvc) Scan(ctx context.Context, root string) ([]sec.Finding, error) {
	return f.scanFn(ctx, root)
}
func (f *fakeSecSvc) FilterBySeverity(findings []sec.Finding, allow []string) []sec.Finding {
	return f.filterFn(findings, allow)
}
func (f *fakeSecSvc) Render(findings []sec.Finding, max int) string { return f.renderFn(findings, max) }
func (f *fakeSecSvc) Mask(ctx context.Context, text string, names []string) (pii.Result, error) {
	f.gotNames = names
	return f.maskFn(ctx, text, names)
}

// writeTree writes a map of relative path -> content under a fresh temp dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestMaskPII(t *testing.T) {
	ctx := context.Background()
	_, err := MaskPII(ctx, nil, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "text is required") {
		t.Fatalf("expected text is required error, got: %v", err)
	}
}

func TestMaskPIIHappy(t *testing.T) {
	ctx := context.Background()
	svc := &fakeSecSvc{
		maskFn: func(ctx context.Context, text string, names []string) (pii.Result, error) {
			return pii.Result{
				Text:     "connect to [MASKED_TOKEN_1]",
				Replaced: 1,
				ByLabel:  map[string]int{"TOKEN": 1},
			}, nil
		},
	}
	out, err := MaskPII(ctx, svc, map[string]any{"text": "connect to sk-secret"})
	if err != nil {
		t.Fatalf("MaskPII failed: %v", err)
	}
	if !strings.Contains(out, "connect to [MASKED_TOKEN_1]") {
		t.Errorf("expected masked text in output, got: %q", out)
	}
	if !strings.Contains(out, "masked 1 secrets") || !strings.Contains(out, "TOKEN 1") {
		t.Errorf("expected summary line with label counts, got: %q", out)
	}
	if !strings.Contains(out, "[kern]") {
		t.Errorf("expected [kern] marker in output, got: %q", out)
	}
}

func TestMaskPIINamesParsing(t *testing.T) {
	ctx := context.Background()
	svc := &fakeSecSvc{
		maskFn: func(ctx context.Context, text string, names []string) (pii.Result, error) {
			return pii.Result{Text: text}, nil
		},
	}
	_, err := MaskPII(ctx, svc, map[string]any{
		"text": "hello", "mask_names": " alice , bob ,,",
	})
	if err != nil {
		t.Fatalf("MaskPII failed: %v", err)
	}
	want := []string{"alice", "bob"}
	if len(svc.gotNames) != len(want) || svc.gotNames[0] != want[0] || svc.gotNames[1] != want[1] {
		t.Errorf("expected names %v, got %v", want, svc.gotNames)
	}
}

func TestMaskPIIError(t *testing.T) {
	ctx := context.Background()
	svc := &fakeSecSvc{
		maskFn: func(ctx context.Context, text string, names []string) (pii.Result, error) {
			return pii.Result{}, context.DeadlineExceeded
		},
	}
	_, err := MaskPII(ctx, svc, map[string]any{"text": "hello"})
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("expected propagated mask error, got: %v", err)
	}
}

func TestScanJSON(t *testing.T) {
	ctx := context.Background()
	svc := &fakeSecSvc{
		scanFn: func(ctx context.Context, root string) ([]sec.Finding, error) {
			return []sec.Finding{{File: "a.go", Line: 3, Rule: "r1", Severity: "error", Message: "boom"}}, nil
		},
		filterFn: func(findings []sec.Finding, allow []string) []sec.Finding { return findings },
		renderFn: func(findings []sec.Finding, max int) string { return "rendered" },
	}
	out, err := Scan(ctx, svc, map[string]any{"root": ".", "format": "json", "severity": "error"})
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	var got []sec.Finding
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("expected JSON findings output, got %q: %v", out, err)
	}
	if len(got) != 1 || got[0].File != "a.go" || got[0].Severity != "error" {
		t.Errorf("unexpected findings: %+v", got)
	}
}

func TestScanNoFindings(t *testing.T) {
	ctx := context.Background()
	svc := &fakeSecSvc{
		scanFn: func(ctx context.Context, root string) ([]sec.Finding, error) { return nil, nil },
		filterFn: func(findings []sec.Finding, allow []string) []sec.Finding {
			return nil
		},
		renderFn: func(findings []sec.Finding, max int) string { return "" },
	}
	out, err := Scan(ctx, svc, map[string]any{"root": "."})
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	if !strings.Contains(out, "no security findings") {
		t.Errorf("expected no findings message, got: %q", out)
	}
}

func TestScanRenderedCounts(t *testing.T) {
	ctx := context.Background()
	findings := []sec.Finding{
		{File: "a.go", Line: 1, Rule: "r1", Severity: "error", Message: "e"},
		{File: "a.go", Line: 2, Rule: "r2", Severity: "warning", Message: "w"},
		{File: "a.go", Line: 3, Rule: "r3", Severity: "info", Message: "i"},
	}
	svc := &fakeSecSvc{
		scanFn:   func(ctx context.Context, root string) ([]sec.Finding, error) { return findings, nil },
		filterFn: func(f []sec.Finding, allow []string) []sec.Finding { return f },
		renderFn: func(f []sec.Finding, max int) string {
			if max != 100 {
				t.Errorf("expected default max 100, got %d", max)
			}
			return "RENDERED"
		},
	}
	out, err := Scan(ctx, svc, map[string]any{"root": "."})
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	if !strings.Contains(out, "RENDERED") {
		t.Errorf("expected rendered findings, got: %q", out)
	}
	if !strings.Contains(out, "[kern] 3 findings: 1 error, 1 warning, 1 info") {
		t.Errorf("expected counts summary, got: %q", out)
	}
}

func TestScanMaxInvalid(t *testing.T) {
	ctx := context.Background()
	svc := &fakeSecSvc{
		scanFn:   func(ctx context.Context, root string) ([]sec.Finding, error) { return nil, nil },
		filterFn: func(f []sec.Finding, allow []string) []sec.Finding { return f },
		renderFn: func(f []sec.Finding, max int) string { return "" },
	}
	_, err := Scan(ctx, svc, map[string]any{"root": ".", "max": "not-a-number"})
	if err == nil || !strings.Contains(err.Error(), "max: invalid integer") {
		t.Fatalf("expected max parse error, got: %v", err)
	}
}

func TestScanServiceError(t *testing.T) {
	ctx := context.Background()
	svc := &fakeSecSvc{
		scanFn: func(ctx context.Context, root string) ([]sec.Finding, error) {
			return nil, context.Canceled
		},
		filterFn: func(f []sec.Finding, allow []string) []sec.Finding { return f },
		renderFn: func(f []sec.Finding, max int) string { return "" },
	}
	_, err := Scan(ctx, svc, map[string]any{"root": "."})
	if err == nil || !strings.Contains(err.Error(), "security scan failed") {
		t.Fatalf("expected scan error wrapper, got: %v", err)
	}
}

func TestTaintEmptyDir(t *testing.T) {
	ctx := context.Background()
	h := Hooks{
		LoadIndex: func(ctx context.Context, root string) (*index.Index, error) { return index.New(root), nil },
	}
	out, err := Taint(ctx, h, map[string]any{"root": t.TempDir()})
	if err != nil {
		t.Fatalf("Taint failed: %v", err)
	}
	if !strings.Contains(out, "no security findings") {
		t.Errorf("expected no findings, got: %q", out)
	}
}

func TestTaintFileFilter(t *testing.T) {
	ctx := context.Background()
	h := Hooks{
		LoadIndex: func(ctx context.Context, root string) (*index.Index, error) { return index.New(root), nil },
	}
	out, err := Taint(ctx, h, map[string]any{"root": t.TempDir(), "file": "x.go"})
	if err != nil {
		t.Fatalf("Taint failed: %v", err)
	}
	if !strings.Contains(out, "no security findings") {
		t.Errorf("expected no findings, got: %q", out)
	}
}

func TestTaintInvalidRange(t *testing.T) {
	ctx := context.Background()
	h := Hooks{LoadIndex: func(ctx context.Context, root string) (*index.Index, error) { return index.New(root), nil }}
	_, err := Taint(ctx, h, map[string]any{"root": t.TempDir(), "range": "abc"})
	if err == nil || !strings.Contains(err.Error(), "invalid range") {
		t.Fatalf("expected invalid range error, got: %v", err)
	}
}

func TestTaintRangeWorktree(t *testing.T) {
	ctx := context.Background()
	dir := writeTree(t, map[string]string{
		"main.go": "package main\n\nconst k = \"sk-abcdefghijklmnopqrstuvwxyz1234567890\"\n\nfunc main() {}\n",
	})
	// A bare git repo (no commits): the worktree diff falls back to
	// `git status --porcelain`, which reports the uncommitted file.
	runGit(t, dir, "init")
	h := Hooks{
		LoadIndex: func(ctx context.Context, root string) (*index.Index, error) { return nil, nil },
	}
	out, err := Taint(ctx, h, map[string]any{"root": dir, "range": ".."})
	if err != nil {
		t.Fatalf("Taint range failed: %v", err)
	}
	if !strings.Contains(out, "scope: 1 file(s) changed in worktree") {
		t.Errorf("expected worktree scope line, got: %q", out)
	}
	if !strings.Contains(out, "main.go:") || !strings.Contains(out, "tainted: no") {
		t.Errorf("expected a taint finding for main.go, got: %q", out)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func TestSafeDeleteMissingSymbol(t *testing.T) {
	ctx := context.Background()
	_, err := SafeDelete(ctx, Hooks{}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "symbol is required") {
		t.Fatalf("expected symbol is required error, got: %v", err)
	}
}

func TestSafeDeleteIndexError(t *testing.T) {
	ctx := context.Background()
	h := Hooks{LoadIndex: func(ctx context.Context, root string) (*index.Index, error) {
		return nil, context.Canceled
	}}
	_, err := SafeDelete(ctx, h, map[string]any{"symbol": "Foo"})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected index error, got: %v", err)
	}
}

func TestSafeDeleteHappy(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ix := index.New(root)
	ix.Symbols = []index.Symbol{{Kind: "func", Name: "Foo", File: "a.go", Line: 1, Lang: "go"}}
	h := Hooks{LoadIndex: func(ctx context.Context, r string) (*index.Index, error) { return ix, nil }}
	out, err := SafeDelete(ctx, h, map[string]any{"symbol": "Foo", "root": root})
	if err != nil {
		t.Fatalf("SafeDelete failed: %v", err)
	}
	if !strings.Contains(out, "defined at a.go") {
		t.Errorf("expected definition line, got: %q", out)
	}
	if !strings.Contains(out, "SAFE") && !strings.Contains(out, "NOT SAFE") {
		t.Errorf("expected a verdict line, got: %q", out)
	}
}

func TestSafeDeleteJSON(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ix := index.New(root)
	ix.Symbols = []index.Symbol{{Kind: "func", Name: "Bar", File: "a.go", Line: 1, Lang: "go"}}
	h := Hooks{LoadIndex: func(ctx context.Context, r string) (*index.Index, error) { return ix, nil }}
	out, err := SafeDelete(ctx, h, map[string]any{"symbol": "Bar", "root": root, "format": "json"})
	if err != nil {
		t.Fatalf("SafeDelete failed: %v", err)
	}
	var rep map[string]any
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("expected JSON report, got %q: %v", out, err)
	}
	if rep["symbol"] != "Bar" {
		t.Errorf("expected symbol Bar in JSON report, got: %v", rep["symbol"])
	}
}

func TestSchemaValidate(t *testing.T) {
	ctx := context.Background()
	_, err := SchemaValidate(ctx, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected required error, got: %v", err)
	}

	res, err := SchemaValidate(ctx, map[string]any{
		"data":   `{"name": "test"}`,
		"schema": `{"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res, "schema OK") {
		t.Fatalf("expected schema OK, got: %q", res)
	}
}

func TestSchemaValidateMalformedSchema(t *testing.T) {
	ctx := context.Background()
	_, err := SchemaValidate(ctx, map[string]any{
		"data":   `{}`,
		"schema": `{not json`,
	})
	if err == nil {
		t.Fatal("expected malformed schema error")
	}
}

func TestSchemaValidateViolations(t *testing.T) {
	ctx := context.Background()
	res, err := SchemaValidate(ctx, map[string]any{
		"data":   `{"name": 42}`,
		"schema": `{"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res, "schema violations (") {
		t.Errorf("expected violation listing, got: %q", res)
	}
}

func TestSchemaValidateUnicode(t *testing.T) {
	ctx := context.Background()
	res, err := SchemaValidate(ctx, map[string]any{
		"data":   `{"name": "héllo — wörld ✓"}`,
		"schema": `{"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res, "schema OK") {
		t.Errorf("unicode string must validate, got: %q", res)
	}
}

func TestVerifyOutputTextRequired(t *testing.T) {
	ctx := context.Background()
	_, err := VerifyOutput(ctx, Hooks{}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "text is required") {
		t.Fatalf("expected text required error, got: %v", err)
	}
}

func TestVerifyOutputIndexError(t *testing.T) {
	ctx := context.Background()
	h := Hooks{LoadIndex: func(ctx context.Context, root string) (*index.Index, error) {
		return nil, context.Canceled
	}}
	_, err := VerifyOutput(ctx, h, map[string]any{"text": "Foo"})
	if err == nil || !strings.Contains(err.Error(), "cannot verify") {
		t.Fatalf("expected index unavailable error, got: %v", err)
	}
}

func TestVerifyOutputHappy(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ix := index.New(root)
	ix.Symbols = []index.Symbol{{Kind: "func", Name: "Foo", File: "a.go", Line: 1, Lang: "go"}}
	h := Hooks{LoadIndex: func(ctx context.Context, r string) (*index.Index, error) { return ix, nil }}
	out, err := VerifyOutput(ctx, h, map[string]any{"text": "we call Foo and it works", "root": root})
	if err != nil {
		t.Fatalf("VerifyOutput failed: %v", err)
	}
	if !strings.Contains(out, "verified 1 references") {
		t.Errorf("expected verified references line, got: %q", out)
	}
	if !strings.Contains(out, "[ok") {
		t.Errorf("expected ok marker for the found symbol, got: %q", out)
	}
}

func TestCheckDraftCodeRequired(t *testing.T) {
	ctx := context.Background()
	_, err := CheckDraft(ctx, Hooks{}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "code is required") {
		t.Fatalf("expected code required error, got: %v", err)
	}
}

func TestCheckDraftIndexError(t *testing.T) {
	ctx := context.Background()
	h := Hooks{LoadIndex: func(ctx context.Context, root string) (*index.Index, error) {
		return nil, context.Canceled
	}}
	_, err := CheckDraft(ctx, h, map[string]any{"code": "package main"})
	if err == nil || !strings.Contains(err.Error(), "cannot check draft") {
		t.Fatalf("expected index unavailable error, got: %v", err)
	}
}

func TestCheckDraftClean(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ix := index.New(root)
	h := Hooks{LoadIndex: func(ctx context.Context, r string) (*index.Index, error) { return ix, nil }}
	out, err := CheckDraft(ctx, h, map[string]any{
		"code": "package main\n\nfunc main() { println(\"hi\") }\n", "root": root,
	})
	if err != nil {
		t.Fatalf("CheckDraft failed: %v", err)
	}
	if !strings.Contains(out, "OK: draft validates cleanly") {
		t.Errorf("expected clean verdict, got: %q", out)
	}
}

func TestCheckDraftUnknownSymbol(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ix := index.New(root)
	h := Hooks{LoadIndex: func(ctx context.Context, r string) (*index.Index, error) { return ix, nil }}
	out, err := CheckDraft(ctx, h, map[string]any{
		"code": "package main\n\nfunc main() { Foo() }\n", "root": root,
	})
	if err != nil {
		t.Fatalf("CheckDraft failed: %v", err)
	}
	if !strings.Contains(out, "[unknown_symbol]") {
		t.Errorf("expected unknown_symbol finding, got: %q", out)
	}
	if !strings.Contains(out, "1 issue(s) found") {
		t.Errorf("expected issue count, got: %q", out)
	}
}

func TestCheckDraftParseError(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ix := index.New(root)
	h := Hooks{LoadIndex: func(ctx context.Context, r string) (*index.Index, error) { return ix, nil }}
	out, err := CheckDraft(ctx, h, map[string]any{
		"code": "package main\nfunc main( {\n", "root": root,
	})
	if err != nil {
		t.Fatalf("CheckDraft failed: %v", err)
	}
	if !strings.Contains(out, "[parse_error]") {
		t.Errorf("expected parse_error finding, got: %q", out)
	}
}

func TestCheckDraftUnicode(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ix := index.New(root)
	h := Hooks{LoadIndex: func(ctx context.Context, r string) (*index.Index, error) { return ix, nil }}
	out, err := CheckDraft(ctx, h, map[string]any{
		"code": "// héllo — wörld\npackage main\n\nfunc main() {}\n", "root": root,
	})
	if err != nil {
		t.Fatalf("CheckDraft failed: %v", err)
	}
	if !strings.Contains(out, "OK: draft validates cleanly") {
		t.Errorf("unicode comment must not break the draft check, got: %q", out)
	}
}

func TestGuardCheckChangedContextError(t *testing.T) {
	ctx := context.Background()
	h := Hooks{ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
		return nil, nil, context.Canceled
	}}
	_, err := GuardCheck(ctx, h, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected changed-context error, got: %v", err)
	}
}

func TestGuardCheckNoChanges(t *testing.T) {
	ctx := context.Background()
	h := Hooks{ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
		return nil, index.New(t.TempDir()), nil
	}}
	out, err := GuardCheck(ctx, h, map[string]any{})
	if err != nil {
		t.Fatalf("GuardCheck failed: %v", err)
	}
	if !strings.Contains(out, "no changed files") {
		t.Errorf("expected no-changes message, got: %q", out)
	}
}

func TestGuardCheckUnconfiguredInfersBoundaries(t *testing.T) {
	ctx := context.Background()
	root := writeTree(t, map[string]string{
		"go.mod":     "module m\n\ngo 1.20\n",
		"web/web.go": "package web\n\nfunc W() {}\n",
	})
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	h := Hooks{
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return []intel.FileChange{{File: "web/web.go"}}, ix, nil
		},
	}
	out, err := GuardCheck(ctx, h, map[string]any{"root": root})
	if err != nil {
		t.Fatalf("GuardCheck failed: %v", err)
	}
	// No .kern/boundaries.json: the guard infers default boundaries and
	// reports a clean pass rather than failing.
	if !strings.Contains(out, "no boundary violations") {
		t.Errorf("expected inferred-boundaries clean pass, got: %q", out)
	}
}

func TestGuardCheckThresholdRejected(t *testing.T) {
	ctx := context.Background()
	root := writeTree(t, map[string]string{
		"go.mod":                "module m\n\ngo 1.20\n",
		".kern/boundaries.json": `{"description":"d","rules":[{"from":"api","to":"web","action":"forbid"}]}`,
		"web/web.go":            "package web\n\nfunc W() {}\n",
		"api/api.go":            "package api\n\nimport \"m/web\"\n\nfunc A() { web.W() }\n",
	})
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	h := Hooks{
		ServerVersion: "test",
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return []intel.FileChange{{File: "api/api.go"}}, ix, nil
		},
	}
	_, err = GuardCheck(ctx, h, map[string]any{"root": root, "threshold": "0"})
	if err == nil || !strings.Contains(err.Error(), "rejected:") || !strings.Contains(err.Error(), "exceed threshold 0") {
		t.Fatalf("expected threshold rejection, got: %v", err)
	}
}

func TestGuardCheckSARIF(t *testing.T) {
	ctx := context.Background()
	root := writeTree(t, map[string]string{
		"go.mod":                "module m\n\ngo 1.20\n",
		".kern/boundaries.json": `{"description":"d","rules":[{"from":"api","to":"web","action":"forbid"}]}`,
		"web/web.go":            "package web\n\nfunc W() {}\n",
		"api/api.go":            "package api\n\nimport \"m/web\"\n\nfunc A() { web.W() }\n",
	})
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	h := Hooks{
		ServerVersion: "test",
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return []intel.FileChange{{File: "api/api.go"}}, ix, nil
		},
	}
	out, err := GuardCheck(ctx, h, map[string]any{"root": root, "format": "sarif", "threshold": "10"})
	if err != nil {
		t.Fatalf("GuardCheck sarif failed: %v", err)
	}
	if !strings.Contains(out, `"version": "2.1.0"`) {
		t.Errorf("expected SARIF 2.1.0 document, got: %q", out)
	}
	if !strings.Contains(out, "api/api.go") {
		t.Errorf("expected the violating file in SARIF output, got: %q", out)
	}
}

func TestGuardCheckThresholdParseError(t *testing.T) {
	ctx := context.Background()
	h := Hooks{
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return []intel.FileChange{{File: "a.go"}}, index.New(t.TempDir()), nil
		},
	}
	_, err := GuardCheck(ctx, h, map[string]any{"root": t.TempDir(), "threshold": "lots"})
	if err == nil {
		t.Fatal("expected threshold parse error")
	}
}
