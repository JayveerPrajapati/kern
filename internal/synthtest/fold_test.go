package synthtest

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestFoldEvaluatorExactValues unit-tests the constant-folding evaluator
// directly: Add(1,1)→2 and Div(4,2)→2 fold to the exact value, while a
// division by zero (0,0) is not a derivable value (F9).
func TestFoldEvaluatorExactValues(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "math.go", `package mathutil

func Add(a, b int) int {
	return a + b
}

func Div(a, b int) int {
	return a / b
}
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	fe := newFoldEnv(f)

	vals, ok := fe.evalBody(fe.funcs["Add"], []any{int64(1), int64(1)})
	if !ok || len(vals) != 1 || vals[0] != int64(2) {
		t.Fatalf("Add(1,1) = %v (ok=%v); want 2", vals, ok)
	}

	vals, ok = fe.evalBody(fe.funcs["Div"], []any{int64(4), int64(2)})
	if !ok || len(vals) != 1 || vals[0] != int64(2) {
		t.Fatalf("Div(4,2) = %v (ok=%v); want 2", vals, ok)
	}

	if _, ok := fe.evalBody(fe.funcs["Div"], []any{int64(0), int64(0)}); ok {
		t.Fatal("Div(0,0) must not be foldable (division by zero)")
	}
}

// TestSynthesizeFoldsLiteralExpressions: for functions whose body is a pure
// expression over literals, the generated test must assert the EXACT
// constant-folded value — not the old hardcoded zero-value want.
func TestSynthesizeFoldsLiteralExpressions(t *testing.T) {
	code := `package mathutil

func Add(a, b int) int {
	return a + b
}

func Div(a, b int) int {
	return a / b
}
`

	t.Run("Add(1,1)==2", func(t *testing.T) {
		res, err := Synthesize(Request{Target: "Add", Code: code})
		if err != nil {
			t.Fatalf("Synthesize failed: %v", err)
		}
		if !strings.Contains(res.TestCode, "a: 1,") || !strings.Contains(res.TestCode, "b: 1,") {
			t.Errorf("generated args missing standard case a:1 b:1:\n%s", res.TestCode)
		}
		if !strings.Contains(res.TestCode, "wantOut0: 2,") {
			t.Errorf("Add(1,1) must assert wantOut0: 2:\n%s", res.TestCode)
		}
		if strings.Contains(res.TestCode, "wantOut0: 1,") {
			t.Errorf("Add(1,1) must not assert the old zero-value want 1:\n%s", res.TestCode)
		}
	})

	t.Run("Div(1,1)==1 with gated zero case", func(t *testing.T) {
		res, err := Synthesize(Request{Target: "Div", Code: code})
		if err != nil {
			t.Fatalf("Synthesize failed: %v", err)
		}
		if !strings.Contains(res.TestCode, "wantOut0: 1,") {
			t.Errorf("Div(1,1) must assert wantOut0: 1:\n%s", res.TestCode)
		}
		// The zero-value boundary (0,0) is a division by zero — its value is
		// NOT derivable, so that case must be gated, never asserted.
		if !strings.Contains(res.TestCode, "wantKnown: false,") {
			t.Errorf("zero-value div-by-zero case must be gated with wantKnown: false:\n%s", res.TestCode)
		}
		if !strings.Contains(res.TestCode, "expected value not statically derivable") {
			t.Errorf("gated case must carry the not-derivable comment:\n%s", res.TestCode)
		}
	})
}

// TestSynthesizeNonFoldableEmitsSmokeOnly: when the expected value is not
// statically derivable (e.g. a function reading a package-level map), the
// generated test must NOT assert any want at all — a call-only smoke
// assertion with an explanatory comment (never a wrong assertion).
func TestSynthesizeNonFoldableEmitsSmokeOnly(t *testing.T) {
	code := `package service

var lookup = map[string]int{"a": 1}

func Lookup(key string) int {
	return lookup[key]
}
`
	res, err := Synthesize(Request{Target: "Lookup", Code: code})
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if !strings.Contains(res.TestCode, "expected value not statically derivable") {
		t.Errorf("smoke assertion missing explanatory comment:\n%s", res.TestCode)
	}
	if strings.Contains(res.TestCode, "wantOut0") {
		t.Errorf("non-foldable function must not emit a want column:\n%s", res.TestCode)
	}
	if strings.Contains(res.TestCode, "wantKnown") {
		t.Errorf("non-foldable function must not emit wantKnown:\n%s", res.TestCode)
	}
	if !strings.Contains(res.TestCode, "Lookup(tt.key)") {
		t.Errorf("smoke assertion must still call the function:\n%s", res.TestCode)
	}
}

// TestSynthesizeFoldsCallsToFoldableFunctions: a call to another foldable
// function in the same file folds through (Double(1) = Add(1,1) = 2).
func TestSynthesizeFoldsCallsToFoldableFunctions(t *testing.T) {
	code := `package mathutil

func Add(a, b int) int {
	return a + b
}

func Double(x int) int {
	return Add(x, x)
}
`
	res, err := Synthesize(Request{Target: "Double", Code: code})
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if !strings.Contains(res.TestCode, "wantOut0: 2,") {
		t.Errorf("Double(1) must fold through Add to wantOut0: 2:\n%s", res.TestCode)
	}
}
