package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// gitIn runs `git -C dir <args...>` via the real git binary, failing the test
// on any error. The EnsureFresh tests need real git because the cheap
// non-strict freshness probe (TreeOIDProbe) compares git tree OIDs.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// initGitRepo initializes a git repository in dir with a user identity so
// commits succeed, and returns a helper that commits all changes.
func initGitRepo(t *testing.T, dir string) func(msg string) {
	t.Helper()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "config", "user.name", "t")
	gitIn(t, dir, "config", "user.email", "t@t")
	return func(msg string) {
		t.Helper()
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-qm", msg)
	}
}

// preserveMtimeEdit rewrites path with sameLength different bytes and restores
// the original mtime with os.Chtimes — exactly what `git apply` does — and
// returns the mutated content. The test fails if the mtime is not preserved.
func preserveMtimeEdit(t *testing.T, path, mutated string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	oldMtime := fi.ModTime()
	orig, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(mutated) != len(orig) {
		t.Fatalf("preserveMtimeEdit: mutated length %d != original length %d (must be byte-identical length)", len(mutated), len(orig))
	}
	if err := os.WriteFile(path, []byte(mutated), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, oldMtime, oldMtime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
	fi2, err := os.Stat(path)
	if err != nil {
		t.Fatalf("re-stat %s: %v", path, err)
	}
	if !fi2.ModTime().Equal(oldMtime) {
		t.Fatalf("test setup: mtime not preserved (%v != %v)", fi2.ModTime(), oldMtime)
	}
}

const mtimeFixture = "package main\n\nfunc A() {}\n\nfunc B() {}\n"

// mtimeFixtureMutated is byte-for-byte the same length as mtimeFixture with
// different content (A -> C), so it changes the git tree OID and the content
// root while keeping the mtime invisible to stat-based gates.
const mtimeFixtureMutated = "package main\n\nfunc C() {}\n\nfunc B() {}\n"

// TestEnsureFresh_Fresh: on a clean committed git tree with a fresh index,
// EnsureFresh returns "fresh" WITHOUT issuing an update (the on-disk index is
// not rewritten).
func TestEnsureFresh_Fresh(t *testing.T) {
	root := t.TempDir()
	commit := initGitRepo(t, root)
	writeGoFile(t, root, "main.go", mtimeFixture)
	commit("init")

	svc := New()
	ix, err := svc.Index.Build(context.Background(), root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ix.Identity == nil || ix.Identity.TreeOID == "" {
		t.Fatalf("Build must record a git TreeOID for a committed worktree, got %+v", ix.Identity)
	}
	idxPath := filepath.Join(root, ".kern", "index.json")
	before, err := os.Stat(idxPath)
	if err != nil {
		t.Fatalf("stat index.json: %v", err)
	}

	res, err := svc.Index.EnsureFresh(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}
	if res.Freshness != "fresh" {
		t.Fatalf("Freshness = %q, want %q", res.Freshness, "fresh")
	}
	if res.FreshnessProof.Verdict != index.FreshnessFresh {
		t.Errorf("verdict = %q, want %q", res.FreshnessProof.Verdict, index.FreshnessFresh)
	}
	if !res.Built {
		t.Error("Built = false, want true")
	}
	after, err := os.Stat(idxPath)
	if err != nil {
		t.Fatalf("re-stat index.json: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("index.json rewritten on the fresh path (update issued despite fresh index)")
	}
}

// TestEnsureFresh_RebuildsWhenStale: an unstaged content edit makes the index
// stale; EnsureFresh updates it in one call and converges to "rebuilt" with a
// fresh final verdict.
func TestEnsureFresh_RebuildsWhenStale(t *testing.T) {
	root := t.TempDir()
	commit := initGitRepo(t, root)
	writeGoFile(t, root, "main.go", mtimeFixture)
	commit("init")

	svc := New()
	if _, err := svc.Index.Build(context.Background(), root); err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Unstaged edit (dirty working tree): tree OID differs from the recorded
	// one, so the cheap probe reports stale.
	writeGoFile(t, root, "main.go", "package main\n\nfunc A() {}\n\nfunc B() {}\n\nfunc C() {}\n")

	res, err := svc.Index.EnsureFresh(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}
	if res.Freshness != "rebuilt" {
		t.Fatalf("Freshness = %q, want %q", res.Freshness, "rebuilt")
	}
	if res.FreshnessProof.Verdict != index.FreshnessFresh {
		t.Errorf("final verdict = %q, want %q (strict post-save re-verify must observe fresh)", res.FreshnessProof.Verdict, index.FreshnessFresh)
	}
	if res.Symbols < 3 {
		t.Errorf("Symbols = %d, want >= 3 (updated index must contain the new function)", res.Symbols)
	}
	// The persisted index now converges under a strict re-read.
	st, err := svc.Index.Status(context.Background(), root, true)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Stale {
		t.Error("Status(strict) still stale after EnsureFresh rebuilt the index")
	}
}

// TestEnsureFresh_NoIndex: with no saved index, EnsureFresh falls back to a
// full build and returns "rebuilt" with a fresh verdict.
func TestEnsureFresh_NoIndex(t *testing.T) {
	root := t.TempDir()
	commit := initGitRepo(t, root)
	writeGoFile(t, root, "main.go", mtimeFixture)
	commit("init")

	svc := New()
	res, err := svc.Index.EnsureFresh(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}
	if res.Freshness != "rebuilt" {
		t.Fatalf("Freshness = %q, want %q", res.Freshness, "rebuilt")
	}
	if !res.Built {
		t.Error("Built = false, want true (full build must persist a built index)")
	}
	if res.FreshnessProof.Verdict != index.FreshnessFresh {
		t.Errorf("final verdict = %q, want %q", res.FreshnessProof.Verdict, index.FreshnessFresh)
	}
	if _, err := os.Stat(filepath.Join(root, ".kern", "index.json")); err != nil {
		t.Errorf("index.json not persisted after full build: %v", err)
	}
}

// TestEnsureFresh_LegacyIndexWithoutTreeOID: a saved index whose Identity has
// NO recorded TreeOID (a legacy index, or a kern repo before the treeOID slow
// path was repaired — the live defect) must still be served as "fresh" when
// the on-disk tree content matches the recorded ContentRoot. The probe is
// inconclusive (decided=false), so EnsureFresh falls back to the LOOSE
// content proof; a match proves the index is current and the rebuild is
// skipped. Regression test for the defect that rebuilt on EVERY warm run.
func TestEnsureFresh_LegacyIndexWithoutTreeOID(t *testing.T) {
	root := t.TempDir()
	commit := initGitRepo(t, root)
	writeGoFile(t, root, "main.go", mtimeFixture)
	commit("init")

	svc := New()
	if _, err := svc.Index.Build(context.Background(), root); err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Blank the recorded TreeOID and persist: simulates a legacy/kern-repo
	// index whose saved identity has no tree_oid (json omitempty).
	ix, err := index.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ix.Identity == nil || ix.Identity.TreeOID == "" {
		t.Fatalf("precondition: Build must have recorded a TreeOID, got %+v", ix.Identity)
	}
	ix.Identity.TreeOID = ""
	if err := ix.Save(); err != nil {
		t.Fatalf("Save blanked index: %v", err)
	}

	idxPath := filepath.Join(root, ".kern", "index.json")
	before, err := os.Stat(idxPath)
	if err != nil {
		t.Fatalf("stat index.json: %v", err)
	}

	res, err := svc.Index.EnsureFresh(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}
	if res.Freshness != "fresh" {
		t.Fatalf("Freshness = %q, want %q (loose content proof matches the recorded ContentRoot; no rebuild)", res.Freshness, "fresh")
	}
	if res.FreshnessProof.Verdict != index.FreshnessFresh {
		t.Errorf("verdict = %q, want %q", res.FreshnessProof.Verdict, index.FreshnessFresh)
	}
	// The proof must come from the loose Status fallback (recorded TreeOID
	// still empty), proving the fallback fired instead of the OID probe.
	if res.FreshnessProof.Recorded.TreeOID != "" {
		t.Errorf("proof.Recorded.TreeOID = %q, want empty (loose fallback must observe the blanked legacy identity)", res.FreshnessProof.Recorded.TreeOID)
	}
	after, err := os.Stat(idxPath)
	if err != nil {
		t.Fatalf("re-stat index.json: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("index.json rewritten on the legacy-fresh path (update issued despite fresh content)")
	}
}

// TestEnsureFresh_DecisiveStaleSkipsLooseCheck: when the probe is DECISIVE
// (recorded TreeOID present, current tree differs) EnsureFresh goes straight
// to the rebuild path — the loose content walk is not run before the update,
// so the cold path stays single-walk.
func TestEnsureFresh_DecisiveStaleSkipsLooseCheck(t *testing.T) {
	root := t.TempDir()
	commit := initGitRepo(t, root)
	writeGoFile(t, root, "main.go", mtimeFixture)
	commit("init")

	svc := New()
	if _, err := svc.Index.Build(context.Background(), root); err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Control: the probe is decisive AND fresh before any edit.
	ix, err := index.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if fresh, decided, _ := ix.TreeOIDProbe(root); !fresh || !decided {
		t.Fatalf("pre-edit probe = (fresh=%v, decided=%v), want (true, true)", fresh, decided)
	}

	// Unstaged same-size edit: decisive stale (git hashes content, not mtime).
	writeGoFile(t, root, "main.go", mtimeFixtureMutated)
	if fresh, decided, _ := ix.TreeOIDProbe(root); fresh || !decided {
		t.Fatalf("post-edit probe = (fresh=%v, decided=%v), want (false, true)", fresh, decided)
	}

	res, err := svc.Index.EnsureFresh(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}
	if res.Freshness != "rebuilt" {
		t.Fatalf("Freshness = %q, want %q (decisive stale must rebuild)", res.Freshness, "rebuilt")
	}
	if res.FreshnessProof.Verdict != index.FreshnessFresh {
		t.Errorf("final verdict = %q, want %q (post-rebuild strict re-verify must observe fresh)", res.FreshnessProof.Verdict, index.FreshnessFresh)
	}
}

// TestEnsureFresh_NonConvergence: an index that stays stale after the update
// must fail closed with Freshness "stale". Deterministic non-convergence:
// a non-git repo (no tree-OID identity to fall back on) whose source tree
// contains a file larger than the build's maxFileBytes cap — the build (and
// the incremental update) skip it, but the strict/loose content-root proof
// counts every indexable file, so the content root can never match. Both the
// strict and the loose re-verify report stale → fail-closed "stale".
func TestEnsureFresh_NonConvergence(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "small.go", "package small\n\nfunc S() {}\n")
	// > maxFileBytesCap (64 MiB) so no machine's adaptive limit admits it.
	big := "package big\n" + strings.Repeat("// "+strings.Repeat("x", 60)+"\n", 70*1024*1024/64)
	writeGoFile(t, root, "big.go", big)

	svc := New()
	if _, err := svc.Index.Build(context.Background(), root); err != nil {
		t.Fatalf("Build: %v", err)
	}

	res, err := svc.Index.EnsureFresh(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}
	if res.Freshness != "stale" {
		t.Fatalf("Freshness = %q, want %q (non-converging index must fail closed)", res.Freshness, "stale")
	}
	if res.FreshnessProof.Verdict == index.FreshnessFresh {
		t.Errorf("verdict = %q, want non-fresh for a non-converged index", res.FreshnessProof.Verdict)
	}
}

// --- Attack-class regression tests: mtime-preserving edits (git apply) ---

// TestStrictProbeDetectsMtimePreservingEdit: an edit that preserves BOTH size
// and mtime (exactly what `git apply` produces) must still flip the STRICT
// status verdict to stale — strict recomputes the content root, which hashes
// content, not mtime.
func TestStrictProbeDetectsMtimePreservingEdit(t *testing.T) {
	root := t.TempDir()
	commit := initGitRepo(t, root)
	writeGoFile(t, root, "main.go", mtimeFixture)
	commit("init")

	svc := New()
	if _, err := svc.Index.Build(context.Background(), root); err != nil {
		t.Fatalf("Build: %v", err)
	}
	st, err := svc.Index.Status(context.Background(), root, true)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.FreshnessProof.Verdict != index.FreshnessFresh {
		t.Fatalf("baseline strict verdict = %q, want %q", st.FreshnessProof.Verdict, index.FreshnessFresh)
	}

	preserveMtimeEdit(t, filepath.Join(root, "main.go"), mtimeFixtureMutated)

	st, err = svc.Index.Status(context.Background(), root, true)
	if err != nil {
		t.Fatalf("Status after edit: %v", err)
	}
	if st.FreshnessProof.Verdict != index.FreshnessStale {
		t.Errorf("strict verdict after mtime-preserving edit = %q, want %q (git apply regression: strict must catch content changes)", st.FreshnessProof.Verdict, index.FreshnessStale)
	}
	if !st.Stale {
		t.Error("Status.Stale = false after mtime-preserving edit, want true")
	}
}

// TestEnsureFreshRebuildsOnMtimePreservingEdit: the same mtime-preserving
// edit drives EnsureFresh to "rebuilt" and the final strict verdict is fresh
// — the consolidated flow converges on the attack class instead of serving
// the stale index as fresh.
func TestEnsureFreshRebuildsOnMtimePreservingEdit(t *testing.T) {
	root := t.TempDir()
	commit := initGitRepo(t, root)
	writeGoFile(t, root, "main.go", mtimeFixture)
	commit("init")

	svc := New()
	if _, err := svc.Index.Build(context.Background(), root); err != nil {
		t.Fatalf("Build: %v", err)
	}

	preserveMtimeEdit(t, filepath.Join(root, "main.go"), mtimeFixtureMutated)

	res, err := svc.Index.EnsureFresh(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}
	if res.Freshness != "rebuilt" {
		t.Fatalf("Freshness = %q, want %q (mtime-preserving edit must trigger a rebuild)", res.Freshness, "rebuilt")
	}
	if res.FreshnessProof.Verdict != index.FreshnessFresh {
		t.Errorf("final verdict = %q, want %q (post-rebuild strict re-verify must observe fresh)", res.FreshnessProof.Verdict, index.FreshnessFresh)
	}
	if res.IndexIdentity == nil {
		t.Error("IndexIdentity missing from rebuilt result")
	}
}

// TestTreeOIDMatchesDetectsMtimePreservingEdit: the cheap NON-strict probe
// itself catches the attack class — git hashes content, not mtime, so a
// same-size same-mtime content edit flips the working-tree tree OID and
// TreeOIDMatches returns false. This proves the probe is a genuine guard, not
// a stat-based shortcut.
func TestTreeOIDMatchesDetectsMtimePreservingEdit(t *testing.T) {
	root := t.TempDir()
	commit := initGitRepo(t, root)
	writeGoFile(t, root, "main.go", mtimeFixture)
	commit("init")

	svc := New()
	if _, err := svc.Index.Build(context.Background(), root); err != nil {
		t.Fatalf("Build: %v", err)
	}
	ix, err := index.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ix.Identity == nil || ix.Identity.TreeOID == "" {
		t.Fatalf("index missing git TreeOID: %+v", ix.Identity)
	}
	if ok, _ := ix.TreeOIDMatches(root); !ok {
		t.Fatal("TreeOIDMatches = false before the edit, want true (control)")
	}

	preserveMtimeEdit(t, filepath.Join(root, "main.go"), mtimeFixtureMutated)

	ok, err := ix.TreeOIDMatches(root)
	if err != nil {
		t.Fatalf("TreeOIDMatches: %v", err)
	}
	if ok {
		t.Error("TreeOIDMatches = true after mtime-preserving edit, want false (git hashes content, not mtime)")
	}
}

// TestTreeOIDProbeDetectsMtimePreservingEdit: the tri-state probe reports a
// DECISIVE stale (fresh=false, decided=true) on an mtime-preserving same-size
// edit — git hashes content, not mtime, so the working-tree OID flips and the
// probe knows the recorded TreeOID is outdated rather than merely
// inconclusive.
func TestTreeOIDProbeDetectsMtimePreservingEdit(t *testing.T) {
	root := t.TempDir()
	commit := initGitRepo(t, root)
	writeGoFile(t, root, "main.go", mtimeFixture)
	commit("init")

	svc := New()
	if _, err := svc.Index.Build(context.Background(), root); err != nil {
		t.Fatalf("Build: %v", err)
	}
	ix, err := index.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ix.Identity == nil || ix.Identity.TreeOID == "" {
		t.Fatalf("index missing git TreeOID: %+v", ix.Identity)
	}
	if fresh, decided, _ := ix.TreeOIDProbe(root); !fresh || !decided {
		t.Fatalf("TreeOIDProbe = (fresh=%v, decided=%v) before the edit, want (true, true) (control)", fresh, decided)
	}

	preserveMtimeEdit(t, filepath.Join(root, "main.go"), mtimeFixtureMutated)

	fresh, decided, err := ix.TreeOIDProbe(root)
	if err != nil {
		t.Fatalf("TreeOIDProbe: %v", err)
	}
	if fresh {
		t.Error("TreeOIDProbe = fresh after mtime-preserving edit, want false (git hashes content, not mtime)")
	}
	if !decided {
		t.Error("TreeOIDProbe = not decided after mtime-preserving edit, want decided=true (recorded TreeOID exists and current differs)")
	}
}
