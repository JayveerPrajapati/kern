package context

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// clearanceTestEngine builds an Engine whose memory store is seeded with
// lessons at three classification levels (unclassified, internal, restricted),
// all scoped to "Foo". When withClearance is true the engine is configured with
// clearance 1 (internal); otherwise it uses the zero-value (legacy) config.
func clearanceTestEngine(t *testing.T, withClearance bool) *Engine {
	t.Helper()

	mem := memory.NewMemoryStore(t.TempDir())
	for _, m := range []domain.Memory{
		{Type: domain.MemoryLesson, Content: "Foo: unclassified lesson", Scope: "Foo", Source: "human", Classification: ""},
		{Type: domain.MemoryLesson, Content: "Foo: internal lesson", Scope: "Foo", Source: "human", Classification: domain.ClassificationInternal},
		{Type: domain.MemoryLesson, Content: "Foo: restricted lesson", Scope: "Foo", Source: "human", Classification: domain.ClassificationRestricted},
	} {
		if _, err := mem.Add(m); err != nil {
			t.Fatalf("add memory: %v", err)
		}
	}

	fw := governance.NewFirewall().WithAgents(governance.NewAgent(
		engineAgent, "Context Engine", "planner",
		[]governance.Permission{{Resource: "source", Action: "write"}},
	))

	g := intelligence.FromIndex(fakeIndex())
	e := NewEngine("/fake", &g, mem, fw)
	if withClearance {
		e.WithClearance(1) // internal — restricted (level 3) must be filtered
	}
	return e
}

// TestEngineClearanceFiltersUnauthorizedMemory verifies AUD-13: with a
// clearance set, retrieval goes through the authorization-filtered path and a
// memory classified above the caller's clearance is excluded from the packet.
func TestEngineClearanceFiltersUnauthorizedMemory(t *testing.T) {
	e := clearanceTestEngine(t, true)
	pkt, err := e.AnalyzeChange("Foo")
	if err != nil {
		t.Fatalf("AnalyzeChange: %v", err)
	}

	var classes []string
	for _, m := range pkt.Memory {
		classes = append(classes, m.Classification)
		if m.Classification == domain.ClassificationRestricted {
			t.Errorf("restricted memory leaked through clearance=1 (internal): %+v", m)
		}
	}
	if !hasMemoryClass(pkt.Memory, "") {
		t.Errorf("unclassified memory should survive clearance=1; got classes %v", classes)
	}
	if !hasMemoryClass(pkt.Memory, domain.ClassificationInternal) {
		t.Errorf("internal memory should survive clearance=1; got classes %v", classes)
	}
}

// TestEngineNoClearanceGoldenBehavior verifies AUD-13 back-compat: with no
// clearance the engine uses the legacy plain Recall, so classification is not
// enforced and the results are identical to before (all memories untouched).
func TestEngineNoClearanceGoldenBehavior(t *testing.T) {
	e := clearanceTestEngine(t, false)
	pkt, err := e.AnalyzeChange("Foo")
	if err != nil {
		t.Fatalf("AnalyzeChange: %v", err)
	}

	if len(pkt.Memory) != 3 {
		t.Errorf("golden: no-clearance recall returned %d memories, want 3 (unfiltered)", len(pkt.Memory))
	}
	if !hasMemoryClass(pkt.Memory, domain.ClassificationRestricted) {
		t.Errorf("golden: restricted memory should be present without clearance (legacy path unchanged)")
	}
}

func hasMemoryClass(ms []domain.Memory, class string) bool {
	for _, m := range ms {
		if m.Classification == class {
			return true
		}
	}
	return false
}
