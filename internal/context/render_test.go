package context

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func buildPacket() domain.ContextPacket {
	return domain.ContextPacket{
		Task:        "Analyze this proposed change: Foo",
		GeneratedAt: time.Now(),
		Symbols: []domain.Symbol{
			{Name: "Foo", Qualified: "Foo", Kind: "func", File: "foo.go"},
		},
		Files: []domain.File{
			{Path: "foo.go", Language: "go"},
		},
		Memory: []domain.Memory{
			{Content: "Foo must not return nil", Type: domain.MemoryLesson},
		},
		Risks: []domain.Risk{
			{Level: domain.RiskMedium, Score: 0.5},
		},
		RequiredValidation: []string{"run unit tests covering Foo", "build verification"},
		TokenCount:         40,
	}
}

func TestRenderTextStructured(t *testing.T) {
	out := RenderText(buildPacket())
	if out == "" {
		t.Fatal("RenderText returned empty string")
	}
	lower := strings.ToLower(out)
	for _, want := range []string{"impact", "risk", "required validation", "architecture", "estimated affected files"} {
		if !strings.Contains(lower, want) {
			t.Errorf("RenderText missing %q section", want)
		}
	}
	if strings.Contains(lower, "plan:") {
		t.Errorf("RenderText should not contain %q section, got:\n%s", "plan:", out)
	}
	if !strings.Contains(out, "I understand the request") {
		t.Errorf("RenderText missing opener: %q", out)
	}
}

func TestRenderTextProceedApproval(t *testing.T) {
	// No risks require approval -> no "Proceed?" prompt.
	out := RenderText(buildPacket())
	if strings.Contains(out, "Proceed?") {
		t.Errorf("RenderText should not prompt Proceed? without approval-required risk, got:\n%s", out)
	}

	// A risk requiring approval -> "Proceed?" prompt present.
	pkt := buildPacket()
	pkt.Risks = []domain.Risk{{Level: domain.RiskHigh, ApprovalRequired: true}}
	out = RenderText(pkt)
	if !strings.Contains(out, "Proceed?") {
		t.Errorf("RenderText should prompt 'Proceed?' when a risk requires approval, got:\n%s", out)
	}
}

func TestRenderTextRiskLevel(t *testing.T) {
	out := RenderText(buildPacket())
	if !strings.Contains(out, "MEDIUM") {
		t.Errorf("RenderText should surface MEDIUM risk, got:\n%s", out)
	}
}

func TestRenderJSONValid(t *testing.T) {
	s, err := RenderJSON(buildPacket())
	if err != nil {
		t.Fatalf("RenderJSON error: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(s), &decoded); err != nil {
		t.Fatalf("RenderJSON produced invalid JSON: %v", err)
	}
	for _, key := range []string{"Task", "Symbols", "Files", "Risks", "RequiredValidation", "TokenCount", "GeneratedAt"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("RenderJSON missing field %q", key)
		}
	}
}

func TestRenderJSONTokenCount(t *testing.T) {
	pkt := buildPacket()
	pkt.TokenCount = 123
	s, err := RenderJSON(pkt)
	if err != nil {
		t.Fatalf("RenderJSON error: %v", err)
	}
	if !strings.Contains(s, `"TokenCount": 123`) {
		t.Errorf("RenderJSON should encode TokenCount=123, got: %s", s)
	}
}

func TestRiskLevelOfHighest(t *testing.T) {
	pkt := buildPacket()
	pkt.Risks = []domain.Risk{{Level: domain.RiskLow}, {Level: domain.RiskHigh}}
	if got := riskLevelOf(pkt); got != domain.RiskHigh {
		t.Errorf("riskLevelOf = %v, want HIGH", got)
	}
}
