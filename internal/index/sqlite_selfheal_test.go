//go:build !nosqlite

package index

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestOpenSQLiteSelfHealsCorruptStore (QA Pick #14, F-IX1): a corrupt
// index.sqlite previously failed every open with a raw driver error and no
// command ever recreated the store. OpenSQLite must quarantine the corrupt
// file (kept for inspection, suffixed .corrupt-<unix>) and succeed on a
// fresh one.
func TestOpenSQLiteSelfHealsCorruptStore(t *testing.T) {
	root := t.TempDir()
	storePath := sqliteDBPath(root)
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Garbage that is definitely not a SQLite database.
	if err := os.WriteFile(storePath, []byte("this is definitely not a sqlite database at all, nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSQLite(root)
	if err != nil {
		t.Fatalf("OpenSQLite must self-heal a corrupt store, got %v", err)
	}
	defer s.Close()
	// The fresh store is usable: a Save round-trip works.
	if err := s.Save(&Index{Symbols: []Symbol{{Kind: "func", Name: "Fit", File: "fit.go", Line: 1}}}); err != nil {
		t.Fatalf("Save on the healed store failed: %v", err)
	}
	// The corrupt original was quarantined, not silently destroyed.
	ents, _ := os.ReadDir(filepath.Dir(storePath))
	quarantined := false
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "index.sqlite.corrupt-") {
			quarantined = true
		}
	}
	if !quarantined {
		t.Fatalf("corrupt store was not quarantined next to the fresh one")
	}
}

// TestOpenSQLiteAnnouncesCorruptQuarantine pins the corruption-message
// honesty fix: a corrupt store used to be quarantined SILENTLY, so the next
// load reported "no index found" and the user never learned their store was
// corrupt (it looked like a fresh repo). The quarantine path must surface
// "corrupt index store quarantined to X — rebuilding from source" on stderr.
// The announcement is deliberately NOT announce-gated (2026-10-28 campaign):
// corruption is rare and always actionable — an operator must never miss it,
// unlike routine build announcements.
func TestOpenSQLiteAnnouncesCorruptQuarantine(t *testing.T) {
	root := t.TempDir()
	storePath := sqliteDBPath(root)
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(storePath, []byte("this is definitely not a sqlite database at all, nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A stale quarantine from a previous corruption (older than the GC
	// window) must be collected when a NEW quarantine succeeds; a fresh
	// one must survive. Backdate one, keep one recent.
	dir := filepath.Dir(storePath)
	base := filepath.Base(storePath)
	stale := filepath.Join(dir, base+".corrupt-1000000000")
	if err := os.WriteFile(stale, []byte("stale quarantine payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stale, time.Time{}.Add(time.Unix(1, 0).Sub(time.Time{})), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, base+".corrupt-"+fmt.Sprintf("%d", time.Now().Add(-time.Hour).Unix()))
	if err := os.WriteFile(fresh, []byte("fresh quarantine payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	s, err := OpenSQLite(root)
	_ = w.Close()
	os.Stderr = old
	stderr, _ := io.ReadAll(r)

	if err != nil {
		t.Fatalf("OpenSQLite must self-heal a corrupt store, got %v", err)
	}
	defer s.Close()

	msg := string(stderr)
	if !strings.Contains(msg, "corrupt index store quarantined to ") || !strings.Contains(msg, "rebuilding from source") {
		t.Fatalf("expected the corrupt-quarantine announcement on stderr, got %q", msg)
	}
	if !strings.Contains(msg, ".corrupt-") {
		t.Fatalf("announcement must name the quarantine path (index.sqlite.corrupt-<ts>), got %q", msg)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale quarantine (older than the GC window) must be collected, stat err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh quarantine (inside the inspection window) must survive GC: %v", err)
	}
}
