package verifyorchestration

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/verdict"
)

func TestFailedTestEvidenceMapsIndexedSymbols(t *testing.T) {
	ix := index.New("")
	ix.Symbols = append(ix.Symbols, index.Symbol{
		Kind: "func",
		Name: "TestFullOnlyFailure",
		File: "b/b_test.go",
		Line: 5,
	})
	ix.Pkgs["b"] = &index.Pkg{Name: "b", Path: "b"}
	v := verdict.VerificationResult{UnitTests: &verdict.TestResult{
		Output: "--- FAIL: TestFullOnlyFailure (0.00s)\n",
	}}
	got := FailedTestEvidence(ix, v)
	want := "failed test evidence:\nTestFullOnlyFailure -> b.TestFullOnlyFailure — b/b_test.go:5"
	if got != want {
		t.Fatalf("FailedTestEvidence() =\n%s\nwant:\n%s", got, want)
	}
}

func TestFailedTestEvidenceReportsMissingIndex(t *testing.T) {
	v := verdict.VerificationResult{UnitTests: &verdict.TestResult{
		Output: "--- FAIL: TestMissing (0.00s)\n",
	}}
	got := FailedTestEvidence(nil, v)
	want := "failed test evidence:\nTestMissing -> not indexed"
	if got != want {
		t.Fatalf("FailedTestEvidence() = %q, want %q", got, want)
	}
}
