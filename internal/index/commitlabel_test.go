package index

import (
	"testing"
)

// TestRefreshCommitLabelSealOnlyCommit is the label-refresh counterpart of
// TestSealOnlyCommitDoesNotFlagStale: an index is built at commit A, then a
// seal-only commit B moves HEAD without changing any indexed file's content.
// The content proof stays fresh (V7) — but the recorded git_commit label
// lags at A. RefreshCommitLabel (and the status surface) must refresh the
// label to B WITHOUT a rebuild, keeping the content proof fresh.
func TestRefreshCommitLabelSealOnlyCommit(t *testing.T) {
	root := t.TempDir()
	commit := initGitRepo(t, root)
	writeGoFile(t, root, "main.go", sealOnlyFixture)
	// sealed.txt is tracked by git but excluded from the index (both
	// .kernignore'd and a non-source extension), so the seal commit moves
	// HEAD^{tree} without changing any indexed file's content.
	writeGoFile(t, root, ".kernignore", "sealed.txt\n")
	commit("init")
	if _, err := BuildPersisted(root); err != nil {
		t.Fatalf("Build: %v", err)
	}
	ix, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ix.Identity == nil || ix.Identity.GitCommit == "" {
		t.Fatalf("Build must record a git_commit label, got %+v", ix.Identity)
	}
	buildCommit := ix.Identity.GitCommit
	// Seal-only commit: add the excluded file.
	writeGoFile(t, root, "sealed.txt", "sealed payload\n")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-qm", "seal")
	head := gitIn(t, root, "rev-parse", "--short", "HEAD")
	if head == buildCommit {
		t.Fatalf("fixture broken: seal commit did not move HEAD (build %q, head %q)", buildCommit, head)
	}
	// Control: content is still fresh, but the recorded label lags.
	ix2, err := Load(root)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if p := ix2.FreshnessProof(root); p.Verdict != FreshnessFresh {
		t.Fatalf("control: FreshnessProof verdict = %q, want fresh", p.Verdict)
	}
	if ix2.Identity.GitCommit != buildCommit {
		t.Fatalf("fixture broken: recorded git_commit %q already equals HEAD %q (label should lag)", ix2.Identity.GitCommit, head)
	}
	// 1. RefreshCommitLabel refreshes the persisted label, no rebuild.
	changed, err := RefreshCommitLabel(root, ix2)
	if err != nil {
		t.Fatalf("RefreshCommitLabel: %v", err)
	}
	if !changed {
		t.Errorf("RefreshCommitLabel changed (changed=true) — want changed=true when HEAD advanced")
	}
	// 2. Persisted label now reads HEAD; content proof still fresh.
	ix3, err := Load(root)
	if err != nil {
		t.Fatalf("re-Load after refresh: %v", err)
	}
	if ix3.Identity.GitCommit != head {
		t.Errorf("recorded git_commit after refresh = %q, want %q", ix3.Identity.GitCommit, head)
	}
	if p := ix3.FreshnessProof(root); p.Verdict != FreshnessFresh {
		t.Errorf("FreshnessProof verdict after refresh = %q, want fresh", p.Verdict)
	}
	// 3. The status surface shows the current commit while staying fresh.
	st, err := StatusReport(root, false)
	if err != nil {
		t.Fatalf("StatusReport: %v", err)
	}
	if st.IndexIdentity == nil || st.IndexIdentity.GitCommit != head {
		t.Errorf("StatusReport identity git_commit = %v, want %q", st.IndexIdentity, head)
	}
	if st.FreshnessProof.Verdict != FreshnessFresh {
		t.Errorf("StatusReport verdict = %q, want fresh", st.FreshnessProof.Verdict)
	}
	if st.Stale {
		t.Errorf("StatusReport stale = true, want false (label refresh must not flag stale)")
	}
	// 4. No rebuild is issued just to refresh the label.
	res, err := EnsureFresh(root)
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}
	if res.Freshness != "fresh" {
		t.Errorf("EnsureFresh Freshness = %q, want fresh (no rebuild for a label refresh)", res.Freshness)
	}
}

// TestRefreshCommitLabelIdempotent verifies the refresh is a no-op once the
// label is current: the second call changes nothing and writes nothing.
func TestRefreshCommitLabelIdempotent(t *testing.T) {
	root := t.TempDir()
	commit := initGitRepo(t, root)
	writeGoFile(t, root, "main.go", sealOnlyFixture)
	writeGoFile(t, root, ".kernignore", "sealed.txt\n")
	commit("init")
	if _, err := BuildPersisted(root); err != nil {
		t.Fatalf("Build: %v", err)
	}
	writeGoFile(t, root, "sealed.txt", "sealed payload\n")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-qm", "seal")
	head := gitIn(t, root, "rev-parse", "--short", "HEAD")
	ix, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	changed, err := RefreshCommitLabel(root, ix)
	if err != nil {
		t.Fatalf("RefreshCommitLabel: %v", err)
	}
	if !changed {
		t.Fatalf("first RefreshCommitLabel changed = false, want true")
	}
	// Second call on a freshly loaded index: no-op.
	ix2, err := Load(root)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	changed2, err := RefreshCommitLabel(root, ix2)
	if err != nil {
		t.Fatalf("second RefreshCommitLabel: %v", err)
	}
	if changed2 {
		t.Errorf("second RefreshCommitLabel changed = true, want idempotent no-op")
	}
	ix3, err := Load(root)
	if err != nil {
		t.Fatalf("re-Load after no-op: %v", err)
	}
	if ix3.Identity.GitCommit != head {
		t.Errorf("recorded git_commit after no-op call = %q, want %q", ix3.Identity.GitCommit, head)
	}
	// The status surface is idempotent too: two consecutive status calls
	// must not flip the label or the verdict.
	st1, err := StatusReport(root, false)
	if err != nil {
		t.Fatalf("StatusReport 1: %v", err)
	}
	st2, err := StatusReport(root, false)
	if err != nil {
		t.Fatalf("StatusReport 2: %v", err)
	}
	if st1.IndexIdentity == nil || st2.IndexIdentity == nil {
		t.Fatalf("StatusReport identity missing: %v / %v", st1.IndexIdentity, st2.IndexIdentity)
	}
	if st1.IndexIdentity.GitCommit != st2.IndexIdentity.GitCommit {
		t.Errorf("status git_commit changed across no-op calls: %q -> %q", st1.IndexIdentity.GitCommit, st2.IndexIdentity.GitCommit)
	}
	if st1.FreshnessProof.Verdict != FreshnessFresh || st2.FreshnessProof.Verdict != FreshnessFresh {
		t.Errorf("status verdict not fresh across no-op calls: %q / %q", st1.FreshnessProof.Verdict, st2.FreshnessProof.Verdict)
	}
}

// TestRefreshCommitLabelStaleRebuildsNormally verifies the refresh never
// touches a genuinely stale index: RefreshCommitLabel no-ops, the status
// surface reports stale, and the NORMAL rebuild path rebuilds and records
// the new commit at build time.
func TestRefreshCommitLabelStaleRebuildsNormally(t *testing.T) {
	root := t.TempDir()
	commit := initGitRepo(t, root)
	writeGoFile(t, root, "main.go", "package main\n\nfunc A() {}\n")
	commit("init")
	if _, err := BuildPersisted(root); err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Real content edit, then commit: the content root moves.
	writeGoFile(t, root, "main.go", "package main\n\nfunc A() {}\n\nfunc B() {}\n")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-qm", "edit indexed content")
	head := gitIn(t, root, "rev-parse", "--short", "HEAD")
	ix, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ix.Identity == nil || ix.Identity.GitCommit == "" {
		t.Fatalf("Build must record a git_commit label, got %+v", ix.Identity)
	}
	buildCommit := ix.Identity.GitCommit
	// RefreshCommitLabel must NOT touch a stale index.
	changed, err := RefreshCommitLabel(root, ix)
	if err != nil {
		t.Fatalf("RefreshCommitLabel: %v", err)
	}
	if changed {
		t.Errorf("RefreshCommitLabel changed = true on a STALE index, want false (rebuild path owns it)")
	}
	if ix.Identity.GitCommit != buildCommit {
		t.Errorf("stale index label was mutated: %q -> %q", buildCommit, ix.Identity.GitCommit)
	}
	// Status reports stale (falling through to the rebuild path untouched).
	st, err := StatusReport(root, false)
	if err != nil {
		t.Fatalf("StatusReport: %v", err)
	}
	if !st.Stale {
		t.Errorf("StatusReport stale = false on edited content, want true")
	}
	// The normal rebuild path rebuilds and records the new commit.
	res, err := EnsureFresh(root)
	if err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}
	if res.Freshness != "rebuilt" {
		t.Errorf("EnsureFresh Freshness = %q, want rebuilt (stale content must still rebuild)", res.Freshness)
	}
	ix2, err := Load(root)
	if err != nil {
		t.Fatalf("re-Load after rebuild: %v", err)
	}
	if ix2.Identity == nil || ix2.Identity.GitCommit != head {
		t.Errorf("rebuilt identity git_commit = %v, want %q", ix2.Identity, head)
	}
	if p := ix2.FreshnessProof(root); p.Verdict != FreshnessFresh {
		t.Errorf("post-rebuild verdict = %q, want fresh", p.Verdict)
	}
}
