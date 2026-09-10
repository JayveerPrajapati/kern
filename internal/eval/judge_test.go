package eval

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/llm"
)

// fakeProvider records the last Generate call and returns a canned response.
type fakeProvider struct {
	response string
	system   string
	user     string
	opts     llm.Options
}

func (f *fakeProvider) Generate(ctx context.Context, system, user string, opts llm.Options) (string, error) {
	f.system = system
	f.user = user
	f.opts = opts
	return f.response, nil
}

func (f *fakeProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, fmt.Errorf("eval test: embed not supported")
}

func (f *fakeProvider) Capabilities() llm.Capabilities {
	return llm.Capabilities{}
}

func (f *fakeProvider) Stream(ctx context.Context, system, user string, opts llm.Options) (*llm.Stream, error) {
	return nil, fmt.Errorf("eval test: stream not supported")
}

func TestJudgeParsesScores(t *testing.T) {
	fp := &fakeProvider{response: "Output A: 90\nOutput B: 60"}
	j := &BlindJudge{Provider: fp, Model: "test-model", Blind: true}
	cand, base, err := j.Judge(context.Background(), Sample{Name: "s1", Baseline: "b", Candidate: "c"})
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	// Stable mapping: A = baseline, B = candidate.
	if cand != 60 || base != 90 {
		t.Errorf("candidate=%v baseline=%v, want 60/90", cand, base)
	}
}

func TestJudgeBlindPromptHidesLabels(t *testing.T) {
	sample := Sample{Name: "s1", Baseline: "the original text", Candidate: "the compressed text"}

	fp := &fakeProvider{response: "Output A: 80\nOutput B: 70"}
	blind := &BlindJudge{Provider: fp, Blind: true}
	if _, _, err := blind.Judge(context.Background(), sample); err != nil {
		t.Fatalf("blind Judge: %v", err)
	}
	if strings.Contains(fp.user, "baseline") || strings.Contains(fp.user, "candidate") {
		t.Errorf("blind prompt must hide baseline/candidate labels, got: %q", fp.user)
	}

	fp2 := &fakeProvider{response: "Output A: 80\nOutput B: 70"}
	labeled := &BlindJudge{Provider: fp2, Blind: false}
	if _, _, err := labeled.Judge(context.Background(), sample); err != nil {
		t.Fatalf("labeled Judge: %v", err)
	}
	if !strings.Contains(fp2.user, "baseline") || !strings.Contains(fp2.user, "candidate") {
		t.Errorf("labeled prompt must name baseline/candidate, got: %q", fp2.user)
	}
}

func TestJudgeNilProviderError(t *testing.T) {
	j := &BlindJudge{Provider: nil}
	if _, _, err := j.Judge(context.Background(), Sample{}); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("nil provider: err = %v, want nil-provider error", err)
	}
}

func TestJudgeUnparseableOutputError(t *testing.T) {
	fp := &fakeProvider{response: "I refuse to answer"}
	j := &BlindJudge{Provider: fp}
	if _, _, err := j.Judge(context.Background(), Sample{}); err == nil || !strings.Contains(err.Error(), "unparseable") {
		t.Errorf("unparseable response: err = %v, want unparseable error", err)
	}
}

func TestJudgeSeedAndTemperature(t *testing.T) {
	fp := &fakeProvider{response: "Output A: 50\nOutput B: 60"}
	j := &BlindJudge{Provider: fp, Model: "m"}
	if _, _, err := j.Judge(context.Background(), Sample{}); err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if fp.opts.Temperature != 0 {
		t.Errorf("Temperature = %v, want 0", fp.opts.Temperature)
	}
	if fp.opts.Seed == nil || *fp.opts.Seed != 42 {
		t.Errorf("Seed = %v, want &42", fp.opts.Seed)
	}
	if fp.opts.Model != "m" {
		t.Errorf("Model = %q, want m", fp.opts.Model)
	}
}
