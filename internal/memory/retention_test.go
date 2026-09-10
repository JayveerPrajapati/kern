package memory

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestParseDurationDays(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"1d", 24 * time.Hour},
		{"0.5d", 12 * time.Hour},
		{"720h", 30 * 24 * time.Hour},
		{"", 0},
		{"0", 0},
	}
	for _, c := range cases {
		got, err := ParseDuration(c.in)
		if err != nil {
			t.Fatalf("ParseDuration(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("ParseDuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if _, err := ParseDuration("not-a-duration"); err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestDurationJSONRoundTrip(t *testing.T) {
	var p RetentionPolicy
	raw := []byte(`{"enabled":true,"max_entries":50,"expire_after":"30d","archive_after":"7d"}`)
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !p.Enabled || p.MaxEntries != 50 {
		t.Fatalf("policy fields wrong: %+v", p)
	}
	if time.Duration(p.ExpireAfter) != 30*24*time.Hour {
		t.Fatalf("expire_after = %v, want 720h", time.Duration(p.ExpireAfter))
	}
	if time.Duration(p.ArchiveAfter) != 7*24*time.Hour {
		t.Fatalf("archive_after = %v, want 168h", time.Duration(p.ArchiveAfter))
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var p2 RetentionPolicy
	if err := json.Unmarshal(out, &p2); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if p2.Enabled != p.Enabled || p2.MaxEntries != p.MaxEntries ||
		p2.ExpireAfter != p.ExpireAfter || p2.ArchiveAfter != p.ArchiveAfter {
		t.Fatalf("round-trip mismatch: %+v vs %+v", p, p2)
	}
}

func mem(id string, age time.Duration) domain.Memory {
	return domain.Memory{
		ID:        id,
		Content:   "memory " + id,
		Type:      domain.MemoryLesson,
		CreatedAt: time.Now().UTC().Add(-age),
	}
}

func TestRetentionExpire(t *testing.T) {
	p := RetentionPolicy{Enabled: true, ExpireAfter: Duration(30 * 24 * time.Hour), MaxEntries: 100}
	ms := []domain.Memory{
		mem("old", 40*24*time.Hour),
		mem("new", time.Hour),
	}
	kept, res := p.Apply(ms, time.Now().UTC())
	if res.Expired != 1 || len(kept) != 1 || kept[0].ID != "new" {
		t.Fatalf("expire: res=%+v kept=%+v", res, kept)
	}
}

func TestRetentionArchive(t *testing.T) {
	p := RetentionPolicy{Enabled: true, ArchiveAfter: Duration(7 * 24 * time.Hour), MaxEntries: 100}
	ms := []domain.Memory{
		mem("old", 10*24*time.Hour),
		mem("recent", time.Hour),
	}
	kept, res := p.Apply(ms, time.Now().UTC())
	if res.Archived != 1 {
		t.Fatalf("archived = %d, want 1", res.Archived)
	}
	if len(kept) != 2 {
		t.Fatalf("archiving keeps memories, got %d", len(kept))
	}
	for _, m := range kept {
		if m.ID == "old" && m.Status != domain.MemoryHistorical {
			t.Fatalf("old memory should be historical, got %q", m.Status)
		}
		if m.ID == "recent" && m.Status == domain.MemoryHistorical {
			t.Fatal("recent memory must not be archived")
		}
	}
}

func TestRetentionTrim(t *testing.T) {
	p := RetentionPolicy{MaxEntries: 2} // size cap applies even when disabled
	ms := []domain.Memory{mem("a", 3*time.Hour), mem("b", 2*time.Hour), mem("c", time.Hour)}
	kept, res := p.Apply(ms, time.Now().UTC())
	if res.Trimmed != 1 || len(kept) != 2 {
		t.Fatalf("trim: res=%+v kept=%d", res, len(kept))
	}
	// Oldest dropped; newest two survive, in input order.
	if kept[0].ID != "b" || kept[1].ID != "c" {
		t.Fatalf("trim kept wrong entries: %+v", kept)
	}
}

func TestRetentionDefaultNoExpiry(t *testing.T) {
	p := DefaultRetention()
	if p.Enabled || p.ExpireAfter != 0 || p.ArchiveAfter != 0 {
		t.Fatalf("default must not expire/archive: %+v", p)
	}
	old := mem("old", 365*24*time.Hour)
	kept, res := p.Apply([]domain.Memory{old}, time.Now().UTC())
	if res.Expired != 0 || res.Archived != 0 || len(kept) != 1 {
		t.Fatalf("default policy must keep everything: res=%+v", res)
	}
}

func TestRetentionFromEnvJSON(t *testing.T) {
	t.Setenv("KERN_MEMORY_RETENTION", `{"enabled":true,"max_entries":10,"expire_after":"60d"}`)
	t.Setenv("KERN_MEMORY_RETENTION_FILE", "")
	p, err := RetentionFromEnv()
	if err != nil {
		t.Fatalf("RetentionFromEnv: %v", err)
	}
	if !p.Enabled || p.MaxEntries != 10 || time.Duration(p.ExpireAfter) != 60*24*time.Hour {
		t.Fatalf("unexpected policy: %+v", p)
	}
}

func TestRetentionFromEnvDefault(t *testing.T) {
	t.Setenv("KERN_MEMORY_RETENTION", "")
	t.Setenv("KERN_MEMORY_RETENTION_FILE", "")
	p, err := RetentionFromEnv()
	if err != nil {
		t.Fatalf("RetentionFromEnv: %v", err)
	}
	if p.Enabled || p.MaxEntries != maxTypedEntries {
		t.Fatalf("expected default policy, got %+v", p)
	}
}

func TestApplyRetentionOnStore(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	trail := NewAuditTrail()

	// Seed memories while governance is permissive (no expiry), so the stale
	// entry is persisted.
	permissive := &Governance{
		Access:    NewAccessControl(DefaultPolicy()),
		Audit:     trail,
		Retention: DefaultRetention(),
	}
	s := NewMemoryStore(dir).WithGovernance(permissive)
	if _, err := s.Add(domain.Memory{Content: "fresh", Type: domain.MemoryLesson, Source: "a"}); err != nil {
		t.Fatal(err)
	}
	stale := domain.Memory{Content: "stale", Type: domain.MemoryLesson, Source: "a",
		CreatedAt: time.Now().UTC().Add(-60 * 24 * time.Hour)}
	if _, err := s.Add(stale); err != nil {
		t.Fatal(err)
	}

	// Switch to an expiring policy and sweep.
	s.WithGovernance(&Governance{
		Access:    NewAccessControl(DefaultPolicy()),
		Audit:     trail,
		Retention: RetentionPolicy{Enabled: true, ExpireAfter: Duration(30 * 24 * time.Hour), MaxEntries: 100},
	})
	res, err := s.ApplyRetention()
	if err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	if res.Expired != 1 {
		t.Fatalf("expected 1 expired, got %+v", res)
	}
	ms, _ := s.List("")
	if len(ms) != 1 || ms[0].Content != "fresh" {
		t.Fatalf("store should keep only the fresh memory: %+v", ms)
	}
	// The sweep is audited.
	if got := trail.FilterByOperation(OpRetention); len(got) != 1 || !got[0].Allowed {
		t.Fatalf("expected one allowed retention audit event, got %+v", got)
	}
}

func TestAutoExpireOnWrite(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	gov := &Governance{
		Access:    NewAccessControl(DefaultPolicy()),
		Audit:     NewAuditTrail(),
		Retention: RetentionPolicy{Enabled: true, ExpireAfter: Duration(24 * time.Hour), MaxEntries: 100},
	}
	s := NewMemoryStore(t.TempDir()).WithGovernance(gov)

	// An already-expired memory is dropped the moment it is written, because
	// every save enforces the retention policy.
	stale := domain.Memory{Content: "ancient", Type: domain.MemoryLesson,
		CreatedAt: time.Now().UTC().Add(-10 * 24 * time.Hour)}
	if _, err := s.Add(stale); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(domain.Memory{Content: "current", Type: domain.MemoryLesson}); err != nil {
		t.Fatal(err)
	}
	ms, _ := s.List("")
	for _, m := range ms {
		if m.Content == "ancient" {
			t.Fatal("expired memory surfaced despite retention policy")
		}
	}
	if len(ms) != 1 {
		t.Fatalf("expected only the current memory, got %+v", ms)
	}
}
