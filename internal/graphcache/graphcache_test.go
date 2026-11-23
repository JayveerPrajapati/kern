package graphcache

import (
	"os"
	"path/filepath"
	"testing"
)

type payload struct {
	Name  string
	Items []string
}

func TestSaveLoadRoundtrip(t *testing.T) {
	root := t.TempDir()
	key := Key("v1", "root-a", "oid-a", "42")
	p := payload{Name: "catalog", Items: []string{"a", "b"}}
	if err := Save(root, "catalog", key, &p); err != nil {
		t.Fatalf("save: %v", err)
	}
	var got payload
	if !Load(root, "catalog", key, &got) {
		t.Fatal("expected sidecar hit")
	}
	if got.Name != "catalog" || len(got.Items) != 2 || got.Items[1] != "b" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestLoadMissOnWrongKeyOrMissing(t *testing.T) {
	root := t.TempDir()
	key := Key("v1")
	if err := Save(root, "catalog", key, &payload{Name: "x"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	var got payload
	if Load(root, "catalog", Key("other"), &got) {
		t.Fatal("wrong key must miss")
	}
	if Load(root, "graph", key, &got) {
		t.Fatal("wrong name must miss")
	}
	if Load(root, "catalog", "", &got) {
		t.Fatal("empty key must miss")
	}
}

func TestLoadMissOnCorruptFile(t *testing.T) {
	root := t.TempDir()
	key := Key("v1")
	path := filepath.Join(root, ".kern", "catalog-"+key+".gob")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not gob"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got payload
	if Load(root, "catalog", key, &got) {
		t.Fatal("corrupt sidecar must miss, not error")
	}
}

func TestSavePrunesOtherGenerations(t *testing.T) {
	root := t.TempDir()
	oldKey, newKey := Key("gen-1"), Key("gen-2")
	if err := Save(root, "catalog", oldKey, &payload{Name: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, "catalog", newKey, &payload{Name: "new"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".kern"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one sidecar after prune, got %d", len(entries))
	}
	if entries[0].Name() != "catalog-"+newKey+".gob" {
		t.Fatalf("surviving sidecar is not the newest: %s", entries[0].Name())
	}
	// Unrelated names survive the prune.
	if err := Save(root, "graph", Key("g1"), &payload{Name: "g"}); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(filepath.Join(root, ".kern"))
	if len(entries) != 2 {
		t.Fatalf("graph sidecar must survive a catalog prune, got %d entries", len(entries))
	}
}

func TestKeyIsStableAndSeparated(t *testing.T) {
	a := Key("a", "b")
	b := Key("a", "b")
	c := Key("ab")
	if a != b {
		t.Fatal("key must be deterministic")
	}
	if a == c {
		t.Fatal("NUL separation must prevent part-boundary collisions")
	}
	if len(a) != 64 {
		t.Fatalf("key must be 64 hex chars, got %d", len(a))
	}
}
