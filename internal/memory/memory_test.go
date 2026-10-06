package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestAddAndList(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	_ = Add(root, "first lesson")
	_ = Add(root, "second lesson")
	ls := List(root)
	if len(ls) != 2 {
		t.Fatalf("got %d entries, want 2", len(ls))
	}
	// Most recent first.
	if ls[0].Text != "second lesson" || ls[1].Text != "first lesson" {
		t.Fatalf("ordering wrong: %+v", ls)
	}
}

func TestPersistence(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	_ = Add(root, "lesson")
	ls := List(root)
	if len(ls) != 1 || ls[0].Text != "lesson" {
		t.Fatalf("did not persist: %+v", ls)
	}
}

func TestCap(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	for i := 0; i < maxEntries+10; i++ {
		_ = Add(root, strings.Repeat("x", i))
	}
	ls := List(root)
	if len(ls) != maxEntries {
		t.Fatalf("got %d entries, want cap %d", len(ls), maxEntries)
	}
}

func TestEmptyIgnored(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := Add(root, "  "); err != nil {
		t.Fatal(err)
	}
	if ls := List(root); len(ls) != 0 {
		t.Fatalf("empty lesson should be ignored, got %+v", ls)
	}
}

func TestClear(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	_ = Add(root, "lesson")
	if err := Clear(root); err != nil {
		t.Fatal(err)
	}
	if ls := List(root); len(ls) != 0 {
		t.Fatalf("expected cleared, got %+v", ls)
	}
}

func TestTimestampSet(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	_ = Add(root, "lesson")
	e := List(root)[0]
	if e.Time.IsZero() {
		t.Fatal("timestamp not set")
	}
	if e.Time.Location() != time.UTC {
		t.Fatal("timestamps should be UTC")
	}
}

func TestRecallRanksByOverlap(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	_ = Add(root, "the fastapi session stores the bearer token in a signed cookie")
	_ = Add(root, "validate the bearer token in a fastapi dependency")
	_ = Add(root, "use go ast to extract struct fields")
	got := Recall(root, "how does the fastapi session handle bearer tokens?", 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 recalls, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Text, "fastapi") {
		t.Fatalf("expected fastapi lesson first, got %q", got[0].Text)
	}
}

func TestRecallEmpty(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if got := Recall(t.TempDir(), "anything here", 3); len(got) != 0 {
		t.Fatalf("expected no recalls, got %+v", got)
	}
}

func TestRecallKCap(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	for _, l := range []string{
		"handle jwt expiry in the api middleware",
		"jwt refresh lives in the api layer",
		"jwt rotation must log in the api",
	} {
		_ = Add(root, l)
	}
	got := Recall(root, "jwt api middleware", 2)
	if len(got) > 2 {
		t.Fatalf("expected at most 2, got %d", len(got))
	}
}

func TestAutoCaptureLabeledAndExcludedFromRecall(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()

	if err := Add(root, "User: refactor the auth middleware to use the new session store"); err != nil {
		t.Fatalf("Add prompt: %v", err)
	}
	if err := Add(root, "Edited internal/api/handlers.go"); err != nil {
		t.Fatalf("Add outcome: %v", err)
	}
	if err := Add(root, "Command failed: go build ./..."); err != nil {
		t.Fatalf("Add failure: %v", err)
	}
	if err := Add(root, "always store the session id in the auth cookie"); err != nil {
		t.Fatalf("Add lesson: %v", err)
	}
	if err := AddAuto(root, "User: auto-tagged prompt about the billing slice"); err != nil {
		t.Fatalf("AddAuto: %v", err)
	}

	// All three capture kinds are labeled auto (the explicit AddAuto one and
	// the prefix-classified ones).
	for _, e := range List(root) {
		switch {
		case e.Source == "auto" && strings.HasPrefix(e.Text, "always"):
			t.Fatalf("deliberate lesson mislabeled auto: %+v", e)
		case e.Source != "auto" && (strings.HasPrefix(e.Text, "User:") || strings.HasPrefix(e.Text, "Edited") || strings.HasPrefix(e.Text, "Command failed")):
			t.Fatalf("capture %q not labeled auto (source=%q)", e.Text, e.Source)
		case e.Source == "" && strings.HasPrefix(e.Text, "always"):
			// deliberate lesson: fine
		case e.Source == "auto":
			// capture: fine
		default:
			t.Fatalf("unexpected source %q for %q", e.Source, e.Text)
		}
	}

	// Recall must not surface any auto capture, even for an exact prompt match.
	got := Recall(root, "refactor the auth middleware with the new session store", 10)
	for _, e := range got {
		if e.Source == "auto" {
			t.Fatalf("Recall leaked an auto capture: %+v", e)
		}
	}

	// The deliberate lesson must still recall.
	got = Recall(root, "session id stored in the auth cookie", 10)
	found := false
	for _, e := range got {
		if strings.HasPrefix(e.Text, "always") {
			found = true
		}
	}
	if !found {
		t.Fatal("deliberate lesson should be recallable while auto captures are excluded")
	}
}

// TestAutoCaptureMigration: entries persisted before Source existed carry no
// field; Load must backfill the auto label from the capture prefixes.
// TestRecallDeterministicTies: scoring is unchanged, but ties must resolve
// deterministically — score desc, then recency desc (newer first), then the
// entry text as the final key.
func TestRecallDeterministicTies(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Both lessons score identically for the query (same token set); the
	// newer one must rank first on the recency tiebreak.
	_ = writeJSON(Path(root), Store{Root: root, Entries: []Entry{
		{Time: base, Text: "alpha beta gamma delta"},
		{Time: base.Add(time.Hour), Text: "beta gamma delta alpha"},
	}})
	got := Recall(root, "alpha beta gamma delta", 10)
	if len(got) != 2 {
		t.Fatalf("expected 2 recalls, got %d", len(got))
	}
	if got[0].Text != "beta gamma delta alpha" {
		t.Fatalf("score tie must rank by recency (newer first), got %q first", got[0].Text)
	}

	// Identical timestamps: the full tie must resolve deterministically by
	// text, regardless of store order.
	_ = writeJSON(Path(root), Store{Root: root, Entries: []Entry{
		{Time: base, Text: "beta gamma delta alpha"},
		{Time: base, Text: "alpha beta gamma delta"},
	}})
	got = Recall(root, "alpha beta gamma delta", 10)
	if got[0].Text != "alpha beta gamma delta" {
		t.Fatalf("full tie must resolve deterministically by text, got %q first", got[0].Text)
	}
}

// TestRecallRankedDeterministicTies: identical age and token set produce an
// identical score; the order must then be deterministic by text.
func TestRecallRankedDeterministicTies(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	base := time.Now().UTC().Add(-24 * time.Hour)
	_ = writeJSON(Path(root), Store{Root: root, Entries: []Entry{
		{Time: base, Text: "zeta eta theta iota"},
		{Time: base, Text: "eta theta iota zeta"},
	}})
	got := RecallRanked(root, "zeta eta theta iota", 10, 7.0)
	if len(got) != 2 {
		t.Fatalf("expected 2 ranked recalls, got %d", len(got))
	}
	if got[0].Entry.Text != "eta theta iota zeta" {
		t.Fatalf("full tie must resolve deterministically by text, got %q first", got[0].Entry.Text)
	}
}

func TestAutoCaptureMigration(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()

	// Simulate a pre-fix store (no source field written).
	p := Path(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir store dir: %v", err)
	}
	if err := os.WriteFile(p, []byte(`{"root":"`+root+`","entries":[{"time":"2026-09-06T10:00:00Z","text":"User: pre-fix prompt"},{"time":"2026-09-06T10:00:01Z","text":"legacy lesson"}]}`), 0o600); err != nil {
		t.Fatalf("write legacy store: %v", err)
	}
	ls := List(root)
	if len(ls) != 2 {
		t.Fatalf("got %d entries, want 2", len(ls))
	}
	if ls[0].Source != "" {
		t.Fatalf("legacy lesson source = %q, want \"\"", ls[0].Source)
	}
	if ls[1].Source != "auto" {
		t.Fatalf("legacy prompt source = %q, want auto (backfilled)", ls[1].Source)
	}
	// The backfilled auto label persists on the next save.
	if err := Add(root, "new lesson"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	s := Load(root)
	for _, e := range s.Entries {
		if e.Text == "User: pre-fix prompt" && e.Source != "auto" {
			t.Fatalf("migrated prompt source = %q, want auto", e.Source)
		}
	}
}

// TestAddAutoDedupesDuplicateCaptures: the same conversation event captured
// by several agent surfaces (opencode plugin + Claude/Codex/Gemini hooks)
// within seconds must store ONE auto entry, not three; a different capture
// in the same window must still be stored.
func TestAddAutoDedupesDuplicateCaptures(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := AddAuto(root, "Command failed: go build ./..."); err != nil {
			t.Fatalf("AddAuto #%d: %v", i, err)
		}
	}
	if err := AddAuto(root, "Edited internal/api/handlers.go"); err != nil {
		t.Fatalf("AddAuto different capture: %v", err)
	}
	counts := map[string]int{}
	for _, e := range List(root) {
		counts[e.Text]++
	}
	if counts["Command failed: go build ./..."] != 1 {
		t.Fatalf("expected 1 auto entry after 3 identical captures, got %d (entries: %v)", counts["Command failed: go build ./..."], counts)
	}
	if counts["Edited internal/api/handlers.go"] != 1 {
		t.Fatalf("different capture must not be deduped, got %d", counts["Edited internal/api/handlers.go"])
	}
}

// TestAddExplicitWritesTypedAndV1 pins the P2-14 follow-up: an explicit user
// lesson (the body behind `kern memory add` / kern_memory action=add) must
// land in the TYPED store buddy's "Project memory" reads — Source "user",
// not "auto", so CurrentMemories keeps it and brief's auto guard does not
// skip it — AND in the v1 store (Source "") so `kern memory recall` keeps
// finding it.
func TestAddExplicitWritesTypedAndV1(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	const lesson = "Always run tests before committing code changes"
	if err := AddExplicit(root, lesson); err != nil {
		t.Fatalf("AddExplicit: %v", err)
	}

	// Typed store: buddy's "Project memory" source of truth.
	mems, err := NewMemoryStore(root).CurrentMemories("")
	if err != nil {
		t.Fatal(err)
	}
	var found *domain.Memory
	for i := range mems {
		if mems[i].Content == lesson {
			found = &mems[i]
		}
	}
	if found == nil {
		t.Fatalf("explicit lesson missing from typed store CurrentMemories: %+v", mems)
	}
	if found.Source == "auto" {
		t.Fatalf("explicit lesson Source = %q, want a non-auto source", found.Source)
	}
	if found.Type != domain.MemoryLesson {
		t.Fatalf("explicit lesson Type = %q, want %q", found.Type, domain.MemoryLesson)
	}

	// v1 store: recall and list keep working against the same lesson.
	if got := Recall(root, "run tests before committing", DefaultRecallLimit); len(got) != 1 || got[0].Text != lesson {
		t.Fatalf("v1 recall lost explicit lesson: %+v", got)
	}
	for _, e := range List(root) {
		if e.Text == lesson && e.Source == "auto" {
			t.Fatalf("v1 explicit lesson tagged auto: %+v", e)
		}
	}
}

// TestAutoFloodDoesNotEvictExplicitLessons: a flood of automatic captures far
// beyond maxEntries must never evict explicit lessons — the auto entries are
// trimmed oldest-first and fill only the leftover capacity.
func TestAutoFloodDoesNotEvictExplicitLessons(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	const n = 5
	explicit := make([]string, n)
	for i := 0; i < n; i++ {
		explicit[i] = fmt.Sprintf("explicit lesson %d about the api", i)
		if err := Add(root, explicit[i]); err != nil {
			t.Fatalf("Add explicit #%d: %v", i, err)
		}
	}
	// Flood with unique auto captures far beyond maxEntries.
	for i := 0; i < maxEntries*4; i++ {
		if err := AddAuto(root, fmt.Sprintf("User: auto flood prompt %d", i)); err != nil {
			t.Fatalf("AddAuto #%d: %v", i, err)
		}
	}
	ls := List(root)
	if len(ls) > maxEntries {
		t.Fatalf("store exceeded cap: %d > %d", len(ls), maxEntries)
	}
	// Every explicit lesson must survive the flood.
	for _, want := range explicit {
		found := false
		for _, e := range ls {
			if e.Text == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("explicit lesson %q was evicted by auto flood", want)
		}
	}
	// The flood fills the remaining capacity with auto entries (oldest
	// autos trimmed first, so the newest maxEntries-n autos survive).
	auto := 0
	for _, e := range ls {
		if e.Source == "auto" {
			auto++
		}
	}
	if auto != len(ls)-n {
		t.Fatalf("got %d auto entries, want %d", auto, len(ls)-n)
	}
	if !containsText(ls, fmt.Sprintf("User: auto flood prompt %d", maxEntries*4-1)) {
		t.Fatalf("newest auto capture should survive, got %+v", ls)
	}
	if containsText(ls, "User: auto flood prompt 0") {
		t.Fatalf("oldest auto capture should have been trimmed first")
	}
}

// TestExplicitOnlyStoreDropsOldestAtCap: with no auto entries present, the
// hard cap still applies — an explicit-only store at capacity drops the
// oldest explicit lesson on the next add.
func TestExplicitOnlyStoreDropsOldestAtCap(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	for i := 0; i < maxEntries; i++ {
		if err := Add(root, fmt.Sprintf("explicit lesson %02d", i)); err != nil {
			t.Fatalf("Add #%d: %v", i, err)
		}
	}
	if err := Add(root, "newest explicit lesson"); err != nil {
		t.Fatalf("Add over cap: %v", err)
	}
	ls := List(root)
	if len(ls) != maxEntries {
		t.Fatalf("got %d entries, want cap %d", len(ls), maxEntries)
	}
	if containsText(ls, "explicit lesson 00") {
		t.Fatalf("oldest explicit lesson should be dropped at cap, still present: %+v", ls)
	}
	if ls[0].Text != "newest explicit lesson" {
		t.Fatalf("newest lesson must survive, got %+v", ls)
	}
}

// TestMixedAutoAndExplicitInterleaved: interleaved explicit lessons and auto
// captures — all explicit lessons survive and autos are trimmed oldest-first.
func TestMixedAutoAndExplicitInterleaved(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	explicit := map[string]bool{}
	for i := 0; i < maxEntries*2; i++ {
		if i%3 == 0 {
			lesson := fmt.Sprintf("interleaved explicit lesson %d", i)
			explicit[lesson] = true
			if err := Add(root, lesson); err != nil {
				t.Fatalf("Add explicit #%d: %v", i, err)
			}
		} else {
			if err := AddAuto(root, fmt.Sprintf("User: interleaved auto capture %d", i)); err != nil {
				t.Fatalf("AddAuto #%d: %v", i, err)
			}
		}
	}
	ls := List(root)
	if len(ls) > maxEntries {
		t.Fatalf("store exceeded cap: %d > %d", len(ls), maxEntries)
	}
	got := map[string]bool{}
	for _, e := range ls {
		got[e.Text] = true
	}
	for want := range explicit {
		if !got[want] {
			t.Fatalf("explicit lesson %q evicted by interleaved auto captures", want)
		}
	}
	// Oldest autos evicted first: the very first auto capture is gone,
	// the very last one survives.
	if got["User: interleaved auto capture 1"] {
		t.Fatalf("oldest auto capture should have been trimmed first")
	}
	if !got["User: interleaved auto capture 98"] {
		t.Fatalf("newest auto capture should survive, got %+v", ls)
	}
}

// TestRecallFindsExplicitAfterAutoFlood: after an auto flood, Recall (which
// skips auto entries) must still surface the explicit lessons.
func TestRecallFindsExplicitAfterAutoFlood(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := Add(root, "the billing service signs every webhook with hmac sha256"); err != nil {
		t.Fatalf("Add lesson: %v", err)
	}
	for i := 0; i < maxEntries*3; i++ {
		if err := AddAuto(root, fmt.Sprintf("User: flood prompt %d about deployment", i)); err != nil {
			t.Fatalf("AddAuto #%d: %v", i, err)
		}
	}
	got := Recall(root, "how is the billing webhook signed?", 5)
	if len(got) == 0 {
		t.Fatalf("recall found nothing after auto flood: %+v", List(root))
	}
	if !strings.Contains(got[0].Text, "billing") {
		t.Fatalf("expected the explicit billing lesson first, got %q", got[0].Text)
	}
}

// containsText reports whether any entry in ls carries the exact text want.
func containsText(ls []Entry, want string) bool {
	for _, e := range ls {
		if e.Text == want {
			return true
		}
	}
	return false
}

// TestRestartPersistsExplicitLessons: explicit lessons written by one store
// instance must be readable by a FRESH instance. The v1 store keeps no
// in-memory cache — Load/List/Recall re-read the JSON file from disk on every
// call (Load -> readJSON(Path(root))), so a second Load over the same root is
// the true restart boundary: the file at Path(root) is the only state that
// carries between processes.
func TestRestartPersistsExplicitLessons(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()

	// Instance 1: real Add calls (load -> mutate -> writeJSON).
	lessons := []string{
		"the payment service retries idempotency keys with backoff",
		"the auth middleware validates jwt expiry in the api layer",
	}
	for _, l := range lessons {
		if err := Add(root, l); err != nil {
			t.Fatalf("Add %q: %v", l, err)
		}
	}

	// The JSON file is the persistence boundary — it must exist on disk.
	if _, err := os.Stat(Path(root)); err != nil {
		t.Fatalf("store file missing after Add: %v", err)
	}

	// Instance 2 (fresh process): a fresh Load re-reads the file from disk.
	// No state is shared with instance 1 beyond the file.
	s := Load(root)
	if len(s.Entries) != len(lessons) {
		t.Fatalf("restart: got %d entries, want %d: %+v", len(s.Entries), len(lessons), s.Entries)
	}
	for _, want := range lessons {
		if !containsText(s.Entries, want) {
			t.Fatalf("restart: explicit lesson %q missing after reload", want)
		}
	}

	// The public List surface must agree, most recent first.
	ls := List(root)
	if len(ls) != len(lessons) || ls[0].Text != lessons[1] || ls[1].Text != lessons[0] {
		t.Fatalf("restart: List mismatch: %+v", ls)
	}

	// Recall must surface the explicit lessons after restart.
	got := Recall(root, "how does the payment service retry idempotency?", 5)
	if len(got) == 0 || !strings.Contains(got[0].Text, "retries") {
		t.Fatalf("restart: recall failed: %+v", got)
	}
}

// TestExplicitLessonsSurviveRestartAndAutoFlood: explicit lessons must survive
// BOTH a restart (fresh instance re-reading the JSON file) AND an auto flood
// beyond maxEntries. This pins the regression where a 50/50 auto ring evicted
// explicit lessons before the source-aware trim existed — the pre-fix store
// lost explicit lessons permanently because the trim dropped oldest-first
// regardless of source.
func TestExplicitLessonsSurviveRestartAndAutoFlood(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()

	// Instance 1: seed explicit lessons, then flood with auto captures.
	explicit := []string{
		"explicit lesson about the billing webhook signature",
		"explicit lesson about the session cookie",
		"explicit lesson about the tenant id prefix",
	}
	for _, l := range explicit {
		if err := Add(root, l); err != nil {
			t.Fatalf("Add explicit %q: %v", l, err)
		}
	}
	for i := 0; i < maxEntries*4; i++ {
		if err := AddAuto(root, fmt.Sprintf("User: auto flood prompt %d", i)); err != nil {
			t.Fatalf("AddAuto #%d: %v", i, err)
		}
	}

	// Instance 2 (restart): a fresh Load re-reads the ring from disk.
	s := Load(root)
	if len(s.Entries) != maxEntries {
		t.Fatalf("restart: store exceeded cap: got %d, want %d", len(s.Entries), maxEntries)
	}
	for _, want := range explicit {
		if !containsText(s.Entries, want) {
			t.Fatalf("restart: explicit lesson %q evicted by auto flood", want)
		}
	}

	// Recall across the restart boundary must still surface the explicit
	// lessons (auto captures are excluded from recall).
	got := Recall(root, "how is the billing webhook signed?", 5)
	if len(got) == 0 || !strings.Contains(got[0].Text, "billing") {
		t.Fatalf("restart: explicit lesson not recallable after flood: %+v", List(root))
	}

	// The remaining capacity is auto captures, oldest autos trimmed first.
	auto := 0
	for _, e := range s.Entries {
		if e.Source == "auto" {
			auto++
		}
	}
	if auto != maxEntries-len(explicit) {
		t.Fatalf("restart: got %d auto entries, want %d", auto, maxEntries-len(explicit))
	}
}
