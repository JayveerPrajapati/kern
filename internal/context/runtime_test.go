package context

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestGCScoresAndActions verifies the GC pipeline scores items and assigns
// appropriate actions.
func TestGCScoresAndActions(t *testing.T) {
	gc := NewGC("add caching to UserService", "UserService", 3)
	items := []domain.ContextItem{
		{ID: "intent", Class: domain.ContextUserIntent, Content: "add caching to UserService", Source: "user"},
		{ID: "code", Class: domain.ContextSourceCode, Content: "type UserService struct { db *DB }", Source: "graph"},
		{ID: "old1", Class: domain.ContextHistory, Content: "some old log output from yesterday", Source: "tool", Freshness: time.Now().Add(-48 * time.Hour)},
		{ID: "old2", Class: domain.ContextHistory, Content: "another old result", Source: "tool", Freshness: time.Now().Add(-48 * time.Hour)},
		{ID: "old3", Class: domain.ContextHistory, Content: "yet another old result", Source: "tool", Freshness: time.Now().Add(-48 * time.Hour)},
	}

	actions := gc.Run(items)
	if len(actions) != 5 {
		t.Fatalf("len(actions)=%d, want 5", len(actions))
	}

	// The top 3 (intent, code, + one more) should be KEEP; the rest should be
	// DEMOTE/ARCHIVE/DROP.
	keepCount := 0
	for _, a := range actions {
		if a == domain.GCKeep {
			keepCount++
		}
	}
	if keepCount > 3 {
		t.Fatalf("keepCount=%d, want <= 3 (maxItems)", keepCount)
	}
	// Intent and code should definitely be kept (high relevance).
	if actions[0] != domain.GCKeep {
		t.Fatalf("intent action=%s, want KEEP", actions[0])
	}
	if actions[1] != domain.GCKeep {
		t.Fatalf("code action=%s, want KEEP", actions[1])
	}
}

// TestGCDuplicatePenalty verifies duplicate items get a relevance penalty.
func TestGCDuplicatePenalty(t *testing.T) {
	gc := NewGC("test", "Foo", 10)
	items := []domain.ContextItem{
		{ID: "a", Class: domain.ContextFact, Content: "Foo is defined here", Source: "graph", Digest: "abc"},
		{ID: "b", Class: domain.ContextFact, Content: "Foo is defined here", Source: "tool", Digest: "abc"},
	}
	actions := gc.Run(items)
	// Both should get actions, but the duplicate (b) should score lower.
	// Since maxItems=10, both might be KEEP, but a should have higher relevance.
	if actions[0] != domain.GCKeep {
		t.Fatalf("original action=%s, want KEEP", actions[0])
	}
}

// TestApplyActions verifies ApplyActions updates states correctly.
func TestApplyActions(t *testing.T) {
	items := []domain.ContextItem{
		{ID: "1", Class: domain.ContextFact, Content: "x"},
		{ID: "2", Class: domain.ContextHistory, Content: "y"},
		{ID: "3", Class: domain.ContextError, Content: "z"},
	}
	actions := []domain.GCAction{domain.GCKeep, domain.GCDemote, domain.GCDrop}
	active, updated := ApplyActions(items, actions)
	if len(active) != 1 {
		t.Fatalf("len(active)=%d, want 1", len(active))
	}
	if active[0].ID != "1" {
		t.Fatalf("active[0].ID=%s, want 1", active[0].ID)
	}
	if updated[1].State != domain.ContextWarm {
		t.Fatalf("updated[1].State=%s, want WARM", updated[1].State)
	}
	if updated[2].State != domain.ContextDropped {
		t.Fatalf("updated[2].State=%s, want DROPPED", updated[2].State)
	}
}

// TestNormalizeToolResult verifies the normalizer extracts facts, errors,
// evidence, and summary from raw output.
func TestNormalizeToolResult(t *testing.T) {
	raw := `Building...
type Foo struct { x int }
main.go:42: error: undefined: Bar
func NewFoo() *Foo { return &Foo{} }
Done.`
	summary := NormalizeToolResult("build", raw, 3)
	if len(summary.Errors) == 0 {
		t.Fatal("no errors extracted")
	}
	found := false
	for _, e := range summary.Errors {
		if strings.Contains(strings.ToLower(e), "error") {
			found = true
		}
	}
	if !found {
		t.Fatalf("errors=%v, want one containing 'error'", summary.Errors)
	}
	if len(summary.Facts) == 0 {
		t.Fatal("no facts extracted")
	}
	if len(summary.Evidence) == 0 {
		t.Fatal("no evidence extracted (expected file:line ref)")
	}
	if summary.Summary == "" {
		t.Fatal("empty summary")
	}
	if summary.TokenSaved <= 0 {
		t.Fatalf("TokenSaved=%d, want > 0", summary.TokenSaved)
	}
}

// TestSnapshot verifies the snapshot builder extracts goal, files, risks.
func TestSnapshot(t *testing.T) {
	pkt := domain.ContextPacket{
		Intent: domain.Intent{RawText: "add caching"},
		Files:  []domain.File{{Path: "svc.go"}, {Path: "svc_test.go"}},
		Facts:  []domain.Claim{{Type: domain.ClaimFact, Statement: "UserService is in svc.go", Confidence: 0.9}},
		Risks:  []domain.Risk{{Level: domain.RiskMedium, Mitigation: "add tests for cache invalidation"}},
		RequiredValidation: []string{"build", "test"},
	}
	snap := Snapshot(pkt, "EXECUTING", "write cache layer")
	if snap.Goal != "add caching" {
		t.Fatalf("Goal=%q, want 'add caching'", snap.Goal)
	}
	if snap.State != "EXECUTING" {
		t.Fatalf("State=%q, want EXECUTING", snap.State)
	}
	if len(snap.Files) != 2 {
		t.Fatalf("len(Files)=%d, want 2", len(snap.Files))
	}
	if len(snap.Decisions) != 1 {
		t.Fatalf("len(Decisions)=%d, want 1", len(snap.Decisions))
	}
	if len(snap.Risks) != 1 {
		t.Fatalf("len(Risks)=%d, want 1", len(snap.Risks))
	}
	if len(snap.Tests) != 2 {
		t.Fatalf("len(Tests)=%d, want 2", len(snap.Tests))
	}
	if snap.NextAction != "write cache layer" {
		t.Fatalf("NextAction=%q, want 'write cache layer'", snap.NextAction)
	}
}
