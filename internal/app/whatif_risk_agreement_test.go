package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// riskAgreementFixture writes a tiny Go module with a heavily-depended-on
// symbol: Base is called directly by 14 callers, so its transitive blast
// radius crosses the shared classifier's HIGH threshold (10). The fixture
// mirrors the D3 repro shape ("rename NewServer ..." -> 263 affected) at a
// scale that stays fast to index.
func riskAgreementFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	var src strings.Builder
	src.WriteString("package main\n\nfunc Base() {}\n\n")
	for i := 1; i <= 14; i++ {
		src.WriteString("func Caller" + string(rune('A'+i)) + "() { Base() }\n\n")
	}
	src.WriteString("func main() { CallerA() }\n")
	files := map[string]string{
		"go.mod":  "module riskagreement\n\ngo 1.20\n",
		"main.go": src.String(),
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return dir
}

// TestRiskAndSimulateAgreeOnSameChangeString is the D3 regression at the
// shared app layer (the facade both `kern risk` and `kern simulate` route
// through): the IDENTICAL change string must yield the same risk tier via the
// context engine's risk path and the what-if simulation. Before the shared
// classifier, risk counted only the root symbols (always 1 for a symbol
// change -> "blast-radius:isolated" / low) while simulate counted the
// transitively affected set (-> high for wide changes).
func TestRiskAndSimulateAgreeOnSameChangeString(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes fixture; skipped with -short")
	}

	const change = "rename Base to Base2"
	root := riskAgreementFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build: %v", err)
	}
	p, err := NewWithIndex(root, ix)
	if err != nil {
		t.Fatalf("NewWithIndex: %v", err)
	}

	// kern risk path (context engine).
	pkt, _, err := p.Risk(change)
	if err != nil {
		t.Fatalf("Risk: %v", err)
	}
	if len(pkt.Risks) != 1 {
		t.Fatalf("expected 1 risk, got %d", len(pkt.Risks))
	}
	riskTier := pkt.Risks[0].Level

	// kern simulate path (what-if simulation), same change string.
	imp, _, err := p.WhatIf(whatif.RenameSymbol, change, "")
	if err != nil {
		t.Fatalf("WhatIf: %v", err)
	}

	// The same change must land on the same tier under both commands, and on
	// the WIDE tier (the change affects more than 10 symbols).
	if imp.Risk != "high" {
		t.Errorf("simulate risk = %q, want high (affected=%d)", imp.Risk, len(imp.Affected))
	}
	if got, want := strings.ToLower(string(riskTier)), imp.Risk; got != want {
		t.Errorf("risk tier %q and simulate risk %q disagree for the same change %q", got, want, change)
	}
	// The blast-radius factor must be the wide one, not the old root-count
	// "isolated" misclassification.
	found := false
	for _, f := range pkt.Risks[0].Factors {
		if f == "blast-radius:large" {
			found = true
		}
	}
	if !found {
		t.Errorf("risk factors = %v, want blast-radius:large for a wide change", pkt.Risks[0].Factors)
	}
	if riskTier != domain.RiskHigh {
		t.Errorf("risk tier = %q, want HIGH (affected=%d)", riskTier, len(imp.Affected))
	}
}
