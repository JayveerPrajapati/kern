package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func newTestStore(t *testing.T) *MemoryStore {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	return NewMemoryStore(t.TempDir())
}

func sampleMemories() []domain.Memory {
	return []domain.Memory{
		{
			Type:    domain.MemorySemantic,
			Content: "the auth service caches session tokens in redis",
			Source:  "human",
			Scope:   "service:AuthService",
			Tags:    []string{"auth", "cache"},
		},
		{
			Type:    domain.MemoryDecision,
			Content: "we chose postgres over mysql for the payment store",
			Source:  "human",
			Scope:   "service:PaymentService",
			Tags:    []string{"database", "decision"},
		},
		{
			Type:    domain.MemoryIncident,
			Content: "payment timeout spikes every friday under load",
			Source:  "agent",
			Scope:   "incident:INC-123",
			Tags:    []string{"payment", "timeout"},
		},
		{
			Type:    domain.MemoryConstraint,
			Content: "redis keys must contain the tenant id prefix",
			Source:  "human",
			Scope:   "service:BillingService",
			Tags:    []string{"redis", "tenant"},
		},
	}
}

func TestStoreAdd(t *testing.T) {
	s := newTestStore(t)
	m, err := s.Add(domain.Memory{Type: domain.MemoryDecision, Content: "use go ast"})
	if err != nil {
		t.Fatal(err)
	}
	if m.ID == "" {
		t.Fatal("Add should set an ID")
	}
	if m.CreatedAt.IsZero() || m.UpdatedAt.IsZero() {
		t.Fatal("Add should set timestamps")
	}
	// Persisted to disk.
	if _, err := os.Stat(s.path); err != nil {
		t.Fatalf("store file not persisted: %v", err)
	}
}

func TestStoreAddEmptyIgnored(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Add(domain.Memory{Type: domain.MemorySemantic, Content: "   "}); err != nil {
		t.Fatal(err)
	}
	got, err := s.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("empty content should not be stored, got %d", len(got))
	}
}

func TestStoreListAllAndByType(t *testing.T) {
	s := newTestStore(t)
	for _, m := range sampleMemories() {
		if _, err := s.Add(m); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("List all: got %d, want 4", len(all))
	}
	dec, err := s.List(domain.MemoryDecision)
	if err != nil {
		t.Fatal(err)
	}
	if len(dec) != 1 || dec[0].Type != domain.MemoryDecision {
		t.Fatalf("List by type: got %+v", dec)
	}
}

func TestStoreGet(t *testing.T) {
	s := newTestStore(t)
	m, _ := s.Add(sampleMemories()[0])
	got, err := s.Get(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != m.ID || got.Content != m.Content {
		t.Fatalf("Get mismatch: %+v vs %+v", got, m)
	}
	if _, err := s.Get("does-not-exist"); err != os.ErrNotExist {
		t.Fatalf("Get missing: want os.ErrNotExist, got %v", err)
	}
}

func TestStoreDelete(t *testing.T) {
	s := newTestStore(t)
	m, _ := s.Add(sampleMemories()[0])
	if err := s.Delete(m.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.List(""); len(got) != 0 {
		t.Fatalf("expected empty after delete, got %d", len(got))
	}
	if err := s.Delete(m.ID); err != os.ErrNotExist {
		t.Fatalf("deleting twice: want ErrNotExist, got %v", err)
	}
}

func TestStoreUpdate(t *testing.T) {
	s := newTestStore(t)
	m, _ := s.Add(sampleMemories()[0])
	up, err := s.Update(m.ID, "new content", []string{"x", "y"})
	if err != nil {
		t.Fatal(err)
	}
	if up.Content != "new content" || len(up.Tags) != 2 {
		t.Fatalf("Update mismatch: %+v", up)
	}
	if up.UpdatedAt.Before(m.UpdatedAt) {
		t.Fatal("UpdatedAt should move forward")
	}
	if _, err := s.Update("nope", "c", nil); err != os.ErrNotExist {
		t.Fatalf("Update missing: want ErrNotExist, got %v", err)
	}
}

func TestStoreRecallByText(t *testing.T) {
	s := newTestStore(t)
	for _, m := range sampleMemories() {
		s.Add(m)
	}
	got, err := s.Recall(Query{Text: "redis session token"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("expected some recalls")
	}
	if !strings.Contains(got[0].Content, "redis") {
		t.Fatalf("expected redis memory first, got %q", got[0].Content)
	}
}

func TestStoreRecallByType(t *testing.T) {
	s := newTestStore(t)
	for _, m := range sampleMemories() {
		s.Add(m)
	}
	got, _ := s.Recall(Query{Type: domain.MemoryIncident})
	if len(got) != 1 || got[0].Type != domain.MemoryIncident {
		t.Fatalf("by type: got %+v", got)
	}
}

func TestStoreRecallByScope(t *testing.T) {
	s := newTestStore(t)
	for _, m := range sampleMemories() {
		s.Add(m)
	}
	got, _ := s.Recall(Query{Scope: "service:BillingService"})
	// Query.Scope currently only filters exact scope equality is not applied
	// in Recall filtering; it only influences scoring. All memories remain.
	if len(got) != 4 {
		t.Fatalf("scope query keeps all for scoring, got %d", len(got))
	}
}

func TestStoreRecallByService(t *testing.T) {
	s := newTestStore(t)
	for _, m := range sampleMemories() {
		s.Add(m)
	}
	got, _ := s.Recall(Query{Service: "PaymentService"})
	if len(got) != 1 || got[0].Scope != "service:PaymentService" {
		t.Fatalf("service:PaymentService want 1 (decision), got %+v", got)
	}
	for _, m := range got {
		if !strings.HasPrefix(m.Scope, "service:") {
			t.Fatalf("non-service memory leaked: %+v", m)
		}
	}
}

func TestStoreRecallByIncident(t *testing.T) {
	s := newTestStore(t)
	for _, m := range sampleMemories() {
		s.Add(m)
	}
	got, _ := s.Recall(Query{Incident: "INC-123"})
	if len(got) != 1 || got[0].Scope != "incident:INC-123" {
		t.Fatalf("incident query: got %+v", got)
	}
}

func TestStoreRecallByTags(t *testing.T) {
	s := newTestStore(t)
	for _, m := range sampleMemories() {
		s.Add(m)
	}
	got, _ := s.Recall(Query{Tags: []string{"redis"}})
	if len(got) != 1 || got[0].Tags[0] != "redis" {
		t.Fatalf("tags redis: got %d", len(got))
	}
}

func TestStoreRecallLimit(t *testing.T) {
	s := newTestStore(t)
	for _, m := range sampleMemories() {
		s.Add(m)
	}
	got, _ := s.Recall(Query{Limit: 2})
	if len(got) != 2 {
		t.Fatalf("limit 2: got %d", len(got))
	}
}

func TestStoreRecallBySubject(t *testing.T) {
	s := newTestStore(t)
	s.Add(domain.Memory{Type: domain.MemorySemantic, Content: "charge flow caches in redis", Subject: "PaymentService", Provenance: "loop:learn", RelatedEntities: []string{"symbol:Charge", "task:T-42"}})
	s.Add(domain.Memory{Type: domain.MemorySemantic, Content: "billing keys carry tenant prefix", Subject: "BillingService", Provenance: "human"})

	got, err := s.Recall(Query{Subject: "PaymentService"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Subject != "PaymentService" {
		t.Fatalf("subject query: got %+v", got)
	}
}

func TestStoreRecallByRelatedEntities(t *testing.T) {
	s := newTestStore(t)
	s.Add(domain.Memory{Type: domain.MemorySemantic, Content: "auth flow", Subject: "AuthService", RelatedEntities: []string{"symbol:Login", "pr:12"}})
	s.Add(domain.Memory{Type: domain.MemorySemantic, Content: "billing flow", Subject: "BillingService", RelatedEntities: []string{"symbol:Charge"}})

	got, err := s.Recall(Query{RelatedEntities: []string{"PR:12"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].RelatedEntities) != 2 {
		t.Fatalf("related entities query: got %+v", got)
	}
}

func TestStoreRecallByProvenance(t *testing.T) {
	s := newTestStore(t)
	s.Add(domain.Memory{Type: domain.MemorySemantic, Content: "learned in loop", Provenance: "loop:learn"})
	s.Add(domain.Memory{Type: domain.MemorySemantic, Content: "told by human", Provenance: "human"})

	got, err := s.Recall(Query{Provenance: "human"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Provenance != "human" {
		t.Fatalf("provenance query: got %+v", got)
	}
}

func TestMatchScoreSubjectBoost(t *testing.T) {
	m := domain.Memory{Content: "x", Subject: "PaymentService"}
	q := Query{Subject: "PaymentService"}
	if MatchScore(m, q) == 0 {
		t.Fatal("subject match should add score")
	}
}

func TestStoreRecallDeterministic(t *testing.T) {
	s := newTestStore(t)
	for _, m := range sampleMemories() {
		s.Add(m)
	}
	a, _ := s.Recall(Query{Text: "payment"})
	b, _ := s.Recall(Query{Text: "payment"})
	if len(a) != len(b) {
		t.Fatalf("recall not deterministic")
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("recall ordering changed at %d", i)
		}
	}
}

func TestMatchScoreExactTagBeatsText(t *testing.T) {
	tagMatch := domain.Memory{Type: domain.MemorySemantic, Content: "unrelated text", Tags: []string{"auth"}}
	textOnly := domain.Memory{Type: domain.MemorySemantic, Content: "auth cache handling", Tags: []string{"other"}}
	q := Query{Text: "auth", Tags: []string{"auth"}}
	if MatchScore(tagMatch, q) <= MatchScore(textOnly, q) {
		t.Fatalf("exact tag match should score higher: tag=%d text=%d", MatchScore(tagMatch, q), MatchScore(textOnly, q))
	}
}

func TestMatchScoreEntityBoost(t *testing.T) {
	m := domain.Memory{Scope: "service:PaymentService", Content: "x", Tags: []string{"db"}}
	q := Query{Entity: "PaymentService"}
	if MatchScore(m, q) == 0 {
		t.Fatal("entity match should add score")
	}
}

func TestStorePersistenceReopen(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	s := NewMemoryStore(root)
	m, _ := s.Add(sampleMemories()[0])
	// Reopen a fresh store over the same root — data must survive.
	s2 := NewMemoryStore(root)
	got, err := s2.Get(m.ID)
	if err != nil {
		t.Fatalf("data lost on reopen: %v", err)
	}
	if got.Content != m.Content {
		t.Fatalf("content mismatch after reopen")
	}
}

func TestStoreUsesSeparateFile(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	s := NewMemoryStore(root)

	// v1 lesson store path.
	lessonPath := Path(root)
	if lessonPath == "" {
		t.Fatal("v1 Path returned empty")
	}
	if s.path == lessonPath {
		t.Fatal("new store must use a different file than v1 memory.json")
	}
	if filepath.Base(filepath.Dir(s.path)) != "ememory" {
		t.Fatalf("new store should live under ememory/, got %s", s.path)
	}
	// v1 uses memory/ namespace.
	if !strings.Contains(filepath.ToSlash(lessonPath), "memory/") {
		t.Fatalf("v1 lesson path should live under memory/, got %s", lessonPath)
	}
}

func TestV1LessonAPIStillWorks(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()

	// Use the typed store.
	ms := NewMemoryStore(root)
	if _, err := ms.Add(domain.Memory{Type: domain.MemoryDecision, Content: "typed memory entry"}); err != nil {
		t.Fatal(err)
	}
	typed, _ := ms.List("")
	if len(typed) != 1 {
		t.Fatalf("typed store should have 1 entry, got %d", len(typed))
	}

	// Use the v1 lesson API — must still work independently.
	if err := Add(root, "v1 lesson"); err != nil {
		t.Fatal(err)
	}
	ls := List(root)
	if len(ls) != 1 || ls[0].Text != "v1 lesson" {
		t.Fatalf("v1 List broken: %+v", ls)
	}
	// The two stores must not interfere.
	if got, _ := ms.List(""); len(got) != 1 {
		t.Fatalf("typed store polluted by v1 Add, got %d", len(got))
	}
}

func TestStoreFileNamesSeparateOnDisk(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	ms := NewMemoryStore(root)
	ms.Add(domain.Memory{Type: domain.MemorySemantic, Content: "typed"})
	Add(root, "lesson")

	// Distinct filepaths, both present on disk.
	if s, err := os.Stat(ms.path); err != nil || s.IsDir() {
		t.Fatalf("typed store file missing: %v", err)
	}
	if _, err := os.Stat(Path(root)); err != nil {
		t.Fatalf("v1 memory file missing: %v", err)
	}
	if filepath.Clean(ms.path) == filepath.Clean(Path(root)) {
		t.Fatal("typed and v1 stores share a file")
	}
}

func TestAuthorizedRecallFiltersByClassification(t *testing.T) {
	s := NewMemoryStore(t.TempDir())
	s.Add(domain.Memory{ID: "m1", Content: "public memo", Classification: "public"})
	s.Add(domain.Memory{ID: "m2", Content: "internal memo", Classification: "internal"})
	s.Add(domain.Memory{ID: "m3", Content: "confidential memo", Classification: "confidential"})
	s.Add(domain.Memory{ID: "m4", Content: "restricted memo", Classification: "restricted"})

	// Clearance 0 (public): should see only public + unclassified
	got, _ := s.AuthorizedRecall(Query{}, "agent-low", 0)
	if len(got) != 1 || got[0].ID != "m1" {
		t.Errorf("clearance 0: got %v, want [m1]", got)
	}

	// Clearance 2 (confidential): should see public + internal + confidential
	got, _ = s.AuthorizedRecall(Query{}, "agent-mid", 2)
	if len(got) != 3 {
		t.Errorf("clearance 2: got %d memories, want 3", len(got))
	}

	// Clearance 3 (restricted): should see all
	got, _ = s.AuthorizedRecall(Query{}, "agent-high", 3)
	if len(got) != 4 {
		t.Errorf("clearance 3: got %d memories, want 4", len(got))
	}
}

func TestMemoryReasonField(t *testing.T) {
	m := domain.Memory{
		ID:      "d1",
		Type:    domain.MemoryDecision,
		Content: "Use separate transactions for Payment and Order",
		Reason:  "Avoid distributed transaction coupling",
	}
	if m.Reason != "Avoid distributed transaction coupling" {
		t.Errorf("Reason = %q", m.Reason)
	}
}

func TestStoreSupersede(t *testing.T) {
	s := NewMemoryStore(t.TempDir())
	m1, _ := s.Add(domain.Memory{Type: domain.MemoryConstraint, Scope: "svc:x", Content: "use redis for cache"})
	// Supersede with a newer memory of the same scope+type.
	m2, err := s.Supersede(domain.Memory{Type: domain.MemoryConstraint, Scope: "svc:x", Content: "use dynamodb for cache"})
	if err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	if m2.Status != domain.MemoryCurrent {
		t.Errorf("new memory status = %q, want current", m2.Status)
	}
	old, _ := s.Get(m1.ID)
	if old.Status != domain.MemorySuperseded {
		t.Errorf("old memory status = %q, want superseded (15.4)", old.Status)
	}
	// CurrentMemories must exclude the superseded one.
	cur, _ := s.CurrentMemories(domain.MemoryConstraint)
	if len(cur) != 1 || cur[0].ID != m2.ID {
		t.Errorf("current memories = %+v, want only the new one", cur)
	}
}

func TestStoreMarkHistorical(t *testing.T) {
	s := NewMemoryStore(t.TempDir())
	m, _ := s.Add(domain.Memory{Type: domain.MemoryLesson, Content: "lesson", Scope: "svc"})
	if err := s.MarkHistorical(m.ID); err != nil {
		t.Fatalf("MarkHistorical: %v", err)
	}
	cur, _ := s.CurrentMemories(domain.MemoryLesson)
	if len(cur) != 0 {
		t.Errorf("historical memory should be excluded from current: %+v", cur)
	}
	got, err := s.Get(m.ID)
	if err != nil || got.Status != domain.MemoryHistorical {
		t.Errorf("memory status = %q, want historical (15.4)", got.Status)
	}
}
