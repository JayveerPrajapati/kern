package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// fixtureRoot writes a tiny Go module (go.mod + a helper func + a test) that
// index.Build can parse quickly, seeds the incident and memory stores, and
// returns the temp project root.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module consolefixture\n\ngo 1.20\n",
		"main.go": `package main

func helper() string {
	return "h"
}

func main() {
	_ = helper()
}
`,
		"main_test.go": `package main

import "testing"

func TestHelper(t *testing.T) {
	if helper() != "h" {
		t.Fatal("helper() != h")
	}
}
`,
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Seed one incident and one memory before building the App so the console
	// reads them back from the store.
	_, err := incident.NewStore(root).Save(&domain.Incident{
		ID:              "inc-1",
		Title:           "checkout 500s",
		Severity:        domain.SeverityError,
		Status:          domain.IncidentRootCauseFound,
		AffectedService: "checkout",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = memory.NewMemoryStore(root).Add(domain.Memory{
		Type:    domain.MemoryIncident,
		Content: "checkout incident",
		Scope:   "service:checkout",
		Tags:    []string{"checkout"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestWebBridgesPlatformBus: web.New wires the App's event bus into the
// Platform (so verification-engine architecture events reach the
// webhook-subscribed bus) and enables persistence/replay from the shared
// .kern/events.jsonl file. Replay must be safe when no events file exists
// (first run): New ignores the error and never propagates it. Replaying an
// existing file must not re-append the replayed events to the same file.
func TestWebBridgesPlatformBus(t *testing.T) {
	root := fixtureRoot(t)
	a, err := New(root)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	if a.Bus() == nil {
		t.Fatal("a.Bus() is nil")
	}
	if a.platform.Bus() != a.bus {
		t.Fatal("platform bus is not bridged to the app bus")
	}
	// Best-effort replay: a missing events file must not panic or fail.
	_, _ = a.bus.Replay(filepath.Join(root, ".kern", "events.jsonl"))

	// A pre-existing events file (e.g. written by the guard CLI) must be
	// replayed without growing the file itself on every server restart.
	eventsPath := filepath.Join(root, ".kern", "events.jsonl")
	line := `{"ID":"e-1-1","Kind":"architecture.violation","Source":"guard","Subject":"Do","OccurredAt":"2026-09-01T00:00:00Z"}` + "\n"
	if err := os.WriteFile(eventsPath, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	a2, err := New(root)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	if a2.Bus() == nil || a2.platform.Bus() != a2.bus {
		t.Fatal("platform bus not bridged on second New")
	}
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(data, []byte("e-1-1")); got != 1 {
		t.Fatalf("replay re-appended events to the persistence file: %d copies of e-1-1 in %q", got, data)
	}
}

// newTestApp builds an App over a fresh fixture root.
func newTestApp(t *testing.T) *App {
	t.Helper()
	app, err := New(fixtureRoot(t))
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	return app
}

func get(t *testing.T, app *App, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

// seedApproval requests a pending approval on the App's approval workflow and
// returns its ID.
func seedApproval(a *App, requester, reason string) string {
	return a.approvals.Request("task-1", requester, reason).ID
}

// postJSON issues a POST with the given JSON body and returns the recorder.
func postJSON(t *testing.T, app *App, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func TestOverviewEndpoint(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/api/overview")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var body struct {
		Symbols     int    `json:"symbols"`
		GeneratedAt string `json:"generated_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Symbols < 1 {
		t.Fatalf("symbols = %d, want >= 1", body.Symbols)
	}
	if body.GeneratedAt == "" {
		t.Fatal("generated_at is empty")
	}
}

func TestIncidentsEndpoint(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/api/incidents")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Items []struct {
			Severity string `json:"severity"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) < 1 {
		t.Fatalf("items = %d, want >= 1", len(body.Items))
	}
	if body.Items[0].Severity == "" {
		t.Fatal("items[0].severity is empty")
	}
}

func TestGovernanceEndpoint(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/api/governance")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Policies         []json.RawMessage `json:"policies"`
		ApprovalsPending []json.RawMessage `json:"approvals_pending"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Policies) == 0 {
		t.Fatal("policies is empty, want non-empty from DefaultPolicies")
	}
	if len(body.ApprovalsPending) < 0 {
		t.Fatalf("approvals_pending = %d, want >= 0", len(body.ApprovalsPending))
	}
}

func TestGovernanceMetricsAvgConfidence(t *testing.T) {
	app := newTestApp(t)

	// Seed two tasks on the TaskService registry, each carrying a what-if
	// ImpactReport whose Confidence is populated by TaskService.WhatIf.
	seed := func(id string, conf float64) {
		task := agent.NewTask("what-if", "change "+id)
		task.ImpactReport = &whatif.Impact{Confidence: conf}
		if err := app.taskSvc.Registry().SubmitTask(task); err != nil {
			t.Fatalf("submit task: %v", err)
		}
	}
	seed("a", 0.8)
	seed("b", 0.6)

	rec := get(t, app, "/api/governance/metrics")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		AvgConfidence float64 `json:"AvgConfidence"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := 0.7 // (0.8 + 0.6) / 2
	if body.AvgConfidence != want {
		t.Fatalf("AvgConfidence = %v, want %v", body.AvgConfidence, want)
	}
}

func TestNotFound(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/api/does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error != "not found" {
		t.Fatalf("error = %q, want %q", body.Error, "not found")
	}
}

func TestDashboard(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<title>") && !strings.Contains(body, "<h1>") {
		t.Fatal("dashboard body contains neither <title> nor <h1>")
	}
}

func TestApprovalApproveEndpoint(t *testing.T) {
	app := newTestApp(t)
	id := seedApproval(app, "sre", "deploy")
	rec := postJSON(t, app, "/api/approvals/approve", `{"id":"`+id+`","approver":"human"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "approved" {
		t.Fatalf("status = %q, want %q", body.Status, "approved")
	}
}

func TestApprovalApproveMethodGuard(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/api/approvals/approve")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// newEmptyApp builds an App over a fresh root with no seeded incident or
// memory, so write-endpoint tests can assert exact store state.
func newEmptyApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module consoleempty\n\ngo 1.20\n"), 0o644)
	os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	app, err := New(root)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	return app
}

func TestIncidentSaveEndpoint(t *testing.T) {
	app := newEmptyApp(t)
	rec := postJSON(t, app, "/api/incidents", `{"title":"checkout 500s","severity":"error","status":"OPEN","affected_service":"checkout"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	list, err := app.inter.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("incidents = %d, want 1", len(list))
	}
	if list[0].Title != "checkout 500s" {
		t.Fatalf("title = %q, want %q", list[0].Title, "checkout 500s")
	}
}

func TestHealthEndpoint(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/api/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("body = %q, want to contain %q", rec.Body.String(), "ok")
	}
}

// TestContextEngineWiredRuntimeAndBoundary verifies G3: the shipped web App
// wires the runtime source + boundary provider into the context engine, so a
// change in a real production run populates ContextPacket.RuntimeEvidence and
// surfaces boundary rules in ArchitectureRules. Fixture-based (no whole-repo
// re-index).
func TestContextEngineWiredRuntimeAndBoundary(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module boundaryfixture\n\ngo 1.20\n",
		"web/caller.go": `package web

import "boundaryfixture/db"

func Caller() string { return db.Do() }
`,
		"db/db.go":              "package db\n\nfunc Do() string { return \"d\" }\n",
		".kern/boundaries.json": `{"rules":[{"from":"web","to":"db","action":"forbid"}]}`,
		".kern/runtime.json":    `{"events":[{"id":"e1","type":"error","severity":"error","message":"boom","service":"checkout"}]}`,
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	app, err := New(root)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	pkt, _, err := app.platform.Analyze("Caller")
	if err != nil {
		t.Fatalf("platform.Analyze(Caller): %v", err)
	}

	if len(pkt.RuntimeEvidence) == 0 {
		t.Error("expected RuntimeEvidence from the wired runtime source (error snapshot)")
	}

	found := false
	for _, p := range pkt.ArchitectureRules {
		if strings.Contains(p.Name, "boundary:web->db") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the web->db boundary rule in ArchitectureRules, got %v", ruleNames(pkt.ArchitectureRules))
	}
}

func ruleNames(ps []domain.Policy) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}
