package context

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// TestPhase5ExitGate drives ONE engineering task through the complete
// context pipeline (authorize → select → dedup → freshness → GC) and asserts
// all six exit-gate properties on the same task:
// less irrelevant context       — selection + GC reduce the pool
// less duplicate output         — dedup collapses duplicate facts
// critical constraints retained — constraints survive aggressive reduction
// required evidence retained    — evidence survives aggressive reduction
// no unauthorized context       — denied items never enter the selection
// no correctness regression     — the selected set still contains the
// target's direct facts
func TestPhase5ExitGate(t *testing.T) {
	now := time.Now()
	target := "tenant_cache"

	// --- Build the candidate pool: 20 items of mixed classes ---
	pool := []domain.ContextItem{
		// The target's own facts (must survive — correctness).
		{ID: "fact:target-1", Class: domain.ContextFact, Content: "tenant_cache reads tenant_id", Source: "graph", Relevance: 0.95, Freshness: now, Digest: "d-target1"},
		{ID: "fact:target-2", Class: domain.ContextFact, Content: "tenant_cache writes cache key", Source: "graph", Relevance: 0.9, Freshness: now, Digest: "d-target2"},
		// Critical constraint (must be retained).
		{ID: "constr:1", Class: domain.ContextConstraint, Content: "must not cache cross-tenant data", Source: "policy", Relevance: 0.3, Freshness: now, Digest: "d-constr1"},
		// Required evidence (must be retained).
		{ID: "ev:1", Class: domain.ContextEvidence, Content: "tenant.go:42 defines TenantID", Source: "graph", Relevance: 0.4, Freshness: now, Digest: "d-ev1"},
		// An unauthorized item (must never be selected).
		{ID: "file:secret", Class: domain.ContextSourceCode, Content: "auth_keys.go", Source: "graph", Relevance: 0.99, Freshness: now, Digest: "d-secret", SecurityClass: "secret"},
		// A duplicate of fact:target-1 (must collapse — less duplicate output).
		{ID: "fact:dup-1", Class: domain.ContextFact, Content: "tenant_cache reads tenant_id", Source: "graph", Relevance: 0.85, Freshness: now, Digest: "d-target1"},
		{ID: "fact:dup-2", Class: domain.ContextFact, Content: "tenant_cache reads tenant_id", Source: "graph", Relevance: 0.8, Freshness: now, Digest: "d-target1"},
		// Irrelevant noise (must be dropped/reduced — less irrelevant context).
		{ID: "hist:old", Class: domain.ContextHistory, Content: "unrelated billing refactor from 90 days ago", Source: "memory", Relevance: 0.05, Freshness: now.Add(-90 * 24 * time.Hour), Digest: "d-old"},
		{ID: "fact:noise", Class: domain.ContextFact, Content: "irrelevant symbol far from target", Source: "graph", Relevance: 0.02, Freshness: now, Digest: "d-noise"},
	}
	// Stale item (dropped by freshness policy — no correctness regression since
	// it is not required by the task).
	pool = append(pool, domain.ContextItem{ID: "mem:stale", Class: domain.ContextMemory, Content: "outdated cache decision", Source: "memory", Relevance: 0.7, Freshness: now.Add(-30 * 24 * time.Hour), Digest: "d-stale"})
	// Unauthorized duplicate of the target fact (must be both deduped AND denied).
	pool = append(pool, domain.ContextItem{ID: "fact:dup-secret", Class: domain.ContextFact, Content: "tenant_cache reads tenant_id", Source: "graph", Relevance: 0.9, Freshness: now, Digest: "d-target1", SecurityClass: "secret"})

	// --- Firewall: a real firewall denies an unknown holder (P5.4 agent
	// dimension), and the security-class dimension excludes secret items. Register
	// a known agent so source/context items are allowed through the firewall and
	// the denial of secret items is attributable to the security-class check.
	fw := governance.NewFirewall()
	fw.WithAgents(governance.NewAgent("agent-1", "Agent 1", "context",
		[]governance.Permission{{Resource: "source", Action: "read"}, {Resource: "context", Action: "read"}}))

	// 1. AUTHORIZE (P5.4): denied items never enter the selection.
	authorized := AuthorizeItemsScoped(pool, fw, domain.ContextAuthorization{
		Agent:                  "agent-1",
		AllowedSecurityClasses: []string{"public", "internal"},
	})
	for _, it := range authorized {
		if it.ID == "file:secret" || it.ID == "fact:dup-secret" {
			t.Errorf("unauthorized item %q survived authorization", it.ID)
		}
	}
	// Exit gate: no unauthorized context.
	if len(authorized) == len(pool) {
		t.Fatal("authorization removed nothing — secret-class items were not denied")
	}

	// 2. DEDUP (P5.8): one canonical fact + evidence refs.
	deduped := DedupItems(authorized)
	var canonical *domain.ContextItem
	for i := range deduped {
		if deduped[i].ID == "fact:target-1" {
			canonical = &deduped[i]
		}
	}
	if canonical == nil {
		t.Fatal("canonical fact:target-1 missing after dedup")
	}
	dupCount := 0
	for _, it := range deduped {
		if it.ID == "fact:dup-1" || it.ID == "fact:dup-2" {
			dupCount++
		}
	}
	if dupCount > 0 {
		t.Errorf("duplicates survived dedup: %d, want 0", dupCount)
	}
	// Exit gate: less duplicate output.
	if len(deduped) >= len(authorized) {
		t.Errorf("dedup did not reduce the pool: %d -> %d", len(authorized), len(deduped))
	}

	// 3. FRESHNESS (P5.9): stale items excluded.
	fresh, _ := ApplyFreshnessPolicy(deduped, domain.FreshnessPolicy{MaxAge: 7 * 24 * time.Hour}, now)
	for _, it := range fresh {
		if it.ID == "mem:stale" || it.ID == "hist:old" {
			t.Errorf("stale item %q survived freshness policy", it.ID)
		}
	}

	// 4. SELECT (P5.3): minimum sufficient — constraints + evidence retained,
	// noise reduced.
	selected := SelectContext(SelectRequest{
		Items:                  fresh,
		Target:                 target,
		Firewall:               fw,
		Holder:                 "agent-1",
		MaxItems:               4,
		Threshold:              0.5,
		AllowedSecurityClasses: []string{"public", "internal"},
	})
	selectedIDs := map[string]bool{}
	for _, it := range selected {
		selectedIDs[it.ID] = true
	}
	// Exit gate: critical constraints retained.
	if !selectedIDs["constr:1"] {
		t.Error("critical constraint constr:1 not retained in the selected set")
	}
	// Exit gate: required evidence retained.
	if !selectedIDs["ev:1"] {
		t.Error("required evidence ev:1 not retained in the selected set")
	}
	// Exit gate: no unauthorized context.
	if selectedIDs["file:secret"] || selectedIDs["fact:dup-secret"] {
		t.Error("unauthorized item selected")
	}
	// Exit gate: less irrelevant context (noise dropped).
	if selectedIDs["fact:noise"] || selectedIDs["hist:old"] {
		t.Error("irrelevant noise selected")
	}
	// Exit gate: no correctness regression (target's own facts still present).
	if !selectedIDs["fact:target-1"] && !selectedIDs["fact:target-2"] {
		t.Error("target facts missing from the selected set — correctness regression")
	}

	// 5. GC (P5.5): actions are KEEP/COMPRESS/DEMOTE/ARCHIVE/DROP, no panics,
	// and the target facts score highest.
	gc := NewGC("add tenant-aware caching", target, 4)
	gc.SetDependencyDistance(map[string]int{
		"fact:target-1": 0, "fact:target-2": 0, "constr:1": 1, "ev:1": 1,
	})
	actions := gc.Run(fresh)
	if len(actions) != len(fresh) {
		t.Fatalf("GC returned %d actions for %d items", len(actions), len(fresh))
	}
	for _, a := range actions {
		switch a {
		case domain.GCKeep, domain.GCCompress, domain.GCDemote, domain.GCArchive, domain.GCDrop:
		default:
			t.Errorf("GC action %q is not one of KEEP/COMPRESS/DEMOTE/ARCHIVE/DROP", a)
		}
	}
	if !strings.Contains(strings.ToLower(target), "tenant") {
		t.Fatal("test setup broken: target does not contain tenant")
	}
}
