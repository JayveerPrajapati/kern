package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestApprovalsRBACAllowedRole pins the multi-user approve flow (Feature
// Batch G): with a user registry wired, an approver whose role allows
// approve/reject succeeds and the decision is recorded in the governance
// audit trail with the approver's identity.
func TestApprovalsRBACAllowedRole(t *testing.T) {
	app := newTestApp(t)
	app.SetUserRoleLookup(func(id string) (string, bool) {
		switch id {
		case "root":
			return "org-admin", true
		case "member":
			return "developer", true
		}
		return "", false
	})
	approvalID := seedApproval(app, "requester", "needs a sign-off")
	// org-admin may approve.
	rec := postJSON(t, app, "/api/approvals/approve",
		`{"id":"`+approvalID+`","approver":"root"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve by org-admin = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	// The governance audit trail records the decision (approver identity +
	// approval ID + decision).
	entries := app.firewall.AuditLog().Filter("root")
	found := false
	for _, e := range entries {
		if e.Action == "approve" && e.Resource == approvalID && e.Result == "approved" {
			found = true
		}
	}
	if !found {
		t.Error("no audit entry for the approved decision (approver root, approval " + approvalID + ")")
	}

	// A member-level role may only user-list: approve must be denied with 403
	// and an audit entry for the denial.
	approvalID2 := seedApproval(app, "requester", "second approval")
	rec = postJSON(t, app, "/api/approvals/approve",
		`{"id":"`+approvalID2+`","approver":"member"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("approve by member = %d (%s), want 403", rec.Code, rec.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if !strings.Contains(body["error"], "approve") {
		t.Errorf("denial body %q must name the denied action", body["error"])
	}
	// The denied decision is audited too (visible in the console audit surface).
	denied := app.firewall.AuditLog().Filter("member")
	deniedFound := false
	for _, e := range denied {
		if e.Action == "approve" && e.Resource == approvalID2 && e.Result == "denied" {
			deniedFound = true
		}
	}
	if !deniedFound {
		t.Error("no audit entry for the denied decision (approver member, approval " + approvalID2 + ")")
	}
}

// TestApprovalsRBACReject pins the reject path: an allowed role rejects, a
// denied role gets 403, and the audit trail records the decision.
func TestApprovalsRBACReject(t *testing.T) {
	app := newTestApp(t)
	app.SetUserRoleLookup(func(id string) (string, bool) {
		if id == "root" {
			return "org-admin", true
		}
		return "org-member", true
	})
	approvalID := seedApproval(app, "requester", "reject me")
	rec := postJSON(t, app, "/api/approvals/reject",
		`{"id":"`+approvalID+`","approver":"root"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reject by org-admin = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	entries := app.firewall.AuditLog().Filter("root")
	found := false
	for _, e := range entries {
		if e.Action == "reject" && e.Resource == approvalID && e.Result == "denied" {
			found = true
		}
	}
	if !found {
		t.Error("no audit entry for the rejection (approver root, approval " + approvalID + ")")
	}
	// Member role cannot reject.
	approvalID2 := seedApproval(app, "requester", "reject me too")
	rec = postJSON(t, app, "/api/approvals/reject",
		`{"id":"`+approvalID2+`","approver":"member"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("reject by member = %d (%s), want 403", rec.Code, rec.Body.String())
	}
}

// TestApprovalsRBACUnknownApprover pins the fail-closed path: with a registry
// wired, an approver who is not a registered user is denied.
func TestApprovalsRBACUnknownApprover(t *testing.T) {
	app := newTestApp(t)
	app.SetUserRoleLookup(func(string) (string, bool) { return "", false })
	approvalID := seedApproval(app, "requester", "unknown approver")
	rec := postJSON(t, app, "/api/approvals/approve",
		`{"id":"`+approvalID+`","approver":"ghost"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("approve by unknown approver = %d (%s), want 403", rec.Code, rec.Body.String())
	}
}

// TestApprovalAuditRecordsAuthenticatedPrincipal pins the impersonation
// detection fix: the audit entry records the AUTHENTICATED principal
// (server-side) SEPARATELY from the approver identity the request body
// declares. With KERN_AUTH_TOKEN set, every token holder is the same
// principal ("shared-token"); with no token the loopback client is the
// principal ("loopback"). Any token holder can declare any approver name —
// the principal column is what an auditor compares against.
func TestApprovalAuditRecordsAuthenticatedPrincipal(t *testing.T) {
	t.Setenv("KERN_AUTH_TOKEN", "sekret")
	app := newTestApp(t)
	approvalID := seedApproval(app, "requester", "principal pin")
	req := httptest.NewRequest(http.MethodPost, "/api/approvals/approve",
		strings.NewReader(`{"id":"`+approvalID+`","approver":"alice"}`))
	req.Header.Set("Authorization", "Bearer sekret")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	found := false
	for _, e := range app.firewall.AuditLog().All() {
		if e.Action == "approve" && e.Resource == approvalID {
			found = true
			if e.AgentID != "alice" {
				t.Errorf("AgentID = %q, want the declared approver alice", e.AgentID)
			}
			if e.Principal != "shared-token" {
				t.Errorf("Principal = %q, want shared-token (KERN_AUTH_TOKEN set)", e.Principal)
			}
		}
	}
	if !found {
		t.Fatal("no audit entry for the approved decision")
	}
}

// TestApprovalsBackwardCompat pins the no-registry path: when no user role
// lookup is wired (the single-project/local console), approve/reject keep the
// historical behavior — no RBAC check, and a missing approver still 400s.
func TestApprovalsBackwardCompat(t *testing.T) {
	app := newTestApp(t) // no SetUserRoleLookup
	approvalID := seedApproval(app, "requester", "legacy flow")
	rec := postJSON(t, app, "/api/approvals/approve",
		`{"id":"`+approvalID+`","approver":"ui-operator"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve without registry = %d (%s), want 200 (historical behavior)", rec.Code, rec.Body.String())
	}
	// Missing approver still errors exactly as before.
	approvalID2 := seedApproval(app, "requester", "missing approver")
	rec = postJSON(t, app, "/api/approvals/approve",
		`{"id":"`+approvalID2+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("approve without approver = %d (%s), want 400 (historical behavior)", rec.Code, rec.Body.String())
	}
}
