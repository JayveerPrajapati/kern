package govaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/auditdiag"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// The verdict is cached per chain state: a second call must not re-walk, and
// any chain change must invalidate the stamp so a stale verdict can never hide
// an edit (2026-11-10 campaign: three full HMAC walks per command).
func TestCachedChainVerdictCachesAndInvalidates(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kern", "audit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	first := CachedChainVerdict(dir)
	if _, err := os.Stat(auditdiag.VerifyCacheDir(dir)); err != nil {
		t.Fatalf("verdict cache not written: %v", err)
	}
	second, ok := auditdiag.ReadVerifyCacheDir(dir)
	if !ok {
		t.Fatal("cache file unreadable after write")
	}
	if second.Stamp != first.Stamp {
		t.Fatalf("stamp changed without a chain change: %q -> %q", first.Stamp, second.Stamp)
	}

	// Append a chain line: size and mtime change, so the stamp must differ and
	// the next call re-verifies instead of trusting the old verdict.
	chain := filepath.Join(dir, "chain.jsonl")
	if err := os.WriteFile(chain, []byte("{\"id\":\"x\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	third := CachedChainVerdict(dir)
	if third.Stamp == first.Stamp {
		t.Fatalf("stamp not invalidated after chain change: %q", third.Stamp)
	}
}

// TestCachedChainVerdictUngovernedRepoIsNotTampering pins the F-C3 contract
// (dogfood 2026-11-22, lane C): a repo that was never governed has no audit
// chain by design — that is not tampering. CachedChainVerdict is the exact
// verdict `kern verify` consumes at platform construction (internal/app), and
// the platform renders its FAILED-level warnings only when FirstBroken >= 0.
// So the ungoverned contract is: absent or empty chain ⇒ no break reported,
// nothing verified, nothing to warn about.
func TestCachedChainVerdictUngovernedRepoIsNotTampering(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name string
		prep func(dir string)
	}{
		{"no audit dir", func(dir string) {}},
		{"audit dir without a chain file", func(dir string) {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"empty chain file", func(dir string) {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "chain.jsonl"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, tc.name, ".kern", "audit")
			tc.prep(dir)
			v := CachedChainVerdict(dir)
			if v.FirstBroken != -1 {
				t.Errorf("FirstBroken = %d for an ungoverned repo, want -1 (no break)", v.FirstBroken)
			}
			if v.BrokenCount != 0 {
				t.Errorf("BrokenCount = %d for an ungoverned repo, want 0", v.BrokenCount)
			}
			if v.Verified != 0 || v.Total != 0 {
				t.Errorf("Verified/Total = %d/%d for an ungoverned repo, want 0/0", v.Verified, v.Total)
			}
		})
	}
}

// TestCachedChainVerdictDetectsTamperedChain is the other half of the F-C3
// contract: a PRESENT chain that fails verification must still be reported as
// broken through the same cached-verdict path. Corrupt the LAST entry's
// stored hash (an external edit of exactly the kind tamper detection exists
// for): the earlier entries still verify, the edited one breaks, and the
// verdict carries the break to the platform's FirstBroken >= 0 warning gate.
func TestCachedChainVerdictDetectsTamperedChain(t *testing.T) {
	dir := t.TempDir()
	chain := filepath.Join(dir, "chain.jsonl")
	l := NewAuditLog().
		WithStore(storage.NewLog(dir)).
		WithLockPath(filepath.Join(dir, ".lock"))
	for i := 0; i < 3; i++ {
		l.Record(entry("", "agent-tamper"))
	}
	// Sanity: the intact chain verifies fully — corrupting must be the only
	// reason a break is ever reported.
	if v := CachedChainVerdict(dir); v.FirstBroken != -1 || v.Verified != 3 || v.Total != 3 {
		t.Fatalf("intact chain verdict = (broken %d, verified %d, total %d), want (-1, 3, 3)", v.FirstBroken, v.Verified, v.Total)
	}
	// Flip the first hex digit of the LAST entry's stored hash.
	data, err := os.ReadFile(chain)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	i := strings.LastIndex(s, `"Hash":"`)
	if i < 0 {
		t.Fatal("no Hash field found in the persisted chain")
	}
	pos := i + len(`"Hash":"`)
	flip := byte('0')
	if s[pos] == '0' {
		flip = '1'
	}
	s = s[:pos] + string(flip) + s[pos+1:]
	if err := os.WriteFile(chain, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	v := CachedChainVerdict(dir)
	if v.FirstBroken != 2 {
		t.Errorf("FirstBroken = %d after corrupting the last entry, want 2 (third entry)", v.FirstBroken)
	}
	if v.Verified != 2 || v.Total != 3 || v.BrokenCount != 1 {
		t.Errorf("verified/total/broken = %d/%d/%d after corrupting the last entry, want 2/3/1", v.Verified, v.Total, v.BrokenCount)
	}
}
