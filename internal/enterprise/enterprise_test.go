package enterprise

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
