package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/orgapprovals"
)

// TestOrgApprovalsNavLinkConditional covers the finding-10 discovery fix:
// the /org-approvals page is reachable from every console page's topnav
// when an org root is configured, and hidden when it is not.
func TestOrgApprovalsNavLinkConditional(t *testing.T) {
	// No org root → no link in the console chrome.
	app := newTestApp(t)
	body := get(t, app, "/").Body.String()
	if strings.Contains(body, `href="/org-approvals"`) {
		t.Error("dashboard nav must NOT link /org-approvals without an org root")
	}

	// Org root configured → the link appears on the shared chrome pages.
	orgRoot := t.TempDir()
	t.Setenv("KERN_ORG_ROOT", orgRoot)
	app = newTestApp(t)
	body = get(t, app, "/").Body.String()
	if !strings.Contains(body, `href="/org-approvals"`) {
		t.Error("dashboard nav must link /org-approvals when an org root is configured")
	}
}

// TestOrgApprovalsPageNoOrgRoot asserts the view renders the inert empty
// state when no org root is configured — the per-project approval flow stays
// untouched and the page never errors.
func TestOrgApprovalsPageNoOrgRoot(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/org-approvals")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"<title>Org Approvals", "topnav", `href="/org-approvals"`, "No org root configured"} {
		if !strings.Contains(body, want) {
			t.Fatalf("org approvals page missing %q", want)
		}
	}
}

// TestOrgApprovalsPageListsApprovals asserts the view renders the org
// approval store with approve/reject decision forms on pending approvals.
func TestOrgApprovalsPageListsApprovals(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_ORG_ROOT", orgRoot)
	seed, err := orgapprovals.Create(orgRoot, "deploy", "production", "org-admin", "pre-approve multi-project deploy")
	if err != nil {
		t.Fatalf("orgapprovals.Create: %v", err)
	}

	app := newTestApp(t)
	rec := get(t, app, "/org-approvals")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{seed.ID, "deploy", "production", "org-admin", "pending", `name="action" value="approve"`, `name="action" value="reject"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("org approvals page missing %q", want)
		}
	}
}

// TestOrgApprovalsPageApproveFlipsStatus drives the approve form: POST
// /org-approvals with action=approve records the decision (303 back to the
// list) and the store reflects the approved status.
func TestOrgApprovalsPageApproveFlipsStatus(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_ORG_ROOT", orgRoot)
	t.Setenv("KERN_AUTH_TOKEN", "test-console-token") // org mutations require the token (finding 7)
	seed, err := orgapprovals.Create(orgRoot, "deploy", "production", "org-admin", "pre-approve")
	if err != nil {
		t.Fatalf("orgapprovals.Create: %v", err)
	}

	app := newTestApp(t)
	form := url.Values{"action": {"approve"}, "id": {seed.ID}, "granted_by": {"org-admin"}}
	req := httptest.NewRequest(http.MethodPost, "/org-approvals", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer test-console-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /org-approvals = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	approvals := orgapprovals.List(orgRoot)
	if len(approvals) != 1 || approvals[0].Status != orgapprovals.StatusApproved {
		t.Fatalf("org approvals after approve = %+v, want the seeded approval approved", approvals)
	}
}

// TestOrgApprovalsPageApproveWithoutTokenRefused covers the finding-7 gate:
// with an org root active but KERN_AUTH_TOKEN unset, org-approval mutations
// are refused (403 with guidance) — an unauthenticated loopback console must
// not be able to approve org-wide governance decisions.
func TestOrgApprovalsPageApproveWithoutTokenRefused(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_ORG_ROOT", orgRoot)
	t.Setenv("KERN_AUTH_TOKEN", "")
	seed, err := orgapprovals.Create(orgRoot, "deploy", "production", "org-admin", "pre-approve")
	if err != nil {
		t.Fatalf("orgapprovals.Create: %v", err)
	}

	app := newTestApp(t)
	form := url.Values{"action": {"approve"}, "id": {seed.ID}, "granted_by": {"org-admin"}}
	req := httptest.NewRequest(http.MethodPost, "/org-approvals", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /org-approvals without token = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "KERN_AUTH_TOKEN") {
		t.Errorf("403 body must name KERN_AUTH_TOKEN, got: %s", rec.Body.String())
	}
	// The store is untouched: the approval is still pending.
	approvals := orgapprovals.List(orgRoot)
	if len(approvals) != 1 || approvals[0].Status != orgapprovals.StatusPending {
		t.Fatalf("org approvals after refused approve = %+v, want still pending", approvals)
	}
}

// TestOrgApprovalsPageRejectFlipsStatus drives the reject form.
func TestOrgApprovalsPageRejectFlipsStatus(t *testing.T) {
	orgRoot := t.TempDir()
	t.Setenv("KERN_ORG_ROOT", orgRoot)
	t.Setenv("KERN_AUTH_TOKEN", "test-console-token") // org mutations require the token (finding 7)
	seed, err := orgapprovals.Create(orgRoot, "rollback", "production", "org-admin", "reject me")
	if err != nil {
		t.Fatalf("orgapprovals.Create: %v", err)
	}

	app := newTestApp(t)
	form := url.Values{"action": {"reject"}, "id": {seed.ID}, "granted_by": {"org-admin"}, "reason": {"not now"}}
	req := httptest.NewRequest(http.MethodPost, "/org-approvals", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer test-console-token")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /org-approvals = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	approvals := orgapprovals.List(orgRoot)
	if len(approvals) != 1 || approvals[0].Status != orgapprovals.StatusRejected {
		t.Fatalf("org approvals after reject = %+v, want the seeded approval rejected", approvals)
	}
}
