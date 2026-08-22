package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// These focused tests exercise the new Workflow D/E surfaces only. They run
// against the tiny `newEmptyApp` fixture (a single-package module), so they
// never re-index the whole repo and stay fast.

// TestV1AgentsEndpoint verifies POST /v1/agents returns the standard specialist
// roster (>=7 roles) plus the current task states.
func TestV1AgentsEndpoint(t *testing.T) {
	app := newEmptyApp(t)
	rec := postJSON(t, app, "/v1/agents", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Specialists []struct {
			ID   string   `json:"id"`
			Role string   `json:"role"`
			Caps []string `json:"capabilities"`
		} `json:"specialists"`
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Specialists) < 7 {
		t.Fatalf("specialists = %d, want >= 7", len(body.Specialists))
	}
	foundSRE := false
	for _, s := range body.Specialists {
		if s.Role == "sre" {
			foundSRE = true
		}
		if s.ID == "" {
			t.Fatal("specialist id empty")
		}
	}
	if !foundSRE {
		t.Fatal("specialists missing sre role")
	}
	if body.Tasks == nil {
		t.Fatal("tasks key missing")
	}
}

// TestV1TaskSubmitAndGet asserts a submitted task is returned with its id and
// initial state, and that GET /v1/tasks/{id} resolves it (404 only when
// genuinely unknown).
func TestV1TaskSubmitAndGet(t *testing.T) {
	app := newEmptyApp(t)
	rec := postJSON(t, app, "/v1/tasks", `{"input":"fix the bug","type":"code"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var sub struct {
		ID    string `json:"id"`
		State string `json:"state"`
		Type  string `json:"type"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sub); err != nil {
		t.Fatalf("decode submit: %v", err)
	}
	if sub.ID == "" {
		t.Fatal("submit did not return an id")
	}
	if sub.State == "" {
		t.Fatal("submit did not return a state")
	}
	if sub.Type != "code" {
		t.Fatalf("type = %q, want code", sub.Type)
	}

	rec2 := get(t, app, "/v1/tasks/"+sub.ID)
	if rec2.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", rec2.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got["ID"] != sub.ID && got["id"] != sub.ID {
		t.Fatalf("task id mismatch: got %v, want %s", got["ID"], sub.ID)
	}

	// Genuinely unknown id → 404.
	rec3 := get(t, app, "/v1/tasks/does-not-exist")
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("unknown task status = %d, want 404", rec3.Code)
	}
}

// TestV1LoopEndpoint asserts POST /v1/loop runs the closed loop at the default
// L0 (read-only) level and returns a stage timeline.
func TestV1LoopEndpoint(t *testing.T) {
	app := newEmptyApp(t)
	rec := postJSON(t, app, "/v1/loop", `{"intent":"add a Greet function","level":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Intent string `json:"intent"`
		Level  string `json:"level"`
		Stages []struct {
			Stage  string `json:"stage"`
			Status string `json:"status"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Intent != "add a Greet function" {
		t.Fatalf("intent = %q", body.Intent)
	}
	if body.Level != "L0" {
		t.Fatalf("level = %q, want L0 (default)", body.Level)
	}
	if len(body.Stages) < 3 {
		t.Fatalf("stages = %d, want >= 3", len(body.Stages))
	}
}

// TestV1IncidentInvestigateEndpoint asserts the Workflow D investigation
// returns an incident (root-caused), the hypotheses field, and an affected
// service. With the empty fixture the engine yields no runtime evidence, so
// hypotheses may be empty — the key must still be present.
func TestV1IncidentInvestigateEndpoint(t *testing.T) {
	app := newEmptyApp(t)
	rec := postJSON(t, app, "/v1/incidents/investigate", `{"alert":{"id":"a1","severity":"error","message":"checkout 500s","service":"checkout","source":"prometheus"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"hypotheses"`) {
		t.Fatalf("response missing hypotheses key: %s", rec.Body.String())
	}
	var body struct {
		Incident struct {
			Status string `json:"Status"`
			ID     string `json:"ID"`
		} `json:"incident"`
		AffectedService string `json:"affected_service"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Incident.ID == "" {
		t.Fatal("incident id missing")
	}
	if body.Incident.Status != string(domain.IncidentRootCauseFound) {
		t.Fatalf("incident status = %q, want ROOT_CAUSE_FOUND", body.Incident.Status)
	}
	if body.AffectedService != "checkout" {
		t.Fatalf("affected_service = %q, want checkout", body.AffectedService)
	}
}

// TestV1IncidentInvestigateMethodGuard asserts the endpoint is POST-only.
func TestV1IncidentInvestigateMethodGuard(t *testing.T) {
	app := newEmptyApp(t)
	rec := get(t, app, "/v1/incidents/investigate")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// TestGetIncidentByID verifies the spec's GET /v1/incidents/{id} route: after
// recording an incident it is retrievable by id, and unknown ids return 404.
func TestGetIncidentByID(t *testing.T) {
	app := newEmptyApp(t)
	rec := postJSON(t, app, "/api/incidents", `{"title":"checkout 500s","severity":"error","status":"OPEN","affected_service":"checkout"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var saved map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode saved: %v", err)
	}
	savedID, _ := saved["ID"].(string)
	if savedID == "" {
		t.Fatal("saved incident has no ID")
	}

	rec2 := get(t, app, "/v1/incidents/"+savedID)
	if rec2.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200: %s", rec2.Code, rec2.Body.String())
	}
	var got domain.Incident
	if err := json.Unmarshal(rec2.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode incident: %v", err)
	}
	if got.ID != savedID {
		t.Fatalf("id = %q, want %q", got.ID, savedID)
	}
	if got.Title != "checkout 500s" {
		t.Fatalf("title = %q, want %q", got.Title, "checkout 500s")
	}

	// Unknown id → 404.
	rec3 := get(t, app, "/v1/incidents/does-not-exist")
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("unknown status = %d, want 404", rec3.Code)
	}
}

// TestGetIncidentByIDMethodGuard asserts the by-id route is GET-only.
func TestGetIncidentByIDMethodGuard(t *testing.T) {
	app := newEmptyApp(t)
	rec := postJSON(t, app, "/v1/incidents/does-not-exist", `{}`)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// TestNestedTaskActionRoutes verifies the spec's nested /v1/tasks/{id}/{action}
// aliases behave identically to the top-level routes, and that the reserved
// task-detail GET and unknown actions are handled correctly.
func TestNestedTaskActionRoutes(t *testing.T) {
	app := newTestApp(t)
	sym := firstSymbolNodeID(t, app)
	sub := postJSON(t, app, "/v1/tasks", `{"input":"submit a task","type":"code"}`)
	if sub.Code != http.StatusOK {
		t.Fatalf("submit status = %d, want 200: %s", sub.Code, sub.Body.String())
	}
	var s struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(sub.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode submit: %v", err)
	}
	if s.ID == "" {
		t.Fatal("submit did not return an id")
	}

	// GET /v1/tasks/{id} task detail still works after the routing change.
	detail := get(t, app, "/v1/tasks/"+s.ID)
	if detail.Code != http.StatusOK {
		t.Fatalf("task detail status = %d, want 200: %s", detail.Code, detail.Body.String())
	}

	// Nested analyze alias behaves identically to the top-level /v1/analyze.
	body := `{"change":"` + sym + `"}`
	top := postJSON(t, app, "/v1/analyze", body)
	nested := postJSON(t, app, "/v1/tasks/"+s.ID+"/analyze", body)
	if top.Code != http.StatusOK {
		t.Fatalf("top-level analyze status = %d, want 200: %s", top.Code, top.Body.String())
	}
	if nested.Code != http.StatusOK {
		t.Fatalf("nested analyze status = %d, want 200: %s", nested.Code, nested.Body.String())
	}
	var nresp v1AnalyzeResponse
	if err := json.Unmarshal(nested.Body.Bytes(), &nresp); err != nil {
		t.Fatalf("decode nested analyze: %v", err)
	}
	if nresp.TaskID == "" {
		t.Fatal("nested analyze missing task_id")
	}

	// Unknown action → 404.
	bad := postJSON(t, app, "/v1/tasks/"+s.ID+"/bogus", body)
	if bad.Code != http.StatusNotFound {
		t.Fatalf("bogus action status = %d, want 404", bad.Code)
	}
}
