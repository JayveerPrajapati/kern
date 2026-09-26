package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// orgTestPolicies is a small deterministic policy set for the store tests.
func orgTestPolicies() []domain.Policy {
	return []domain.Policy{
		{
			ID:          "pol-source-write",
			Name:        "source_write",
			Description: "Changes to source code.",
			Rule:        "MEDIUM source.write",
			Scope:       "source",
			Enabled:     true,
		},
		{
			ID:          "pol-prod-deploy",
			Name:        "production_deploy",
			Description: "Deploying to production (approval required).",
			Rule:        "CRITICAL production.deploy",
			Scope:       "production",
			Enabled:     true,
		},
	}
}

func TestSaveLoadOrgPolicyRoundTrip(t *testing.T) {
	root := t.TempDir()
	policies := orgTestPolicies()

	hash, err := SaveOrgPolicy(root, policies)
	if err != nil {
		t.Fatalf("SaveOrgPolicy: %v", err)
	}
	if hash != PolicyHash(policies) {
		t.Fatalf("returned hash = %q, want PolicyHash = %q", hash, PolicyHash(policies))
	}

	doc, ok, err := LoadOrgPolicy(root)
	if err != nil {
		t.Fatalf("LoadOrgPolicy: %v", err)
	}
	if !ok {
		t.Fatal("LoadOrgPolicy ok = false, want true after save")
	}
	if doc.Version != 1 {
		t.Errorf("doc.Version = %d, want 1", doc.Version)
	}
	if doc.Hash != hash {
		t.Errorf("doc.Hash = %q, want %q", doc.Hash, hash)
	}
	if len(doc.Policies) != len(policies) {
		t.Fatalf("len(doc.Policies) = %d, want %d", len(doc.Policies), len(policies))
	}
	for i, p := range policies {
		if doc.Policies[i].ID != p.ID || doc.Policies[i].Rule != p.Rule || doc.Policies[i].Enabled != p.Enabled {
			t.Errorf("policy %d round-trip mismatch: %+v vs %+v", i, doc.Policies[i], p)
		}
	}
	if doc.UpdatedAt.IsZero() {
		t.Error("doc.UpdatedAt is zero — save must stamp the write time")
	}

	// The file lives at the documented path with the documented mode.
	fi, err := os.Stat(OrgPolicyPath(root))
	if err != nil {
		t.Fatalf("stat %s: %v", OrgPolicyPath(root), err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("org policy file mode = %o, want 0600", fi.Mode().Perm())
	}
}

func TestLoadOrgPolicyMissingIsNotAnError(t *testing.T) {
	root := t.TempDir()
	doc, ok, err := LoadOrgPolicy(root)
	if err != nil {
		t.Fatalf("LoadOrgPolicy on a fresh root: %v", err)
	}
	if ok {
		t.Fatalf("LoadOrgPolicy ok = true, want false (no document); doc = %+v", doc)
	}
}

func TestLoadOrgPolicyEmptyFileIsAbsent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(OrgPolicyPath(root), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := LoadOrgPolicy(root); err != nil || ok {
		t.Fatalf("empty file: ok = %v, err = %v, want (false, nil)", ok, err)
	}
}

func TestLoadOrgPolicyCorruptFailsClosed(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Corrupt: valid JSON but not an OrgPolicy document.
	if err := os.WriteFile(OrgPolicyPath(root), []byte(`{"version":1,"policies":[{"ID":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := LoadOrgPolicy(root); err == nil {
		t.Fatal("LoadOrgPolicy on a corrupt document: err = nil, want fail-closed error")
	} else if ok {
		t.Fatalf("LoadOrgPolicy on a corrupt document: ok = true, want false")
	}
	// A completely non-JSON file fails closed too.
	if err := os.WriteFile(OrgPolicyPath(root), []byte("not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrgPolicy(root); err == nil {
		t.Fatal("LoadOrgPolicy on garbage: err = nil, want fail-closed error")
	}
}

func TestSaveOrgPolicyOverwritesAtomically(t *testing.T) {
	root := t.TempDir()
	first := orgTestPolicies()
	second := orgTestPolicies()
	second[0].Rule = "HIGH source.write" // same IDs, changed rule

	h1, err := SaveOrgPolicy(root, first)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := SaveOrgPolicy(root, second)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("hash must change when the policy content changes")
	}
	doc, ok, err := LoadOrgPolicy(root)
	if err != nil || !ok {
		t.Fatalf("LoadOrgPolicy after second save: ok=%v err=%v", ok, err)
	}
	if doc.Hash != h2 {
		t.Errorf("doc.Hash = %q, want %q", doc.Hash, h2)
	}
	if doc.Policies[0].Rule != "HIGH source.write" {
		t.Errorf("stored rule = %q, want the second save's rule", doc.Policies[0].Rule)
	}
	// No temp files may be left behind (atomic rename leaves nothing).
	matches, _ := filepath.Glob(filepath.Join(root, ".kern", ".org-policy-tmp-*"))
	if len(matches) != 0 {
		t.Errorf("leftover temp files after save: %v", matches)
	}
}

func TestPolicyHashDeterministic(t *testing.T) {
	a := orgTestPolicies()
	b := orgTestPolicies()
	if PolicyHash(a) != PolicyHash(b) {
		t.Fatal("identical policy sets must hash identically")
	}
	if PolicyHash(nil) != PolicyHash([]domain.Policy{}) {
		t.Fatal("nil and empty sets must hash identically")
	}
	changed := orgTestPolicies()
	changed[0].Description = "tweaked"
	if PolicyHash(a) == PolicyHash(changed) {
		t.Fatal("a changed field must change the hash")
	}
	reordered := []domain.Policy{changed[1], changed[0]}
	if PolicyHash(changed) == PolicyHash(reordered) {
		t.Fatal("reordering distinct policies must change the hash")
	}
	// The hash is a sha256 hex string.
	if len(PolicyHash(a)) != 64 || !strings.ContainsAny(PolicyHash(a), "0123456789abcdef") {
		t.Fatalf("hash = %q, want a 64-char hex sha256", PolicyHash(a))
	}
}

func TestPolicyDrift(t *testing.T) {
	root := t.TempDir()
	org := orgTestPolicies()
	if _, err := SaveOrgPolicy(root, org); err != nil {
		t.Fatal(err)
	}

	// Applied == org → no drift.
	rep, err := PolicyDrift(root, org)
	if err != nil {
		t.Fatalf("PolicyDrift: %v", err)
	}
	if rep.Drifted {
		t.Errorf("identical sets reported drifted: %+v", rep)
	}
	if rep.OrgHash != PolicyHash(org) || rep.AppliedHash != PolicyHash(org) {
		t.Errorf("hashes = org %q applied %q, want %q/%q", rep.OrgHash, rep.AppliedHash, PolicyHash(org), PolicyHash(org))
	}

	// Applied set differs → drift.
	drifted := orgTestPolicies()
	drifted[0].Rule = "CRITICAL source.write"
	rep, err = PolicyDrift(root, drifted)
	if err != nil {
		t.Fatalf("PolicyDrift: %v", err)
	}
	if !rep.Drifted {
		t.Errorf("differing sets reported no drift: %+v", rep)
	}
	if rep.OrgHash == rep.AppliedHash {
		t.Errorf("differing sets should have different hashes: org %q, applied %q", rep.OrgHash, rep.AppliedHash)
	}

	// Default policies vs the org set → drift (org is stricter/different).
	rep, err = PolicyDrift(root, DefaultPolicies())
	if err != nil {
		t.Fatalf("PolicyDrift(defaults): %v", err)
	}
	if !rep.Drifted {
		t.Error("DefaultPolicies must drift from a 2-policy org document")
	}
}

func TestPolicyDriftMissingDocumentIsError(t *testing.T) {
	root := t.TempDir()
	if _, err := PolicyDrift(root, DefaultPolicies()); err == nil {
		t.Fatal("PolicyDrift without an org policy document: err = nil, want error")
	} else if !strings.Contains(err.Error(), "no org policy") {
		t.Errorf("error = %q, want a 'no org policy' message", err)
	}
}

func TestOrgRootResolver(t *testing.T) {
	t.Setenv(OrgRootEnv, "/orgs/acme")
	if got := OrgRoot(); got != "/orgs/acme" {
		t.Fatalf("OrgRoot() = %q, want /orgs/acme", got)
	}
	t.Setenv(OrgRootEnv, "")
	if got := OrgRoot(); got != "" {
		t.Fatalf("OrgRoot() with empty env = %q, want \"\"", got)
	}
}
