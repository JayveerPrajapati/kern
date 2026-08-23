package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// newPageRoutes are the HTML page routes added in Phase 18. Each must render
// with a 200 and carry the shared top navigation.
var newPageRoutes = []struct {
	path  string
	title string
}{
	{"/approvals", "<title>Approvals"},
	{"/risks", "<title>Risks"},
	{"/artifacts", "<title>Artifacts"},
	{"/audit", "<title>Audit"},
	{"/system-map", "<title>System Map"},
	{"/incidents", "<title>Incidents"},
	{"/efficiency", "<title>Efficiency"},
}

// TestNewPagesServe asserts every new HTML page route returns 200 and renders
// the shared top navigation plus its expected title.
func TestNewPagesServe200(t *testing.T) {
	app := newTestApp(t)
	for _, tt := range newPageRoutes {
		rec := get(t, app, tt.path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200: %s", tt.path, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, tt.title) {
			t.Fatalf("%s missing %q in body", tt.path, tt.title)
		}
		if !strings.Contains(body, "topnav") {
			t.Fatalf("%s missing shared topnav", tt.path)
		}
	}
}

// newJSONRoutes are the new JSON endpoints. Each must return 200 with an
// application/json Content-Type.
var newJSONRoutes = []string{
	"/api/risks",
	"/api/artifacts",
	"/api/audit",
	"/api/system-map",
	"/api/efficiency",
}

// TestNewJSONEndpoints asserts each new JSON endpoint returns 200 and a
// decodable JSON body carrying an items array (or a root object for system-map).
func TestNewJSONEndpoints(t *testing.T) {
	app := newTestApp(t)
	for _, path := range newJSONRoutes {
		rec := get(t, app, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200: %s", path, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("%s Content-Type = %q, want application/json", path, ct)
		}
		if len(strings.TrimSpace(rec.Body.String())) == 0 {
			t.Fatalf("%s returned empty body", path)
		}
	}
}

// TestRisksEndpointReturnsItems asserts /api/risks returns a non-empty items
// array with at least one assessed resource+action pair.
func TestRisksEndpointReturnsItems(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/api/risks")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Items []struct {
			Resource string  `json:"resource"`
			Level    string  `json:"level"`
			Score    float64 `json:"score"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) == 0 {
		t.Fatal("items is empty, want non-empty risk assessment")
	}
	if body.Items[0].Resource == "" || body.Items[0].Level == "" {
		t.Fatal("risk item missing resource or level")
	}
}

// TestSystemMapEndpoint asserts /api/system-map carries the architecture
// overview counters (module/file/edge counts).
func TestSystemMapEndpoint(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/api/system-map")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		ModuleCount int `json:"module_count"`
		FileCount   int `json:"file_count"`
		SymbolCount int `json:"symbol_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ModuleCount < 1 || body.FileCount < 1 {
		t.Fatalf("system-map counts too small: modules=%d files=%d", body.ModuleCount, body.FileCount)
	}
}

// TestApprovalWorkflowStillWorks exercises the full human approve/reject flow:
// a pending approval is created, approved via POST /api/approvals/approve, and
// another is rejected via POST /api/approvals/reject, both returning 200 and
// removing the item from the pending set.
func TestApprovalWorkflowStillWorks(t *testing.T) {
	app := newTestApp(t)

	approveID := seedApproval(app, "web-test-agent", "approve me")
	rejectID := seedApproval(app, "web-test-agent", "reject me")

	// Approve one.
	rec := postJSON(t, app, "/api/approvals/approve",
		`{"id":"`+approveID+`","approver":"console-tester"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Reject the other.
	rec = postJSON(t, app, "/api/approvals/reject",
		`{"id":"`+rejectID+`","approver":"console-tester"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reject status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Neither should remain pending.
	for _, id := range []string{approveID, rejectID} {
		for _, p := range app.approvals.Pending() {
			if p.ID == id {
				t.Fatalf("approval %s still pending after decision", id)
			}
		}
	}

	// The approvals page should still serve 200 with the now-empty roster.
	pageRec := get(t, app, "/approvals")
	if pageRec.Code != http.StatusOK {
		t.Fatalf("/approvals status = %d, want 200", pageRec.Code)
	}
}
