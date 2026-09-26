package duplication

import (
	"math"
	"testing"
)

// FuzzSimilarity asserts the documented Similarity contract over arbitrary
// fingerprint pairs:
//
//   - the score is always finite and within [0.0, 1.0] (never NaN/Inf, never
//     negative, never above 1.0);
//   - the size floor holds: a pair where either side is below
//     MinCandidateStatements scores exactly 0.
//
// Counts are derived from the fuzz bytes as non-negative small ints, matching
// what the real fingerprint pipeline emits (a real parser can never produce
// negative control-flow counts).
func FuzzSimilarity(f *testing.F) {
	// Seeds: identical, empty, tiny, and structurally distinct pairs.
	f.Add([]byte(""), []byte(""))
	f.Add([]byte("abc"), []byte("abc"))
	f.Add([]byte("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"), []byte("yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy"))
	f.Add([]byte("func(a int) int"), []byte("func(b string) error"))
	f.Add([]byte("a|b|c|d"), []byte("a|b|c|d"))
	f.Fuzz(func(t *testing.T, aData, bData []byte) {
		a := fingerprintFromBytes(aData)
		b := fingerprintFromBytes(bData)
		score := Similarity(a, b)
		if math.IsNaN(score) || math.IsInf(score, 0) {
			t.Fatalf("Similarity(%+v, %+v) = %v (not finite)", a, b, score)
		}
		if score < 0 || score > 1 {
			t.Fatalf("Similarity(%+v, %+v) = %v (outside [0,1])", a, b, score)
		}
		if a.StatementCount < MinCandidateStatements || b.StatementCount < MinCandidateStatements {
			if score != 0 {
				t.Fatalf("Similarity(%+v, %+v) = %v; size floor violated (want 0)", a, b, score)
			}
		}
	})
}

// fingerprintFromBytes derives a Fingerprint from fuzz bytes. Control-flow
// and size counts are derived as non-negative small ints (the real parser
// only emits counts >= 0); CalledSymbols is a deterministic 0-4 split of the
// bytes on '|'.
func fingerprintFromBytes(b []byte) Fingerprint {
	fp := Fingerprint{}

	var syms []string
	start := 0
	for i := 0; i < len(b) && len(syms) < 4; i++ {
		if b[i] == '|' {
			if i > start {
				syms = append(syms, string(b[start:i]))
			}
			start = i + 1
		}
	}
	if start < len(b) && len(syms) < 4 {
		syms = append(syms, string(b[start:]))
	}
	fp.CalledSymbols = syms
	fp.SignatureShape = string(b)

	if len(b) > 0 {
		fp.ParamCount = int(b[0] % 16)
		fp.LiteralCount = int(b[0] % 32)
	}
	if len(b) > 1 {
		fp.ReturnCount = int(b[1] % 8)
		fp.StatementCount = int(b[1] % 64)
		fp.ControlFlow.IfCount = int(b[1] % 8)
	}
	if len(b) > 2 {
		fp.ControlFlow.ForCount = int(b[2] % 8)
		fp.ControlFlow.RangeCount = int(b[2] % 8)
		fp.ControlFlow.SwitchCount = int(b[2] % 8)
		fp.ControlFlow.ReturnCount = int(b[2] % 8)
	}
	if len(b) > 3 {
		fp.ControlFlow.DeferCount = int(b[3] % 8)
		fp.ControlFlow.GoCount = int(b[3] % 8)
		fp.ControlFlow.AssignCount = int(b[3] % 8)
		fp.ControlFlow.CallCount = int(b[3] % 8)
	}
	return fp
}
