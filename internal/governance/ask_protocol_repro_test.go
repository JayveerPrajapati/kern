package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAskProtocolPrefixGrantUseKeepsStore reproduces the live P1.6 e2e wipe:
// after a prefix grant authorizes one command (kern build 'make install'),
// the approvals store was rewritten to JSON null — the grant record vanished,
// so the next same-prefix command re-minted and re-asked. The full ask→use
// sequence must leave a live, use-counted grant in the store.
func TestAskProtocolPrefixGrantUseKeepsStore(t *testing.T) {
	overrideExecApprovalSecret(t)
	root := t.TempDir()

	// 1. Mint: a governed installer-class command requires approval.
	mintErr := CheckExecCommand("make install", root, "kern_build")
	if mintErr == nil {
		t.Fatal("expected an approval-required error for the installer-class command")
	}
	recs, err := NewFileStore(root).Load()
	if err != nil {
		t.Fatalf("load after mint: %v", err)
	}
	var id string
	for _, a := range recs {
		if a.Status == "pending" {
			id = a.ID
		}
	}
	if id == "" {
		t.Fatalf("no pending approval minted (records=%d)", len(recs))
	}

	// 2. Human answers the ask: [1] accept, then [2] widen to a prefix grant.
	if _, err := NewFileStore(root).DecideWithSigner(id, "human-test", true, "test ask accept", testExecSigner(t)); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if _, err := SetScopePrefixWithSigner(root, id, "", "human-test", testExecSigner(t)); err != nil {
		t.Fatalf("SetScopePrefix: %v", err)
	}
	recs, err = NewFileStore(root).Load()
	if err != nil {
		t.Fatalf("load after ask: %v", err)
	}
	if len(recs) != 1 || recs[0].Scope != "prefix" || recs[0].Uses != 0 {
		t.Fatalf("grant not persisted as expected: %+v", recs)
	}

	// 3. The gated command runs under the grant: one use is consumed.
	if err := CheckExecCommand("make install", root, "kern_build"); err != nil {
		t.Fatalf("prefix grant did not authorize the command: %v", err)
	}

	// 4. THE REGRESSION: the grant must survive its own use.
	after, err := NewFileStore(root).Load()
	if err != nil {
		t.Fatalf("load after use: %v", err)
	}
	live := 0
	for _, a := range after {
		if a.ID == id && livePrefixGrant(a, execRootIdentity(root)) && a.Uses == 1 {
			live++
		}
	}
	if live != 1 {
		t.Fatalf("prefix grant vanished after use: %d live records, file=%v", live, after)
	}

	// 5. And the second same-prefix command must also run without re-asking.
	if err := CheckExecCommand("make install", root, "kern_build"); err != nil {
		t.Fatalf("second same-prefix command re-asked (grant lost): %v", strings.TrimSpace(err.Error()))
	}
}

// TestPeekGateGrantDoesNotConsume pins the 2026-10-28 campaign fix: the
// mint-form CLI's consult must be READ-ONLY. A peek that consumed the
// single-use approval burned the operator's answer without any execution,
// and the taught byte-identical retry then denied and re-minted — an
// operator re-approval loop. The consuming claim stays in CheckGateGrant
// (the --check plugin/script form and the exec firewall).
func TestPeekGateGrantDoesNotConsume(t *testing.T) {
	overrideExecApprovalSecret(t)
	root := t.TempDir()

	// 1. Mint a gate approval for a GATE-class command.
	id, err := MintGateApproval("cp -R src dst", root)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := NewFileStore(root).DecideWithSigner(id, "human-test", true, "test ask accept", testExecSigner(t)); err != nil {
		t.Fatalf("decide: %v", err)
	}

	// 2. Peek (the no-check CLI form's consult): must report authorized…
	if err := PeekGateGrant("cp -R src dst", root); err != nil {
		t.Fatalf("peek should see the approved grant: %v", err)
	}
	// …twice (still there — nothing consumed), and for an uncovered command
	// it must NOT report authorized.
	if err := PeekGateGrant("cp -R src dst", root); err != nil {
		t.Fatalf("second peek consumed the grant (peek must be read-only): %v", err)
	}
	if err := PeekGateGrant("rm -rf /somewhere", root); err == nil {
		t.Fatal("peek must not authorize an uncovered command")
	}

	// 3. The approval must still be UNCONSUMED after the peeks — the claim
	// is reserved for the executing surface.
	recs, err := NewFileStore(root).Load()
	if err != nil {
		t.Fatalf("load after peeks: %v", err)
	}
	if len(recs) != 1 || recs[0].Uses != 0 {
		t.Fatalf("peek consumed the approval (uses should be 0): %+v", recs)
	}

	// 4. The executing claim still works exactly once: first CheckGateGrant
	// authorizes (and consumes), the second denies.
	if err := CheckGateGrant("cp -R src dst", root); err != nil {
		t.Fatalf("claim after peek should authorize: %v", err)
	}
	if err := CheckGateGrant("cp -R src dst", root); err == nil {
		t.Fatal("single-use approval survived its own execution claim")
	}
}

// TestCloseApprovalHintDecoratedRetry pins the 2026-10-28 campaign fix: when a
// retry is decorated ("approved-cmd && echo ok", "approved-cmd | tail"), the
// mint surfaces must point at the near-miss approval a human already
// answered, instead of silently minting a fresh one. The hint lives in
// contextcli (guard_grant.go, the surface agents actually loop on).
func TestCloseApprovalHintGovernanceSurface(t *testing.T) {
	root := t.TempDir()
	if _, err := MintGateApproval("cp -R a b", root); err != nil {
		t.Fatalf("mint: %v", err)
	}
	// The governance-side building blocks the contextcli hint uses: the
	// store exposes the near-miss record, and PrefixMatches sees the
	// token-boundary relation in both directions.
	records, err := NewFileStore(root).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(records) != 1 || records[0].Command != "cp -R a b" {
		t.Fatalf("minted record must carry the command for hint matching: %+v", records)
	}
	if !PrefixMatches("cp -R a b && echo done", "cp -R a b") {
		t.Fatal("PrefixMatches must see the decorated retry as extending the approved command")
	}
	if PrefixMatches("cp -R a b", "cp -R a b && echo done") {
		t.Fatal("PrefixMatches direction must be command-extends-prefix, not arbitrary similarity")
	}
}

// TestExecApprovalRootBound pins the 2026-10-28 campaign fix: an
// approval record copied to a DIFFERENT workspace (a copied
// .kern/approvals.json) must not grant there. The v4 exec-approval MAC binds
// the record to the canonical root identity, so the copy fails verification
// at the new root while the original still authorizes at its own.
func TestExecApprovalRootBound(t *testing.T) {
	overrideExecApprovalSecret(t)
	rootA := t.TempDir()
	rootB := t.TempDir()

	// 1. Mint + approve a gate approval at root A.
	id, err := MintGateApproval("cp -R src dst", rootA)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := NewFileStore(rootA).DecideWithSigner(id, "human-test", true, "test ask accept", testExecSigner(t)); err != nil {
		t.Fatalf("decide: %v", err)
	}

	// 2. THE ATTACK: copy .kern wholesale to root B and replay there —
	// the operator answered once, so the grant must not be portable.
	src := filepath.Join(rootA, ".kern")
	dst := filepath.Join(rootB, ".kern")
	data, err := os.ReadFile(filepath.Join(src, "approvals.json"))
	if err != nil {
		t.Fatalf("read source store: %v", err)
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatalf("mkdir copy store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dst, "approvals.json"), data, 0o600); err != nil {
		t.Fatalf("write copied store: %v", err)
	}
	if err := CheckGateGrant("cp -R src dst", rootB); err == nil {
		t.Fatal("copied approval granted at a different root (G-4 replay)")
	}
	// And the honest integrity surface flags it there too.
	recs, err := NewFileStore(rootB).Load()
	if err != nil {
		t.Fatalf("load copied store: %v", err)
	}
	if len(recs) != 1 || ApprovalIntegrityOK(recs[0], rootB) {
		t.Fatalf("copied record must fail integrity at the foreign root: %+v", recs)
	}

	// 3. The original root is unaffected — one approval, one workspace.
	if err := PeekGateGrant("cp -R src dst", rootA); err != nil {
		t.Fatalf("original root should still see the grant: %v", err)
	}
	if err := CheckGateGrant("cp -R src dst", rootA); err != nil {
		t.Fatalf("original root should authorize the claim: %v", err)
	}

	// 4. Root spelling must not matter: the same directory reached through
	// a symlinked path is the same identity (macOS /tmp vs /private/tmp).
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(rootA, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := MintGateApproval("mkdir -p a/b", link); err != nil {
		t.Fatalf("mint via alias: %v", err)
	}
	linkRecs, err := NewFileStore(link).Load()
	if err != nil {
		t.Fatalf("load via alias: %v", err)
	}
	for _, a := range linkRecs {
		if a.Status != "pending" {
			continue
		}
		if !execApprovalMACValid(a, execRootIdentity(rootA)) {
			t.Fatalf("record minted via the aliased path must verify under the canonical identity: %+v", a)
		}
	}
}
