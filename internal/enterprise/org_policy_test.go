package enterprise

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// orgTestPolicySet returns a small deterministic org policy set.
func orgTestPolicySet() []domain.Policy {
	return []domain.Policy{
		{
			ID:          "pol-org-source",
			Name:        "org_source_write",
			Description: "Org rule: source writes are high risk.",
			Rule:        "HIGH source.write",
			Scope:       "source",
			Enabled:     true,
		},
		{
			ID:          "pol-org-deploy",
			Name:        "org_production_deploy",
			Description: "Org rule: production deploys always require approval.",
			Rule:        "CRITICAL production.deploy",
			Scope:       "production",
			Enabled:     true,
		},
	}
}

// govPolicyJSON is the wire projection of one policy in the web /api/governance
// payload (id/name/description/scope).
type govPolicyJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
}

// projectGovernancePolicies serves GET /<project>/api/governance through the
// enterprise handler and returns the policy list the project App's firewall
// currently enforces (the propagation assertion: does the built firewall use
// the org policy or the defaults?).
func projectGovernancePolicies(t *testing.T, s *Server, project string) []govPolicyJSON {
	t.Helper()
	req := authedRequest(t, "GET", "/"+project+"/api/governance")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /%s/api/governance = %d: %s", project, rr.Code, rr.Body.String())
	}
	var body struct {
		Policies []govPolicyJSON `json:"policies"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /%s/api/governance: %v; body=%s", project, err, rr.Body.String())
	}
	return body.Policies
}

// orgPoliciesList serves GET /org/policies and returns the listed policies.
func orgPoliciesList(t *testing.T, s *Server) []govPolicyJSON {
	t.Helper()
	req := authedRequest(t, "GET", "/org/policies")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /org/policies = %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Policies []govPolicyJSON `json:"policies"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /org/policies: %v; body=%s", err, rr.Body.String())
	}
	return body.Policies
}

func policyIDs(policies []govPolicyJSON) []string {
	out := make([]string, len(policies))
	for i, p := range policies {
		out[i] = p.ID
	}
	return out
}

func TestAppForBuildsFirewallFromOrgPolicy(t *testing.T) {
	orgRoot := t.TempDir()
	if _, err := governance.SaveOrgPolicy(orgRoot, orgTestPolicySet()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1")
	s := withStubFactory(mustNew(t)).WithOrgRoot(orgRoot)
	if err := s.Register("proj-a", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.appFor("proj-a"); err != nil {
		t.Fatalf("appFor: %v", err)
	}
	// The project firewall is built from the ORG policy, not the defaults.
	got := projectGovernancePolicies(t, s, "proj-a")
	if len(got) != 2 || got[0].ID != "pol-org-source" || got[1].ID != "pol-org-deploy" {
		t.Fatalf("project firewall policies = %v, want the 2 org policies", policyIDs(got))
	}
	// The org endpoint reports the same applied set.
	org := orgPoliciesList(t, s)
	if len(org) != 2 || org[0].ID != "pol-org-source" {
		t.Fatalf("/org/policies = %v, want the 2 org policies", policyIDs(org))
	}
}

func TestAppForUsesDefaultsWhenNoOrgRoot(t *testing.T) {
	s := withStubFactory(mustNew(t)) // no KERN_ORG_ROOT, no WithOrgRoot → no org
	if err := s.Register("proj-a", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.appFor("proj-a"); err != nil {
		t.Fatalf("appFor: %v", err)
	}
	// Byte-for-byte today's behavior: the firewall enforces the defaults.
	got := projectGovernancePolicies(t, s, "proj-a")
	if len(got) != len(governance.DefaultPolicies()) {
		t.Fatalf("project firewall policies = %d, want %d defaults", len(got), len(governance.DefaultPolicies()))
	}
	want := map[string]bool{}
	for _, p := range governance.DefaultPolicies() {
		want[p.ID] = true
	}
	for _, p := range got {
		if !want[p.ID] {
			t.Errorf("unexpected default-set mismatch: policy %q is not a default", p.ID)
		}
	}
}

func TestNewResolvesOrgRootFromEnv(t *testing.T) {
	orgRoot := t.TempDir()
	if _, err := governance.SaveOrgPolicy(orgRoot, orgTestPolicySet()); err != nil {
		t.Fatal(err)
	}
	t.Setenv(governance.OrgRootEnv, orgRoot)
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1") // org-mode pairing gate (finding 2)
	s := mustNew(t)
	// The applied snapshot must equal the org document (no drift at startup).
	drift, err := s.PolicyDrift()
	if err != nil {
		t.Fatalf("PolicyDrift: %v", err)
	}
	if drift.Drifted {
		t.Fatalf("startup applied snapshot drifted from the org document: %+v", drift)
	}
	if drift.OrgHash != governance.PolicyHash(orgTestPolicySet()) {
		t.Errorf("org hash = %q, want the saved org policy hash", drift.OrgHash)
	}
}

// TestOrgModeRequiresRBACDefaultDeny covers the finding-2 pairing gate: org
// mode without KERN_RBAC_DEFAULT_DENY=1 is refused — WithOrgRoot leaves org
// scope inactive and New() (env path) errors naming both env vars — while
// KERN_ORG_ALLOW_WEAK_RBAC=1 is the documented-unsafe escape hatch.
func TestOrgModeRequiresRBACDefaultDeny(t *testing.T) {
	orgRoot := t.TempDir()
	// WithOrgRoot path: the root is refused — org scope stays inactive.
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if s.WithOrgRoot(orgRoot).orgRoot != "" {
		t.Fatal("WithOrgRoot must refuse to apply an org root without KERN_RBAC_DEFAULT_DENY=1")
	}
	// Env path: New() refuses to start and names both env vars.
	t.Setenv(governance.OrgRootEnv, orgRoot)
	if _, err := New(); err == nil {
		t.Fatal("New() must refuse org mode without KERN_RBAC_DEFAULT_DENY=1")
	} else if !strings.Contains(err.Error(), "KERN_RBAC_DEFAULT_DENY") || !strings.Contains(err.Error(), governance.OrgRootEnv) {
		t.Fatalf("refusal must name both env vars, got: %v", err)
	}
	// Escape hatch: KERN_ORG_ALLOW_WEAK_RBAC=1 explicitly accepts the unsafe
	// weak-RBAC posture.
	t.Setenv("KERN_ORG_ALLOW_WEAK_RBAC", "1")
	s2, err := New()
	if err != nil || s2.orgRoot == "" {
		t.Fatalf("KERN_ORG_ALLOW_WEAK_RBAC=1 must permit org mode: orgRoot=%q err=%v", s2.orgRoot, err)
	}
}

func TestServeOrgPoliciesPostWritesAuditsAndRefreshes(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1")
	s := withStubFactory(mustNew(t)).WithOrgRoot(orgRoot)
	if err := s.Register("proj-a", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// Build the project App BEFORE the write so we can assert the cached app
	// is refreshed in place (no rebuild needed).
	if _, err := s.appFor("proj-a"); err != nil {
		t.Fatalf("appFor: %v", err)
	}

	body := `{"policies":[{"ID":"pol-org-1","Name":"org_source_write","Description":"org rule","Rule":"HIGH source.write","Scope":"source","Enabled":true}]}`
	req := authedRequest(t, "POST", "/org/policies")
	req.Body = io.NopCloser(strings.NewReader(body))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST /org/policies = %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Hash  string `json:"hash"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode POST response: %v", err)
	}
	if resp.Hash == "" || resp.Count != 1 {
		t.Fatalf("POST response = %+v, want a hash and count 1", resp)
	}

	// Persisted on disk at the org root.
	doc, ok, err := governance.LoadOrgPolicy(orgRoot)
	if err != nil || !ok {
		t.Fatalf("LoadOrgPolicy after POST: ok=%v err=%v", ok, err)
	}
	if len(doc.Policies) != 1 || doc.Policies[0].ID != "pol-org-1" {
		t.Fatalf("persisted policies = %+v, want pol-org-1", doc.Policies)
	}
	if doc.Hash != resp.Hash {
		t.Errorf("persisted hash = %q, want the echoed %q", doc.Hash, resp.Hash)
	}

	// Audited on the shared org audit log.
	entries := s.OrgAudit().All()
	found := false
	for _, e := range entries {
		if e.Action == "apply" && e.Resource == "policy" && e.Result == "allowed" {
			found = true
			if !strings.Contains(e.Reason, resp.Hash) {
				t.Errorf("audit reason %q does not carry the hash %q", e.Reason, resp.Hash)
			}
		}
	}
	if !found {
		t.Error("POST /org/policies produced no org audit entry")
	}

	// GET /org/policies reflects the written set.
	org := orgPoliciesList(t, s)
	if len(org) != 1 || org[0].ID != "pol-org-1" {
		t.Fatalf("/org/policies after POST = %v, want pol-org-1", policyIDs(org))
	}

	// The ALREADY-BUILT app was refreshed in place: no rebuild, new policies.
	got := projectGovernancePolicies(t, s, "proj-a")
	if len(got) != 1 || got[0].ID != "pol-org-1" {
		t.Fatalf("cached app policies after POST = %v, want pol-org-1 (in-place refresh)", policyIDs(got))
	}
}

func TestServeOrgPolicyApplyResolvesDrift(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1")
	s := withStubFactory(mustNew(t)).WithOrgRoot(orgRoot)
	if err := s.Register("proj-a", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.appFor("proj-a"); err != nil {
		t.Fatalf("appFor: %v", err)
	}
	v1 := orgTestPolicySet()
	if _, err := s.WriteOrgPolicy(v1); err != nil {
		t.Fatal(err)
	}

	// Out-of-band change: an operator writes a new org policy document
	// directly to the shared file while the server keeps its v1 snapshot.
	v2 := orgTestPolicySet()
	v2[0].Rule = "CRITICAL source.write"
	if _, err := governance.SaveOrgPolicy(orgRoot, v2); err != nil {
		t.Fatal(err)
	}

	// Drift is reported: applied snapshot (v1) vs org document (v2).
	drift, err := s.PolicyDrift()
	if err != nil {
		t.Fatalf("PolicyDrift: %v", err)
	}
	if !drift.Drifted {
		t.Fatalf("expected drift after an out-of-band org policy change, got %+v", drift)
	}

	// POST /org/policies/apply re-propagates and resolves the drift.
	req := authedRequest(t, "POST", "/org/policies/apply")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /org/policies/apply = %d: %s", rr.Code, rr.Body.String())
	}
	drift, err = s.PolicyDrift()
	if err != nil {
		t.Fatalf("PolicyDrift after apply: %v", err)
	}
	if drift.Drifted {
		t.Fatalf("drift must be resolved after apply, got %+v", drift)
	}
	// The cached app enforces the re-propagated v2 policy.
	got := projectGovernancePolicies(t, s, "proj-a")
	found := false
	for _, p := range got {
		if p.ID == "pol-org-source" {
			found = true
		}
	}
	if !found || len(got) != 2 {
		t.Fatalf("cached app policies after apply = %v, want the v2 org set", policyIDs(got))
	}
	// The apply is audited too.
	entries := s.OrgAudit().All()
	foundApply := false
	for _, e := range entries {
		if e.Action == "apply" && e.Resource == "policy" {
			foundApply = true
		}
	}
	if !foundApply {
		t.Error("POST /org/policies/apply produced no org audit entry")
	}
}

func TestServeOrgPoliciesPostWithoutOrgRootRefused(t *testing.T) {
	s := mustNew(t) // no org root configured
	req := authedRequest(t, "POST", "/org/policies")
	req.Body = io.NopCloser(strings.NewReader(`{"policies":[{"ID":"pol-x"}]}`))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("POST /org/policies without org root = %d, want 400: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "org root") {
		t.Errorf("400 body = %q, want a 'org root' explanation", rr.Body.String())
	}
}

func TestServeOrgPoliciesPostStrictBody(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1")
	s := mustNew(t).WithOrgRoot(orgRoot)

	post := func(body string) *httptest.ResponseRecorder {
		req := authedRequest(t, "POST", "/org/policies")
		req.Body = io.NopCloser(strings.NewReader(body))
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		return rr
	}

	// Unknown fields (e.g. a client-submitted "hash") are rejected by name.
	rr := post(`{"policies":[{"ID":"pol-x"}],"hash":"forged"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field POST = %d, want 400 (body: %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "hash") {
		t.Errorf("400 should name the unknown field, got: %s", rr.Body.String())
	}

	// Empty policy list is refused.
	rr = post(`{"policies":[]}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty-policies POST = %d, want 400", rr.Code)
	}

	// A malformed body is refused.
	rr = post(`{not json`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("malformed POST = %d, want 400", rr.Code)
	}

	// Nothing was persisted by any rejected write.
	if _, ok, _ := governance.LoadOrgPolicy(orgRoot); ok {
		t.Fatal("rejected writes must not persist an org policy")
	}

	// Bad method → 405.
	del := authedRequest(t, "DELETE", "/org/policies")
	drr := httptest.NewRecorder()
	s.ServeHTTP(drr, del)
	if drr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /org/policies = %d, want 405", drr.Code)
	}
}

func TestServeOrgPolicyApplyWithoutDocument(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1")
	s := mustNew(t).WithOrgRoot(orgRoot)
	req := authedRequest(t, "POST", "/org/policies/apply")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("POST /org/policies/apply with no document = %d, want 404: %s", rr.Code, rr.Body.String())
	}
}

func TestServeOrgPolicyApplyWithoutOrgRoot(t *testing.T) {
	s := mustNew(t)
	req := authedRequest(t, "POST", "/org/policies/apply")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("POST /org/policies/apply without org root = %d, want 400", rr.Code)
	}
}
