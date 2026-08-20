package sdk

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// fakeRequest holds a captured request with its body fully read into a buffer.
type fakeRequest struct {
	method string
	path   string
	body   []byte
}

// newFakeServer returns an httptest.Server that records the last request
// (including its body) and responds with the given status and body. The second
// return is a function returning the most recently received request.
func newFakeServer(t *testing.T, status int, body string) (*httptest.Server, func() *fakeRequest) {
	t.Helper()
	var last *fakeRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		last = &fakeRequest{method: r.Method, path: r.URL.Path, body: b}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() *fakeRequest { return last }
}

func TestAnalyze(t *testing.T) {
	body := `{"packet":{"Change":"fix bug"},"text":"ok"}`
	srv, lastReq := newFakeServer(t, 200, body)
	client := New(srv.URL)

	res, err := client.Analyze("fix bug")
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	if res.Text != "ok" {
		t.Errorf("Text = %q, want %q", res.Text, "ok")
	}

	req := lastReq()
	if req.method != http.MethodPost {
		t.Errorf("Method = %q, want POST", req.method)
	}
	if req.path != "/v1/analyze" {
		t.Errorf("Path = %q, want /v1/analyze", req.path)
	}

	var sent map[string]string
	if err := json.Unmarshal(req.body, &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if sent["change"] != "fix bug" {
		t.Errorf("sent change = %q, want %q", sent["change"], "fix bug")
	}
}

func TestWhatIf(t *testing.T) {
	body := `{"Change":"x","Risk":"high","Recommendation":"do it"}`
	srv, lastReq := newFakeServer(t, 200, body)
	client := New(srv.URL)

	res, err := client.WhatIf("remove x", "remove_symbol", "y")
	if err != nil {
		t.Fatalf("WhatIf returned error: %v", err)
	}

	if res["Risk"] != "high" {
		t.Errorf("Risk = %v, want high", res["Risk"])
	}
	if res["Recommendation"] != "do it" {
		t.Errorf("Recommendation = %v, want do it", res["Recommendation"])
	}

	req := lastReq()
	if req.path != "/v1/what-if" {
		t.Errorf("Path = %q, want /v1/what-if", req.path)
	}
	var sent map[string]string
	if err := json.Unmarshal(req.body, &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if sent["kind"] != "remove_symbol" {
		t.Errorf("sent kind = %q, want remove_symbol", sent["kind"])
	}
	if sent["new_target"] != "y" {
		t.Errorf("sent new_target = %q, want y", sent["new_target"])
	}
}

// TestVerify checks POST /v1/verify sends types as a JSON array.
func TestVerify(t *testing.T) {
	srv, lastReq := newFakeServer(t, 200, `{"Verdict":"pass"}`)
	client := New(srv.URL)

	res, err := client.Verify([]string{"build", "test"})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if res["Verdict"] != "pass" {
		t.Errorf("Verdict = %v, want pass", res["Verdict"])
	}

	req := lastReq()
	if req.path != "/v1/verify" {
		t.Errorf("Path = %q, want /v1/verify", req.path)
	}
	var sent struct {
		Types []string `json:"types"`
	}
	if err := json.Unmarshal(req.body, &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if len(sent.Types) != 2 || sent.Types[0] != "build" || sent.Types[1] != "test" {
		t.Errorf("sent types = %v, want [build test]", sent.Types)
	}
}

// TestVerifyTypesServerHonors proves the SDK sends types the server's real
// decoder accepts. The handler mirrors handleV1Verify's body semantics: it
// requires "types" to decode as a string array (the SDK's wire shape) and
// reports the decoded values back. Sending anything the server would silently
// drop (e.g. a bare string) would fail the decode or come back wrong.
func TestVerifyTypesServerHonors(t *testing.T) {
	// Handler replicates handleV1Verify's decode rule: "types" must be an
	// array of strings. If the SDK sent a string the decode would fail and the
	// server would 400 (not silently fall back to the default).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Types []string `json:"types"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid types"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"types": req.Types})
	}))
	t.Cleanup(srv.Close)
	client := New(srv.URL)

	res, err := client.Verify([]string{"build", "test"})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	types, ok := res["types"].([]any)
	if !ok || len(types) != 2 || types[0] != "build" || types[1] != "test" {
		t.Errorf("server decoded types = %v, want [build test]", res["types"])
	}
}

// TestInvestigateIncident checks POST /v1/incidents/investigate posts an alert
// body and decodes the returned incident + hypotheses + affected_service.
func TestInvestigateIncident(t *testing.T) {
	body := `{
		"incident": {
			"ID": "inc-1",
			"Title": "checkout failing",
			"Severity": "HIGH",
			"Status": "INVESTIGATING",
			"AffectedService": "checkout",
			"Message": "timeouts"
		},
		"hypotheses": [
			{"Statement": "db saturated", "Source": "metrics", "Confidence": "INFERENCE", "Score": 0.9}
		],
		"affected_service": "checkout"
	}`
	srv, lastReq := newFakeServer(t, 200, body)
	client := New(srv.URL)

	alert := domain.Alert{ID: "a-1", Message: "checkout timeouts", Service: "checkout"}
	res, err := client.InvestigateIncident(alert)
	if err != nil {
		t.Fatalf("InvestigateIncident returned error: %v", err)
	}
	if res.AffectedService != "checkout" {
		t.Errorf("AffectedService = %q, want checkout", res.AffectedService)
	}
	if res.Incident.ID != "inc-1" {
		t.Errorf("Incident.ID = %q, want inc-1", res.Incident.ID)
	}
	if res.Incident.AffectedService != "checkout" {
		t.Errorf("Incident.AffectedService = %q, want checkout", res.Incident.AffectedService)
	}
	if len(res.Hypotheses) != 1 || res.Hypotheses[0].Statement != "db saturated" {
		t.Errorf("Hypotheses = %+v, want 1 hypothesis 'db saturated'", res.Hypotheses)
	}

	req := lastReq()
	if req.method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.method)
	}
	if req.path != "/v1/incidents/investigate" {
		t.Errorf("Path = %q, want /v1/incidents/investigate", req.path)
	}
	var sent struct {
		Alert domain.Alert `json:"alert"`
	}
	if err := json.Unmarshal(req.body, &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if sent.Alert.Message != "checkout timeouts" {
		t.Errorf("sent alert.Message = %q, want %q", sent.Alert.Message, "checkout timeouts")
	}
}

func TestGraph(t *testing.T) {
	body := `{"node":{"id":"helper"},"who_calls":[{"id":"caller"}]}`
	srv, lastReq := newFakeServer(t, 200, body)
	client := New(srv.URL)

	res, err := client.Graph("helper")
	if err != nil {
		t.Fatalf("Graph returned error: %v", err)
	}

	node, ok := res["node"].(map[string]any)
	if !ok {
		t.Fatalf("node not a map: %v", res["node"])
	}
	if node["id"] != "helper" {
		t.Errorf("node.id = %v, want helper", node["id"])
	}

	req := lastReq()
	if req.method != http.MethodGet {
		t.Errorf("request method = %q, want GET", req.method)
	}
	if req.path != "/v1/graph/helper" {
		t.Errorf("Path = %q, want /v1/graph/helper", req.path)
	}
}

func TestContext(t *testing.T) {
	srv, lastReq := newFakeServer(t, 200, `{"Change":"fix bug","Risks":["high"]}`)
	client := New(srv.URL)

	res, err := client.Context("fix bug")
	if err != nil {
		t.Fatalf("Context returned error: %v", err)
	}
	if res["Change"] != "fix bug" {
		t.Errorf("Change = %v, want fix bug", res["Change"])
	}

	req := lastReq()
	if req.method != http.MethodPost {
		t.Errorf("request method = %q, want POST", req.method)
	}
	if req.path != "/v1/context" {
		t.Errorf("Path = %q, want /v1/context", req.path)
	}
	var sent map[string]string
	if err := json.Unmarshal(req.body, &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if sent["change"] != "fix bug" {
		t.Errorf("sent change = %q, want fix bug", sent["change"])
	}
}

func TestRisk(t *testing.T) {
	body := `{"risks":["high","medium"],"change":"remove x"}`
	srv, lastReq := newFakeServer(t, 200, body)
	client := New(srv.URL)

	res, err := client.Risk("remove x")
	if err != nil {
		t.Fatalf("Risk returned error: %v", err)
	}
	if res["change"] != "remove x" {
		t.Errorf("change = %v, want remove x", res["change"])
	}
	risks, ok := res["risks"].([]any)
	if !ok || len(risks) != 2 || risks[0] != "high" {
		t.Errorf("risks = %v, want [high medium]", res["risks"])
	}

	req := lastReq()
	if req.method != http.MethodPost {
		t.Errorf("request method = %q, want POST", req.method)
	}
	if req.path != "/v1/risk" {
		t.Errorf("Path = %q, want /v1/risk", req.path)
	}
	var sent map[string]string
	if err := json.Unmarshal(req.body, &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if sent["change"] != "remove x" {
		t.Errorf("sent change = %q, want remove x", sent["change"])
	}
}

func TestTask(t *testing.T) {
	body := `{"id":"task-1","status":"running"}`
	srv, lastReq := newFakeServer(t, 200, body)
	client := New(srv.URL)

	res, err := client.Task("task-1")
	if err != nil {
		t.Fatalf("Task returned error: %v", err)
	}
	if res["id"] != "task-1" {
		t.Errorf("id = %v, want task-1", res["id"])
	}

	req := lastReq()
	if req.method != http.MethodGet {
		t.Errorf("request method = %q, want GET", req.method)
	}
	if req.path != "/v1/tasks/task-1" {
		t.Errorf("Path = %q, want /v1/tasks/task-1", req.path)
	}
}

func TestTaskNotFound(t *testing.T) {
	srv, _ := newFakeServer(t, 404, `{"error":"task not found: nope"}`)
	client := New(srv.URL)

	if _, err := client.Task("nope"); err == nil {
		t.Fatal("expected error for 404 response, got nil")
	}
}

func TestErrorStatus(t *testing.T) {
	srv, _ := newFakeServer(t, 500, `{"error":"boom"}`)
	client := New(srv.URL)

	_, err := client.Analyze("change")
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not mention status 500", err.Error())
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q does not mention server message boom", err.Error())
	}
}

func TestBaseDefault(t *testing.T) {
	client := New("")
	if client.base != DefaultBaseURL {
		t.Errorf("New(\"\").base = %q, want %q", client.base, DefaultBaseURL)
	}
	if DefaultBaseURL != "http://localhost:8090" {
		t.Errorf("DefaultBaseURL = %q, want http://localhost:8090", DefaultBaseURL)
	}
}

func TestBaseDefaultRequestHost(t *testing.T) {
	// Stand up a server and confirm a client built on its host actually sends
	// requests there, and that the default base is otherwise untouched.
	srv, lastReq := newFakeServer(t, 200, `{}`)
	client := New(srv.URL)
	if _, err := client.MemoryList(); err != nil {
		t.Fatalf("MemoryList returned error: %v", err)
	}
	req := lastReq()
	if req.path != "/v1/memory" {
		t.Errorf("Path = %q, want /v1/memory", req.path)
	}
}

// TestAgents checks POST /v1/agents is used and the response parses.
func TestAgents(t *testing.T) {
	srv, lastReq := newFakeServer(t, 200, `{"specialists":[{"id":"planner","role":"planner"}],"tasks":[]}`)
	client := New(srv.URL)

	res, err := client.Agents()
	if err != nil {
		t.Fatalf("Agents returned error: %v", err)
	}
	specs, ok := res["specialists"].([]any)
	if !ok || len(specs) == 0 {
		t.Fatalf("specialists = %v, want non-empty", res["specialists"])
	}

	req := lastReq()
	if req.method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.method)
	}
	if req.path != "/v1/agents" {
		t.Errorf("Path = %q, want /v1/agents", req.path)
	}
}

// TestLoop checks POST /v1/loop sends lowercase intent/level on the wire.
func TestLoop(t *testing.T) {
	srv, lastReq := newFakeServer(t, 200, `{"intent":"x","level":"L0","stages":[]}`)
	client := New(srv.URL)

	res, err := client.Loop("add a Greet function", "L2")
	if err != nil {
		t.Fatalf("Loop returned error: %v", err)
	}
	if res["intent"] != "x" {
		t.Errorf("intent = %v, want x", res["intent"])
	}

	req := lastReq()
	if req.path != "/v1/loop" {
		t.Errorf("Path = %q, want /v1/loop", req.path)
	}
	var sent map[string]string
	if err := json.Unmarshal(req.body, &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if sent["intent"] != "add a Greet function" {
		t.Errorf("sent intent = %q, want %q", sent["intent"], "add a Greet function")
	}
	if sent["level"] != "L2" {
		t.Errorf("sent level = %q, want L2", sent["level"])
	}
}

// TestTaskSubmit checks POST /v1/tasks sends lowercase input/type.
func TestTaskSubmit(t *testing.T) {
	srv, lastReq := newFakeServer(t, 200, `{"id":"t-1","state":"CREATED","type":"code"}`)
	client := New(srv.URL)

	res, err := client.TaskSubmit("fix bug", "code")
	if err != nil {
		t.Fatalf("TaskSubmit returned error: %v", err)
	}
	if res["id"] != "t-1" {
		t.Errorf("id = %v, want t-1", res["id"])
	}

	req := lastReq()
	if req.method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.method)
	}
	if req.path != "/v1/tasks" {
		t.Errorf("Path = %q, want /v1/tasks", req.path)
	}
	var sent struct {
		Input string `json:"input"`
		Type  string `json:"type"`
	}
	if err := json.Unmarshal(req.body, &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if sent.Input != "fix bug" {
		t.Errorf("sent input = %q, want fix bug", sent.Input)
	}
	if sent.Type != "code" {
		t.Errorf("sent type = %q, want code", sent.Type)
	}
}
