package integration

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/host"
)

// TestHostAdapter exercises the real opencode host adapter on a temp root:
// Detect before/after inject, Inject writes the marked block into AGENTS.md,
// Extract round-trips the injected text, Uninstall removes the block cleanly,
// and a real engine packet survives the inject -> extract round-trip with its
// target symbol intact.
func TestHostAdapter(t *testing.T) {
	root := t.TempDir()
	ad := host.NewOpenCodeAdapter()

	// Detect before inject: no AGENTS.md in the empty root yet.
	if ad.Detect(root) {
		t.Fatal("Detect true before Inject on an empty root")
	}

	// Inject a packet: writes the marked block into root/AGENTS.md and
	// returns the rendered block.
	pkt := &domain.ContextPacket{
		Task:       "Analyze this proposed change: Count",
		Facts:      []domain.Claim{{Statement: "Count panics on negative input"}},
		Risks:      []domain.Risk{{Level: domain.RiskMedium}},
		TokenCount: 42,
	}
	block, err := ad.Inject(root, pkt, 0)
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if !strings.Contains(block, "Count") {
		t.Errorf("injected block missing symbol: %q", block)
	}

	// Detect after inject: AGENTS.md now exists.
	if !ad.Detect(root) {
		t.Error("Detect false after Inject")
	}

	// Extract returns the injected block (trimmed).
	extracted, err := ad.Extract(root)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if strings.TrimSpace(extracted) != strings.TrimSpace(block) {
		t.Errorf("Extract = %q, want injected block %q", extracted, block)
	}

	// Uninstall removes only this adapter's marked block cleanly; Extract
	// after Uninstall returns empty (no block left).
	if err := ad.Uninstall(root); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	after, err := ad.Extract(root)
	if err != nil {
		t.Fatalf("Extract after Uninstall: %v", err)
	}
	if after != "" {
		t.Errorf("Extract after Uninstall = %q, want empty (block removed)", after)
	}

	// Integration: a real engine packet for the fixture symbol round-trips
	// through Inject/Extract and keeps the symbol name.
	ix := buildFixtureIndex(t)
	eng := newContextEngine(t, ix)
	pkt2, err := eng.AnalyzeChange("Count")
	if err != nil {
		t.Fatalf("AnalyzeChange(Count): %v", err)
	}
	root2 := t.TempDir()
	ad2 := host.NewOpenCodeAdapter()
	block2, err := ad2.Inject(root2, &pkt2, 0)
	if err != nil {
		t.Fatalf("Inject(real packet): %v", err)
	}
	extracted2, err := ad2.Extract(root2)
	if err != nil {
		t.Fatalf("Extract(real packet): %v", err)
	}
	if !strings.Contains(block2, "Count") {
		t.Errorf("injected real-packet block missing symbol: %q", block2)
	}
	if !strings.Contains(extracted2, "Count") {
		t.Errorf("extracted real-packet block missing symbol: %q", extracted2)
	}
}
