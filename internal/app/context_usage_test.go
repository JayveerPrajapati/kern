package app

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// usagePacket returns a ContextPacket carrying every measured slice kind with
// known member sets, plus a zero-member slice that must be skipped.
func usagePacket() *domain.ContextPacket {
	return &domain.ContextPacket{
		Task: "task-usage-1",
		Files: []domain.File{
			{Path: "internal/web/server.go"},
			{Path: "internal/web/handler.go"},
			{Path: "internal/db/store.go"},
		},
		Symbols: []domain.Symbol{
			{Name: "NewServer", File: "internal/web/server.go"},
			{Name: "Listen", File: "internal/web/server.go"},
			{Name: "Query", File: "internal/db/store.go"},
		},
		Memory: []domain.Memory{
			{Scope: "web.NewServer"},
			{Scope: "db"},
			{Scope: "unrelated-service"},
		},
		Incidents: []domain.Memory{
			{Scope: "incident:web"},
			{Scope: "incident:payments"},
		},
		RuntimeEvidence: []domain.Evidence{
			{Source: "runtime"},
			{Source: "internal/web/server.go"},
		},
		ArchitectureRules: []domain.Policy{
			{ID: "p-web", Scope: "web"},
			{ID: "p-db", Scope: "db"},
		},
		// Zero-member slice: must be skipped by ComputeSliceUsage.
		Risks: nil,
	}
}

// TestComputeSliceUsageMatchesFilesAndSymbols proves the deterministic
// matching rules: used files match File.Path and Symbol.File, used symbols
// match Symbol.Name, memory/incidents/architecture match by Scope, and
// runtime evidence matches by Source. Zero-member slices are skipped.
func TestComputeSliceUsageMatchesFilesAndSymbols(t *testing.T) {
	pkt := usagePacket()
	usedFiles := []string{"internal/web/server.go"}
	usedSymbols := []string{"NewServer", "db"}
	records := ComputeSliceUsage(pkt, usedFiles, usedSymbols)
	if len(records) != 6 {
		t.Fatalf("records = %d, want 6 (zero-member slices skipped)", len(records))
	}
	got := map[string]domain.ContextUsageRecord{}
	for _, r := range records {
		got[r.Slice] = r
	}
	// files: 1 of 3 (server.go used).
	if r := got["files"]; r.Members != 3 || r.Used != 1 {
		t.Errorf("files = %+v, want Members=3 Used=1", r)
	}
	// symbols: NewServer by name, Listen by its defining file (server.go is
	// in usedFiles), Query unused -> 2 of 3.
	if r := got["symbols"]; r.Members != 3 || r.Used != 2 {
		t.Errorf("symbols = %+v, want Members=3 Used=2", r)
	}
	// memory: "web.NewServer" (scope contains NewServer) and "db" (matches
	// symbol "db") -> 2 of 3.
	if r := got["memory"]; r.Members != 3 || r.Used != 2 {
		t.Errorf("memory = %+v, want Members=3 Used=2", r)
	}
	// incidents: "incident:web" contains "web"? usedFiles has no "web" alone;
	// "db" is not in the incident scopes -> 0 of 2.
	if r := got["incidents"]; r.Members != 2 || r.Used != 0 {
		t.Errorf("incidents = %+v, want Members=2 Used=0", r)
	}
	// runtime_evidence: Source "internal/web/server.go" in usedFiles -> 1 of 2.
	if r := got["runtime_evidence"]; r.Members != 2 || r.Used != 1 {
		t.Errorf("runtime_evidence = %+v, want Members=2 Used=1", r)
	}
	// architecture_rules: "p-db" by name in usedSymbols AND "p-web" by Scope
	// ("web" is a substring of used file "internal/web/server.go") -> 2 of 2.
	if r := got["architecture_rules"]; r.Members != 2 || r.Used != 2 {
		t.Errorf("architecture_rules = %+v, want Members=2 Used=2", r)
	}
	// Every record carries the packet's task and an empty outcome (the
	// caller stamps it).
	for slice, r := range got {
		if r.Task != "task-usage-1" {
			t.Errorf("%s: Task = %q, want task-usage-1", slice, r.Task)
		}
		if r.Outcome != "" {
			t.Errorf("%s: Outcome = %q, want empty (caller stamps)", slice, r.Outcome)
		}
	}
	// Nil packet yields no records (nil-guard).
	if got := ComputeSliceUsage(nil, usedFiles, usedSymbols); got != nil {
		t.Fatalf("nil packet produced records: %+v", got)
	}
}

// TestRecordContextUsageWritesRecommendation proves the full learning pass:
// enough zero-usage records for one slice kind write exactly one
// RECOMMENDATION typed-claim memory through the learning path (deterministic
// "shrink" statement, "context:<slice>" scope, task IDs as provenance), and a
// second identical batch upserts the same scope instead of duplicating
// (idempotent).
func TestRecordContextUsageWritesRecommendation(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := make([]domain.ContextUsageRecord, 0, DefaultContextUsageThreshold)
	for i := 0; i < DefaultContextUsageThreshold; i++ {
		records = append(records, domain.ContextUsageRecord{
			Task:    "task-" + string(rune('a'+i)),
			Slice:   "runtime_evidence",
			Members: 3,
			Used:    0,
			Outcome: "analyze",
			At:      base.Add(time.Duration(i) * time.Hour),
		})
	}
	root := t.TempDir()
	mem := memory.NewMemoryStore(root)
	n, err := RecordContextUsage(records, mem, DefaultContextUsageThreshold)
	if err != nil {
		t.Fatalf("RecordContextUsage: %v", err)
	}
	if n != 1 {
		t.Fatalf("written = %d, want 1", n)
	}
	mems, err := mem.List(domain.MemoryConstraint)
	if err != nil {
		t.Fatalf("memory List: %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("memories = %d, want 1", len(mems))
	}
	m := mems[0]
	if m.ClaimType != domain.ClaimRecommendation {
		t.Errorf("ClaimType = %q, want RECOMMENDATION", m.ClaimType)
	}
	if m.Scope != "context:runtime_evidence" {
		t.Errorf("Scope = %q, want context:runtime_evidence", m.Scope)
	}
	if !strings.Contains(m.Content, "context slice runtime_evidence unused in 5/5 tasks") {
		t.Errorf("Content = %q, want the shrink statement", m.Content)
	}
	if !strings.Contains(m.Provenance, "task task-a (analyze)") {
		t.Errorf("Provenance = %q, want the contributing task IDs", m.Provenance)
	}
	// Second identical batch: accumulator grows to 10 records, still 0%
	// usage, so the same scope is upserted — no duplicate memory.
	n2, err := RecordContextUsage(records, mem, DefaultContextUsageThreshold)
	if err != nil {
		t.Fatalf("RecordContextUsage (2nd): %v", err)
	}
	if n2 != 1 {
		t.Fatalf("written (2nd) = %d, want 1 (upsert)", n2)
	}
	mems, err = mem.List(domain.MemoryConstraint)
	if err != nil {
		t.Fatalf("memory List (2nd): %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("memories (2nd) = %d, want 1 (idempotent upsert, no duplicate)", len(mems))
	}
}

// TestRecordContextUsageNilGuard proves a nil memory store is a no-op that
// never panics (0, nil) — unwired paths keep their zero behavior change.
func TestRecordContextUsageNilGuard(t *testing.T) {
	records := []domain.ContextUsageRecord{
		{Task: "t1", Slice: "files", Members: 2, Used: 0, Outcome: "analyze", At: time.Now()},
	}
	n, err := RecordContextUsage(records, nil, DefaultContextUsageThreshold)
	if err != nil {
		t.Fatalf("nil mem returned error: %v", err)
	}
	if n != 0 {
		t.Fatalf("nil mem written = %d, want 0", n)
	}
}
