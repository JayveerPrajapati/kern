package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSamplesFromDir(t *testing.T) {
	dir := t.TempDir()
	// 2 valid samples (os.ReadDir sorts by name, so a.json/b.json are read
	// before the invalid file and both survive into the returned slice).
	writeFixture(t, filepath.Join(dir, "a.json"),
		`{"name":"a","baseline":"long baseline a","candidate":"candidate a","critical_evidence":["a"]}`)
	writeFixture(t, filepath.Join(dir, "b.json"),
		`{"name":"b","baseline":"long baseline b","candidate":"candidate b","critical_evidence":["b","frag"]}`)
	// 1 invalid JSON file (must be reported by name).
	writeFixture(t, filepath.Join(dir, "bad.json"), `{"name": oops`)
	// 1 non-json file (must be ignored).
	writeFixture(t, filepath.Join(dir, "notes.txt"), "not a sample")

	samples, err := LoadSamplesFromDir(dir)
	if err == nil {
		t.Fatal("want error naming bad.json, got nil")
	}
	if !strings.Contains(err.Error(), "bad.json") {
		t.Errorf("error should name the bad file, got: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("got %d samples, want 2", len(samples))
	}

	t.Run("field mapping", func(t *testing.T) {
		// samples[0] is a.json (ReadDir sorts by name).
		a := samples[0]
		if a.Name != "a" || a.Baseline != "long baseline a" || a.Candidate != "candidate a" {
			t.Errorf("sample a not mapped: %+v", a)
		}
		if len(a.CriticalEvidence) != 1 || a.CriticalEvidence[0] != "a" {
			t.Errorf("sample a critical evidence not mapped: %+v", a.CriticalEvidence)
		}
		b := samples[1]
		if b.Name != "b" || b.Baseline != "long baseline b" || b.Candidate != "candidate b" {
			t.Errorf("sample b not mapped: %+v", b)
		}
		if len(b.CriticalEvidence) != 2 || b.CriticalEvidence[0] != "b" || b.CriticalEvidence[1] != "frag" {
			t.Errorf("sample b critical evidence not mapped: %+v", b.CriticalEvidence)
		}
	})
}

func TestLoadSamplesFromDirEmpty(t *testing.T) {
	dir := t.TempDir()
	samples, err := LoadSamplesFromDir(dir)
	if err != nil {
		t.Fatalf("empty dir should be nil error, got: %v", err)
	}
	if len(samples) != 0 {
		t.Fatalf("empty dir should yield 0 samples, got %d", len(samples))
	}
}

func TestLoadSamplesFromDirMissing(t *testing.T) {
	if _, err := LoadSamplesFromDir(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("missing dir should error")
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
