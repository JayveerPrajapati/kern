package intel

import (
	"fmt"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// hubFixture builds a synthetic index: Hub is called by n callers C0..Cn-1,
// Leaf has one caller, and Solo has none.
func hubFixture(n int) *index.Index {
	syms := []index.Symbol{
		{Kind: "func", Name: "Hub", File: "hub/hub.go", Line: 10},
		{Kind: "func", Name: "Leaf", File: "lib/lib.go", Line: 5},
		{Kind: "func", Name: "Solo", File: "lib/lib.go", Line: 20},
	}
	callers := []string{}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("C%d", i)
		syms = append(syms, index.Symbol{Kind: "func", Name: name, File: "app/app.go", Line: 10 + i})
		callers = append(callers, name)
	}
	return &index.Index{
		Symbols: syms,
		Callers: map[string][]string{
			"Hub":  callers,
			"Leaf": {"Solo"},
		},
	}
}

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}

// TestAssessEditRiskThresholds pins the shared P2 thresholds (identical to
// the pre_edit report): >10 direct or >25 transitive is HIGH, >3/>8 MEDIUM.
func TestAssessEditRiskThresholds(t *testing.T) {
	if v := AssessEditRisk(hubFixture(11), "", "Hub"); v.Risk != "HIGH" {
		t.Errorf("11 callers = %s, want HIGH (direct=%d)", v.Risk, len(v.Direct))
	}
	if v := AssessEditRisk(hubFixture(4), "", "Hub"); v.Risk != "MEDIUM" {
		t.Errorf("4 callers = %s, want MEDIUM", v.Risk)
	}
	if v := AssessEditRisk(hubFixture(1), "", "Hub"); v.Risk != "LOW" {
		t.Errorf("1 caller = %s, want LOW", v.Risk)
	}
	if v := AssessEditRisk(hubFixture(11), "", "Leaf"); v.Risk != "LOW" {
		t.Errorf("Leaf (1 caller) = %s, want LOW", v.Risk)
	}
	if v := AssessEditRisk(hubFixture(0), "", "Solo"); v.Risk != "LOW" {
		t.Errorf("uncalled Solo = %s, want LOW", v.Risk)
	}
}

// TestAssessEditRiskTransitive pins second-degree counting: one direct
// caller with nine of its own is ten transitive → MEDIUM via the
// transitive leg.
func TestAssessEditRiskTransitive(t *testing.T) {
	callers := []string{"Mid"}
	for i := 0; i < 9; i++ {
		callers = append(callers, "T"+itoa(i))
	}
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "Hub", File: "hub/hub.go", Line: 10},
			{Kind: "func", Name: "Mid", File: "app/app.go", Line: 5},
		},
		Callers: map[string][]string{
			"Hub": {"Mid"},
			"Mid": callers[1:],
		},
	}
	v := AssessEditRisk(ix, "", "Hub")
	if v.Risk != "MEDIUM" {
		t.Errorf("1 direct + 10 transitive = %s, want MEDIUM", v.Risk)
	}
	if len(v.Transitive) != 10 {
		t.Errorf("transitive = %d, want 10: %v", len(v.Transitive), v.Transitive)
	}
}

// TestAssessEditRiskMatching pins target resolution: bare name, FullName,
// file path (incl. suffix), and unknown symbols (LOW, no targets).
func TestAssessEditRiskMatching(t *testing.T) {
	ix := hubFixture(11)
	if v := AssessEditRisk(ix, "", "Hub"); len(v.Targets) != 1 || v.Targets[0] != "Hub" {
		t.Errorf("bare-name targets = %v", v.Targets)
	}
	if v := AssessEditRisk(ix, "hub/hub.go", ""); v.Risk != "HIGH" {
		t.Errorf("file match = %s, want HIGH", v.Risk)
	}
	if v := AssessEditRisk(ix, "hub.go", ""); v.Risk != "HIGH" {
		t.Errorf("suffix file match = %s, want HIGH", v.Risk)
	}
	if v := AssessEditRisk(ix, "", "NoSuchSymbolXYZ"); v.Risk != "LOW" || len(v.Targets) != 0 {
		t.Errorf("unknown symbol = %s/%v, want LOW/empty", v.Risk, v.Targets)
	}
}

// TestAssessEditFilesUnion pins multi-file assessment: the union over every
// defined symbol, worst risk wins.
func TestAssessEditFilesUnion(t *testing.T) {
	ix := hubFixture(11)
	v := AssessEditFiles(ix, []string{"lib/lib.go", "hub/hub.go"})
	if v.Risk != "HIGH" {
		t.Errorf("union with hub = %s, want HIGH", v.Risk)
	}
	if v := AssessEditFiles(ix, []string{"lib/lib.go"}); v.Risk != "LOW" {
		t.Errorf("lib only = %s, want LOW", v.Risk)
	}
	if v := AssessEditFiles(ix, nil); v.Risk != "LOW" {
		t.Errorf("empty files = %s, want LOW", v.Risk)
	}
}

// TestRiskVerdictRefusal pins the fail-closed message contract: HIGH names
// the override, anything else returns "" (proceed).
func TestRiskVerdictRefusal(t *testing.T) {
	ix := hubFixture(11)
	msg := AssessEditRisk(ix, "", "Hub").Refusal("rename --apply of Hub")
	if msg == "" || !strings.Contains(msg, "--force") || !strings.Contains(msg, "11 direct") {
		t.Errorf("HIGH refusal must name --force and counts, got %q", msg)
	}
	if msg := AssessEditRisk(ix, "", "Leaf").Refusal("delete"); msg != "" {
		t.Errorf("LOW must proceed, got %q", msg)
	}
	if msg := AssessEditRisk(hubFixture(4), "", "Hub").Refusal("x"); msg != "" {
		t.Errorf("MEDIUM must proceed, got %q", msg)
	}
}
