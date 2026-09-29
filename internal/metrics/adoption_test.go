package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordKernAdoption(t *testing.T) {
	r := New()
	r.RecordKernAdoption(true)
	r.RecordKernAdoption(true)
	r.RecordKernAdoption(false)

	s := r.Snapshot()
	if s.KernAdoptionTotal != 3 {
		t.Errorf("KernAdoptionTotal = %d, want 3", s.KernAdoptionTotal)
	}
	if s.KernAdoptionRouted != 2 {
		t.Errorf("KernAdoptionRouted = %d, want 2", s.KernAdoptionRouted)
	}
	if want := 200.0 / 3.0; abs(s.KernAdoptionPct-want) > 1e-9 {
		t.Errorf("KernAdoptionPct = %v, want %v", s.KernAdoptionPct, want)
	}
	if !strings.Contains(r.Render(), "kern-first adoption") {
		t.Error("Render missing kern-first adoption line")
	}

	r.Reset()
	s = r.Snapshot()
	if s.KernAdoptionTotal != 0 || s.KernAdoptionRouted != 0 || s.KernAdoptionPct != 0 {
		t.Errorf("adoption counters not cleared by Reset: %+v", s)
	}
}

func TestRecordKernAdoptionNilSafe(t *testing.T) {
	var r *Recorder
	r.RecordKernAdoption(true) // must not panic
	if err := r.IngestAdoptionLog("nonexistent"); err != nil {
		t.Errorf("nil IngestAdoptionLog error: %v", err)
	}
}

func TestAggregateAdoptionLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "adoption.log")
	lines := []string{
		`{"ts":1,"tool":"read","routed":true}`,
		`{"ts":2,"tool":"grep","routed":true}`,
		`{"ts":3,"tool":"bash","routed":false}`,
		``,         // blank line — skipped
		`not json`, // malformed line — skipped
		`{"ts":4,"tool":"glob","routed":true}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := AggregateAdoptionLog(path)
	if err != nil {
		t.Fatalf("AggregateAdoptionLog: %v", err)
	}
	if c.Total != 4 {
		t.Errorf("Total = %d, want 4", c.Total)
	}
	if c.Routed != 3 {
		t.Errorf("Routed = %d, want 3", c.Routed)
	}
	if want := 75.0; abs(c.Pct()-want) > 1e-9 {
		t.Errorf("Pct = %v, want %v", c.Pct(), want)
	}
}

func TestAggregateAdoptionLogMissingFile(t *testing.T) {
	c, err := AggregateAdoptionLog(filepath.Join(t.TempDir(), "no-such.log"))
	if err != nil {
		t.Fatalf("missing file should be a no-op, got error: %v", err)
	}
	if c.Total != 0 || c.Routed != 0 || c.Pct() != 0 {
		t.Errorf("missing file should yield zero counts, got %+v", c)
	}
}

func TestIngestAdoptionLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "adoption.log")
	log := "{\"ts\":1,\"tool\":\"read\",\"routed\":true}\n" +
		"{\"ts\":2,\"tool\":\"read\",\"routed\":false}\n"
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}

	r := New()
	if err := r.IngestAdoptionLog(path); err != nil {
		t.Fatalf("IngestAdoptionLog: %v", err)
	}
	s := r.Snapshot()
	if s.KernAdoptionTotal != 2 || s.KernAdoptionRouted != 1 {
		t.Errorf("ingested counts = routed %d / total %d, want 1 / 2", s.KernAdoptionRouted, s.KernAdoptionTotal)
	}

	// Empty log → no-op.
	if err := r.IngestAdoptionLog(filepath.Join(dir, "missing")); err != nil {
		t.Errorf("IngestAdoptionLog missing file: %v", err)
	}
	s = r.Snapshot()
	if s.KernAdoptionTotal != 2 {
		t.Errorf("ingest of missing file should not change counts, got total %d", s.KernAdoptionTotal)
	}
}
