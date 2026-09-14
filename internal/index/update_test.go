package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFileAt writes a file under root, creating parent dirs.
func writeFileAt(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// Ensure mtime advances so filesystem timestamp granularity on WSL/FAT/ext4
	// doesn't mask immediate overwrites within the same microsecond.
	mt := time.Now().Add(100 * time.Millisecond)
	_ = os.Chtimes(p, mt, mt)
}

// unchangedCount returns how many of prev's indexed files still exist on disk
// with the same content hash — the set Update must reuse, never re-parse.
func unchangedCount(t *testing.T, root string, prev *Index) int {
	t.Helper()
	cur, err := FileHashes(root)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for f, h := range cur {
		if ph, ok := prev.FileHashes[f]; ok && ph == h {
			n++
		}
	}
	return n
}

// TestUpdateEquivalentAfterModify asserts Update(root, prior) equals a full
// rebuild after one file is edited: same symbol set, same edge multiset, same
// per-file attribution, same packages and hashes.
func TestUpdateEquivalentAfterModify(t *testing.T) {
	root := fixtureTree(t)
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, root, "a/one.go", "package a\n\nfunc One() int { return 1 }\n\nfunc OneExtra() {}\n")

	inc, err := Update(root, prior)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalent(t, "modify one file", inc, full)
	if got := inc.ReusedResults(); got != unchangedCount(t, root, prior) {
		t.Errorf("modify: reused %d files, want %d (all unchanged files must skip re-parsing)", got, unchangedCount(t, root, prior))
	}
}

// TestUpdateEquivalentAfterAdd asserts Update equals a full rebuild after a
// new file is added.
func TestUpdateEquivalentAfterAdd(t *testing.T) {
	root := fixtureTree(t)
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, root, "b/four.go", "package b\n\nfunc Four() int { return 4 }\n")

	inc, err := Update(root, prior)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalent(t, "add one file", inc, full)
	if got := inc.ReusedResults(); got != unchangedCount(t, root, prior) {
		t.Errorf("add: reused %d files, want %d", got, unchangedCount(t, root, prior))
	}
	// The new symbol must be present, via the merged index only.
	if _, ok := inc.FindSymbol("Four"); !ok {
		t.Error("added symbol Four missing from incremental index")
	}
}

// TestUpdateEquivalentAfterDelete asserts Update equals a full rebuild after
// a file no other file references is deleted.
func TestUpdateEquivalentAfterDelete(t *testing.T) {
	root := fixtureTree(t)
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "b", "generated_gen.go")); err != nil {
		t.Fatal(err)
	}

	inc, err := Update(root, prior)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalent(t, "delete one file", inc, full)
	if got := inc.ReusedResults(); got != unchangedCount(t, root, prior) {
		t.Errorf("delete: reused %d files, want %d", got, unchangedCount(t, root, prior))
	}
	// Deleted file's symbols must be gone.
	for _, s := range inc.Symbols {
		if s.File == "b/generated_gen.go" {
			t.Errorf("deleted file still present in incremental index: %+v", s)
		}
	}
}

// TestUpdateLoadedPriorEquivalent exercises the reconstruction path: a prior
// loaded from disk has no per-file parse results, so Update must rebuild each
// unchanged file's contribution from prev's merged symbols/edges. The result
// must still equal a full rebuild (fixture has no inheritance edges, so
// prev's call lists carry no dispatch edges and copy order is preserved).
func TestUpdateLoadedPriorEquivalent(t *testing.T) {
	root := fixtureTree(t)
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := prior.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.fileResults) != 0 {
		t.Fatal("loaded prior must carry no per-file parse results")
	}
	// Modify one file, add one, delete one — all at once.
	writeFileAt(t, root, "a/one.go", "package a\n\nfunc One() int { return 1 }\n\nfunc OneExtra() {}\n")
	writeFileAt(t, root, "b/four.go", "package b\n\nfunc Four() int { return 4 }\n")
	if err := os.Remove(filepath.Join(root, "b", "generated_gen.go")); err != nil {
		t.Fatal(err)
	}

	inc, err := Update(root, loaded)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalent(t, "loaded prior, mixed change", inc, full)
	if got := inc.ReusedResults(); got != unchangedCount(t, root, loaded) {
		t.Errorf("loaded prior: reused %d files, want %d", got, unchangedCount(t, root, loaded))
	}
}

// TestUpdateDropsDanglingEdges asserts that removing a callee symbol drops the
// copied edges into it — for bare callees and for qualified cross-package
// callees ("a.One"), where the callee endpoint is qualified but the graph node
// ID is bare ("One"). A full rebuild keeps these edges (per-file extraction is
// syntactic), so this documents the intended incremental semantic: edges whose
// target no longer exists are pruned instead of dangling.
func TestUpdateDropsDanglingEdges(t *testing.T) {
	root := t.TempDir()
	writeFileAt(t, root, "a/one.go", "package a\n\nfunc One() int { return 1 }\n")
	writeFileAt(t, root, "a/two.go", "package a\n\nfunc Two() int { return One() }\n")
	writeFileAt(t, root, "b/three.go", "package b\n\nimport \"a\"\n\nfunc Three() int { return a.One() }\n")

	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "a", "one.go")); err != nil {
		t.Fatal(err)
	}

	inc, err := Update(root, prior)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}

	// Incremental: both the bare and the qualified edge into the removed
	// callee must be gone.
	for _, ce := range inc.Calls["Two"] {
		if ce.Target == "One" {
			t.Errorf("incremental index kept dangling bare edge Two->One")
		}
	}
	for _, ce := range inc.Calls["Three"] {
		if ce.Target == "a.One" {
			t.Errorf("incremental index kept dangling qualified edge Three->a.One")
		}
	}
	if got := inc.CallersOfName("One"); len(got) != 0 {
		t.Errorf("incremental index still reports callers of removed symbol One: %v", got)
	}

	// Full rebuild (documentation of the divergence): extraction is
	// syntactic, so the edges survive there.
	fullBare, fullQualified := false, false
	for _, ce := range full.Calls["Two"] {
		if ce.Target == "One" {
			fullBare = true
		}
	}
	for _, ce := range full.Calls["Three"] {
		if ce.Target == "a.One" {
			fullQualified = true
		}
	}
	if !fullBare || !fullQualified {
		t.Error("full rebuild should keep syntactic edges into removed callee (documents divergence)")
	}
}

// TestUpdateDoesNotReParseUnchanged pins the core B2 contract with an exact
// parse-count check: every file whose content hash matches the previous index
// must be reused (not re-parsed), regardless of whether the previous index
// came from an in-memory build or was loaded from disk.
func TestUpdateDoesNotReParseUnchanged(t *testing.T) {
	t.Run("in-memory prior", func(t *testing.T) {
		root := fixtureTree(t)
		prior, err := Build(root)
		if err != nil {
			t.Fatal(err)
		}
		writeFileAt(t, root, "b/four.go", "package b\n\nfunc Four() int { return 4 }\n")
		inc, err := Update(root, prior)
		if err != nil {
			t.Fatal(err)
		}
		want := unchangedCount(t, root, prior)
		if inc.ReusedResults() != want {
			t.Errorf("reused %d files, want %d (all unchanged files, and only those)", inc.ReusedResults(), want)
		}
		if want != 5 {
			t.Fatalf("fixture should have 5 unchanged files, got %d", want)
		}
	})
	t.Run("loaded prior", func(t *testing.T) {
		root := fixtureTree(t)
		prior, err := Build(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := prior.Save(); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		writeFileAt(t, root, "b/four.go", "package b\n\nfunc Four() int { return 4 }\n")
		inc, err := Update(root, loaded)
		if err != nil {
			t.Fatal(err)
		}
		want := unchangedCount(t, root, loaded)
		if inc.ReusedResults() != want {
			t.Errorf("reused %d files, want %d", inc.ReusedResults(), want)
		}
		if want != 5 {
			t.Fatalf("fixture should have 5 unchanged files, got %d", want)
		}
	})
}

// TestUpdateNilPriorReturnsError covers the fallback contract: Update with no
// previous index must error so callers fall back to a full Build.
func TestUpdateNilPriorReturnsError(t *testing.T) {
	root := fixtureTree(t)
	if _, err := Update(root, nil); err == nil {
		t.Fatal("Update(root, nil) must return an error")
	}
}

// TestUpdateWrongRootPriorReturnsError covers the fallback contract for a
// previous index that cannot be reused (different root): error, not a
// silently wrong incremental result.
func TestUpdateWrongRootPriorReturnsError(t *testing.T) {
	root := fixtureTree(t)
	other := fixtureTree(t)
	prior, err := Build(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Update(root, prior); err == nil {
		t.Fatal("Update with a prior from a different root must return an error")
	}
}

// TestUpdateChained checks Update(Update(...)) — the in-memory prior of the
// second call has fileResults populated by the first Update, so reuse flows
// through the per-file-results path again.
func TestUpdateChained(t *testing.T) {
	root := fixtureTree(t)
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, root, "a/one.go", "package a\n\nfunc One() int { return 1 }\n\nfunc OneExtra() {}\n")
	first, err := Update(root, prior)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReusedResults() == 0 {
		t.Fatal("first update reused nothing")
	}
	writeFileAt(t, root, "b/four.go", "package b\n\nfunc Four() int { return 4 }\n")
	second, err := Update(root, first)
	if err != nil {
		t.Fatal(err)
	}
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalent(t, "chained update", second, full)
	if second.ReusedResults() != unchangedCount(t, root, first) {
		t.Errorf("chained: reused %d files, want %d", second.ReusedResults(), unchangedCount(t, root, first))
	}
}

// TestUpdateLoadedPriorSkipsIgnoredFiles ensures the incremental walk applies
// the same .gitignore/.kernignore rules as Build: a gitignored file added
// after the prior build is not indexed by either path.
func TestUpdateLoadedPriorSkipsIgnoredFiles(t *testing.T) {
	root := fixtureTree(t)
	writeFileAt(t, root, ".gitignore", "ignored/\n")
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, root, "ignored/secret.py", "def leaked():\n    return 1\n")
	inc, err := Update(root, prior)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := inc.FindSymbol("leaked"); ok {
		t.Error("gitignored file was indexed by Update")
	}
	if _, ok := inc.FileHashes["ignored/secret.py"]; ok {
		t.Error("gitignored file hash recorded by Update")
	}
	// The unignored addition is still picked up.
	writeFileAt(t, root, "b/four.go", "package b\n\nfunc Four() int { return 4 }\n")
	inc2, err := Update(root, inc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := inc2.FindSymbol("Four"); !ok {
		t.Error("non-ignored added file missing from second Update")
	}
	if strings.Contains(inc2.Identity.ContentRoot, "leaked") {
		t.Error("ignored file leaked into content identity")
	}
}

// updateParallelTree writes a deterministic multi-package fixture sized to
// cross Update's parallelMin threshold (>= parallelMinFloor files, and below
// the large-profile parallelMin the tests use for the serial side), mirroring
// parallelTestTree's shape: each package imports and calls the previous one,
// plus foreign-language files, so Symbols, Calls, Inherits, Pkg import
// merging, ImportsByFile and GeneratedFiles all flow through the pool's
// ordered replay.
func updateParallelTree(t *testing.T) string {
	t.Helper()
	files := map[string]string{}
	for p := 0; p < 56; p++ {
		pkg := fmt.Sprintf("pkg%d", p)
		imports := ""
		if p > 0 {
			imports = fmt.Sprintf("\nimport \"pkg%d\"\n", p-1)
		}
		for f := 0; f < 5; f++ {
			callee := `"x"`
			if p > 0 {
				callee = fmt.Sprintf("pkg%d.Func%d()", p-1, f)
			}
			body := fmt.Sprintf(`package %s
%s
func Func%d() string {
	return Helper%d()
}

func Helper%d() string {
	return %s
}

type T%d struct{ V int }

func (t T%d) Method%d() int { return t.V + %d }
`, pkg, imports, f, f, f, callee, f, f, f, f)
			files[filepath.Join(pkg, fmt.Sprintf("file%d.go", f))] = body
		}
	}
	files["scripts/util.py"] = "def helper():\n    return 1\n\n\ndef run():\n    return helper()\n"
	files["scripts/app.js"] = "function helper() { return 1; }\nfunction run() { return helper(); }\n"
	return writeTree(t, files)
}

// updateMutateTree applies the change mix a real incremental refresh sees:
// one edited file (new symbol via re-parse), one new file, one deleted file
// whose cross-package callees dangle into the copied-edge set, and one
// touched-but-unchanged file (new mtime, same content — the content-hash
// match path).
func updateMutateTree(t *testing.T, root string) {
	t.Helper()
	writeFileAt(t, root, "pkg55/file0.go", "package pkg55\n\nfunc Func0() string { return Helper0() }\n\nfunc Helper0() string {\n\treturn \"x\"\n}\n\nfunc NewFunc() {}\n")
	writeFileAt(t, root, "pkg55/newfile.go", "package pkg55\n\nfunc Added() {}\n")
	if err := os.Remove(filepath.Join(root, "pkg0", "file2.go")); err != nil {
		t.Fatal(err)
	}
	// Touch pkg10/file0.go with identical content: new mtime, same hash.
	orig, err := os.ReadFile(filepath.Join(root, "pkg10", "file0.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg10", "file0.go"), orig, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestUpdateParallelMatchesSerial: the pool path must reproduce the serial
// path byte for byte. prev comes from a full Build; the same prev and the
// same (mutated) tree feed one Update forced onto the serial path (a
// large-worker profile whose parallelMin exceeds the file count) and one
// forced onto the pool path (a small-worker profile whose parallelMin the
// file count clears). The only difference between the two calls is the
// machine profile, hence the path taken — the same fakeResources technique
// resources_test.go uses to pin adaptive behavior.
func TestUpdateParallelMatchesSerial(t *testing.T) {
	root := updateParallelTree(t)
	fakeResources(t, 4, 8<<30) // small worker pool: parallelMin 256 <= 282 files
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	updateMutateTree(t, root)

	// Serial path: parallelMin 1024 > 282 files, so the pool is bypassed.
	fakeResources(t, 64, 8<<30)
	serial, err := Update(root, prior)
	if err != nil {
		t.Fatalf("serial-path Update: %v", err)
	}
	if len(serial.Symbols) == 0 {
		t.Fatal("fixture produced no symbols")
	}

	// Pool path: same prev, same tree, different profile.
	fakeResources(t, 4, 8<<30)
	parallel, err := Update(root, prior)
	if err != nil {
		t.Fatalf("pool-path Update: %v", err)
	}
	assertIndexesByteIdentical(t, serial, parallel)
}

// TestUpdateDeterministicRepeat: two pool-path Updates from the same prev and
// tree must be byte-identical (modulo wall-clock timestamps), like
// TestBuildParallelDeterministicRepeat.
func TestUpdateDeterministicRepeat(t *testing.T) {
	root := updateParallelTree(t)
	fakeResources(t, 4, 8<<30) // pool path: parallelMin 256 <= 282 files
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	updateMutateTree(t, root)

	a, err := Update(root, prior)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Update(root, prior)
	if err != nil {
		t.Fatal(err)
	}
	assertIndexesByteIdentical(t, a, b)
}

// TestUpdatePreservesStructFieldsOnLoadedPrior pins the receiver-field schema
// across incremental updates of a disk-loaded prior: reconstructFileResult
// must carry prev's package-merged StructFields forward (per-file attribution
// is not serialized), or every update strips them and the field-access callee
// rewrite ("App.taskSvc.Deploy" -> "TaskService.Deploy") silently degrades.
// Regression for the stale-index class that made kern dead re-flag live
// field-access method calls (verified 2026-09-11: httpClient.roundTrip was
// reported certainly dead after watcher-driven updates emptied StructFields).
func TestUpdatePreservesStructFieldsOnLoadedPrior(t *testing.T) {
	root := t.TempDir()
	writeFileAt(t, root, "a/app.go", `package a

type App struct {
	TaskSvc *TaskService
	Client  *HTTPClient
}

type TaskService struct{ ID string }
type HTTPClient struct{ Base string }
`)
	writeFileAt(t, root, "a/use.go", `package a

func Use(a *App) { a.TaskSvc.Run() }

func (t *TaskService) Run() {}
func (h *HTTPClient) Get() {}
`)
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(prior.Pkgs["a"].StructFields) == 0 {
		t.Fatal("fixture: Build must record struct fields")
	}
	if err := prior.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Pkgs["a"].StructFields) == 0 {
		t.Fatal("fixture: loaded prior must carry struct fields")
	}

	// Touch an unrelated file so the update actually re-runs the
	// reconstruction path for the unchanged struct-bearing file.
	writeFileAt(t, root, "a/use.go", `package a

func Use(a *App) { a.TaskSvc.Run() }

func (t *TaskService) Run() {}
func (h *HTTPClient) Get() {}
func (h *HTTPClient) Post() {}
`)
	inc, err := Update(root, loaded)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(inc.Pkgs["a"].StructFields) != len(full.Pkgs["a"].StructFields) {
		t.Errorf("Update stripped struct fields: inc=%d full=%d\ninc=%v\nfull=%v",
			len(inc.Pkgs["a"].StructFields), len(full.Pkgs["a"].StructFields),
			inc.Pkgs["a"].StructFields, full.Pkgs["a"].StructFields)
	}
	for k, v := range full.Pkgs["a"].StructFields {
		if inc.Pkgs["a"].StructFields[k] != v {
			t.Errorf("struct field %q diverged: inc=%q full=%q", k, inc.Pkgs["a"].StructFields[k], v)
		}
	}
	// The rewrite chain must still resolve the field-access callee.
	if got := inc.Callers["TaskService.Run"]; !containsStr(got, "Use") {
		t.Errorf("field-access caller lost after Update: TaskService.Run callers = %v, want Use", got)
	}
}
