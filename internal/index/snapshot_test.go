package index

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// snapshotFixture is a small two-package tree exercising whole-repo and
// subgraph modes plus the verify walk.
var snapshotFixture = map[string]string{
	"lib/lib.go": `package lib

// Public calls inner and is called by UsePublic.
func Public() string {
	return inner()
}

func inner() string {
	return "x"
}

// UsePublic is a caller of Public.
func UsePublic() string {
	return Public()
}
`,
	"app/main.go": `package main

import "example.com/repo/lib"

func main() {
	lib.UsePublic()
}
`,
}

func buildSnapshotIndex(t *testing.T) (root string, ix *Index) {
	t.Helper()
	root = writeTree(t, snapshotFixture)
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if ix.Identity == nil {
		t.Fatal("Build must set Identity")
	}
	if ix.Identity.TreeOID != "" {
		// The verify tests rely on the walk path (TreeOID empty when root is
		// not a git worktree); t.TempDir is never inside a repo.
		t.Fatalf("test root unexpectedly inside a git worktree (TreeOID %q)", ix.Identity.TreeOID)
	}
	return root, ix
}

// TestSnapshotRoundtrip: Snapshot -> Save -> LoadSnapshot must preserve the
// schema version, identity, graph, and files map.
func TestSnapshotRoundtrip(t *testing.T) {
	_, ix := buildSnapshotIndex(t)
	snap, err := ix.Snapshot("whole", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if snap.SchemaVersion != SnapshotSchemaVersion {
		t.Fatalf("SchemaVersion = %d; want %d", snap.SchemaVersion, SnapshotSchemaVersion)
	}
	if snap.Mode != "whole" {
		t.Fatalf("Mode = %q; want whole", snap.Mode)
	}
	if snap.Identity.ContentRoot == "" {
		t.Fatal("Identity.ContentRoot must be set")
	}
	if len(snap.Files) != len(ix.FileHashes) {
		t.Fatalf("Files has %d entries; want %d", len(snap.Files), len(ix.FileHashes))
	}
	// Fresh copy: mutating the index must not leak into the snapshot.
	ix.FileHashes["lib/lib.go"] = "deadbeef"
	if snap.Files["lib/lib.go"] == "deadbeef" {
		t.Fatal("Files must be a fresh copy of FileHashes")
	}
	path := filepath.Join(t.TempDir(), "snap.json")
	if err := snap.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != SnapshotSchemaVersion {
		t.Errorf("loaded SchemaVersion = %d; want %d", got.SchemaVersion, SnapshotSchemaVersion)
	}
	if got.Identity.ContentRoot != snap.Identity.ContentRoot {
		t.Errorf("loaded ContentRoot = %q; want %q", got.Identity.ContentRoot, snap.Identity.ContentRoot)
	}
	if !got.GeneratedAt.Equal(snap.GeneratedAt) {
		t.Errorf("loaded GeneratedAt = %v; want %v", got.GeneratedAt, snap.GeneratedAt)
	}
	if !reflect.DeepEqual(got.Graph.Nodes, snap.Graph.Nodes) {
		t.Errorf("loaded nodes = %d; want %d", len(got.Graph.Nodes), len(snap.Graph.Nodes))
	}
	if !reflect.DeepEqual(got.Graph.Edges, snap.Graph.Edges) {
		t.Errorf("loaded edges = %d; want %d", len(got.Graph.Edges), len(snap.Graph.Edges))
	}
	if !reflect.DeepEqual(got.Files, snap.Files) {
		t.Errorf("loaded Files = %v; want %v", got.Files, snap.Files)
	}
}

// TestSnapshotVersionGate: a snapshot written with an unknown schema version
// must fail loudly instead of being misread.
func TestSnapshotVersionGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snap.json")
	if err := os.WriteFile(path, []byte(`{"schema_version": 99, "mode": "whole"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSnapshot(path)
	if err == nil {
		t.Fatal("LoadSnapshot must error on schema version mismatch")
	}
	if !strings.Contains(err.Error(), "schema version") {
		t.Errorf("error %q should mention the schema version mismatch", err)
	}
}

// TestVerifySnapshotFresh: an unmodified tree verifies fresh via the content
// walk (TreeOID is empty because t.TempDir is not a git worktree, so the git
// fast path cannot interfere).
func TestVerifySnapshotFresh(t *testing.T) {
	root, ix := buildSnapshotIndex(t)
	snap, err := ix.Snapshot("whole", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := VerifySnapshot(root, &snap, false)
	if err != nil {
		t.Fatal(err)
	}
	if verdict != FreshnessFresh {
		t.Fatalf("verdict = %q; want fresh", verdict)
	}
}

// TestVerifySnapshotStale: mutating one indexed file must flip the verdict to
// stale.
func TestVerifySnapshotStale(t *testing.T) {
	root, ix := buildSnapshotIndex(t)
	snap, err := ix.Snapshot("whole", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lib/lib.go"), []byte("package lib\nfunc Public() string { return \"changed\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verdict, err := VerifySnapshot(root, &snap, false)
	if err != nil {
		t.Fatal(err)
	}
	if verdict != FreshnessStale {
		t.Fatalf("verdict = %q; want stale", verdict)
	}
}

// TestVerifySnapshotMissing: deleting an indexed file must flip the verdict
// to stale immediately.
func TestVerifySnapshotMissing(t *testing.T) {
	root, ix := buildSnapshotIndex(t)
	snap, err := ix.Snapshot("whole", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "app/main.go")); err != nil {
		t.Fatal(err)
	}
	verdict, err := VerifySnapshot(root, &snap, false)
	if err != nil {
		t.Fatal(err)
	}
	if verdict != FreshnessStale {
		t.Fatalf("verdict = %q; want stale", verdict)
	}
}

// TestSnapshotSubgraphMode: subgraph mode returns a neighbourhood with a def
// node for the requested symbol; an unknown symbol errors.
func TestSnapshotSubgraphMode(t *testing.T) {
	_, ix := buildSnapshotIndex(t)
	snap, err := ix.Snapshot("subgraph", "Public", 0)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Mode != "subgraph" {
		t.Fatalf("Mode = %q; want subgraph", snap.Mode)
	}
	found := false
	for _, n := range snap.Graph.Nodes {
		if n.Role == "def" && strings.Contains(n.Name, "Public") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("subgraph nodes have no def node for Public: %+v", snap.Graph.Nodes)
	}
	if _, err := ix.Snapshot("subgraph", "definitelyMissing", 0); err == nil {
		t.Fatal("Snapshot must error for an unknown symbol")
	}
	if _, err := ix.Snapshot("bogus", "", 0); err == nil {
		t.Fatal("Snapshot must error for an unsupported mode")
	}
}

// TestVerifySnapshotNilAndVersion: a nil snapshot and a schema-version
// mismatch both report unknown without error.
func TestVerifySnapshotNilAndVersion(t *testing.T) {
	if verdict, err := VerifySnapshot(t.TempDir(), nil, false); err != nil || verdict != FreshnessUnknown {
		t.Fatalf("nil snapshot: verdict = %q, err = %v; want unknown, nil", verdict, err)
	}
	if verdict, err := VerifySnapshot(t.TempDir(), &GraphSnapshot{SchemaVersion: 99}, false); err != nil || verdict != FreshnessUnknown {
		t.Fatalf("version mismatch: verdict = %q, err = %v; want unknown, nil", verdict, err)
	}
}
