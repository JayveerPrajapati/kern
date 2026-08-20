package modernization

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// writeTree writes a fixture module and returns its root directory.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func build(t *testing.T, files map[string]string) *index.Index {
	t.Helper()
	dir := writeTree(t, files)
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

// ordersPkg and billingPkg are two independent packages that form two
// disconnected call-graph communities: each has internal call edges but no
// cross-package dependency.
const ordersPkg = `package orders

func Serve() int {
	return total(New())
}

func New() *int {
	v := 0
	return &v
}

func total(o *int) int {
	return *o
}
`

const billingPkg = `package billing

func Invoice() int {
	return amount(2)
}

func amount(v int) int {
	return v
}
`

// bridgeOrders and bridgeBilling form a cross-package coupling bridge via the
// shared common.Util. bridgeCommon has its own internal helper chain so it
// coheres as a distinct cluster.
const bridgeOrders = `package a

import "common"

func Service() int {
	return common.Util(1) + B()
}

func B() int {
	return C()
}

func C() int {
	return 1
}

func D() int {
	return B()
}
`

const bridgeBilling = `package billing

import "common"

func X() int {
	return Y() + common.Util(2)
}

func Y() int {
	return Z()
}

func Z() int {
	return 2
}
`

const bridgeCommon = `package common

func Util(v int) int {
	return helper(v)
}

func helper(v int) int {
	return v
}
`

func twoPackageFixture(t *testing.T) *index.Index {
	t.Helper()
	return build(t, map[string]string{
		"orders/orders.go":   ordersPkg,
		"billing/billing.go": billingPkg,
	})
}

func bridgeFixture(t *testing.T) *index.Index {
	t.Helper()
	return build(t, map[string]string{
		"a/a.go":       bridgeOrders,
		"billing/b.go": bridgeBilling,
		"common/c.go":  bridgeCommon,
	})
}

func TestAnalyzeGatedLargeRepo(t *testing.T) {
	ix := &index.Index{}
	for i := 0; i < index.MaxCommunitySymbols+1; i++ {
		ix.Symbols = append(ix.Symbols, index.Symbol{
			Kind: "func", Name: "f", File: "x.go", Line: i + 1,
		})
	}
	a := NewAnalyzer(ix)
	plan, err := a.Analyze()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Contexts) != 0 || len(plan.Phases) != 0 {
		t.Errorf("expected empty plan above gate, got %d contexts %d phases", len(plan.Contexts), len(plan.Phases))
	}
	if !strings.Contains(plan.Summary, "skipped") {
		t.Errorf("expected skip-note in summary, got %q", plan.Summary)
	}
}

func TestAnalyzeDetectsBoundedContexts(t *testing.T) {
	ix := twoPackageFixture(t)
	plan, err := NewAnalyzer(ix).Analyze()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Contexts) < 2 {
		t.Fatalf("expected at least 2 bounded contexts, got %d: %+v", len(plan.Contexts), plan.Contexts)
	}
	for i, ctx := range plan.Contexts {
		if ctx.Name == "" {
			t.Errorf("context %d has empty name", i)
		}
		if len(ctx.Symbols) == 0 {
			t.Errorf("context %q has no symbols", ctx.Name)
		}
		if ctx.Cohesion <= 0 {
			t.Errorf("context %q should have cohesion > 0, got %f", ctx.Name, ctx.Cohesion)
		}
	}
}

func TestExtractionPlanOrderedByRisk(t *testing.T) {
	plan, err := NewAnalyzer(bridgeFixture(t)).Analyze()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Phases) < 2 {
		t.Fatalf("expected multiple phases, got %d", len(plan.Phases))
	}
	// Phase numbers must be sequential starting at 1.
	for i, ph := range plan.Phases {
		if ph.Phase != i+1 {
			t.Errorf("phase %d has wrong number %d", i, ph.Phase)
		}
		if ph.Context == "" {
			t.Errorf("phase %d has empty context", ph.Phase)
		}
		if ph.RiskLevel != "low" && ph.RiskLevel != "medium" && ph.RiskLevel != "high" {
			t.Errorf("phase %d has invalid risk level %q", ph.Phase, ph.RiskLevel)
		}
	}
	// Risk levels must not increase as we advance to earlier phases: a later
	// phase must not be safer than an earlier one in risk ordering.
	rank := map[string]int{"low": 0, "medium": 1, "high": 2}
	for i := 1; i < len(plan.Phases); i++ {
		if rank[plan.Phases[i].RiskLevel] < rank[plan.Phases[i-1].RiskLevel] {
			t.Errorf("phases out of order: phase %d (%s) is safer than phase %d (%s)",
				i, plan.Phases[i].RiskLevel, i-1, plan.Phases[i-1].RiskLevel)
		}
	}
}

func TestBridgesDetected(t *testing.T) {
	plan, err := NewAnalyzer(bridgeFixture(t)).Analyze()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Bridges) == 0 {
		t.Fatal("expected at least one coupling bridge from the shared utility")
	}
	for _, b := range plan.Bridges {
		if b.From == "" || b.To == "" {
			t.Errorf("bridge missing endpoints: %+v", b)
		}
		if len(b.Symbols) == 0 {
			t.Errorf("bridge %s -> %s has no symbols", b.From, b.To)
		}
		if b.RiskLevel != "low" && b.RiskLevel != "medium" && b.RiskLevel != "high" {
			t.Errorf("bridge %s has invalid risk level %q", b.From, b.RiskLevel)
		}
	}
}

func TestExtractionPlanSummary(t *testing.T) {
	plan, err := NewAnalyzer(twoPackageFixture(t)).Analyze()
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary == "" {
		t.Fatal("summary should be non-empty")
	}
	want := "Detected 2 bounded contexts"
	if !strings.Contains(plan.Summary, want) {
		t.Errorf("summary should mention context count: %q", plan.Summary)
	}
	if len(plan.Phases) != len(plan.Contexts) {
		t.Errorf("expected one phase per context, got %d phases for %d contexts",
			len(plan.Phases), len(plan.Contexts))
	}
}
