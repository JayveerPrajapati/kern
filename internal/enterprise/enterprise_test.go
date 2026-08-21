package enterprise

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/governance/identity"
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

func TestRegisterAndProjects(t *testing.T) {
	s := New()
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
	s := New()
	s.Register("p", t.TempDir())
	if err := s.Register("p", t.TempDir()); err == nil {
		t.Error("expected error for duplicate registration")
	}
}

func TestUnregister(t *testing.T) {
	s := New()
	s.Register("p", t.TempDir())
	if err := s.Unregister("p"); err != nil {
		t.Fatal(err)
	}
	if len(s.Projects()) != 0 {
		t.Error("expected 0 projects after unregister")
	}
}

func TestServeHTTPOrgDashboard(t *testing.T) {
	s := New()
	s.Register("proj-a", t.TempDir())
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
	s := New()
	s.Register("proj-a", t.TempDir())
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
	s := New()
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
		s := New()
		t.Setenv("KERN_AUTH_TOKEN", "")
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want %d (fail-closed)", rr.Code, http.StatusServiceUnavailable)
		}
	})
	t.Run("missing header rejected", func(t *testing.T) {
		s := New()
		t.Setenv("KERN_AUTH_TOKEN", testToken)
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})
	t.Run("wrong token rejected", func(t *testing.T) {
		s := New()
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
	s := New()
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
	s := New()
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
	s := New()

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
	s := New()

	// Register an org-level agent.
	err := s.RegisterAgent(identity.NewAgent("agent-coder-1", "Coder Agent", "coder", []identity.Permission{
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
	s := New()
	agent1 := identity.NewAgent("agent-1", "Agent One", "coder", nil)
	if err := s.RegisterAgent(agent1); err != nil {
		t.Fatal(err)
	}
	agent2 := identity.NewAgent("agent-1", "Agent Two", "reviewer", nil)
	if err := s.RegisterAgent(agent2); err == nil {
		t.Error("expected error for duplicate agent registration")
	}
}

func TestOrgDashboardLinksNewEndpoints(t *testing.T) {
	s := New()
	s.Register("proj-a", t.TempDir())
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

func TestProjectMemoryIsolation(t *testing.T) {
	s := New()
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
	s := New()
	s.Register("proj-a", t.TempDir())

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
	s := New()
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
