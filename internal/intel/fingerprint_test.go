package intel

import (
	"reflect"
	"testing"
)

// findFingerprint returns the fingerprint for the named function, failing the
// test if it is missing.
func findFingerprint(t *testing.T, fps []Fingerprint, name string) Fingerprint {
	t.Helper()
	for _, fp := range fps {
		if fp.FuncName == name {
			return fp
		}
	}
	t.Fatalf("fingerprint for %q not found in %d results", name, len(fps))
	return Fingerprint{}
}

// assertParity checks the blueprint-oracle fields of a fingerprint against
// golden values produced by blueprint's own ComputeFingerprint.
func assertParity(t *testing.T, got Fingerprint, want Fingerprint) {
	t.Helper()
	if got.SignatureShape != want.SignatureShape {
		t.Errorf("SignatureShape = %q, want %q", got.SignatureShape, want.SignatureShape)
	}
	if got.ParamCount != want.ParamCount {
		t.Errorf("ParamCount = %d, want %d", got.ParamCount, want.ParamCount)
	}
	if got.ReturnCount != want.ReturnCount {
		t.Errorf("ReturnCount = %d, want %d", got.ReturnCount, want.ReturnCount)
	}
	if !reflect.DeepEqual(got.CalledSymbols, want.CalledSymbols) {
		t.Errorf("CalledSymbols = %v, want %v", got.CalledSymbols, want.CalledSymbols)
	}
	if got.LiteralCount != want.LiteralCount {
		t.Errorf("LiteralCount = %d, want %d", got.LiteralCount, want.LiteralCount)
	}
	if got.StatementCount != want.StatementCount {
		t.Errorf("StatementCount = %d, want %d", got.StatementCount, want.StatementCount)
	}
	if got.ControlFlow != want.ControlFlow {
		t.Errorf("ControlFlow = %+v, want %+v", got.ControlFlow, want.ControlFlow)
	}
}

// TestParityWithBlueprint is the key regression guard. The fixture sources
// (fingerprint_fixtures_test.go) are byte-identical to blueprint's duplication
// fixtures, and the expected values below were produced by blueprint's own
// ComputeFingerprint (blueprintIO/internal/blueprint/checks/duplication) and
// encoded here as literals, so the kern port is verified against the exact
// same inputs and outputs. Blueprint's implementation is deleted in lockstep;
// if this test fails, the port has drifted.
func TestParityWithBlueprint(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		expect map[string]Fingerprint
	}{
		{
			name: "exact-duplicate shared/retry.go",
			src:  blueprintFixtureExactShared,
			expect: map[string]Fingerprint{
				"send": {
					SignatureShape: "func(1ptr)1err",
					ParamCount:     1,
					ReturnCount:    1,
					ControlFlow:    CFFingerprint{ReturnCount: 1},
					CalledSymbols:  nil, // blueprint produced a nil slice here
					LiteralCount:   0,
					StatementCount: 1,
					Line:           10,
				},
				"RetryRequest": {
					SignatureShape: "func(1ptr)1err",
					ParamCount:     1,
					ReturnCount:    1,
					ControlFlow: CFFingerprint{
						IfCount:     1,
						ForCount:    1,
						ReturnCount: 2,
						AssignCount: 2,
						CallCount:   3,
					},
					CalledSymbols:  []string{"errors.New(1)", "send(1)", "time.Sleep(1)"},
					LiteralCount:   3,
					StatementCount: 8,
					Line:           12,
				},
			},
		},
		{
			name: "unrelated-same-signature vault/process.go",
			src:  blueprintFixtureUnrelatedVault,
			expect: map[string]Fingerprint{
				"Process": {
					SignatureShape: "func(1slice)1err",
					ParamCount:     1,
					ReturnCount:    1,
					ControlFlow: CFFingerprint{
						IfCount:     1,
						RangeCount:  1,
						ReturnCount: 2,
						AssignCount: 2,
						CallCount:   2,
					},
					CalledSymbols:  []string{"byte(1)", "len(1)"},
					LiteralCount:   2,
					StatementCount: 8,
					Line:           4,
				},
			},
		},
		{
			name: "wrapper-around-existing api/retry.go",
			src:  blueprintFixtureWrapperAPI,
			expect: map[string]Fingerprint{
				"RetryWithLog": {
					SignatureShape: "func(1ptr)1err",
					ParamCount:     1,
					ReturnCount:    1,
					ControlFlow: CFFingerprint{
						ReturnCount: 1,
						CallCount:   2,
					},
					CalledSymbols:  []string{"log.Println(1)", "shared.RetryRequest(1)"},
					LiteralCount:   1,
					StatementCount: 2,
					Line:           9,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fps, err := ComputeFingerprint(tt.src)
			if err != nil {
				t.Fatalf("ComputeFingerprint: %v", err)
			}
			if len(fps) != len(tt.expect) {
				t.Fatalf("got %d fingerprints, want %d", len(fps), len(tt.expect))
			}
			for name, want := range tt.expect {
				assertParity(t, findFingerprint(t, fps, name), want)
			}
		})
	}
}

// TestSignatureNormalization covers the type-tag mapping that produces
// identifier-independent signature shapes.
func TestSignatureNormalization(t *testing.T) {
	src := `package p

func A(s string, i int) error { return nil }
func B(p *T) (int, error) { return 0, nil }
func C(xs []byte, m map[string]int) string { return "" }
func D(c chan int, f func(int) int) {}
func E() interface{} { return nil }
func F() struct{ X int } { return struct{ X int }{} }
func G(r io.Reader) error { return nil }
func H(v ...string) {}
func I() (a, b string) { return "", "" }
func J(n int32, u uint64, fl float64, bo bool, by byte, r rune) { _ = n; _ = u; _ = fl; _ = bo; _ = by; _ = r }
`
	fps, err := ComputeFingerprint(src)
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	want := map[string]struct {
		shape   string
		params  int
		returns int
	}{
		"A": {"func(1string,1int)1err", 2, 1},
		"B": {"func(1ptr)2int", 1, 2},
		"C": {"func(1slice,1map)1string", 2, 1},
		"D": {"func(1chan,1func)", 2, 0},
		"E": {"func()1iface", 0, 1},
		"F": {"func()1struct", 0, 1},
		"G": {"func(1pkg)1err", 1, 1},
		"H": {"func(1variadic)", 1, 0},
		"I": {"func()2string", 0, 2},
		"J": {"func(1int32,1uint64,1float64,1bool,1byte,1rune)", 6, 0},
	}
	for name, w := range want {
		fp := findFingerprint(t, fps, name)
		if fp.SignatureShape != w.shape {
			t.Errorf("%s: shape = %q, want %q", name, fp.SignatureShape, w.shape)
		}
		if fp.ParamCount != w.params {
			t.Errorf("%s: ParamCount = %d, want %d", name, fp.ParamCount, w.params)
		}
		if fp.ReturnCount != w.returns {
			t.Errorf("%s: ReturnCount = %d, want %d", name, fp.ReturnCount, w.returns)
		}
	}
}

// TestControlFlowCounts verifies the ast.Inspect counters for every
// control-flow construct the oracle tracks.
func TestControlFlowCounts(t *testing.T) {
	src := `package p

func cleanup(x int) {}
func work(x int) {}

func shape(x int) int {
	if x > 0 {
		x++
	} else {
		x--
	}
	for i := 0; i < 10; i++ {
		x += i
	}
	for range []int{1, 2} {
		x++
	}
	switch x {
	case 1:
		return 1
	default:
		x = 0
	}
	defer cleanup(x)
	go work(x)
	return x
}
`
	fps, err := ComputeFingerprint(src)
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	fp := findFingerprint(t, fps, "shape")
	want := CFFingerprint{
		IfCount:     1,
		ForCount:    1,
		RangeCount:  1,
		SwitchCount: 1,
		ReturnCount: 2,
		DeferCount:  1,
		GoCount:     1,
		AssignCount: 3, // i := 0 (for init) is an AssignStmt; x++/x-- are not
		CallCount:   2,
	}
	if fp.ControlFlow != want {
		t.Errorf("ControlFlow = %+v, want %+v", fp.ControlFlow, want)
	}
	if !reflect.DeepEqual(fp.CalledSymbols, []string{"cleanup(1)", "work(1)"}) {
		t.Errorf("CalledSymbols = %v, want [cleanup(1) work(1)]", fp.CalledSymbols)
	}
	if fp.LiteralCount != 8 {
		t.Errorf("LiteralCount = %d, want 8", fp.LiteralCount)
	}
	// countStmtRecursive counts an if/for/range BODY BLOCK as 1 plus its
	// children (matching blueprint's oracle); switch/select case bodies are
	// counted without the extra block.
	if fp.StatementCount != 17 {
		t.Errorf("StatementCount = %d, want 17", fp.StatementCount)
	}
}

// TestCalledSymbolNormalization covers ident, pkg.Sel, non-ident-receiver and
// arity normalization of call signatures.
func TestCalledSymbolNormalization(t *testing.T) {
	src := `package p

type T struct{}
type DB struct{}

func calls(db *DB, t T) {
	db.Query("x")
	fmt.Println("y")
	(*T).M(1)
	a.b.C()
	fn()
}
`
	fps, err := ComputeFingerprint(src)
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	fp := findFingerprint(t, fps, "calls")
	want := []string{".C(0)", ".M(1)", "db.Query(1)", "fmt.Println(1)", "fn(0)"}
	if !reflect.DeepEqual(fp.CalledSymbols, want) {
		t.Errorf("CalledSymbols = %v, want %v", fp.CalledSymbols, want)
	}
	if fp.ControlFlow.CallCount != 5 {
		t.Errorf("CallCount = %d, want 5", fp.ControlFlow.CallCount)
	}
}

// TestStatementCount verifies recursive statement counting through blocks,
// if/else chains, switch cases and select comm clauses.
func TestStatementCount(t *testing.T) {
	src := `package p

func stmts() {
	if a {
		b()
	} else if c {
		d()
	} else {
		e()
	}
	switch x {
	case 1:
		f()
	case 2:
		g()
		h()
	}
	select {
	case <-ch:
		i()
	default:
		j()
	}
}
`
	fps, err := ComputeFingerprint(src)
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	fp := findFingerprint(t, fps, "stmts")
	// if counts 1 + body block(2) + else-if(1 + body block(2) + else block(2)) = 8;
	// switch = 1 + 1 + 2 = 4; select = 1 + 1 + 1 = 3.
	if fp.StatementCount != 15 {
		t.Errorf("StatementCount = %d, want 15", fp.StatementCount)
	}
}

// TestLiteralCounts verifies that every string/int/float/bool/rune literal is
// counted, including inside call arguments and composite literals.
func TestLiteralCounts(t *testing.T) {
	src := `package p

func lits() {
	s := "hello"
	n := 42
	f := 3.14
	b := true
	r := 'x'
	if n > 0 {
		s = "world"
	}
	_, _, _, _, _ = s, n, f, b, r
}
`
	fps, err := ComputeFingerprint(src)
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	fp := findFingerprint(t, fps, "lits")
	if fp.LiteralCount != 6 {
		t.Errorf("LiteralCount = %d, want 6", fp.LiteralCount)
	}
}

// TestComputeFingerprintIgnoresNonFuncDecls verifies that type and var
// declarations produce no fingerprints.
func TestComputeFingerprintIgnoresNonFuncDecls(t *testing.T) {
	src := `package p

type Request struct{ URL string }
var n = 42
const k = 1
`
	fps, err := ComputeFingerprint(src)
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	if len(fps) != 0 {
		t.Errorf("got %d fingerprints, want 0", len(fps))
	}
}

// TestComputeFingerprintUnparsable verifies that a syntax error surfaces as an
// error rather than partial results.
func TestComputeFingerprintUnparsable(t *testing.T) {
	_, err := ComputeFingerprint("package p\nfunc broken( {\n")
	if err == nil {
		t.Fatal("expected error for unparsable source, got nil")
	}
}
