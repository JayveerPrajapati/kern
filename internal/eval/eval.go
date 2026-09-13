// Package eval provides a deterministic evaluation harness that proves a
// candidate context (e.g. a lensed or budget-fitted packet) reduces tokens
// without losing critical evidence, plus a rubric of deterministic
// assertions and an optional blind model judge.
package eval

import (
	"strings"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// Sample is one evaluation case: baseline and candidate outputs plus the
// critical evidence the candidate must retain.
type Sample struct {
	Name             string   // case name (reproducibility reporting)
	Baseline         string   // baseline context/output text
	Candidate        string   // candidate context/output text
	CriticalEvidence []string // fragments that MUST survive in the candidate
	Intent           string   // pipeline mode: when set, generate the candidate via kern orchestrate
}

// SampleResult is the per-sample outcome of a run.
type SampleResult struct {
	Name             string `json:"name"`
	BaselineTokens   int    `json:"baseline_tokens"`
	CandidateTokens  int    `json:"candidate_tokens"`
	EvidenceRetained int    `json:"evidence_retained"`
	EvidenceTotal    int    `json:"evidence_total"`
	Passed           bool   `json:"passed"` // all critical evidence retained AND (no budget set OR candidate fitted within budget)
}

// EvalResult is the aggregate outcome of a run, mirroring the structured
// result shape of internal/verification.VerificationResult and the metric
// style of internal/app/benchmark.go's BaselineComparison (deterministic,
// JSON-tagged where useful).
type EvalResult struct {
	Score             float64        `json:"score"`              // 0-100 quality: 100*(0.5*EvidenceRetention + 0.5*(1-ErrorRate))
	TokenReduction    float64        `json:"token_reduction"`    // 1 - ΣcandidateTokens/ΣbaselineTokens (0 when Σbaseline=0)
	EvidenceRetention float64        `json:"evidence_retention"` // Σretained/Σtotal (1.0 when Σtotal=0)
	OmissionRate      float64        `json:"omission_rate"`      // 1 - EvidenceRetention: fraction of critical evidence dropped
	ErrorRate         float64        `json:"error_rate"`         // failed samples / total (0 when no samples)
	LatencyMs         int64          `json:"latency_ms,omitempty"` // end-to-end candidate-generation time (set by the CLI, not Run)
	Reproducible      bool           `json:"reproducible"`       // echoed from the harness
	Samples           []SampleResult `json:"samples"`
	Rubric            []Assertion    `json:"rubric"` // copies with Pass filled by Run
}

// EvalHarness compares baseline vs candidate over a rubric.
type EvalHarness struct {
	Samples      []Sample
	Budget       int         // token budget the candidate is fitted to; 0 = no fitting
	Rubric       []Assertion // deterministic assertions over the aggregate result
	Reproducible bool        // recorded in the result; Run is deterministic by construction (never calls the judge)
}

// NewEvalHarness returns a harness with Reproducible=true (deterministic).
func NewEvalHarness(samples []Sample, budget int, rubric []Assertion) *EvalHarness {
	return &EvalHarness{Samples: samples, Budget: budget, Rubric: rubric, Reproducible: true}
}

// Run executes the harness deterministically: for each sample in input order,
// count baseline tokens (tokenize.Count), fit the candidate to Budget via
// budget.Fit when Budget > 0, count candidate tokens, count how many
// CriticalEvidence fragments strings.Contains in the fitted candidate,
// evaluate the rubric assertions against the AGGREGATE result, and return the
// aggregate EvalResult. Never calls the judge.
func (h *EvalHarness) Run() EvalResult {
	res := EvalResult{Reproducible: h.Reproducible}
	var sumBase, sumCand, sumRet, sumTotal, failed int
	for _, s := range h.Samples {
		base := tokenize.Count(s.Baseline)
		cand := s.Candidate
		if h.Budget > 0 {
			cand = budget.Fit(cand, h.Budget)
		}
		candTokens := tokenize.Count(cand)
		retained := 0
		for _, frag := range s.CriticalEvidence {
			if strings.Contains(cand, frag) {
				retained++
			}
		}
		total := len(s.CriticalEvidence)
		passed := retained == total && (h.Budget <= 0 || candTokens <= h.Budget)
		if !passed {
			failed++
		}
		sumBase += base
		sumCand += candTokens
		sumRet += retained
		sumTotal += total
		res.Samples = append(res.Samples, SampleResult{
			Name:             s.Name,
			BaselineTokens:   base,
			CandidateTokens:  candTokens,
			EvidenceRetained: retained,
			EvidenceTotal:    total,
			Passed:           passed,
		})
	}

	// Aggregates over ALL samples.
	if sumBase == 0 {
		res.TokenReduction = 0
	} else {
		res.TokenReduction = 1 - float64(sumCand)/float64(sumBase)
	}
	if sumTotal == 0 {
		res.EvidenceRetention = 1.0
	} else {
		res.EvidenceRetention = float64(sumRet) / float64(sumTotal)
	}
	res.OmissionRate = 1 - res.EvidenceRetention
	if len(h.Samples) == 0 {
		res.ErrorRate = 0
	} else {
		res.ErrorRate = float64(failed) / float64(len(h.Samples))
	}
	// Empty harness: zero metrics (Score 0), no panic.
	if len(h.Samples) == 0 {
		res.Score = 0
	} else {
		res.Score = 100 * (0.5*res.EvidenceRetention + 0.5*(1-res.ErrorRate))
	}

	// Rubric: copy each assertion, fill Actual+Pass from the aggregate.
	for _, a := range h.Rubric {
		a.evaluate(res)
		res.Rubric = append(res.Rubric, a)
	}
	return res
}
