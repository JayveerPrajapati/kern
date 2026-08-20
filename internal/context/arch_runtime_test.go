package context

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// boundaryIndex builds a graph with a web handler that calls into a db store —
// the classic forbidden "web -> db" architecture boundary.
func boundaryIndex() *index.Index {
	return &index.Index{
		Root: "/fake",
		Symbols: []index.Symbol{
			sym("func", "WebHandler", "web/handler.go", 1),
			sym("func", "DBStore", "db/store.go", 1),
		},
		Calls: map[string][]string{
			"WebHandler": {"DBStore"},
		},
		Callers: map[string][]string{
			"DBStore": {"WebHandler"},
		},
		UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func policyByID(policies []domain.Policy, id string) bool {
	for _, p := range policies {
		if p.ID == id {
			return true
		}
	}
	return false
}

// TestArchitectureRulesFromGovernance proves the ArchitectureRules field is
// populated from the governance policies relevant to the change scope.
func TestArchitectureRulesFromGovernance(t *testing.T) {
	e := testEngine(t)
	pkt, err := e.AnalyzeChange("Foo")
	if err != nil {
		t.Fatalf("AnalyzeChange(Foo): %v", err)
	}
	if len(pkt.ArchitectureRules) == 0 {
		t.Fatal("ArchitectureRules empty; want at least the source_write policy")
	}
	// The default firewall's source_write policy must surface for a source change.
	if !policyByID(pkt.ArchitectureRules, "pol-source-write") {
		t.Errorf("ArchitectureRules missing pol-source-write; got %+v", pkt.ArchitectureRules)
	}
}

// TestArchitectureRulesBoundaryCrossed verifies that a change crossing a
// forbidden dependency boundary surfaces the boundary rule.
func TestArchitectureRulesBoundaryCrossed(t *testing.T) {
	g := intelligence.FromIndex(boundaryIndex())
	fw := governance.NewFirewall().WithAgents(governance.NewAgent(
		engineAgent, "Context Engine", "planner",
		[]governance.Permission{{Resource: "source", Action: "write"}},
	))
	e := NewEngine("/fake", &g, nil, fw).
		WithBoundaryProvider(func() []domain.Policy {
			return []domain.Policy{{
				ID:      "boundary-web-db",
				Name:    "boundary:web->db",
				Rule:    "forbid web -> db",
				Scope:   "all",
				Enabled: true,
			}}
		})

	pkt, err := e.AnalyzeChange("WebHandler")
	if err != nil {
		t.Fatalf("AnalyzeChange(WebHandler): %v", err)
	}
	if !policyByID(pkt.ArchitectureRules, "boundary-web-db") {
		t.Errorf("ArchitectureRules missing crossed boundary; got %+v", pkt.ArchitectureRules)
	}
}

// TestRuntimeEvidenceEmptyWhenNoSource verifies RuntimeEvidence is an empty
// (non-nil) slice when no runtime source is wired.
func TestRuntimeEvidenceEmptyWhenNoSource(t *testing.T) {
	e := testEngine(t)
	pkt, err := e.AnalyzeChange("Foo")
	if err != nil {
		t.Fatalf("AnalyzeChange(Foo): %v", err)
	}
	if pkt.RuntimeEvidence == nil {
		t.Fatal("RuntimeEvidence nil; want empty non-nil slice")
	}
	if len(pkt.RuntimeEvidence) != 0 {
		t.Fatalf("RuntimeEvidence = %d entries, want 0", len(pkt.RuntimeEvidence))
	}
}

// TestRuntimeEvidencePopulatesWhenSource proves the field populates from the
// runtime source when one is provided.
func TestRuntimeEvidencePopulatesWhenSource(t *testing.T) {
	g := intelligence.FromIndex(boundaryIndex())
	fw := governance.NewFirewall().WithAgents(governance.NewAgent(
		engineAgent, "Context Engine", "planner",
		[]governance.Permission{{Resource: "source", Action: "write"}},
	))

	src := runtime.NewStore()
	src.Ingest(runtime.Event{
		ID:        "e1",
		Type:      runtime.EventError,
		Service:   "web",
		Severity:  "error",
		Message:   "handler timeout",
		Timestamp: time.Now(),
	})
	// Irrelevant event for another service must not leak in.
	src.Ingest(runtime.Event{
		ID:       "e2",
		Type:     runtime.EventError,
		Service:  "other",
		Severity: "error",
		Message:  "unrelated",
	})

	e := NewEngine("/fake", &g, nil, fw).WithRuntimeSource(src)
	pkt, err := e.AnalyzeChange("WebHandler")
	if err != nil {
		t.Fatalf("AnalyzeChange(WebHandler): %v", err)
	}
	if len(pkt.RuntimeEvidence) == 0 {
		t.Fatal("RuntimeEvidence empty; want error event from runtime source")
	}
	for _, ev := range pkt.RuntimeEvidence {
		if ev.Type != domain.EvidenceRuntime {
			t.Errorf("evidence type = %q, want runtime", ev.Type)
		}
	}
}
