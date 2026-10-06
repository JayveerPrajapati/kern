package index

import (
	"testing"
)

// sealOnlyFixture is a small parseable Go file whose content never changes
// across the seal-only-commit scenario.
const sealOnlyFixture = "package main\n\nfunc SealOnly() {}\n"

// TestSealOnlyCommitDoesNotFlagStale reproduces the staleness-probe
// inconsistency: an index is built at commit A, then commit B changes the git
// tree OID WITHOUT changing any indexed file's content (here: a change to a
// file the index excludes — a .kernignore'd, non-source .txt file). The tree
// OID fast-path probe alone still reports decided-stale (it is a fast path,
// not a verdict), but every content-aware surface — FreshnessProof,
// StatusReport (`kern index --status`), DiskIndexView (`kern health` / MCP
// disk view) and EnsureFresh — must judge the index fresh and skip the
// rebuild, so all surfaces agree.
func TestSealOnlyCommitDoesNotFlagStale(t *testing.T) {
	root := t.TempDir()
	commit := initGitRepo(t, root)
	writeGoFile(t, root, "main.go", sealOnlyFixture)
	// sealed.txt is tracked by git but excluded from the index: it is both
	// .kernignore'd and a non-source extension, so no indexed file's content
	// changes when a commit touches it.
	writeGoFile(t, root, ".kernignore", "sealed.txt\n")
	commit("init")

	if _, err := BuildPersisted(root); err != nil {
		t.Fatalf("Build: %v", err)
	}
	ix, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ix.Identity == nil || ix.Identity.TreeOID == "" {
		t.Fatalf("Build must record a git TreeOID on a committed worktree, got %+v", ix.Identity)
	}
	recordedOID := ix.Identity.TreeOID

	// Seal-only commit: add the excluded file. The working-tree content of
	// every INDEXED file is untouched, but HEAD^{tree} moves.
	writeGoFile(t, root, "sealed.txt", "sealed payload\n")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-m", "seal")

	ix2, err := Load(root)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}

	// Control: the fixture must actually move the tree OID, and the raw
	// TreeOIDProbe verdict on it must be decided-stale — the fast path is
	// NOT the final word.
	if cur := treeOID(root); cur == "" || cur == recordedOID {
		t.Fatalf("control: current tree OID %q must differ from recorded %q (seal-only commit must move HEAD^{tree})", cur, recordedOID)
	}
	fresh, decided, _ := ix2.TreeOIDProbe(root)
	if fresh || !decided {
		t.Fatalf("control: TreeOIDProbe = (fresh=%v, decided=%v) after seal-only commit, want (false, true)", fresh, decided)
	}

	// 1. The loose content-aware proof is authoritative: fresh.
	if proof := ix2.FreshnessProof(root); proof.Verdict != FreshnessFresh {
		t.Errorf("FreshnessProof verdict = %q, want %q", proof.Verdict, FreshnessFresh)
	}

	// 2. `kern index --status` (StatusReport, non-strict): not stale.
	st, err := StatusReport(root, false)
	if err != nil {
		t.Fatalf("StatusReport: %v", err)
	}
	if !st.Built {
		t.Errorf("StatusReport Built = false, want true")
	}
	if st.Stale {
		t.Errorf("StatusReport stale = true, want false (seal-only commit must not flag stale)")
	}

	// 3. DiskIndexView (`kern health` / MCP disk view): not stale.
	dv := DiskIndexView(root)
	if dv == nil {
		t.Fatal("DiskIndexView = nil, want a disk view")
	}
	if stale, _ := dv["stale"].(bool); stale {
		t.Errorf("DiskIndexView stale = true, want false")
	}
	if dv["verdict"] != "fresh" {
		t.Errorf("DiskIndexView verdict = %v, want fresh", dv["verdict"])
	}

	// 4. EnsureFresh: a seal-only commit must NOT trigger a rebuild.
	res, err := EnsureFresh(root)
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}
	if res.Freshness != "fresh" {
		t.Errorf("EnsureFresh Freshness = %q, want %q (seal-only commit must not rebuild)", res.Freshness, "fresh")
	}
	if res.FreshnessProof.Verdict != FreshnessFresh {
		t.Errorf("EnsureFresh final verdict = %q, want %q", res.FreshnessProof.Verdict, FreshnessFresh)
	}
}

// TestCommitSealingDirtyWorktreeStaysFresh mirrors the other half of the real
// scenario: the index is built while the working tree contains uncommitted
// (already indexed) changes, then `git commit` seals them. The committed tree
// content is identical to the working tree the index was built from, so both
// the tree-OID probe AND the content proof stay fresh — the only thing that
// moves is the git_commit label, which is provenance, not a staleness
// verdict. No rebuild is issued just to refresh the label.
func TestCommitSealingDirtyWorktreeStaysFresh(t *testing.T) {
	root := t.TempDir()
	commit := initGitRepo(t, root)
	writeGoFile(t, root, "main.go", "package main\n\nfunc Base() {}\n")
	commit("init")

	// Uncommitted-but-indexed change: build the index over the DIRTY tree.
	writeGoFile(t, root, "main.go", sealOnlyFixture)
	if _, err := BuildPersisted(root); err != nil {
		t.Fatalf("Build over dirty tree: %v", err)
	}
	ix, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ix.Identity == nil || ix.Identity.TreeOID == "" {
		t.Fatalf("Build must record a git TreeOID even on a dirty tree, got %+v", ix.Identity)
	}
	if ix.Identity.GitCommit == "" {
		t.Fatalf("Build must record a git_commit label, got %+v", ix.Identity)
	}
	recordedOID := ix.Identity.TreeOID

	// git commit seals the dirty work: HEAD^{tree} == the recorded tree.
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-m", "seal indexed work")

	// The tree OID is unchanged by a seal commit of the indexed content…
	if cur := treeOID(root); cur != recordedOID {
		t.Errorf("tree OID changed by seal commit: recorded %q, current %q (sealing identical content must not move the tree)", recordedOID, cur)
	}
	ix2, err := Load(root)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	fresh, decided, _ := ix2.TreeOIDProbe(root)
	if !fresh || !decided {
		t.Errorf("TreeOIDProbe = (fresh=%v, decided=%v) after sealing the indexed work, want (true, true)", fresh, decided)
	}
	// …and the content-aware surfaces agree.
	if proof := ix2.FreshnessProof(root); proof.Verdict != FreshnessFresh {
		t.Errorf("FreshnessProof verdict = %q, want %q", proof.Verdict, FreshnessFresh)
	}
	if st, err := StatusReport(root, false); err != nil || st.Stale {
		t.Errorf("StatusReport stale = %v (err %v), want false", st != nil && st.Stale, err)
	}
	if dv := DiskIndexView(root); dv == nil || dv["stale"] == true {
		t.Errorf("DiskIndexView = %v, want stale=false", dv)
	}
	// The git_commit LABEL self-heals on the status/diskview surfaces: the
	// recorded provenance now names the current HEAD (the content is still
	// current — the seal commit only moved the label), and refreshing the
	// label must never trigger a rebuild.
	ix3, err := Load(root)
	if err != nil {
		t.Fatalf("re-Load after seal: %v", err)
	}
	curHead := gitIn(t, root, "rev-parse", "--short", "HEAD")
	if ix3.Identity == nil || ix3.Identity.GitCommit == "" {
		t.Fatalf("recorded identity missing git_commit label: %+v", ix3.Identity)
	}
	if ix3.Identity.GitCommit != curHead {
		t.Errorf("recorded git_commit %q != current HEAD %q (the status/diskview surfaces must refresh the label)", ix3.Identity.GitCommit, curHead)
	}
	res, err := EnsureFresh(root)
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}
	if res.Freshness != "fresh" {
		t.Errorf("EnsureFresh Freshness = %q, want %q (do not rebuild just to refresh the git_commit label)", res.Freshness, "fresh")
	}
}
