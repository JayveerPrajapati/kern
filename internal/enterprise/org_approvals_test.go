package enterprise

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/orgapprovals"
)

// orgApprovalsList serves GET /org/approvals through the enterprise handler
// and returns the org approval list.
func orgApprovalsList(t *testing.T, s *Server) []orgapprovals.Approval {
	t.Helper()
	req := authedRequest(t, "GET", "/org/approvals")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /org/approvals = %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Approvals []orgapprovals.Approval `json:"approvals"`
		Count     int                     `json:"count"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode GET /org/approvals: %v", err)
	}
	return body.Approvals
}

// TestServeOrgApprovalsLifecycle drives the full /org/approvals REST surface
// (create → list → approve → reject), asserts the store persisted on disk at
// the org root, and asserts every decision landed on the shared org audit log
// (the WithOrgRoot hook wires the orgapprovals audit events to s.orgAudit).
func TestServeOrgApprovalsLifecycle(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1")
	s := mustNew(t).WithOrgRoot(orgRoot)

	// POST /org/approvals — create a pending org approval.
	body := `{"action":"deploy","resource":"production","granted_by":"org-admin","reason":"multi-project deploy"}`
	req := authedRequest(t, "POST", "/org/approvals")
	req.Body = io.NopCloser(strings.NewReader(body))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST /org/approvals = %d: %s", rr.Code, rr.Body.String())
	}
	var created orgapprovals.Approval
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" || !strings.HasPrefix(created.ID, "orgappr-") || created.Status != orgapprovals.StatusPending {
		t.Fatalf("created = %+v, want a pending orgappr-... approval", created)
	}

	// Persisted on disk at the org root.
	approvals := orgapprovals.List(orgRoot)
	if len(approvals) != 1 || approvals[0].ID != created.ID {
		t.Fatalf("org store after POST = %+v, want the created approval", approvals)
	}

	// GET /org/approvals reflects the store.
	if org := orgApprovalsList(t, s); len(org) != 1 || org[0].ID != created.ID {
		t.Fatalf("/org/approvals after POST = %+v, want the created approval", org)
	}

	// POST /org/approvals/approve — makes it consumable.
	req = authedRequest(t, "POST", "/org/approvals/approve")
	req.Body = io.NopCloser(strings.NewReader(`{"id":"` + created.ID + `","granted_by":"org-admin"}`))
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /org/approvals/approve = %d: %s", rr.Code, rr.Body.String())
	}
	var approved orgapprovals.Approval
	if err := json.Unmarshal(rr.Body.Bytes(), &approved); err != nil {
		t.Fatalf("decode approve response: %v", err)
	}
	if approved.Status != orgapprovals.StatusApproved {
		t.Fatalf("approved = %+v, want status approved", approved)
	}

	// A second approval, rejected.
	req = authedRequest(t, "POST", "/org/approvals")
	req.Body = io.NopCloser(strings.NewReader(`{"action":"rollback","resource":"production","granted_by":"org-admin","reason":"reject me"}`))
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("second POST /org/approvals = %d: %s", rr.Code, rr.Body.String())
	}
	var second orgapprovals.Approval
	if err := json.Unmarshal(rr.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second create: %v", err)
	}
	req = authedRequest(t, "POST", "/org/approvals/reject")
	req.Body = io.NopCloser(strings.NewReader(`{"id":"` + second.ID + `","granted_by":"org-admin","reason":"not now"}`))
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /org/approvals/reject = %d: %s", rr.Code, rr.Body.String())
	}
	var rejected orgapprovals.Approval
	if err := json.Unmarshal(rr.Body.Bytes(), &rejected); err != nil {
		t.Fatalf("decode reject response: %v", err)
	}
	if rejected.Status != orgapprovals.StatusRejected {
		t.Fatalf("rejected = %+v, want status rejected", rejected)
	}

	// Audited on the shared org audit log (create/approve/create/reject).
	entries := s.OrgAudit().All()
	actions := map[string]int{}
	for _, e := range entries {
		if e.Resource == "approval" {
			actions[e.Action]++
		}
	}
	if actions["create"] != 2 || actions["approve"] != 1 || actions["reject"] != 1 {
		t.Errorf("org audit approval actions = %v, want create x2 approve x1 reject x1", actions)
	}
}

// TestServeOrgApprovalsStrictBody asserts the write bodies are strict:
// unknown fields are rejected by name, and missing action/granted_by is a
// 400.
func TestServeOrgApprovalsStrictBody(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1")
	s := mustNew(t).WithOrgRoot(orgRoot)

	for name, body := range map[string]string{
		"unknown field":      `{"action":"deploy","resource":"production","granted_by":"org-admin","hash":"client-supplied"}`,
		"missing action":     `{"resource":"production","granted_by":"org-admin"}`,
		"missing granted_by": `{"action":"deploy","resource":"production"}`,
	} {
		req := authedRequest(t, "POST", "/org/approvals")
		req.Body = io.NopCloser(strings.NewReader(body))
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: POST /org/approvals = %d, want 400: %s", name, rr.Code, rr.Body.String())
		}
	}
}

// TestServeOrgApprovalsWithoutOrgRoot asserts org scope is strictly opt-in:
// with no org root the list is empty and writes are refused.
func TestServeOrgApprovalsWithoutOrgRoot(t *testing.T) {
	s := mustNew(t) // no org root
	if org := orgApprovalsList(t, s); len(org) != 0 {
		t.Fatalf("/org/approvals without org root = %+v, want empty", org)
	}
	req := authedRequest(t, "POST", "/org/approvals")
	req.Body = io.NopCloser(strings.NewReader(`{"action":"deploy","resource":"production","granted_by":"org-admin"}`))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("POST /org/approvals without org root = %d, want 400", rr.Code)
	}
}

// TestServeOrgApprovalsRequiresAuth asserts the org approvals surface is
// gated by the enterprise bearer token like every other org endpoint.
func TestServeOrgApprovalsRequiresAuth(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_RBAC_DEFAULT_DENY", "1")
	s := mustNew(t).WithOrgRoot(orgRoot)

	t.Setenv("KERN_AUTH_TOKEN", testToken)
	req := httptest.NewRequest(http.MethodGet, "/org/approvals", nil) // no Authorization header
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("GET /org/approvals without auth = %d, want 401", rr.Code)
	}
}
