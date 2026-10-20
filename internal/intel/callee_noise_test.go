package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestRenderExploreFiltersNoiseCallees pins the render-layer belt-and-braces:
// a stale index may still carry builtin/type-conversion callees (len, append)
// and builder chains (logger.Info.Int.Dur.Msg) recorded before the
// record-time filter existed; RenderExplore must hide them while keeping
// two-segment qualified callees (db.Do) and real symbols visible.
func TestRenderExploreFiltersNoiseCallees(t *testing.T) {
	rep := &ExploreReport{
		Symbol:     "Target",
		Resolved:   "Target",
		Definition: index.Symbol{Name: "Target", Kind: "func", File: "a.go", Line: 1},
		Callees:    []string{"len", "append", "logger.Info.Int.Dur.Msg", "helper", "db.Do"},
		CalleeConf: map[string]string{
			"len": "EXTRACTED", "append": "EXTRACTED",
			"logger.Info.Int.Dur.Msg": "INFERRED",
			"helper":                  "EXTRACTED", "db.Do": "INFERRED",
		},
	}
	out := RenderExplore(rep)
	for _, want := range []string{"helper", "db.Do"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing preserved callee %q:\n%s", want, out)
		}
	}
	for _, banned := range []string{"len", "append", "logger.Info.Int.Dur.Msg", "== callees (5)"} {
		if strings.Contains(out, banned) {
			t.Errorf("render leaked noise callee %q:\n%s", banned, out)
		}
	}
	if !strings.Contains(out, "== callees (2) ==") {
		t.Errorf("callees count must reflect the filtered list, got:\n%s", out)
	}
}

// TestRenderExploreKeepsNonGoChains pins L5-5: the ≥2-dot chain rule is a
// Go builder-chain heuristic — TS/Python member chains
// (this.service.router.navigate) are ordinary property access and MUST stay
// visible under a non-Go definition, while the same shape under a Go
// definition stays filtered (belt-and-braces).
func TestRenderExploreKeepsNonGoChains(t *testing.T) {
	rep := &ExploreReport{
		Symbol:     "navigate",
		Resolved:   "navigate",
		Definition: index.Symbol{Name: "navigate", Kind: "method", File: "router.ts", Line: 3, Lang: "typescript"},
		Callees:    []string{"this.service.router.navigate", "app.get.use", "helper"},
		CalleeConf: map[string]string{
			"this.service.router.navigate": "INFERRED",
			"app.get.use":                  "INFERRED",
			"helper":                       "EXTRACTED",
		},
	}
	out := RenderExplore(rep)
	for _, want := range []string{"this.service.router.navigate", "app.get.use", "helper"} {
		if !strings.Contains(out, want) {
			t.Errorf("non-Go chain callee %q must stay visible:\n%s", want, out)
		}
	}
	// The same chain under a Go definition stays filtered.
	goRep := &ExploreReport{
		Symbol:     "navigate",
		Resolved:   "navigate",
		Definition: index.Symbol{Name: "navigate", Kind: "func", File: "router.go", Line: 3, Lang: "go"},
		Callees:    []string{"this.service.router.navigate", "helper"},
		CalleeConf: map[string]string{
			"this.service.router.navigate": "INFERRED",
			"helper":                       "EXTRACTED",
		},
	}
	out = RenderExplore(goRep)
	if strings.Contains(out, "this.service.router.navigate") {
		t.Errorf("Go builder chain must stay filtered:\n%s", out)
	}
	if !strings.Contains(out, "helper") {
		t.Errorf("Go render missing preserved callee helper:\n%s", out)
	}
}

// TestRenderExploreFiltersTestingHarnessCallees pins the render-layer half of
// F3-6: a TEST symbol's explore report never lists testing.T method plumbing,
// while the same short-receiver names in a production file survive.
func TestRenderExploreFiltersTestingHarnessCallees(t *testing.T) {
	r := &ExploreReport{
		Definition: sym("func", "TestSubject", "app/subject_test.go", 10),
		Callees:    []string{"t.Parallel", "t.Run", "helper", "t.Fatalf"},
		CalleeConf: map[string]string{"t.Parallel": "EXTRACTED", "t.Run": "EXTRACTED", "helper": "EXTRACTED", "t.Fatalf": "EXTRACTED"},
	}
	out := RenderExplore(r)
	if !strings.Contains(out, "== callees (1) ==") {
		t.Errorf("callees count must reflect the harness-filtered list, got:\n%s", out)
	}
	for _, banned := range []string{"t.Parallel", "t.Run", "t.Fatalf"} {
		if strings.Contains(out, banned+" ") {
			t.Errorf("harness callee %q must be filtered:\n%s", banned, out)
		}
	}
	if !strings.Contains(out, "helper") {
		t.Errorf("real callee must survive:\n%s", out)
	}
	prod := &ExploreReport{
		Definition: sym("func", "Use", "app/use.go", 10),
		Callees:    []string{"t.Run"},
	}
	if out := RenderExplore(prod); !strings.Contains(out, "t.Run") {
		t.Errorf("production t.Run must survive the render filter:\n%s", out)
	}
}
