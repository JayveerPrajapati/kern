package evidence

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// writeFile writes a test fixture file under dir, creating parent dirs.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// fixtureRoot creates a temp repo with a couple of Go files and builds an
// index over it. Tests share it so the fixture mirrors what a real export
// sees: symbols, edges and a persisted index governance.
func fixtureRoot(t *testing.T) (string, *index.Index) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "public/a.go", `package public

func PublicA() int { return PublicB() + 1 }
`)
	writeFile(t, dir, "public/b.go", `package public

func PublicB() int { return 2 }
`)
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	return dir, ix
}

// recordAuditEntries persists n entries into <root>/.kern/audit, the same
// store Generate snapshots. One entry carries a ValidationOutcome so the
// bundle's coverage of that field is exercised.
func recordAuditEntries(t *testing.T, root string, n int) {
	t.Helper()
	store := storage.NewLocal(filepath.Join(root, ".kern", "audit"))
	log := governance.NewAuditLog().WithStore(store)
	for i := 0; i < n; i++ {
		e := governance.AuditEntry{
			AgentID:  "test-agent",
			Action:   "read",
			Resource: "public/a.go",
			Result:   "allowed",
		}
		if i == 1 {
			e.ValidationOutcome = &governance.ValidationOutcome{
				Status:        "PASS",
				ExitCode:      0,
				BlockedFiles:  []string{"public/a.go"},
				CorrelationID: "corr-1",
				Findings:      1,
			}
		}
		log.Record(e)
	}
}

// TestGenerate_ProducesValidBundle is the DoD gate: a generated bundle has
// all sections populated, a schema version of 1, a non-empty bundle hash,
// and passes its own Verify().
func TestGenerate_ProducesValidBundle(t *testing.T) {
	dir, ix := fixtureRoot(t)
	b, err := Generate(dir, "default", "T-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if b.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", b.SchemaVersion)
	}
	if b.BundleID == "" {
		t.Error("BundleID is empty")
	}
	if b.Authorization == nil {
		t.Fatal("Authorization section is nil")
	}
	if b.Freshness == nil {
		t.Fatal("Freshness section is nil")
	}
	if b.Lineage == nil {
		t.Fatal("Lineage section is nil")
	}
	if b.AuditTrail == nil {
		t.Fatal("AuditTrail is nil (want empty slice, not null)")
	}
	if b.BundleHash == "" {
		t.Error("BundleHash is empty")
	}
	if err := b.Verify(); err != nil {
		t.Fatalf("Verify() on a fresh bundle: %v", err)
	}
}

// TestBundleVerify_DetectsTamper mirrors audit_test.go's TestTamperBreaksChain
// (the style model): modify a field after sealing, and the seal must break.
func TestBundleVerify_DetectsTamper(t *testing.T) {
	dir, ix := fixtureRoot(t)
	b, err := Generate(dir, "default", "T-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := b.Verify(); err != nil {
		t.Fatalf("chain should be intact before tampering: %v", err)
	}

	// Tamper with the authorization decision after sealing.
	b.Authorization.Proof.Decision.Allowed = !b.Authorization.Proof.Decision.Allowed
	if err := b.Verify(); err == nil {
		t.Error("Verify() = nil after tampering with Authorization.Proof.Decision, want error")
	}
}

// TestBundleVerify_DetectsHashMismatch: changing BundleHash itself must break
// the seal.
func TestBundleVerify_DetectsHashMismatch(t *testing.T) {
	dir, ix := fixtureRoot(t)
	b, err := Generate(dir, "default", "T-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b.BundleHash = "deadbeef"
	if err := b.Verify(); err == nil {
		t.Error("Verify() = nil after altering BundleHash, want error")
	}
}

// TestBundleVerify_SchemaVersion: a bundle claiming an unsupported schema
// version must be rejected before the hash is even considered.
func TestBundleVerify_SchemaVersion(t *testing.T) {
	dir, ix := fixtureRoot(t)
	b, err := Generate(dir, "default", "T-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b.SchemaVersion = 2
	if err := b.Verify(); err == nil {
		t.Error("Verify() = nil with SchemaVersion 2, want error")
	}
}

// TestGenerate_IncludesAuditTrail: entries persisted under <root>/.kern/audit
// are snapshotted into the bundle, ValidationOutcome included, and
// AuditChainHash pins the last chain link.
func TestGenerate_IncludesAuditTrail(t *testing.T) {
	dir, ix := fixtureRoot(t)
	recordAuditEntries(t, dir, 3)

	b, err := Generate(dir, "default", "T-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(b.AuditTrail) != 3 {
		t.Fatalf("AuditTrail has %d entries, want 3", len(b.AuditTrail))
	}
	for i, s := range b.AuditTrail {
		if s.Hash == "" {
			t.Errorf("snapshot %d has empty hash", i)
		}
		if s.AgentID != "test-agent" {
			t.Errorf("snapshot %d AgentID = %q, want test-agent", i, s.AgentID)
		}
	}
	// ValidationOutcome survives into the snapshot (the bundle's own hash
	// covers it — unlike computeAuditHash, a documented gap).
	if b.AuditTrail[1].ValidationOutcome == nil {
		t.Fatal("snapshot 1 lost its ValidationOutcome")
	}
	if got := b.AuditTrail[1].ValidationOutcome.Status; got != "PASS" {
		t.Errorf("snapshot 1 ValidationOutcome.Status = %q, want PASS", got)
	}
	// AuditChainHash must be the last entry's hash.
	if b.AuditChainHash != b.AuditTrail[2].Hash {
		t.Errorf("AuditChainHash = %q, want last entry hash %q", b.AuditChainHash, b.AuditTrail[2].Hash)
	}
	// And the sealed bundle still verifies.
	if err := b.Verify(); err != nil {
		t.Fatalf("Verify() with audit trail: %v", err)
	}
}

// TestGenerate_IncludesFreshness: the freshness pillar is populated and the
// freshly built index must not be judged stale.
func TestGenerate_IncludesFreshness(t *testing.T) {
	dir, ix := fixtureRoot(t)
	b, err := Generate(dir, "default", "T-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if b.Freshness.Proof.Verdict == "" {
		t.Fatal("Freshness.Proof.Verdict is empty")
	}
	switch b.Freshness.Proof.Verdict {
	case index.FreshnessFresh, index.FreshnessStale, index.FreshnessUnknown:
	default:
		t.Errorf("Freshness.Proof.Verdict = %q, want one of fresh/stale/unknown", b.Freshness.Proof.Verdict)
	}
	if b.Freshness.Proof.Stale() {
		t.Errorf("freshly built index judged stale: %+v", b.Freshness.Proof)
	}
	if b.Freshness.IndexVersion == "" {
		t.Error("Freshness.IndexVersion is empty")
	}
}

// TestGenerate_IncludesAuthorization: the authorization pillar carries the
// live decision (default agent, allowed), a non-empty scope, and the
// reconstructed flag that marks this as a re-derivation.
func TestGenerate_IncludesAuthorization(t *testing.T) {
	dir, ix := fixtureRoot(t)
	b, err := Generate(dir, "default", "T-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !b.Authorization.Proof.Decision.Allowed {
		t.Errorf("default agent should be allowed, got %+v", b.Authorization.Proof.Decision)
	}
	if b.Authorization.Proof.Agent.ID != "default" {
		t.Errorf("Proof.Agent.ID = %q, want default", b.Authorization.Proof.Agent.ID)
	}
	if len(b.Authorization.Scope.Symbols) == 0 {
		t.Error("Scope.Symbols is empty for an allowed default authorization")
	}
	if !b.Authorization.Reconstructed {
		t.Error("Reconstructed = false, want true (proof is always re-derived at export)")
	}
}

// TestGenerate_LineageFromScope: the lineage section mirrors the authorized
// scope's symbols and edges.
func TestGenerate_LineageFromScope(t *testing.T) {
	dir, ix := fixtureRoot(t)
	b, err := Generate(dir, "default", "T-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if b.Lineage.Task != "T-1" {
		t.Errorf("Lineage.Task = %q, want T-1", b.Lineage.Task)
	}
	if len(b.Lineage.Symbols) != len(b.Authorization.Scope.Symbols) {
		t.Fatalf("Lineage.Symbols = %d, want %d (matches authorized scope)",
			len(b.Lineage.Symbols), len(b.Authorization.Scope.Symbols))
	}
	want := b.Authorization.Scope.Symbols[0]
	got := b.Lineage.Symbols[0]
	if got.Name != want.Name || got.Qualified != want.Qualified ||
		got.File != want.File || got.Line != want.Line {
		t.Errorf("Lineage.Symbols[0] = %+v, want %+v", got, want)
	}
}

// TestGenerate_DeniedAuthorizationStillProducesBundle: an unknown agent is
// denied, but the bundle still captures the denial as evidence — a denial is
// evidence too.
func TestGenerate_DeniedAuthorizationStillProducesBundle(t *testing.T) {
	dir, ix := fixtureRoot(t)
	b, err := Generate(dir, "ghost-agent", "T-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if b.Authorization.Proof.Decision.Allowed {
		t.Error("unknown agent should be denied")
	}
	if len(b.Authorization.Scope.Symbols) != 0 {
		t.Errorf("denied scope should carry zero symbols, got %d", len(b.Authorization.Scope.Symbols))
	}
	if err := b.Verify(); err != nil {
		t.Fatalf("denied bundle should still verify: %v", err)
	}
}

// TestComputeBundleHashDeterministic: the same bundle content seals to the
// same hash — reproducibility is what makes the seal verifiable by a third
// party.
func TestComputeBundleHashDeterministic(t *testing.T) {
	dir, ix := fixtureRoot(t)
	b1, err := Generate(dir, "default", "T-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b2 := *b1
	if got := computeBundleHash(&b2); got != b1.BundleHash {
		t.Errorf("recomputed hash %q != BundleHash %q", got, b1.BundleHash)
	}
}
