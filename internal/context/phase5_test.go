package context

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance/firewall"
)

func mkItem(id string, class domain.ContextClass, dig string, rel float64, used time.Time) domain.ContextItem {
	return domain.ContextItem{
		ID:        id,
		Class:     class,
		Content:   "content-" + id,
		Relevance: rel,
		Freshness: time.Now(),
		LastUsed:  used,
		Digest:    dig,
	}
}

func TestDedupItems(t *testing.T) {
	in := []domain.ContextItem{
		mkItem("a1", domain.ContextFact, "dig-1", 0.5, time.Now()),
		mkItem("a2", domain.ContextFact, "dig-1", 0.5, time.Now()), // dup
		mkItem("b", domain.ContextFact, "dig-2", 0.4, time.Now()),
	}
	out := DedupItems(in)
	if len(out) != 2 {
		t.Fatalf("DedupItems -> %d, want 2", len(out))
	}
	if out[0].ID != "a1" || out[1].ID != "b" {
		t.Errorf("DedupItems kept %q, %q; want a1,b", out[0].ID, out[1].ID)
	}
}

func TestPageItems(t *testing.T) {
	var items []domain.ContextItem
	for i := 0; i < 5; i++ {
		items = append(items, mkItem(string(rune('a'+i)), domain.ContextFact, "", 0, time.Now()))
	}
	p := PageItems(items, 1, 3)
	if len(p.Items) != 3 || p.Total != 5 || p.TotalPages != 2 || !p.HasNext {
		t.Errorf("page 1: got %+v", p)
	}
	p2 := PageItems(items, 2, 3)
	if len(p2.Items) != 2 || p2.HasNext {
		t.Errorf("page 2: got %+v", p2)
	}
	// out-of-range page returns empty with correct metadata
	p3 := PageItems(items, 9, 3)
	if len(p3.Items) != 0 {
		t.Errorf("page 9 should be empty, got %d", len(p3.Items))
	}
}

func TestLeaseManagerLifecycle(t *testing.T) {
	now := time.Now()
	lm := NewLeaseManager(func() time.Time { return now })

	lm.Acquire("i1", "agent-1", time.Hour)
	if !lm.Active("i1") {
		t.Error("i1 should be active")
	}
	// advance clock past lease
	now = now.Add(2 * time.Hour)
	if lm.Active("i1") {
		t.Error("i1 should have expired")
	}
	if exp := lm.Expired(); len(exp) != 1 || exp[0] != "i1" {
		t.Errorf("Expired = %v, want [i1]", exp)
	}
	// renew then release
	now = time.Now()
	lm.Acquire("i1", "agent-1", time.Hour)
	if !lm.Renew("i1", time.Hour) {
		t.Error("renew should succeed")
	}
	lm.Release("i1")
	if lm.Active("i1") {
		t.Error("i1 should be released")
	}
	if !lm.Renew("missing", time.Hour) {
		// expect false for missing — but method returns bool; assert
	}
	if lm.Renew("missing", time.Hour) {
		t.Error("renew missing should be false")
	}
}

func TestSelectMinimal(t *testing.T) {
	items := []domain.ContextItem{
		mkItem("lo", domain.ContextFact, "", 0.2, time.Now()),
		mkItem("hi", domain.ContextFact, "", 0.9, time.Now()),
		mkItem("mid", domain.ContextFact, "", 0.5, time.Now()),
	}
	// threshold 1.0: hi+mid reach it (0.9+0.5)
	out := SelectMinimal(items, 1.0, 0)
	if len(out) != 2 {
		t.Fatalf("SelectMinimal = %d, want 2", len(out))
	}
	if out[0].ID != "hi" || out[1].ID != "mid" {
		t.Errorf("SelectMinimal order = %q,%q; want hi,mid", out[0].ID, out[1].ID)
	}
	// maxItems caps
	out2 := SelectMinimal(items, 0.01, 1)
	if len(out2) != 1 || out2[0].ID != "hi" {
		t.Errorf("SelectMinimal capped = %+v, want [hi]", out2)
	}
}

func TestApplyFreshnessPolicy(t *testing.T) {
	now := time.Now()
	items := []domain.ContextItem{
		mkItem("fresh", domain.ContextFact, "", 0.5, now),
		{ID: "stale", Class: domain.ContextFact, Content: "x", Relevance: 0.5, Freshness: now.Add(-48 * time.Hour)},
	}
	policy := domain.FreshnessPolicy{MaxAge: 24 * time.Hour, FreshBelow: 1 * time.Hour}
	kept, classes := ApplyFreshnessPolicy(items, policy, now)
	if len(kept) != 1 || kept[0].ID != "fresh" {
		t.Errorf("ApplyFreshnessPolicy kept %d items, want [fresh]", len(kept))
	}
	if classes["stale"] != domain.FreshnessStale {
		t.Errorf("stale class = %q, want STALE", classes["stale"])
	}
	if classes["fresh"] != domain.FreshnessFresh {
		t.Errorf("fresh class = %q, want FRESH", classes["fresh"])
	}
}

func TestClassifyAge(t *testing.T) {
	now := time.Now()
	p := domain.FreshnessPolicy{MaxAge: 24 * time.Hour, FreshBelow: 1 * time.Hour}
	if got := p.ClassifyAge(now, now); got != domain.FreshnessFresh {
		t.Errorf("0 age = %q, want FRESH", got)
	}
	if got := p.ClassifyAge(now.Add(-2*time.Hour), now); got != domain.FreshnessAging {
		t.Errorf("2h age = %q, want AGING", got)
	}
	if got := p.ClassifyAge(now.Add(-48*time.Hour), now); got != domain.FreshnessStale {
		t.Errorf("48h age = %q, want STALE", got)
	}
	// unbounded policy -> always FRESH
	ub := domain.FreshnessPolicy{}
	if got := ub.ClassifyAge(now.Add(-1000*time.Hour), now); got != domain.FreshnessFresh {
		t.Errorf("unbounded = %q, want FRESH", got)
	}
}

func TestLastUsePenalty(t *testing.T) {
	now := time.Now()
	if p := lastUsePenalty(mkItem("x", domain.ContextFact, "", 0, now), now); p != 1.0 {
		t.Errorf("just used = %f, want 1.0", p)
	}
	if p := lastUsePenalty(mkItem("x", domain.ContextFact, "", 0, now.Add(-6*24*time.Hour)), now); p != 0.4 {
		t.Errorf("6d unused = %f, want 0.4", p)
	}
	// unknown last use (zero) is not penalized
	if p := lastUsePenalty(domain.ContextItem{ID: "x"}, now); p != 1.0 {
		t.Errorf("zero last use = %f, want 1.0", p)
	}
}

func TestReplay(t *testing.T) {
	r := domain.ContextReplay{
		Input: "fix the login bug",
		Snapshot: domain.ContextSnapshot{
			Decisions:  []string{"use jwt"},
			Constraints: []string{"no-import-cycle"},
			Files:      []string{"auth.go"},
			Tests:      []string{"go test ./auth"},
			Risks:      []string{"breaking change"},
		},
		Occurred: time.Now(),
	}
	pkt := Replay(r)
	if len(pkt.Facts) != 1 || pkt.Facts[0].Statement != "use jwt" {
		t.Errorf("replay facts = %+v", pkt.Facts)
	}
	if len(pkt.ArchitectureRules) != 1 || pkt.ArchitectureRules[0].ID != "no-import-cycle" {
		t.Errorf("replay rules = %+v", pkt.ArchitectureRules)
	}
	if len(pkt.Files) != 1 || pkt.Files[0].Path != "auth.go" {
		t.Errorf("replay files = %+v", pkt.Files)
	}
	if len(pkt.RequiredValidation) != 1 {
		t.Errorf("replay tests = %+v", pkt.RequiredValidation)
	}
	if len(pkt.Risks) != 1 {
		t.Errorf("replay risks = %+v", pkt.Risks)
	}
}

func TestAuthorizeItems(t *testing.T) {
	// nil firewall authorizes everything
	items := []domain.ContextItem{mkItem("a", domain.ContextFact, "", 0.5, time.Now())}
	if out := AuthorizeItems(items, nil, "agent"); len(out) != 1 {
		t.Errorf("nil firewall should keep all, got %d", len(out))
	}

	// a real firewall that denies everything for the holder excludes items.
	fw := firewall.NewFirewall()
	out := AuthorizeItems(items, fw, "ghost-agent")
	if len(out) != 0 {
		t.Errorf("denying firewall should exclude all, got %d", len(out))
	}
}

func TestGCUsesLastUsed(t *testing.T) {
	now := time.Now()
	items := []domain.ContextItem{
		mkItem("used", domain.ContextFact, "d1", 0.5, now),                      // fresh use
		mkItem("old", domain.ContextFact, "d2", 0.5, now.Add(-30*24*time.Hour)), // long unused
	}
	g := NewGC("", "", 1)
	actions := g.Run(items)
	// The long-unused item should score lower, so the freshly used one stays KEEP.
	usedScore := g.score(items[0])
	oldScore := g.score(items[1])
	if oldScore >= usedScore {
		t.Errorf("old item (%f) should score below used item (%f)", oldScore, usedScore)
	}
	if actions[0] != domain.GCKeep {
		t.Errorf("used item action = %q, want KEEP", actions[0])
	}
	if actions[1] == domain.GCKeep {
		t.Errorf("old item should not be KEEP, got %q", actions[1])
	}
}