package governance

import (
	"strings"
	"testing"
)

// TestAskProtocolPrefixGrantUseKeepsStore reproduces the live P1.6 e2e wipe:
// after a prefix grant authorizes one command (kern build 'make install'),
// the approvals store was rewritten to JSON null — the grant record vanished,
// so the next same-prefix command re-minted and re-asked. The full ask→use
// sequence must leave a live, use-counted grant in the store.
func TestAskProtocolPrefixGrantUseKeepsStore(t *testing.T) {
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
	if _, err := NewFileStore(root).Decide(id, "human-test", true, "test ask accept"); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if _, err := SetScopePrefix(root, id, "", "human-test"); err != nil {
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
		if a.ID == id && livePrefixGrant(a) && a.Uses == 1 {
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
