package enterprise

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/architecture"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

const testToken = "test-enterprise-token"

// authedRequest builds a request with the enterprise bearer token set, mirroring
// how a real client authenticates against the server.
func authedRequest(t *testing.T, method, target string) *http.Request {
	t.Helper()
	t.Setenv("KERN_AUTH_TOKEN", testToken)
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	return req
}

// mustNew builds an enterprise server, failing the test on a startup refusal
// (org mode without the KERN_RBAC_DEFAULT_DENY pairing is refused — finding
// 2). Tests exercising org mode set that env themselves.
func mustNew(t *testing.T) *Server {
	t.Helper()
	s, err := New()
	if err != nil {
		t.Fatalf("enterprise.New: %v", err)
	}
	return s
}

// stubApp is a minimal ProjectApp implementation for tests: web.New is far
// too heavy (full repo index + relay + MCP server) for unit tests of the
// enterprise server. Calls are recorded so tests can assert delegation; the
// default ArchitectureReport is an empty passing report (never nil — the
// org aggregation endpoint dereferences it). GET /<project>/api/governance
// renders the policies most recently pushed via SetPolicies, so the policy
// propagation tests can assert what the built firewall enforces.
type stubApp struct {
	closed     bool
	policies   []domain.Policy
	roleLookup func(id string) (string, bool)
	tasks      []*agent.Task
	arch       *architecture.Report
	archErr    error
	serveCalls int
}

func (a *stubApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.serveCalls++
	if strings.HasSuffix(r.URL.Path, "/api/governance") {
		type govPolicyJSON struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Scope       string `json:"scope"`
		}
		policies := make([]govPolicyJSON, 0, len(a.policies))
		for _, p := range a.policies {
			policies = append(policies, govPolicyJSON{ID: p.ID, Name: p.Name, Description: p.Description, Scope: p.Scope})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"policies": policies})
		return
	}
	w.WriteHeader(http.StatusOK)
}
func (a *stubApp) SetUserRoleLookup(lookup func(id string) (string, bool)) {
	a.roleLookup = lookup
}
func (a *stubApp) SetPolicies(policies []domain.Policy) { a.policies = policies }
func (a *stubApp) Close() error {
	a.closed = true
	return nil
}
func (a *stubApp) ListTasks() []*agent.Task { return a.tasks }
func (a *stubApp) ArchitectureReport() (*architecture.Report, error) {
	if a.arch != nil || a.archErr != nil {
		return a.arch, a.archErr
	}
	return &architecture.Report{OK: true}, nil
}

// withStubFactory installs a factory returning a fresh stubApp per build so
// tests can drive appFor/eviction/LRU paths without a real web.App. The
// factory records nothing itself; the built apps are reachable via
// appForCached.
func withStubFactory(s *Server) *Server {
	s.SetAppFactory(func(root string) (ProjectApp, error) { return &stubApp{}, nil })
	return s
}

func TestRegisterAndProjects(t *testing.T) {
	s := mustNew(t)
	if err := s.Register("proj-a", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.Register("proj-b", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	projects := s.Projects()
	if len(projects) != 2 {
		t.Fatalf("Projects() = %d, want 2", len(projects))
	}
	if projects[0].Name != "proj-a" || projects[1].Name != "proj-b" {
		t.Errorf("Projects not sorted: %v", projects)
	}
}

func TestRegisterDuplicate(t *testing.T) {
	s := mustNew(t)
	_ = s.Register("p", t.TempDir())
	if err := s.Register("p", t.TempDir()); err == nil {
		t.Error("expected error for duplicate registration")
	}
}

func TestUnregister(t *testing.T) {
	s := mustNew(t)
	_ = s.Register("p", t.TempDir())
	if err := s.Unregister("p"); err != nil {
		t.Fatal(err)
	}
	if len(s.Projects()) != 0 {
		t.Error("expected 0 projects after unregister")
	}
}

func TestServeHTTPOrgDashboard(t *testing.T) {
	s := mustNew(t)
	_ = s.Register("proj-a", t.TempDir())
	req := authedRequest(t, "GET", "/")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "proj-a") {
		t.Error("dashboard should list proj-a")
	}
	if strings.Contains(rr.Body.String(), "TempDir") || strings.Contains(rr.Body.String(), "proj-a</code>") {
		t.Error("dashboard must not expose the project root path")
	}
}

func TestServeHTTPOrgProjectsAPI(t *testing.T) {
	s := mustNew(t)
	_ = s.Register("proj-a", t.TempDir())
	req := authedRequest(t, "GET", "/org/projects")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "proj-a") {
		t.Error("projects API should include proj-a")
	}
	if strings.Contains(rr.Body.String(), "/var/") || strings.Contains(rr.Body.String(), "TempDir") {
		t.Error("projects API must not expose absolute root paths")
	}
}

func TestServeHTTPOrgAudit(t *testing.T) {
	s := mustNew(t)
	s.OrgAudit().Record(governance.AuditEntry{ID: "a1", AgentID: "agent-1", Action: "test"})
	req := authedRequest(t, "GET", "/org/audit")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "a1") {
		t.Error("audit API should include entry a1")
	}
}

func TestServeHTTPRequiresAuth(t *testing.T) {
	t.Run("missing token fails closed", func(t *testing.T) {
		s := mustNew(t)
		t.Setenv("KERN_AUTH_TOKEN", "")
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want %d (fail-closed)", rr.Code, http.StatusServiceUnavailable)
		}
	})
	t.Run("missing header rejected", func(t *testing.T) {
		s := mustNew(t)
		t.Setenv("KERN_AUTH_TOKEN", testToken)
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})
	t.Run("wrong token rejected", func(t *testing.T) {
		s := mustNew(t)
		t.Setenv("KERN_AUTH_TOKEN", testToken)
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})
}

func TestServeHTTPOrgMemory(t *testing.T) {
	s := mustNew(t)
	// Add an org-level memory.
	_, err := s.OrgMemory().Add(domain.Memory{
		Type:    domain.MemoryLesson,
		Content: "always check nil before dereferencing",
		Scope:   "service:checkout",
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("GET lists memories", func(t *testing.T) {
		req := authedRequest(t, "GET", "/org/memory")
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Errorf("status = %d, want 200", rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "nil before dereferencing") {
			t.Error("memory list should contain the added memory")
		}
		if !strings.Contains(body, `"count"`) {
			t.Error("memory response should contain count field")
		}
	})

	t.Run("POST adds a memory", func(t *testing.T) {
		t.Setenv("KERN_AUTH_TOKEN", testToken)
		body := `{"type":"lesson","content":"use context.WithTimeout for external calls","scope":"service:orders"}`
		req := httptest.NewRequest("POST", "/org/memory", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+testToken)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Errorf("status = %d, want 200", rr.Code)
		}
		// Verify it's retrievable.
		req2 := authedRequest(t, "GET", "/org/memory")
		rr2 := httptest.NewRecorder()
		s.ServeHTTP(rr2, req2)
		if !strings.Contains(rr2.Body.String(), "context.WithTimeout") {
			t.Error("added memory should be visible in GET /org/memory")
		}
	})
}

func TestServeHTTPOrgTasks(t *testing.T) {
	s := mustNew(t)
	// No projects built → empty task list.
	req := authedRequest(t, "GET", "/org/tasks")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "\"total\":0") {
		t.Errorf("expected total=0 with no built projects; got: %s", body)
	}
}

func TestServeHTTPOrgSearch(t *testing.T) {
	s := mustNew(t)

	t.Run("missing query returns 400", func(t *testing.T) {
		req := authedRequest(t, "GET", "/org/search")
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != 400 {
			t.Errorf("status = %d, want 400", rr.Code)
		}
	})

	t.Run("with query returns 200", func(t *testing.T) {
		req := authedRequest(t, "GET", "/org/search?q=NewServer")
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Errorf("status = %d, want 200", rr.Code)
		}
		// The response should contain a hits array (may be empty if no repos registered).
		if !strings.Contains(rr.Body.String(), "\"hits\"") {
			t.Error("search response should contain 'hits' field")
		}
	})
}

func TestServeHTTPOrgAgents(t *testing.T) {
	s := mustNew(t)

	// Register an org-level agent.
	err := s.RegisterAgent(governance.NewAgent("agent-coder-1", "Coder Agent", "coder", []governance.Permission{
		{Resource: "code", Action: "write"},
	}))
	if err != nil {
		t.Fatal(err)
	}

	req := authedRequest(t, "GET", "/org/agents")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "agent-coder-1") {
		t.Error("agents response should include agent-coder-1")
	}
	if !strings.Contains(body, `"count"`) {
		t.Error("agents response should contain count field")
	}
}

func TestRegisterAgentDuplicate(t *testing.T) {
	s := mustNew(t)
	agent1 := governance.NewAgent("agent-1", "Agent One", "coder", nil)
	if err := s.RegisterAgent(agent1); err != nil {
		t.Fatal(err)
	}
	agent2 := governance.NewAgent("agent-1", "Agent Two", "reviewer", nil)
	if err := s.RegisterAgent(agent2); err == nil {
		t.Error("expected error for duplicate agent registration")
	}
}

func TestOrgDashboardLinksNewEndpoints(t *testing.T) {
	s := mustNew(t)
	_ = s.Register("proj-a", t.TempDir())
	req := authedRequest(t, "GET", "/")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	body := rr.Body.String()
	for _, link := range []string{"/org/memory", "/org/tasks", "/org/search", "/org/agents"} {
		if !strings.Contains(body, link) {
			t.Errorf("dashboard should link to %s", link)
		}
	}
}

// TestServeHTTPOrgRepositories verifies the "repository" org route
// lists the registered repositories (projects) under the canonical name.
func TestServeHTTPOrgRepositories(t *testing.T) {
	s := mustNew(t)
	if err := s.Register("proj-a", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.Register("proj-b", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	req := authedRequest(t, "GET", "/org/repository")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Repositories []struct {
			Name string `json:"name"`
		} `json:"repositories"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 2 {
		t.Errorf("count = %d, want 2", body.Count)
	}
	names := map[string]bool{}
	for _, r := range body.Repositories {
		names[r.Name] = true
	}
	if !names["proj-a"] || !names["proj-b"] {
		t.Errorf("repositories = %v, want both proj-a and proj-b", body.Repositories)
	}
}

// TestServeHTTPOrgArchitecture verifies the "architecture" org-level route
// (B4): a cold org answers immediately from cached apps with the uncached
// projects reported as "pending" (no serial rebuilds inside the request),
// and the background warm fills the cache so the next request aggregates.
func TestServeHTTPOrgArchitecture(t *testing.T) {
	s := withStubFactory(mustNew(t))
	if err := s.Register("proj-a", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	type archBody struct {
		Architecture []struct {
			Project    string   `json:"project"`
			Violations []string `json:"violations"`
			OK         bool     `json:"ok"`
		} `json:"architecture"`
		Count   int `json:"count"`
		Pending int `json:"pending"`
	}

	// Cold start: nothing cached yet → pending, no serial build in the request.
	req := authedRequest(t, "GET", "/org/architecture")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var body archBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 0 || body.Pending != 1 {
		t.Fatalf("cold start = count %d pending %d, want 0/1 (no serial builds)", body.Count, body.Pending)
	}

	// The async warm must fill the cache shortly after.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, ok := s.appForCached("proj-a"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("async warm never built proj-a")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Second request aggregates the warmed project.
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, authedRequest(t, "GET", "/org/architecture"))
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count < 1 || body.Pending != 0 {
		t.Fatalf("after warm = count %d pending %d, want count>=1 pending 0", body.Count, body.Pending)
	}
	if len(body.Architecture) == 0 || body.Architecture[0].Project != "proj-a" {
		t.Errorf("architecture aggregation missing project name: %+v", body.Architecture)
	}
}

// TestAppForOffLockSingleFlight pins B4: concurrent appFor calls on a fresh
// no duplicate factory build and no deadlock with
// the org-wide mutex released during the build.
func TestAppForOffLockSingleFlight(t *testing.T) {
	s := withStubFactory(mustNew(t))
	if err := s.Register("proj-a", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	const callers = 8
	results := make(chan ProjectApp, callers)
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() {
			app, err := s.appFor("proj-a")
			errs <- err
			results <- app
		}()
	}
	for i := 0; i < callers; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("appFor: %v", err)
		}
	}
	first := <-results
	for i := 1; i < callers; i++ {
		if got := <-results; got != first {
			t.Fatalf("concurrent appFor returned different apps (call %d)", i)
		}
	}
}

// TestAppForCachedDoesNotBuild pins the cached-only accessor: it must never
// trigger a build, and must return the cached app pointer once built.
func TestAppForCachedDoesNotBuild(t *testing.T) {
	s := withStubFactory(mustNew(t))
	if err := s.Register("proj-a", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if app, ok := s.appForCached("proj-a"); ok || app != nil {
		t.Fatalf("appForCached on cold project = (%v, %v), want (nil, false)", app, ok)
	}
	built, err := s.appFor("proj-a")
	if err != nil {
		t.Fatalf("appFor: %v", err)
	}
	cached, ok := s.appForCached("proj-a")
	if !ok || cached != built {
		t.Fatalf("appForCached after build = (%v, %v), want the built app", cached, ok)
	}
}

func TestProjectMemoryIsolation(t *testing.T) {
	s := mustNew(t)
	if err := s.Register("proj-a", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.Register("proj-b", t.TempDir()); err != nil {
		t.Fatal(err)
	}

	// Write a lesson to proj-a's per-project store.
	a := s.projectMemory("proj-a")
	if a == nil {
		t.Fatal("projectMemory(proj-a) = nil, want a store")
	}
	lesson := "payments service: always verify idempotency keys"
	if _, err := a.Add(domain.Memory{
		Type:    domain.MemoryLesson,
		Content: lesson,
		Scope:   "service:payments",
	}); err != nil {
		t.Fatal(err)
	}

	// proj-a's per-project store should expose it...
	got, err := s.projectMemory("proj-a").List("")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range got {
		if m.Content == lesson {
			found = true
		}
	}
	if !found {
		t.Error("proj-a per-project memory should contain the written lesson")
	}

	// ...but proj-b's per-project store must NOT leak it.
	gotB, err := s.projectMemory("proj-b").List("")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range gotB {
		if m.Content == lesson {
			t.Error("proj-b per-project memory leaked a lesson written to proj-a")
		}
	}

	// Unknown project returns nil.
	if s.projectMemory("nope") != nil {
		t.Error("projectMemory for unregistered project should be nil")
	}
}

func TestServeOrgMemoryProjectParam(t *testing.T) {
	s := mustNew(t)
	_ = s.Register("proj-a", t.TempDir())

	t.Run("unknown project returns 404", func(t *testing.T) {
		req := authedRequest(t, "GET", "/org/memory?project=nope")
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rr.Code)
		}
	})

	t.Run("known project returns its per-project store", func(t *testing.T) {
		// Write a project-scoped memory directly.
		_, err := s.projectMemory("proj-a").Add(domain.Memory{
			Type:    domain.MemoryLesson,
			Content: "project-only lesson",
			Scope:   "service:checkout",
		})
		if err != nil {
			t.Fatal(err)
		}
		req := authedRequest(t, "GET", "/org/memory?project=proj-a")
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "project-only lesson") {
			t.Error("per-project GET should include the project-scoped memory")
		}
	})
}

func TestAppEviction(t *testing.T) {
	t.Setenv("KERN_ENTERPRISE_MAX_PROJECTS", "2")
	s := withStubFactory(mustNew(t))
	for _, name := range []string{"p1", "p2", "p3"} {
		if err := s.Register(name, t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	// Build p1, then p2, then p3. With a cap of 2, building p3 evicts the
	// least-recently-used cached app (p1).
	for _, name := range []string{"p1", "p2", "p3"} {
		if _, err := s.appFor(name); err != nil {
			t.Fatalf("appFor(%s): %v", name, err)
		}
	}

	if s.cachedCount() > 2 {
		t.Errorf("cachedCount() = %d, want <= 2", s.cachedCount())
	}
	// Inspect the internal cache state (without rebuilding) to confirm eviction.
	s.mu.RLock()
	evicted := s.projects["p1"].app == nil
	keptP2 := s.projects["p2"].app != nil
	keptP3 := s.projects["p3"].app != nil
	s.mu.RUnlock()
	if !evicted {
		t.Error("oldest cached app (p1) should have been evicted")
	}
	if !keptP2 || !keptP3 {
		t.Error("recently used apps (p2, p3) should still be cached")
	}

	// Eviction drops the cached app but keeps the project registered; the app
	// rebuilds on next access and the per-project memory store is retained.
	if app, _ := s.appFor("p1"); app == nil {
		t.Error("evicted app should rebuild on next access")
	}
	if s.projectMemory("p1") == nil {
		t.Error("evicted project should retain its per-project memory store")
	}
}

func TestServeOrgAgentsPostRegister(t *testing.T) {
	s := mustNew(t)

	// Register via POST /org/agents (must create + return 201).
	body := `{"id":"agent-1","name":"Agent One","type":"coder","permissions":[{"resource":"source","action":"read"}]}`
	req := authedRequest(t, "POST", "/org/agents")
	req.Body = io.NopCloser(strings.NewReader(body))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST /org/agents code = %d, want 201 (body: %s)", rr.Code, rr.Body.String())
	}

	// It must now appear in GET /org/agents.
	greq := authedRequest(t, "GET", "/org/agents")
	grr := httptest.NewRecorder()
	s.ServeHTTP(grr, greq)
	var got struct {
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(grr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET /org/agents: %v", err)
	}
	if got.Count != 1 || len(got.Agents) != 1 || got.Agents[0].ID != "agent-1" {
		t.Fatalf("after POST, agents = %+v, want 1 agent with ID agent-1", got)
	}
	// The strict wire shape never carries permissions at all (accepted in
	// the body but stripped before registering — see TestServeOrgAgentsPostStrictBody
	// for the accepted-and-stripped assertion on the POST echo).
	if strings.Contains(grr.Body.String(), `"permissions":[{`) {
		t.Errorf("registered agent retained client-supplied permissions: %s", grr.Body.String())
	}

	// Duplicate POST → 409.
	dup := authedRequest(t, "POST", "/org/agents")
	dup.Body = io.NopCloser(strings.NewReader(body))
	drr := httptest.NewRecorder()
	s.ServeHTTP(drr, dup)
	if drr.Code != http.StatusConflict {
		t.Fatalf("duplicate POST /org/agents code = %d, want 409", drr.Code)
	}

	// Bad method → 405.
	del := authedRequest(t, "DELETE", "/org/agents")
	dlr := httptest.NewRecorder()
	s.ServeHTTP(dlr, del)
	if dlr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /org/agents code = %d, want 405", dlr.Code)
	}

	// Missing id → 400.
	noid := authedRequest(t, "POST", "/org/agents")
	noid.Body = io.NopCloser(strings.NewReader(`{"name":"No ID"}`))
	nrr := httptest.NewRecorder()
	s.ServeHTTP(nrr, noid)
	if nrr.Code != http.StatusBadRequest {
		t.Fatalf("POST /org/agents without id code = %d, want 400", nrr.Code)
	}
}

// TestEvictionClosesApp pins the app-teardown contract: evicting a cached
// ProjectApp must invoke the close hook (ProjectApp.Close by default) so the
// relay/bus subscriptions New() started do not leak goroutines or sockets
// across evictions — and a Close error must never fail the eviction itself.
func TestEvictionClosesApp(t *testing.T) {
	t.Setenv("KERN_ENTERPRISE_MAX_PROJECTS", "2")
	s := withStubFactory(mustNew(t))
	for _, name := range []string{"p1", "p2", "p3"} {
		if err := s.Register(name, t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	var closed []string
	s.closeApp = func(a ProjectApp) error {
		closed = append(closed, "closed")
		return nil
	}
	// Building p3 with a cap of 2 evicts the LRU cached app (p1).
	for _, name := range []string{"p1", "p2", "p3"} {
		if _, err := s.appFor(name); err != nil {
			t.Fatalf("appFor(%s): %v", name, err)
		}
	}
	if len(closed) == 0 {
		t.Fatal("eviction did not call the app close hook")
	}
	if len(closed) > 1 {
		t.Errorf("expected exactly one eviction close, got %d", len(closed))
	}
}

// TestEvictionSurvivesCloseError pins the best-effort teardown contract: a
// Close error is logged and the eviction still completes (the app slot is
// freed and the project stays registered/rebuildable).
func TestEvictionSurvivesCloseError(t *testing.T) {
	t.Setenv("KERN_ENTERPRISE_MAX_PROJECTS", "2")
	s := withStubFactory(mustNew(t))
	for _, name := range []string{"a1", "a2", "a3"} {
		if err := s.Register(name, t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	s.closeApp = func(ProjectApp) error { return fmt.Errorf("teardown boom") }
	for _, name := range []string{"a1", "a2", "a3"} {
		if _, err := s.appFor(name); err != nil {
			t.Fatalf("appFor(%s): %v", name, err)
		}
	}
	s.mu.RLock()
	evicted := s.projects["a1"].app == nil
	s.mu.RUnlock()
	if !evicted {
		t.Error("eviction must complete even when the app close hook errors")
	}
	// The evicted project remains registered and rebuilds on next access.
	if app, _ := s.appFor("a1"); app == nil {
		t.Error("project with a failed app close should still rebuild on next access")
	}
}

// TestServeOrgAgentsPostStrictBody pins the F-OR2 fix: POST /org/agents must
// stamp CreatedAt server-side (never echo a zero value), reject unknown
// fields by name instead of silently dropping them (e.g. a client-submitted
// "roles"), and keep the accepted-and-stripped permissions posture.
func TestServeOrgAgentsPostStrictBody(t *testing.T) {
	s := mustNew(t)

	post := func(body string) *httptest.ResponseRecorder {
		req := authedRequest(t, "POST", "/org/agents")
		req.Body = io.NopCloser(strings.NewReader(body))
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		return rr
	}

	// Unknown field ("roles") is rejected by name, not silently dropped
	// behind a 201 that looks like it registered something.
	rr := post(`{"id":"agent-x","name":"X","roles":["admin"]}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field POST code = %d, want 400 (body: %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"roles"`) && !strings.Contains(rr.Body.String(), "roles") {
		t.Errorf("400 should name the unknown field, got: %s", rr.Body.String())
	}

	// Valid registration: CreatedAt is stamped server-side, permissions
	// accepted-but-stripped, and the echoed record is complete.
	ok := post(`{"id":"agent-9","name":"Agent Nine","type":"coder","permissions":[{"resource":"source","action":"read"}]}`)
	if ok.Code != http.StatusCreated {
		t.Fatalf("valid POST code = %d, want 201 (body: %s)", ok.Code, ok.Body.String())
	}
	var created struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		Type      string    `json:"type"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.Unmarshal(ok.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created agent: %v; raw=%s", err, ok.Body.String())
	}
	if created.ID != "agent-9" || created.Name != "Agent Nine" || created.Type != "coder" {
		t.Errorf("echoed agent = %+v, want the submitted id/name/type", created)
	}
	if created.CreatedAt.IsZero() {
		t.Error("echoed CreatedAt is the zero value — registration must stamp it server-side")
	}
	if strings.Contains(ok.Body.String(), "source") || strings.Contains(ok.Body.String(), "Permissions\":[{") {
		t.Errorf("echoed agent retained client-supplied permissions: %s", ok.Body.String())
	}

	// A duplicate still 409s (the strict decode must not change the
	// duplicate contract).
	dup := post(`{"id":"agent-9","name":"Agent Nine again"}`)
	if dup.Code != http.StatusConflict {
		t.Fatalf("duplicate POST code = %d, want 409", dup.Code)
	}
}
