// Package eval assertions: deterministic checks over an aggregate EvalResult.
package eval

// Assertion is one deterministic check over an EvalResult.
type Assertion struct {
	Type     string  // "token_reduction" | "evidence_retention" | "error_rate"
	Expected float64 // threshold
	Actual   float64 // filled by the harness at run time
	Pass     bool    // filled by the harness at run time
}

// AssertTokenReduction requires candidate tokens to drop by at least
// minReduction (fraction, e.g. 0.3 = 30%) vs the baseline.
func AssertTokenReduction(minReduction float64) Assertion {
	return Assertion{Type: "token_reduction", Expected: minReduction}
}

// AssertEvidenceRetention requires at least minRetention (0-1) of the
// critical evidence to survive into the candidate.
func AssertEvidenceRetention(minRetention float64) Assertion {
	return Assertion{Type: "evidence_retention", Expected: minRetention}
}

// AssertErrorRate requires at most maxErrorRate (0-1) of samples to fail.
func AssertErrorRate(maxErrorRate float64) Assertion {
	return Assertion{Type: "error_rate", Expected: maxErrorRate}
}

// evaluate fills Actual and Pass against an aggregate result. Pass relations:
// token_reduction: Actual >= Expected; evidence_retention: Actual >= Expected;
// error_rate: Actual <= Expected. Unknown Type: Pass=false.
func (a *Assertion) evaluate(r EvalResult) {
	switch a.Type {
	case "token_reduction":
		a.Actual = r.TokenReduction
		a.Pass = a.Actual >= a.Expected
	case "evidence_retention":
		a.Actual = r.EvidenceRetention
		a.Pass = a.Actual >= a.Expected
	case "error_rate":
		a.Actual = r.ErrorRate
		a.Pass = a.Actual <= a.Expected
	default:
		a.Pass = false
	}
}
