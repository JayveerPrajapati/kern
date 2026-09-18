package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiskIndexViewNoIndex(t *testing.T) {
	dir := t.TempDir()
	if v := DiskIndexView(dir); v != nil {
		t.Fatalf("DiskIndexView on empty dir = %v, want nil", v)
	}
}

func TestDiskIndexViewFresh(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	v := DiskIndexView(dir)
	if v == nil {
		t.Fatal("DiskIndexView = nil, want disk view")
	}
	if v["built"] != true || v["fresh"] != true {
		t.Errorf("built/fresh = %v/%v, want true/true", v["built"], v["fresh"])
	}
	if n, _ := v["symbols"].(int); n <= 0 {
		t.Errorf("symbols = %v, want > 0", v["symbols"])
	}
	if v["store"] != StorePath(dir) {
		t.Errorf("store = %v, want %v", v["store"], StorePath(dir))
	}
}

func TestDiskIndexViewCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(StorePath(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(StorePath(dir), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := DiskIndexView(dir)
	if v == nil {
		t.Fatal("DiskIndexView on corrupt index = nil, want rebuild_required map")
	}
	if v["rebuild_required"] == nil {
		t.Errorf("missing rebuild_required in %v", v)
	}
	if v["stale"] != true {
		t.Errorf("stale = %v, want true", v["stale"])
	}
}
