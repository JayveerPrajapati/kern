package index

// Slice 5 golden-parity tests for the graph sidecars. The speed path
// (loadCatalogSidecar) must never produce different graph data than the
// correctness path (buildExportCatalog + buildCalls). These tests pin:
//
//   1. a fresh catalog and the one decoded from the sidecar carry the same
//      node/edge/record sets;
//   2. the identity key is stable across two builds of the SAME fixture (the
//      exact cross-process contract the sidecar exists for);
//   3. a corrupted sidecar is a MISS (fallback rebuild), never wrong data;
//   4. the counts for a stable fixture are pinned in a golden file so format
//      drift is caught.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// catalogParityFixture is a small, deterministic Go module with cross-package
// calls, so the catalog has real records (not just declarations).
func catalogParityFixture() map[string]string {
	return map[string]string{
		"go.mod":           "module fx\n\ngo 1.20\n",
		"shared/shared.go": "package shared\n\nfunc Shared() int { return 1 }\n",
		"a/a.go": "" +
			"package a\n\nimport \"fx/shared\"\n\n" +
			"func Alpha() int { return shared.Shared() }\n" +
			"func Beta() int { return Help() }\n" +
			"func Help() int { return 2 }\n",
		"b/b.go": "" +
			"package b\n\nimport \"fx/shared\"\n\n" +
			"func Gamma() int { return shared.Shared() + alpha() }\n" +
			"func alpha() int { return 3 }\n",
	}
}

// signatureCatalog renders a canonical, order-independent description of the
// catalog's observable state, so a mismatch names the differing section.
func signatureCatalog(c *exportCatalog) map[string]string {
	sig := map[string]string{}
	var ids []string
	for id := range c.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	sig["byID.keys"] = strings.Join(ids, "\n")

	var fulls []string
	for name := range c.byFullName {
		fulls = append(fulls, name)
	}
	sort.Strings(fulls)
	var b strings.Builder
	for _, name := range fulls {
		var decls []string
		for _, s := range c.byFullName[name] {
			decls = append(decls, s.DeclarationID())
		}
		sort.Strings(decls)
		fmt.Fprintf(&b, "%s=%s\n", name, strings.Join(decls, ","))
	}
	sig["byFullName"] = b.String()

	var dirs []string
	for path, dir := range c.importDirs {
		dirs = append(dirs, path+"="+dir)
	}
	sort.Strings(dirs)
	sig["importDirs"] = strings.Join(dirs, "\n")

	var records []string
	for _, rec := range c.records {
		records = append(records, strings.Join([]string{
			rec.from.ID, rec.to.ID, rec.edge.From, rec.edge.To,
			rec.edge.Reference, rec.edge.SourceFile, rec.edge.Resolution,
			rec.edge.Synth, rec.edge.Binding, rec.edge.Confidence, rec.edge.ConfidenceLabel,
		}, "|"))
	}
	sort.Strings(records)
	sig["records"] = strings.Join(records, "\n")

	var endpoints []string
	for id, positions := range c.byEndpoint {
		var ps []string
		for _, p := range positions {
			ps = append(ps, fmt.Sprintf("%d", p))
		}
		endpoints = append(endpoints, id+"="+strings.Join(ps, ","))
	}
	sort.Strings(endpoints)
	sig["byEndpoint"] = strings.Join(endpoints, "\n")

	sig["files"] = strings.Join(c.files, "\n")
	var names []string
	for id, name := range c.names {
		names = append(names, id+"="+name)
	}
	sort.Strings(names)
	sig["names"] = strings.Join(names, "\n")
	return sig
}

func assertCatalogsEqual(t *testing.T, want, got *exportCatalog) {
	t.Helper()
	if want == nil || got == nil {
		t.Fatalf("nil catalog: want=%v got=%v", want, got)
	}
	a, b := signatureCatalog(want), signatureCatalog(got)
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if a[k] != b[k] {
			t.Fatalf("catalog %s drifted between build and sidecar:\n--- fresh ---\n%s\n--- loaded ---\n%s", k, a[k], b[k])
		}
	}
}

func TestExportCatalogSidecarParity(t *testing.T) {
	root := writeTree(t, catalogParityFixture())
	ix, err := Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := ix.GraphCacheKey(); !ok {
		t.Fatal("fixture index has no identity key; sidecar path cannot be exercised")
	}
	// newExportCatalog builds fresh (no sidecar exists yet), computes the
	// records + endpoint index, and best-effort saves the sidecar.
	fresh := newExportCatalog(ix)

	loaded := loadCatalogSidecar(ix)
	if loaded == nil {
		t.Fatal("sidecar was not saved/loaded for the fixture index")
	}
	assertCatalogsEqual(t, fresh, loaded)

	// Same fixture, second build: the identity key must be stable so a
	// separate process (represented here by a fresh *Index with an empty
	// cache) loads the sidecar instead of rebuilding.
	ix2, err := Build(root)
	if err != nil {
		t.Fatalf("Build (2): %v", err)
	}
	key1, _ := ix.GraphCacheKey()
	key2, _ := ix2.GraphCacheKey()
	if key1 != key2 {
		t.Fatalf("identity key drifted across builds: %s != %s", key1, key2)
	}
	loaded2 := loadCatalogSidecar(ix2)
	if loaded2 == nil {
		t.Fatal("second build missed the sidecar with an identical identity key")
	}
	assertCatalogsEqual(t, fresh, loaded2)

	// The observable graph (nodes/edges) is the actual product; pin it too.
	if got, want := ix2.CallBindings(), ix.CallBindings(); len(got.Nodes) != len(want.Nodes) || len(got.Edges) != len(want.Edges) {
		t.Fatalf("CallBindings node/edge count drifted: built=%d/%d loaded=%d/%d",
			len(want.Nodes), len(want.Edges), len(got.Nodes), len(got.Edges))
	}
}

// TestExportCatalogSidecarCorruptionFallsBack is the required proof that a
// tampered sidecar is never served: each corruption mode must be a miss, and
// the rebuild must reproduce the exact fresh catalog.
func TestExportCatalogSidecarCorruptionFallsBack(t *testing.T) {
	good := catalogParityFixture()
	corruptions := map[string]func([]byte) []byte{
		"garbage":   func([]byte) []byte { return []byte("this is not a gob stream") },
		"truncated": func(b []byte) []byte { return b[:len(b)/2] },
		"empty":     func([]byte) []byte { return nil },
	}
	for name, corrupt := range corruptions {
		t.Run(name, func(t *testing.T) {
			root := writeTree(t, good)
			ix, err := Build(root)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			fresh := newExportCatalog(ix) // builds + saves a valid sidecar
			key, ok := ix.GraphCacheKey()
			if !ok {
				t.Fatal("no identity key")
			}
			path := filepath.Join(root, ".kern", catalogSidecarName+"-"+key+".gob")
			valid, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read saved sidecar: %v", err)
			}
			if err := os.WriteFile(path, corrupt(valid), 0o644); err != nil {
				t.Fatalf("corrupt sidecar: %v", err)
			}
			if loadCatalogSidecar(ix) != nil {
				t.Fatal("corrupted sidecar was served as valid data")
			}
			// A fresh index (empty cache) must fall back to a rebuild and
			// reproduce the exact fresh catalog — never wrong data.
			ix2, err := Build(root)
			if err != nil {
				t.Fatalf("Build (2): %v", err)
			}
			if loadCatalogSidecar(ix2) != nil {
				t.Fatal("corrupted sidecar was served on a fresh index")
			}
			rebuilt := newExportCatalog(ix2)
			assertCatalogsEqual(t, fresh, rebuilt)
			// The rebuild re-saves a valid sidecar for the next process.
			if loadCatalogSidecar(ix2) == nil {
				t.Fatal("rebuild did not persist a fresh sidecar")
			}
		})
	}
}

// TestExportCatalogGoldenCounts pins the counts for the stable fixture. Run
// with UPDATE_GOLDEN=1 to regenerate after a conscious format change.
func TestExportCatalogGoldenCounts(t *testing.T) {
	root := writeTree(t, catalogParityFixture())
	ix, err := Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	catalog := newExportCatalog(ix)
	graph := ix.CallBindings()
	got := fmt.Sprintf(
		"nodes=%d\nedges=%d\nrecords=%d\nfiles=%d\nnames=%d\n",
		len(graph.Nodes), len(graph.Edges), len(catalog.records), len(catalog.files), len(catalog.names),
	)
	golden := filepath.Join("testdata", "export_catalog_counts.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s (run UPDATE_GOLDEN=1 go test ./internal/index -run TestExportCatalogGoldenCounts): %v", golden, err)
	}
	if string(want) != got {
		t.Fatalf("catalog counts drifted:\n--- want ---\n%s--- got ---\n%s", want, got)
	}
}
