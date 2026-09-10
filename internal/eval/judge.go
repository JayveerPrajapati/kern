// Package eval blind judge: optional model-scored sample quality, explicitly
// opt-in — EvalHarness.Run never calls it, so Run stays deterministic.
package eval

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/JayveerPrajapati/kern/internal/llm"
)

// BlindJudge optionally scores sample quality with a model provider. It is an
// explicit, opt-in external evaluation surface: EvalHarness.Run never calls it
// (Run stays deterministic — the Reproducible flag is the contract for that).
type BlindJudge struct {
	Provider llm.Provider // required; nil judge returns an error, never a guess
	Model    string       // model override; empty = provider default
	Blind    bool         // true: judge sees outputs as unlabeled "Output A"/"Output B"; false: labeled "baseline"/"candidate"
}

// scoreA/scoreB parse the judge's answer lines. The mapping is stable: A is
// always the baseline, B always the candidate — when Blind, the judge simply
// cannot tell which label is which.
var (
	reScoreA = regexp.MustCompile(`(?i)output\s*A\s*[:=]\s*([0-9]+)`)
	reScoreB = regexp.MustCompile(`(?i)output\s*B\s*[:=]\s*([0-9]+)`)
)

// Judge scores one sample's baseline and candidate on 0-100 with the provider.
// Blind=true presents both outputs WITHOUT naming which is baseline vs
// candidate, so the judge cannot favor the candidate. Prompt asks for
// "Output A: <0-100>" and "Output B: <0-100>" on separate lines; scores are
// parsed deterministically (regexp). Returns an error when Provider is nil or
// the response is unparseable. Calls Provider.Generate with
// llm.Options{Model: j.Model, Temperature: 0} and a fixed Seed (deterministic
// where the provider honors it).
func (j *BlindJudge) Judge(ctx context.Context, s Sample) (candidateScore, baselineScore float64, err error) {
	if j.Provider == nil {
		return 0, 0, fmt.Errorf("eval: nil judge provider")
	}
	user := fmt.Sprintf("Sample: %s\n\n", s.Name)
	if j.Blind {
		// Unlabeled: the judge sees only "Output A"/"Output B" and cannot tell
		// which is the baseline and which the candidate.
		user += fmt.Sprintf("Output A:\n%s\n\nOutput B:\n%s\n\n", s.Baseline, s.Candidate)
		user += "Score Output A and Output B on quality 0-100. Reply with one line each:\nOutput A: <0-100>\nOutput B: <0-100>"
	} else {
		user += fmt.Sprintf("Baseline:\n%s\n\nCandidate:\n%s\n\n", s.Baseline, s.Candidate)
		user += "Score the baseline (Output A) and the candidate (Output B) on quality 0-100. Reply with one line each:\nOutput A: <0-100>\nOutput B: <0-100>"
	}
	// Fixed seed for deterministic scoring where the provider honors it; the
	// pointer must be allocated (llm.Options.Seed is *int).
	seed := 42
	opts := llm.Options{Model: j.Model, Temperature: 0, Seed: &seed}
	text, err := j.Provider.Generate(ctx, "You are an evaluation judge. Score each output on quality 0-100.", user, opts)
	if err != nil {
		return 0, 0, fmt.Errorf("eval: judge generate: %w", err)
	}
	ma := reScoreA.FindStringSubmatch(text)
	mb := reScoreB.FindStringSubmatch(text)
	if ma == nil || mb == nil {
		return 0, 0, fmt.Errorf("eval: judge response unparseable: %q", text)
	}
	a, _ := strconv.ParseFloat(ma[1], 64)
	b, _ := strconv.ParseFloat(mb[1], 64)
	// Clamp to 0-100 (defensive; parse can only produce digits, but a
	// multi-digit score like "0100" should not leak past 100).
	return clamp(b, 0, 100), clamp(a, 0, 100), nil
}

// clamp bounds v to [lo, hi].
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
