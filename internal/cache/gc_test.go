package cache

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeJSONFile writes payload to path and sets its mtime to t.
func writeJSONFile(t *testing.T, path string, payload []byte, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// bigJSON returns a JSON payload larger than minArchiveBytes.
func bigJSON() []byte {
	m := map[string]string{}
	for i := 0; i < 256; i++ {
		m["k"+string(rune('a'+i%26))+string(rune('0'+i%10))] = "some value that pads the file well beyond the 4 KiB floor"
	}
	b, _ := json.Marshal(m)
	return b
}

func gunzipBytes(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(zr); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestMaintainArchivesAndEvicts covers the core G-7 lifecycle: a fresh file
// is untouched, an old (dormant) one is gzipped with its mtime preserved and
// the plain file removed, and an ancient one is deleted outright.
func TestMaintainArchivesAndEvicts(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	fresh := filepath.Join(dir, "fresh.json")
	old := filepath.Join(dir, "old.json")
	ancient := filepath.Join(dir, "ancient.json")
	payload := bigJSON()
	writeJSONFile(t, fresh, payload, now)
	writeJSONFile(t, old, payload, now.Add(-10*24*time.Hour))
	writeJSONFile(t, ancient, payload, now.Add(-40*24*time.Hour))

	archived, evicted, err := Maintain(dir, 7*24*time.Hour, 30*24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if archived != 1 || evicted != 1 {
		t.Fatalf("expected archived=1 evicted=1, got archived=%d evicted=%d", archived, evicted)
	}

	// Fresh file untouched, no twin.
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh file must survive: %v", err)
	}
	if _, err := os.Stat(fresh + ".gz"); !os.IsNotExist(err) {
		t.Fatal("fresh file must not be archived")
	}

	// Old file archived: plain gone, .gz present with original mtime + content.
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old plain file must be removed after archival")
	}
	zi, err := os.Stat(old + ".gz")
	if err != nil {
		t.Fatalf("old file must be archived to .gz: %v", err)
	}
	if !zi.ModTime().Equal(now.Add(-10 * 24 * time.Hour)) {
		t.Fatalf("gz mtime not preserved: got %v, want %v", zi.ModTime(), now.Add(-10*24*time.Hour))
	}
	if !bytes.Equal(gunzipBytes(t, old+".gz"), payload) {
		t.Fatal("gz content does not match original")
	}

	// Ancient file evicted entirely.
	if _, err := os.Stat(ancient); !os.IsNotExist(err) {
		t.Fatal("ancient file must be evicted")
	}
	if _, err := os.Stat(ancient + ".gz"); !os.IsNotExist(err) {
		t.Fatal("ancient file must not leave a twin behind")
	}
}

// TestMaintainSkipsTinyAndNonJSON verifies the walk filters: tiny files are
// not worth archiving, non-.json files are skipped, and already-archived
// .json.gz files are never re-archived.
func TestMaintainSkipsTinyAndNonJSON(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-10 * 24 * time.Hour)

	tiny := filepath.Join(dir, "tiny.json")
	writeJSONFile(t, tiny, []byte(`{"a":1}`), old) // far below the 4 KiB floor
	note := filepath.Join(dir, "notes.txt")
	writeJSONFile(t, note, bigJSON(), old)
	gz := filepath.Join(dir, "dormant.json.gz")
	writeJSONFile(t, gz, bigJSON(), old) // pre-archived

	archived, evicted, err := Maintain(dir, 7*24*time.Hour, 30*24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if archived != 0 || evicted != 0 {
		t.Fatalf("expected no-op, got archived=%d evicted=%d", archived, evicted)
	}
	if _, err := os.Stat(tiny); err != nil {
		t.Fatalf("tiny file must be left alone: %v", err)
	}
	if _, err := os.Stat(note); err != nil {
		t.Fatalf("non-.json file must be left alone: %v", err)
	}
	if _, err := os.Stat(gz); err != nil {
		t.Fatalf("pre-archived .gz must be left alone: %v", err)
	}
}

// TestMaintainEvictsGzTwin ensures eviction removes both the plain file and
// its .gz twin and counts them once.
func TestMaintainEvictsGzTwin(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)
	plain := filepath.Join(dir, "stale.json")
	writeJSONFile(t, plain, bigJSON(), old)
	writeJSONFile(t, plain+".gz", bigJSON(), old)

	archived, evicted, err := Maintain(dir, 7*24*time.Hour, 30*24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if archived != 0 || evicted != 1 {
		t.Fatalf("expected evicted=1, got archived=%d evicted=%d", archived, evicted)
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Fatal("stale plain file must be evicted")
	}
	if _, err := os.Stat(plain + ".gz"); !os.IsNotExist(err) {
		t.Fatal("stale .gz twin must be evicted too")
	}
}

// TestMaintainDisablePasses verifies that zero/negative durations disable the
// respective pass while the other stays active.
func TestMaintainDisablePasses(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-10 * 24 * time.Hour)
	ancient := now.Add(-40 * 24 * time.Hour)
	oldFile := filepath.Join(dir, "old.json")
	ancientFile := filepath.Join(dir, "ancient.json")
	writeJSONFile(t, oldFile, bigJSON(), old)
	writeJSONFile(t, ancientFile, bigJSON(), ancient)

	// All disabled: nothing happens.
	if a, e, err := Maintain(dir, 0, 0, false); err != nil || a != 0 || e != 0 {
		t.Fatalf("all-disabled pass must be a no-op: a=%d e=%d err=%v", a, e, err)
	}
	if _, err := os.Stat(oldFile); err != nil {
		t.Fatal("archiving disabled: old file must remain plain")
	}
	if _, err := os.Stat(ancientFile); err != nil {
		t.Fatal("eviction disabled: ancient file must remain")
	}

	// Only eviction enabled: old file stays plain, ancient file goes.
	archived, evicted, err := Maintain(dir, 0, 30*24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if archived != 0 || evicted != 1 {
		t.Fatalf("expected evicted=1 archived=0, got archived=%d evicted=%d", archived, evicted)
	}
	if _, err := os.Stat(oldFile); err != nil {
		t.Fatal("archiving disabled: old file must remain plain")
	}
	if _, err := os.Stat(ancientFile); !os.IsNotExist(err) {
		t.Fatal("ancient file must be evicted")
	}
}

// TestMaintainDryRun counts what WOULD change without mutating anything.
func TestMaintainDryRun(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := filepath.Join(dir, "old.json")
	ancient := filepath.Join(dir, "ancient.json")
	writeJSONFile(t, old, bigJSON(), now.Add(-10*24*time.Hour))
	writeJSONFile(t, ancient, bigJSON(), now.Add(-40*24*time.Hour))

	archived, evicted, err := Maintain(dir, 7*24*time.Hour, 30*24*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	if archived != 1 || evicted != 1 {
		t.Fatalf("dry-run must still count: archived=%d evicted=%d", archived, evicted)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("dry-run must not remove the plain file")
	}
	if _, err := os.Stat(old + ".gz"); !os.IsNotExist(err) {
		t.Fatal("dry-run must not create the .gz twin")
	}
	if _, err := os.Stat(ancient); err != nil {
		t.Fatal("dry-run must not evict")
	}
}

// TestMaintainDeterministic runs the pass twice: the second run is a no-op.
func TestMaintainDeterministic(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	fresh := filepath.Join(dir, "fresh.json")
	old := filepath.Join(dir, "old.json")
	writeJSONFile(t, fresh, bigJSON(), now)
	writeJSONFile(t, old, bigJSON(), now.Add(-10*24*time.Hour))

	if a, e, err := Maintain(dir, 7*24*time.Hour, 30*24*time.Hour, false); err != nil || a != 1 || e != 0 {
		t.Fatalf("first pass: a=%d e=%d err=%v", a, e, err)
	}
	if a, e, err := Maintain(dir, 7*24*time.Hour, 30*24*time.Hour, false); err != nil || a != 0 || e != 0 {
		t.Fatalf("second pass must be a no-op: a=%d e=%d err=%v", a, e, err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh file must survive both passes")
	}
	if _, err := os.Stat(fresh + ".gz"); !os.IsNotExist(err) {
		t.Fatal("fresh file must never be archived")
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old file must stay archived")
	}
}

// TestMaintainErrorsOnMissingDir documents Maintain's contract: a nonexistent
// dir is an error, not a silent no-op.
func TestMaintainErrorsOnMissingDir(t *testing.T) {
	if _, _, err := Maintain(filepath.Join(t.TempDir(), "nope"), time.Hour, time.Hour, false); err == nil {
		t.Fatal("Maintain on a missing dir must return an error")
	}
}

// TestMaintainDefaultsEnv verifies the KERN_CACHE_* knobs: float days,
// <=0 disables, garbage falls back to defaults.
func TestMaintainDefaultsEnv(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	payload := bigJSON()

	old := filepath.Join(dir, "old.json")
	ancient := filepath.Join(dir, "ancient.json")
	writeJSONFile(t, old, payload, now.Add(-24*time.Hour))
	writeJSONFile(t, ancient, payload, now.Add(-5*24*time.Hour))

	t.Setenv("KERN_CACHE_ARCHIVE_DAYS", "0.5")
	t.Setenv("KERN_CACHE_TTL_DAYS", "3")
	archived, evicted, err := MaintainDefaults(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if archived != 1 || evicted != 1 {
		t.Fatalf("env pass: archived=%d evicted=%d, want 1/1", archived, evicted)
	}
	if _, err := os.Stat(old + ".gz"); err != nil {
		t.Fatal("old file must be archived by env-driven pass")
	}
	if _, err := os.Stat(ancient); !os.IsNotExist(err) {
		t.Fatal("ancient file must be evicted by env-driven pass")
	}

	// Garbage values fall back to the defaults (7d archive / 30d evict);
	// a 1-day-old file is too fresh for the default 7d archive window, so
	// nothing further changes.
	t.Setenv("KERN_CACHE_ARCHIVE_DAYS", "not-a-number")
	t.Setenv("KERN_CACHE_TTL_DAYS", "garbage")
	if _, _, err := MaintainDefaults(dir, false); err != nil {
		t.Fatal(err)
	}

	// <=0 disables: a 10-day-old file stays plain.
	dir2 := t.TempDir()
	stay := filepath.Join(dir2, "stay.json")
	writeJSONFile(t, stay, payload, now.Add(-10*24*time.Hour))
	t.Setenv("KERN_CACHE_ARCHIVE_DAYS", "0")
	t.Setenv("KERN_CACHE_TTL_DAYS", "-1")
	if a, e, err := MaintainDefaults(dir2, false); err != nil || a != 0 || e != 0 {
		t.Fatalf("disabled pass: a=%d e=%d err=%v", a, e, err)
	}
	if _, err := os.Stat(stay); err != nil {
		t.Fatal("disabled archive must leave the file plain")
	}
	if _, err := os.Stat(stay + ".gz"); !os.IsNotExist(err) {
		t.Fatal("disabled archive must not create a twin")
	}
}

// TestMaintainOnceRateLimit: the second call within the hour must skip the
// pass (marker check), leaving an eligible file untouched, and errors are
// swallowed.
func TestMaintainOnceRateLimit(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	payload := bigJSON()
	first := filepath.Join(dir, "first.json")
	writeJSONFile(t, first, payload, now.Add(-10*24*time.Hour)) // would archive

	MaintainOnce(dir) // runs the pass, writes the marker
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("first MaintainOnce must run the pass")
	}

	// A second eligible file appears; the rate-limited call must skip it.
	second := filepath.Join(dir, "second.json")
	writeJSONFile(t, second, payload, now.Add(-10*24*time.Hour))
	MaintainOnce(dir)
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("second MaintainOnce within the hour must not run: %v", err)
	}
	if _, err := os.Stat(second + ".gz"); !os.IsNotExist(err) {
		t.Fatal("second call must not archive")
	}

	// Errors are swallowed: missing dir must not panic.
	MaintainOnce(filepath.Join(t.TempDir(), "nope"))
}

// TestLoadReadsGzTwin: a hand-written .gz twin is transparently decompressed
// by Load, and neither-variant still returns os.ErrNotExist.
func TestLoadReadsGzTwin(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	key := "gztwin"
	payload := []byte(`{"Name":"dormant","Count":3}`)
	if err := os.MkdirAll(Path("data"), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path("data", key+".json.gz"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	var got struct {
		Name  string
		Count int
	}
	if err := Load(key, &got); err != nil {
		t.Fatalf("Load must transparently gunzip the twin: %v", err)
	}
	if got.Name != "dormant" || got.Count != 3 {
		t.Fatalf("unexpected payload: %+v", got)
	}

	var v any
	if err := Load("neither", &v); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing key must stay os.ErrNotExist, got %v", err)
	}
}

// TestExistsGzTwin: Exists reports true when only the .gz twin is present.
func TestExistsGzTwin(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	key := "existsgz"
	if err := os.MkdirAll(Path("data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if Exists(key) {
		t.Fatal("must not exist before the twin is written")
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(`{}`))
	zw.Close()
	if err := os.WriteFile(Path("data", key+".json.gz"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if !Exists(key) {
		t.Fatal("Exists must see the gz twin")
	}
	if Exists("never") {
		t.Fatal("unknown key must not exist")
	}
}

// TestStoreAfterArchival: Store writes a fresh plain file and removes the
// stale .gz twin so the active copy is always the plain one.
func TestStoreAfterArchival(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	key := "store"
	if err := os.MkdirAll(Path("data"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := Path("data", key+".json")
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(`{"stale":true}`))
	zw.Close()
	if err := os.WriteFile(plain+".gz", buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Store(key, map[string]bool{"active": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plain); err != nil {
		t.Fatalf("plain file must be rewritten: %v", err)
	}
	if _, err := os.Stat(plain + ".gz"); !os.IsNotExist(err) {
		t.Fatal("stale .gz twin must be removed by Store")
	}
	raw, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"active":true}` {
		t.Fatalf("unexpected stored form: %s", raw)
	}
}

// TestMaintainOnceFreshDir additionally proves the marker write on a fresh
// dir works and the pass runs there.
func TestMaintainOnceFreshDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fresh")
	MaintainOnce(dir) // creates dir + marker, runs an empty pass
	marker := filepath.Join(dir, maintainMarker)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker must be written: %v", err)
	}
	MaintainOnce(dir) // marker fresh → skip
}
