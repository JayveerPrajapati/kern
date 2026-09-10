package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

func testPacket() *domain.ContextPacket {
	return &domain.ContextPacket{
		Task:       "Analyze this proposed change: fix the auth panic\nin handlers",
		TokenCount: 999,
		Facts: []domain.Claim{
			{Statement: "dispatch has impact on 6 symbols and touches the routing layer deeply"},
			{Statement: "auth middleware is not panic-safe under concurrent load testing scenarios"},
			{Statement: "handlers share a mutable session map that races during login bursts"},
		},
		Risks: []domain.Risk{
			{Level: domain.RiskHigh},
			{Level: domain.RiskMedium},
			{Level: domain.RiskLow},
		},
		RequiredValidation: []string{
			"run unit tests covering auth middleware",
			"run integration tests for the login flow",
			"run the race detector on handlers",
		},
	}
}

func TestRenderSummary(t *testing.T) {
	pkt := testPacket()
	out := RenderSummary(pkt, 1200)
	if !strings.Contains(out, "kern context") {
		t.Errorf("RenderSummary missing header: %q", out)
	}
	if !strings.Contains(out, "dispatch has impact on 6 symbols") {
		t.Errorf("RenderSummary missing first fact: %q", out)
	}
	if !strings.Contains(out, "tokens: 999") {
		t.Errorf("RenderSummary missing tokens line: %q", out)
	}
	if !strings.Contains(out, "- validate: run unit tests") {
		t.Errorf("RenderSummary missing validation line: %q", out)
	}
	// Title uses the task's first line, capped at 80 runes.
	if !strings.Contains(out, "(Analyze this proposed change: fix the auth panic)") {
		t.Errorf("RenderSummary title should use task first line: %q", out)
	}

	// A small budget truncates: output differs from the generous budget and
	// fits the token cap.
	small := RenderSummary(pkt, 50)
	if small == out {
		t.Error("budget=50 output identical to budget=1200 — expected truncation")
	}
	if n := tokenize.Count(small); n > 50 {
		t.Errorf("budget=50 output is %d tokens, want <= 50", n)
	}
	if tokenize.Count(out) > 1200 {
		t.Errorf("budget=1200 output is %d tokens, want <= 1200", tokenize.Count(out))
	}

	// budget <= 0 falls back to 1200.
	if got := RenderSummary(pkt, 0); got != out {
		t.Error("budget=0 output differs from budget=1200 (fallback expected)")
	}

	// Nil packet renders empty.
	if got := RenderSummary(nil, 1200); got != "" {
		t.Errorf("nil packet should render empty, got %q", got)
	}
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry()
	adapters := reg.Adapters()
	if len(adapters) != 5 {
		t.Fatalf("NewRegistry has %d adapters, want 5", len(adapters))
	}
	want := []string{"opencode", "claude", "cursor", "copilot", "codex"}
	for i, a := range adapters {
		if a.Name() != want[i] {
			t.Errorf("adapter[%d] = %q, want %q", i, a.Name(), want[i])
		}
	}

	dir := t.TempDir()
	// No files → nothing detected.
	if got := reg.Select(dir); len(got) != 0 {
		t.Errorf("Select on empty dir = %d adapters, want 0", len(got))
	}
	// Only AGENTS.md → opencode and codex (both target AGENTS.md).
	if err := writeTestFile(dir, "AGENTS.md", "# test\n"); err != nil {
		t.Fatal(err)
	}
	got := reg.Select(dir)
	if len(got) != 2 || got[0].Name() != "opencode" || got[1].Name() != "codex" {
		t.Errorf("Select with AGENTS.md = %v, want [opencode codex]", names(got))
	}

	// Register appends.
	fake := &fileAdapter{name: "fake", file: "FAKE.md"}
	reg.Register(fake)
	adapters = reg.Adapters()
	if len(adapters) != 6 || adapters[5].Name() != "fake" {
		t.Errorf("after Register, adapters = %v, want 6 with fake last", names(adapters))
	}
}

func names(adapters []Adapter) []string {
	out := make([]string, len(adapters))
	for i, a := range adapters {
		out[i] = a.Name()
	}
	return out
}

func writeTestFile(root, rel, content string) error {
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
