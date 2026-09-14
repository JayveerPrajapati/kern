package mutation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateMutants(t *testing.T) {
	src := `package math

func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func IsValid(flag bool) bool {
	return flag == true
}

func Add(a, b int) int {
	return a + b
}
`

	muts, err := GenerateMutants("math.go", []byte(src))
	if err != nil {
		t.Fatalf("GenerateMutants error: %v", err)
	}

	if len(muts) < 3 {
		t.Fatalf("expected at least 3 mutants, got %d", len(muts))
	}

	hasInvert := false
	hasBoolSwap := false
	hasShift := false

	for _, m := range muts {
		if m.Operator == "invert_condition" && strings.Contains(m.Replacement, "<=") {
			hasInvert = true
		}
		if m.Operator == "swap_boolean" && m.Replacement == "false" {
			hasBoolSwap = true
		}
		if m.Operator == "boundary_shift" && strings.Contains(m.Replacement, "-") {
			hasShift = true
		}
	}

	if !hasInvert {
		t.Errorf("missing invert_condition mutant: %+v", muts)
	}
	if !hasBoolSwap {
		t.Errorf("missing swap_boolean mutant: %+v", muts)
	}
	if !hasShift {
		t.Errorf("missing boundary_shift mutant: %+v", muts)
	}
}

func TestApplyMutantToSource(t *testing.T) {
	src := `package math

func Check(a, b int) bool {
	return a == b
}
`
	mut := Mutant{
		Line:        4,
		Operator:    "invert_condition",
		Original:    "a == b",
		Replacement: "a != b",
	}

	mutSrc, err := ApplyMutantToSource("math.go", []byte(src), mut)
	if err != nil {
		t.Fatalf("ApplyMutantToSource failed: %v", err)
	}

	if !strings.Contains(string(mutSrc), "a != b") {
		t.Errorf("expected mutated condition 'a != b', got:\n%s", string(mutSrc))
	}
}

func TestRunMutationEvaluation(t *testing.T) {
	dir := t.TempDir()

	// Initialize a minimal Go module
	goMod := "module example.com/calc\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	code := `package calc

func Greater(a, b int) bool {
	return a > b
}
`
	if err := os.WriteFile(filepath.Join(dir, "calc.go"), []byte(code), 0644); err != nil {
		t.Fatalf("write calc.go: %v", err)
	}

	// Good unit test that asserts Greater(5, 3) is true and Greater(3, 5) is false
	testCode := `package calc

import "testing"

func TestGreater(t *testing.T) {
	if !Greater(5, 3) {
		t.Errorf("expected 5 > 3 to be true")
	}
	if Greater(3, 5) {
		t.Errorf("expected 3 > 5 to be false")
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "calc_test.go"), []byte(testCode), 0644); err != nil {
		t.Fatalf("write calc_test.go: %v", err)
	}

	report, err := Run(context.Background(), Options{
		Root:        dir,
		Files:       []string{"calc.go"},
		MaxMutants:  10,
		TestCommand: "go test . -count=1",
	})
	if err != nil {
		t.Fatalf("Run mutation testing failed: %v", err)
	}

	if report.KilledCount == 0 {
		t.Errorf("expected at least 1 killed mutant by TestGreater, got report: %+v", report)
	}
}
