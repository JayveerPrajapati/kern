package kernsdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/web"
)

// contractFixture mirrors sdk/contract/tools_call.json — the ONE shared
// route-shape contract for POST /v1/tools/{name} that every shipped SDK
// client suite (Go, Python, TypeScript) asserts against. A route-shape
// change (envelope, status mapping) must fail all three suites until the
// clients are updated.
type contractFixture struct {
	Version int `json:"_version"`
	Cases   []struct {
		ID       string         `json:"id"`
		Name     string         `json:"name"`
		Args     map[string]any `json:"args"`
		Response struct {
			Status int            `json:"status"`
			Body   map[string]any `json:"body"`
		} `json:"response"`
		ExpectOutput string `json:"expect_output"`
	} `json:"cases"`
}

// loadContractFixture reads the shared contract fixture. go test runs with
// cwd = package dir (sdk/go/kernsdk), so the fixture lives two levels up.
func loadContractFixture(t *testing.T) contractFixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "contract", "tools_call.json"))
	if err != nil {
		t.Fatalf("load shared contract fixture: %v", err)
	}
	var fx contractFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("parse shared contract fixture: %v", err)
	}
	if fx.Version != 1 {
		t.Fatalf("contract fixture _version = %d, want 1", fx.Version)
	}
	if len(fx.Cases) == 0 {
		t.Fatal("contract fixture has no cases")
	}
	return fx
}

// sdkFixture writes a tiny Go module so the web console (backed by the same
// TaskService application services the CLI/MCP use) can analyze it.
func sdkFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module sdkserver\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package main\n\n// Greet says hello.\nfunc Greet() string { return \"hi\" }\n\nfunc main() { _ = Greet() }\n"
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestGoSDKAgainstControlPlane is the exit gate: the Go SDK drives
// the SAME control-plane application services as the CLI and MCP — through the
// same REST surface the Python/TypeScript SDKs use. A real kern-server
// (internal/web over a fixture) backs the client.
func TestGoSDKAgainstControlPlane(t *testing.T) {
	root := sdkFixture(t)
	app, err := web.New(root)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	srv := httptest.NewServer(app)
	defer srv.Close()

	c := New(srv.URL, nil)
	ctx := context.Background()

	// Health.
	h, err := c.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h == nil {
		t.Error("Health returned nil")
	}

	// Analyze → context packet (same services as kern analyze / kern_analyze).
	an, err := c.Analyze(ctx, "Greet")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if an == nil {
		t.Error("Analyze returned nil")
	}

	// Plan → implementation plan.
	pl, err := c.Plan(ctx, "Greet")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if pl == nil {
		t.Error("Plan returned nil")
	}

	// What-if → impact simulation.
	wi, err := c.WhatIf(ctx, "remove_symbol", "Greet", "")
	if err != nil {
		t.Fatalf("WhatIf: %v", err)
	}
	if wi == nil {
		t.Error("WhatIf returned nil")
	}

	// Memory add + list.
	if _, err := c.MemoryAdd(ctx, "go sdk lesson", "lesson", "test", []string{"sdk"}); err != nil {
		t.Fatalf("MemoryAdd: %v", err)
	}
	ms, err := c.MemoryList(ctx)
	if err != nil {
		t.Fatalf("MemoryList: %v", err)
	}
	items, _ := ms["items"].([]any)
	if len(items) == 0 {
		t.Errorf("MemoryList returned no memories after MemoryAdd: %v", ms)
	}

	// Task submit + fetch.
	sub, err := c.TaskSubmit(ctx, "Greet", "code")
	if err != nil {
		t.Fatalf("TaskSubmit: %v", err)
	}
	if sub == nil {
		t.Error("TaskSubmit returned nil")
	}

	// Agents roster.
	agents, err := c.Agents(ctx)
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	specs, _ := agents["specialists"].([]any)
	if len(specs) == 0 {
		t.Errorf("Agents returned empty roster: %v", agents)
	}

	// Approvals pending roster (D2).
	ap, err := c.ApprovalsPending(ctx)
	if err != nil {
		t.Fatalf("ApprovalsPending: %v", err)
	}
	if ap == nil {
		t.Error("ApprovalsPending returned nil")
	}

	// Incidents list (D2).
	inc, err := c.Incidents(ctx)
	if err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	if inc == nil {
		t.Error("Incidents returned nil")
	}

	// Single incident by id (D2): create one through the same REST surface,
	// then fetch it back.
	var created map[string]any
	if err := c.do(ctx, http.MethodPost, "/api/incidents", map[string]string{
		"title": "sdk test incident", "severity": "error", "status": "OPEN", "affected_service": "checkout",
	}, &created); err != nil {
		t.Fatalf("create incident: %v", err)
	}
	id, _ := created["ID"].(string)
	if id == "" {
		t.Fatalf("created incident missing ID: %v", created)
	}
	one, err := c.Incident(ctx, id)
	if err != nil {
		t.Fatalf("Incident(%s): %v", id, err)
	}
	if one == nil {
		t.Error("Incident returned nil")
	}

	// Events stream opens and carries at least the SSE headers (D2).
	body, err := c.EventsStream(ctx)
	if err != nil {
		t.Fatalf("EventsStream: %v", err)
	}
	defer func() { _ = body.Close() }()
	if _, err := body.Read(make([]byte, 128)); err != nil && err != io.EOF {
		t.Fatalf("EventsStream read: %v", err)
	}
}

// TestGoSDKReturnsStatusErrors verifies the SDK surfaces non-2xx as errors.
func TestGoSDKReturnsStatusErrors(t *testing.T) {
	root := sdkFixture(t)
	app, err := web.New(root)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	srv := httptest.NewServer(app)
	defer srv.Close()

	c := New(srv.URL, nil)
	// A task that does not exist must yield an error, not a silent nil.
	_, err = c.Task(context.Background(), "task-does-not-exist")
	if err == nil {
		t.Fatal("Task(nonexistent) should error")
	}
}

// sdkToolServer is a minimal web.ToolServer double for the CallTool SDK test:
// the /v1/tools/{name} route delegates to CallToolGoverned, so the test
// asserts the full client → route → dispatch contract. A fake is used because
// internal/web cannot import internal/mcp (mcp → org → enterprise → web cycle)
// and the real governed dispatch is already exercised by the internal/sdk
// catalog tests over the same CallToolGoverned path (web/tools_passthrough_test
// uses the identical pattern).
type sdkToolServer struct {
	out      string
	err      error
	lastName string
	lastArgs map[string]any
}

func (s *sdkToolServer) CallToolGoverned(_ context.Context, name string, args map[string]any) (string, error) {
	s.lastName = name
	s.lastArgs = args
	return s.out, s.err
}

func (s *sdkToolServer) Close() {}

// TestGoSDKCallTool covers the client half of the SDK passthrough
// (POST /v1/tools/{name}): CallTool posts the argument map, decodes the
// {"output": ...} envelope and returns the raw tool text; governed denials
// and unknown tools surface as *Err with the mapped HTTP status.
func TestGoSDKCallTool(t *testing.T) {
	// One shared fake whose behavior is mutated between calls — the factory
	// is consulted once at web.New time, so re-registering per scenario
	// would not affect the already-built App (same pattern as the web
	// tools_passthrough_test).
	fake := &sdkToolServer{out: "masked 1 secrets\n"}
	web.SetToolServerFactory(func(string) web.ToolServer { return fake })

	root := sdkFixture(t)
	app, err := web.New(root)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	srv := httptest.NewServer(app)
	defer srv.Close()

	c := New(srv.URL, nil)
	ctx := context.Background()

	// Success: the raw tool output payload is returned.
	out, err := c.CallTool(ctx, "kern_mask_pii", map[string]any{"text": "token=sk-abc123"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if out != "masked 1 secrets\n" {
		t.Errorf("CallTool output = %q, want the raw tool text", out)
	}

	// Unknown tool: the route maps domain.ErrToolUnknown to 404, which the
	// SDK surfaces as *Err carrying the HTTP status.
	fake.out = ""
	fake.err = domain.ErrToolUnknown
	_, err = c.CallTool(ctx, "kern_no_such_tool", map[string]any{})
	var apiErr *Err
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Fatalf("unknown tool error = %v, want *Err with status 404", err)
	}

	// Governed denial: domain.ErrToolDenied maps to 403.
	fake.err = domain.ErrToolDenied
	_, err = c.CallTool(ctx, "kern_mask_pii", map[string]any{"root": "/etc"})
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Fatalf("denied tool error = %v, want *Err with status 403", err)
	}
}

// TestGoSDKCallToolContractFixture drives CallTool against the SHARED
// contract fixture (sdk/contract/tools_call.json): success returns the
// {"output": ...} payload and forwards the exact name+args to the route, and
// every error class (403 denied, 404 unknown, 500 generic) surfaces as *Err
// with the mapped HTTP status. The Python and TypeScript suites run the same
// cases against the same file, so any route-shape drift fails all three
// suites until the clients are updated.
func TestGoSDKCallToolContractFixture(t *testing.T) {
	fx := loadContractFixture(t)
	fake := &sdkToolServer{out: ""}
	web.SetToolServerFactory(func(string) web.ToolServer { return fake })

	root := sdkFixture(t)
	app, err := web.New(root)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	srv := httptest.NewServer(app)
	defer srv.Close()

	c := New(srv.URL, nil)
	ctx := context.Background()

	for _, tc := range fx.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			// Program the fake for this case class with the sentinel errors
			// the governed path classifies via errors.Is.
			fake.out = ""
			fake.err = nil
			switch tc.Response.Status {
			case http.StatusOK:
				fake.out = tc.ExpectOutput
			case http.StatusForbidden:
				fake.err = domain.ErrToolDenied
			case http.StatusNotFound:
				fake.err = domain.ErrToolUnknown
			case http.StatusInternalServerError:
				fake.err = errors.New("generic tool failure")
			default:
				t.Fatalf("fixture case %q: unhandled status %d", tc.ID, tc.Response.Status)
			}

			out, err := c.CallTool(ctx, tc.Name, tc.Args)

			// Request shape: the exact name and args must reach the route.
			if fake.lastName != tc.Name || !reflect.DeepEqual(fake.lastArgs, tc.Args) {
				t.Errorf("route received (%q, %v), want (%q, %v)", fake.lastName, fake.lastArgs, tc.Name, tc.Args)
			}

			if tc.Response.Status == http.StatusOK {
				if err != nil {
					t.Fatalf("CallTool(%s) = err %v, want output %q", tc.Name, err, tc.ExpectOutput)
				}
				if out != tc.ExpectOutput {
					t.Errorf("CallTool(%s) output = %q, want %q", tc.Name, out, tc.ExpectOutput)
				}
				return
			}
			var apiErr *Err
			if !errors.As(err, &apiErr) || apiErr.Status != tc.Response.Status {
				t.Fatalf("CallTool(%s) error = %v, want *Err with status %d", tc.Name, err, tc.Response.Status)
			}
		})
	}
}
