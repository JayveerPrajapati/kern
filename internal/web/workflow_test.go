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
