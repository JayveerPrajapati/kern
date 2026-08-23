package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestV1ApproveAndRejectRoutes verifies the Phase 19.1 REST surface for approvals:
// POST /v1/approve and POST /v1/reject reachable under the stable /v1 namespace,
// plus GET /v1/approvals/pending returning the pending roster. These are thin
// re-registrations of the existing web handlers.
func TestV1ApproveAndRejectRoutes(t *testing.T) {
	app := newTestApp(t)

	// Seed one pending approval and approve it via /v1/approve.
	approveID := seedApproval(app, "sre", "deploy")
	approveRec := postJSON(t, app, "/v1/approve",
		`{"id":"`+approveID+`","approver":"tester"}`)
	if approveRec.Code != http.StatusOK {
		t.Fatalf("POST /v1/approve status = %d, want 200: %s",
			approveRec.Code, approveRec.Body.String())
	}
	var approved struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(approveRec.Body.Bytes(), &approved); err != nil {
		t.Fatalf("decode approve response: %v", err)
	}
	if approved.ID != approveID {
		t.Fatalf("approved id = %q, want %q", approved.ID, approveID)
	}
	if approved.Status != "approved" {
		t.Fatalf("approved status = %q, want %q", approved.Status, "approved")
	}

	// Seed a second approval and reject it via /v1/reject.
	rejectID := seedApproval(app, "sre", "rollback")
	rejectRec := postJSON(t, app, "/v1/reject",
		`{"id":"`+rejectID+`","approver":"tester"}`)
	if rejectRec.Code != http.StatusOK {
		t.Fatalf("POST /v1/reject status = %d, want 200: %s",
			rejectRec.Code, rejectRec.Body.String())
	}
	var rejected struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rejectRec.Body.Bytes(), &rejected); err != nil {
		t.Fatalf("decode reject response: %v", err)
	}
	if rejected.Status != "rejected" {
		t.Fatalf("rejected status = %q, want %q", rejected.Status, "rejected")
	}

	// GET /v1/approvals/pending should serve a JSON array of the pending roster
	// (both seeded approvals are now decided, so the array is empty but still a
	// valid 200 JSON response).
	pendingRec := get(t, app, "/v1/approvals/pending")
	if pendingRec.Code != http.StatusOK {
		t.Fatalf("GET /v1/approvals/pending status = %d, want 200: %s",
			pendingRec.Code, pendingRec.Body.String())
	}
	if ct := pendingRec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var pending []domainApproval
	if err := json.Unmarshal(pendingRec.Body.Bytes(), &pending); err != nil {
		t.Fatalf("decode pending response: %v", err)
	}
}

// TestV1ApproveRejectsBadBody verifies the REST approve/reject endpoints reject
// malformed or incomplete bodies with 400.
func TestV1ApproveRejectsBadBody(t *testing.T) {
	app := newTestApp(t)

	cases := []struct {
		name string
		path string
		body string
	}{
		{"empty", "/v1/approve", ``},
		{"missing-id", "/v1/approve", `{"approver":"tester"}`},
		{"empty-body-reject", "/v1/reject", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postJSON(t, app, tc.path, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST %s status = %d, want 400: %s",
					tc.path, rec.Code, rec.Body.String())
			}
		})
	}
}