package auditdiag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeVerdict(t *testing.T, root string, v VerifyCache) {
	t.Helper()
	dir := filepath.Join(root, ".kern", "audit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(VerifyCacheDir(dir), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A missing cache is an honest "not yet verified", never a silent ok, and a
// broken verdict must surface as a FAIL row (2026-11-10 campaign: doctor
// stayed green while the chain was broken).
func TestChainIntegrityFinding(t *testing.T) {
	root := t.TempDir()
	f := ChainIntegrity(root)
	if f.Level != "ok" || !strings.Contains(f.Detail, "not yet verified") {
		t.Fatalf("missing cache: got %+v, want ok/not-yet-verified", f)
	}

	writeVerdict(t, root, VerifyCache{Stamp: "s", FirstBroken: 1806, BrokenCount: 1, Verified: 1806, Total: 2055, Keyed: 2000, Plain: 55})
	f = ChainIntegrity(root)
	if f.Level != "fail" || !strings.Contains(f.Detail, "broken at entry 1807 of 2055") {
		t.Fatalf("broken verdict: got %+v, want fail naming entry 1807", f)
	}

	writeVerdict(t, root, VerifyCache{Stamp: "s2", FirstBroken: -1, Verified: 12, Total: 12, Keyed: 12})
	f = ChainIntegrity(root)
	if f.Level != "ok" || !strings.Contains(f.Detail, "12 entries verified end to end") {
		t.Fatalf("intact verdict: got %+v, want ok with counts", f)
	}

	// Findings must include the row so doctor renders it.
	found := false
	for _, x := range Findings(root) {
		if x.Check == "audit-chain-integrity" {
			found = true
		}
	}
	if !found {
		t.Fatal("Findings does not include audit-chain-integrity")
	}
}
