//go:build sqlite

package index

import (
	"fmt"
	"testing"
)

// WAL valve tests: exercise the valve added in sqlite_store.go
// (wal_autocheckpoint=0 + maybeCheckpoint at the end of every Save).
// The live (uncheckpointed) WAL size is measured with the package-internal
// walLiveFrames helper (WAL-index header in the -shm file), since
// modernc.org/sqlite does not expose PRAGMA wal_pages.

// valveTestIndex returns an index with n symbols, each row wide enough to be
// representative of a real symbol (long name/file/params), so n rows produce
// a few MiB of WAL frames per Save.
func valveTestIndex(root string, n int) *Index {
	ix := New(root)
	for i := 0; i < n; i++ {
		ix.Symbols = append(ix.Symbols, Symbol{
			Kind:   "func",
			Name:   fmt.Sprintf("SymbolNumber%dWithAPaddingSuffixToWidenTheRowABCDEF", i),
			File:   fmt.Sprintf("pkg/subpkg/file_%d_generated_for_wal_valve_testing.go", i%40),
			Line:   i + 1,
			End:    i + 5,
			Lang:   "go",
			Params: []string{"ctx context.Context", "request *http.Request", "responseWriter http.ResponseWriter"},
		})
	}
	return ix
}

func valvePageSize(t *testing.T, s *SQLiteStore) int64 {
	t.Helper()
	var ps int64
	if err := s.db.QueryRow("PRAGMA page_size").Scan(&ps); err != nil {
		t.Fatalf("page_size: %v", err)
	}
	return ps
}

func valveLiveWALBytes(t *testing.T, s *SQLiteStore, pageSize int64) int64 {
	t.Helper()
	frames := walLiveFrames(s.path)
	if frames < 0 {
		t.Fatalf("walLiveFrames(%s) unreadable after save", s.path)
	}
	return frames * pageSize
}

// TestWALValveBoundedGrowth writes enough rows to cross walTrigger (2 x
// walSoftCap = 16 MiB of WAL) several times over, and asserts the live WAL
// stays below the trigger (with page-size slack) after every save. The valve
// checkpoints at the end of the save that crosses the trigger, so a live WAL
// of zero after a save is the observable proof the valve fired.
func TestWALValveBoundedGrowth(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	s, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer s.Close()

	ix := valveTestIndex(dir, 6000)
	pageSize := valvePageSize(t, s)

	peak := int64(0)
	fired := false
	const saves = 12
	for i := 0; i < saves; i++ {
		if err := s.Save(ix); err != nil {
			t.Fatalf("Save %d: %v", i+1, err)
		}
		live := valveLiveWALBytes(t, s, pageSize)
		if live > peak {
			peak = live
		}
		if live == 0 {
			fired = true // a save reset the WAL: the valve checkpointed it
		}
		if live > walTrigger+pageSize {
			t.Errorf("after save %d: live WAL = %d bytes, exceeds walTrigger %d + page-size slack",
				i+1, live, walTrigger)
		}
	}
	if !fired {
		t.Fatalf("valve never checkpointed: cumulative writes never crossed walTrigger (%d bytes); peak live WAL = %d — test not exercising the valve",
			walTrigger, peak)
	}
	if peak < walSoftCap {
		t.Errorf("peak live WAL %d bytes below walSoftCap %d: writes too small to be representative", peak, walSoftCap)
	}
}

// TestWALValveDataIntegrity runs the same big-write sequence (crossing the
// trigger several times, so the valve checkpoints mid-stream), then reads the
// index back and asserts every sampled row is intact and the database passes
// PRAGMA integrity_check.
func TestWALValveDataIntegrity(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	s, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer s.Close()

	ix := valveTestIndex(dir, 6000)
	const saves = 12
	for i := 0; i < saves; i++ {
		if err := s.Save(ix); err != nil {
			t.Fatalf("Save %d: %v", i+1, err)
		}
	}
	// The write volume must have crossed the trigger, else the valve never
	// ran and this test proves nothing about post-checkpoint integrity.
	if live := valveLiveWALBytes(t, s, valvePageSize(t, s)); live != 0 {
		t.Fatalf("live WAL %d bytes after %d saves: valve never checkpointed, test not meaningful", live, saves)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil {
		t.Fatal("Load returned nil index")
	}
	if len(got.Symbols) != len(ix.Symbols) {
		t.Fatalf("loaded %d symbols, want %d", len(got.Symbols), len(ix.Symbols))
	}
	// Load order is not guaranteed; index by name and sample.
	byName := make(map[string]Symbol, len(got.Symbols))
	for _, sym := range got.Symbols {
		byName[sym.Name] = sym
	}
	for i := 0; i < len(ix.Symbols); i += 500 {
		want := ix.Symbols[i]
		gotSym, ok := byName[want.Name]
		if !ok {
			t.Errorf("symbol %q missing after load", want.Name)
			continue
		}
		if gotSym.Kind != want.Kind || gotSym.File != want.File ||
			gotSym.Line != want.Line || gotSym.Lang != want.Lang {
			t.Errorf("symbol %q mismatch: got %+v, want %+v", want.Name, gotSym, want)
		}
	}

	var integrity string
	if err := s.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check = %q, want ok", integrity)
	}
}

// TestWALValveSmallWritesNoGrowth verifies the valve stays silent for the
// incremental pattern it targets: a few small writes leave the live WAL tiny
// (never near walSoftCap), fire no checkpoint, and produce no errors.
func TestWALValveSmallWritesNoGrowth(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	s, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer s.Close()

	small := valveTestIndex(dir, 50)
	pageSize := valvePageSize(t, s)
	for i := 0; i < 5; i++ {
		if err := s.Save(small); err != nil {
			t.Fatalf("Save %d: %v", i+1, err)
		}
		live := valveLiveWALBytes(t, s, pageSize)
		if i == 0 && live == 0 {
			t.Error("first small save produced no WAL frames: writes not landing in the WAL")
		}
		if live >= walTrigger {
			t.Errorf("after small save %d: live WAL = %d bytes (>= walTrigger %d): checkpoint fired for tiny writes", i+1, live, walTrigger)
		}
		if live > walSoftCap {
			t.Errorf("after small save %d: live WAL = %d bytes, want tiny (<= walSoftCap %d)", i+1, live, walSoftCap)
		}
	}
}
