package modernization

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
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

// TestBridgeLimitEnv verifies KERN_BRIDGES_LIMIT controls the bridge cap:
// unset -> default, positive -> that cap, 0 -> unlimited (mapped to
// math.MaxInt because intel.Bridges coerces limit<=0 to its own default of
// 15), garbage/negative -> default.
func TestBridgeLimitEnv(t *testing.T) {
	t.Setenv("KERN_BRIDGES_LIMIT", "")
	if got := bridgeLimit(); got != defaultBridgeLimit {
		t.Errorf("unset: expected %d, got %d", defaultBridgeLimit, got)
	}
	t.Setenv("KERN_BRIDGES_LIMIT", "500")
	if got := bridgeLimit(); got != 500 {
		t.Errorf("explicit: expected 500, got %d", got)
	}
	t.Setenv("KERN_BRIDGES_LIMIT", "0")
	if got := bridgeLimit(); got != math.MaxInt {
		t.Errorf("unlimited: expected math.MaxInt, got %d", got)
	}
	t.Setenv("KERN_BRIDGES_LIMIT", "bogus")
	if got := bridgeLimit(); got != defaultBridgeLimit {
		t.Errorf("garbage: expected %d, got %d", defaultBridgeLimit, got)
	}
	t.Setenv("KERN_BRIDGES_LIMIT", "-7")
	if got := bridgeLimit(); got != defaultBridgeLimit {
		t.Errorf("negative: expected %d, got %d", defaultBridgeLimit, got)
	}
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

// TestExtractionPhaseCarriesOwnership verifies ownership is derived
// for bounded contexts and propagated to their extraction phases.
func TestExtractionPhaseCarriesOwnership(t *testing.T) {
	ix := twoPackageFixture(t)
	a := NewAnalyzer(ix)
	plan, err := a.Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(plan.Contexts) == 0 {
		t.Fatal("expected contexts")
	}
	// Every context with an undeterminable owner is empty, but the field must
	// be present; and any phase must carry its context's ownership.
	if len(plan.Phases) > 0 {
		for _, ph := range plan.Phases {
			ctx := contextByName(plan, ph.Context)
			if ctx != nil && ctx.Ownership != ph.Ownership {
				t.Errorf("phase %s ownership %q != context ownership %q", ph.Context, ph.Ownership, ctx.Ownership)
			}
		}
	}
}

func contextByName(plan *ExtractionPlan, name string) *BoundedContext {
	for i := range plan.Contexts {
		if plan.Contexts[i].Name == name {
			return &plan.Contexts[i]
		}
	}
	return nil
}

func TestContextDependenciesAndOwnershipPopulated(t *testing.T) {
	ix := twoPackageFixture(t)
	a := NewAnalyzer(ix)
	plan, err := a.Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	for _, ctx := range plan.Contexts {
		if len(ctx.Dependencies) == 0 && ctx.OutgoingDeps > 0 {
			t.Errorf("context %s has outgoing deps %d but empty Dependencies list (12.2)", ctx.Name, ctx.OutgoingDeps)
		}
		if ctx.Ownership == "" {
			t.Logf("context %s has no determinable owner (acceptable)", ctx.Name)
		}
	}
}

func TestModernizationTotalExtent(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Name: "fn1", File: "pkg/a.go"},
			{Name: "fn2", File: "pkg/a.go"},
			{Name: "fn3", File: "pkg/b.go"},
		},
	}
	contexts := []BoundedContext{
		{
			Name:    "ctx1",
			Symbols: []string{"fn1", "fn2", "fn3"},
		},
	}
	symbols, files := totalExtent(contexts, ix)
	if symbols != 3 {
		t.Errorf("expected 3 symbols, got %d", symbols)
	}
	if files != 2 {
		t.Errorf("expected 2 distinct files, got %d", files)
	}
}

func TestModernizationDisambiguateNames(t *testing.T) {
	comms := []intel.Community{
		{
			ID:       "comm-1",
			Packages: []string{"services/auth/wrapper"},
			Hub:      "TokenValidator",
		},
		{
			ID:       "comm-2",
			Packages: []string{"services/http/wrapper"},
			Hub:      "CorsFilter",
		},
		{
			ID:       "comm-3",
			Packages: []string{"services/http/wrapper"},
			Hub:      "AuthFilter",
		},
		{
			ID:       "comm-4",
			Packages: []string{"services/unique"},
		},
	}

	names := deriveContextNames(comms)
	if names["comm-4"] != "unique" {
		t.Errorf("expected unique name 'unique', got %q", names["comm-4"])
	}
	if names["comm-1"] == names["comm-2"] || names["comm-2"] == names["comm-3"] || names["comm-1"] == names["comm-3"] {
		t.Errorf("expected unique names for duplicate wrapper packages: %v", names)
	}
	if !strings.Contains(names["comm-1"], "auth/wrapper") {
		t.Errorf("expected parent path in comm-1 name, got %q", names["comm-1"])
	}
	if !strings.Contains(names["comm-2"], "CorsFilter") {
		t.Errorf("expected hub in comm-2 name, got %q", names["comm-2"])
	}
}

func TestModernizationWithPathPrefix(t *testing.T) {
	files := map[string]string{
		"project_a/orders/orders.go":   ordersPkg,
		"project_a/billing/billing.go": billingPkg,
		"project_b/other/other.go": `package other
func RunOther() int { return 42 }
`,
	}
	ix := build(t, files)

	// Without prefix, both project_a and project_b symbols are in index
	planAll, err := NewAnalyzer(ix).Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(planAll.Contexts) == 0 {
		t.Fatalf("expected contexts in un-scoped plan")
	}

	// With prefix "project_a", project_b symbols must not be included
	planA, err := NewAnalyzer(ix).WithPathPrefix("project_a").Analyze()
	if err != nil {
		t.Fatalf("Analyze with prefix: %v", err)
	}
	for _, ctx := range planA.Contexts {
		for _, sym := range ctx.Symbols {
			if strings.Contains(sym, "RunOther") || strings.Contains(sym, "other") {
				t.Errorf("expected project_b symbol to be excluded with project_a prefix, found %s in context %s", sym, ctx.Name)
			}
		}
	}
}

// TestExtractionPlanPhaseCap verifies the QA finding fix: a repo with more
// bounded contexts than maxPhases must yield a plan capped at maxPhases
// phases (not one phase per context), while the analysis stays honest —
// TotalContexts reports the full context count and the summary notes the
// consolidation. Phases must remain risk-ordered: sequential numbers and
// non-decreasing risk rank from phase 0 (Phases[0] stays the safest).
func TestExtractionPlanPhaseCap(t *testing.T) {
	// 22 independent packages plus a shared utility called by pkg20 and
	// pkg21: 23 bounded contexts, one of which (shared) carries coupling
	// bridges and is therefore the highest-risk extraction candidate.
	files := map[string]string{}
	for i := 0; i < 22; i++ {
		pkg := fmt.Sprintf("pkg%02d", i)
		upper := fmt.Sprintf("A%02d", i)
		lower := fmt.Sprintf("B%02d", i)
		files[pkg+"/"+pkg+".go"] = fmt.Sprintf(`package %s

func %s() int { return %s() }

func %s() int { return 1 }
`, pkg, upper, lower, lower)
	}
	files["pkg20/pkg20.go"] = `package pkg20

import "shared"

func A20() int { return B20() + shared.Util(1) }

func B20() int { return 1 }
`
	files["pkg21/pkg21.go"] = `package pkg21

import "shared"

func A21() int { return B21() + shared.Util(2) }

func B21() int { return 1 }
`
	files["shared/shared.go"] = `package shared

func Util(v int) int { return helper(v) }

func helper(v int) int { return v }
`

	plan, err := NewAnalyzer(build(t, files)).Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	// The analysis stays honest: all 23 contexts are still reported.
	if len(plan.Contexts) != 23 {
		t.Fatalf("expected 23 contexts, got %d", len(plan.Contexts))
	}
	if plan.TotalContexts != 23 {
		t.Errorf("expected TotalContexts=23, got %d", plan.TotalContexts)
	}

	// The plan is capped: 23 contexts consolidate into at most maxPhases
	// phases, and the consolidation is called out in the summary.
	if len(plan.Phases) > maxPhases {
		t.Errorf("expected at most %d phases, got %d", maxPhases, len(plan.Phases))
	}
	if len(plan.Phases) >= len(plan.Contexts) {
		t.Errorf("expected consolidation below %d contexts, got %d phases", len(plan.Contexts), len(plan.Phases))
	}
	if !strings.Contains(plan.Summary, "consolidated") {
		t.Errorf("expected consolidation note in summary, got %q", plan.Summary)
	}

	// Phases remain risk-ordered: sequential numbers starting at 1, and risk
	// rank never decreases (a later phase is never safer than an earlier one).
	rank := map[string]int{"low": 0, "medium": 1, "high": 2}
	for i, ph := range plan.Phases {
		if ph.Phase != i+1 {
			t.Errorf("phase %d has wrong number %d", i, ph.Phase)
		}
		if ph.RiskLevel != "low" && ph.RiskLevel != "medium" && ph.RiskLevel != "high" {
			t.Errorf("phase %d has invalid risk level %q", ph.Phase, ph.RiskLevel)
		}
		if i > 0 && rank[ph.RiskLevel] < rank[plan.Phases[i-1].RiskLevel] {
			t.Errorf("phases out of order: phase %d (%s) is safer than phase %d (%s)",
				i, ph.RiskLevel, i-1, plan.Phases[i-1].RiskLevel)
		}
	}

	// The consolidated leading phase lists every merged context id, and each
	// id resolves to a real bounded context.
	first := plan.Phases[0]
	if len(first.Contexts) == 0 {
		t.Errorf("expected consolidated leading phase to list merged contexts, got none")
	}
	for _, name := range first.Contexts {
		if contextByName(plan, name) == nil {
			t.Errorf("consolidated phase lists unknown context %q", name)
		}
	}

	// The riskiest context (shared, the bridge owner) keeps its own final
	// phase, so the plan still ends on the highest-risk extraction.
	last := plan.Phases[len(plan.Phases)-1]
	if last.Context != "shared" {
		t.Errorf("expected final phase to extract shared, got %q", last.Context)
	}
	if last.RiskLevel != "medium" {
		t.Errorf("expected final phase risk level medium, got %q", last.RiskLevel)
	}
}

// TestBuildPhasesUnderCap verifies the phase builder keeps one phase per
// context when contexts do not exceed maxPhases (no consolidation).
func TestBuildPhasesUnderCap(t *testing.T) {
	contexts := make([]BoundedContext, 5)
	bridgeCount := map[string]int{}
	order := make([]int, len(contexts))
	for i := range contexts {
		name := fmt.Sprintf("ctx-%d", i)
		contexts[i] = BoundedContext{Name: name, Symbols: []string{name + ".sym"}}
		bridgeCount[name] = i // ascending risk: ctx-0 safest
		order[i] = i
	}
	phases := buildPhases(order, contexts, bridgeCount, nil)
	if len(phases) != len(contexts) {
		t.Fatalf("expected one phase per context below cap, got %d phases for %d contexts",
			len(phases), len(contexts))
	}
	for i, ph := range phases {
		if ph.Phase != i+1 {
			t.Errorf("phase %d has wrong number %d", i, ph.Phase)
		}
		if ph.Context != contexts[order[i]].Name {
			t.Errorf("phase %d extracts %q, want %q", ph.Phase, ph.Context, contexts[order[i]].Name)
		}
		if len(ph.Contexts) != 0 {
			t.Errorf("phase %d should not list merged contexts, got %v", ph.Phase, ph.Contexts)
		}
	}
}
