package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAddAndList(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	Add(root, "first lesson")
	Add(root, "second lesson")
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
	Add(root, "lesson")
	ls := List(root)
	if len(ls) != 1 || ls[0].Text != "lesson" {
		t.Fatalf("did not persist: %+v", ls)
	}
}

func TestCap(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	for i := 0; i < maxEntries+10; i++ {
		Add(root, strings.Repeat("x", i))
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
	Add(root, "lesson")
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
	Add(root, "lesson")
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

// TestAutoCaptureLabeledAndExcludedFromRecall (report A17): raw session
// captures — the "User: …" prompts and "Edited …"/"Command failed: …" tool
// outcomes written by the opencode plugin and native hooks — must be labeled
// Source "auto" and must never be returned by Recall (they can carry PII and
// are session context, not project lessons), while a deliberate lesson stays
// recallable.
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
