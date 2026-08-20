// Package enterprise implements multi-project enterprise mode for kern-server
// (spec §30): shared org-level policies and audit log, plus per-project
// digital-twin state (index, graph, memories, incidents) served from a single
// HTTP listener. It is additive and opt-in; the single-project web.App is
// unchanged.
package enterprise

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
	"github.com/JayveerPrajapati/kern/internal/web"
)

// Project is a registered project in enterprise mode.
type Project struct {
	Name string // human-friendly project name (unique within the org)
	Root string // absolute path to the project root
}

// Server is the multi-project enterprise server. It wraps multiple
// web.App instances (one per project) under a shared org-level audit
// log and policy set.
type Server struct {
	mu       sync.RWMutex
	projects map[string]*projectState // keyed by project name
	orgAudit *governance.AuditLog     // shared org-level audit
	orgBus   *eventbus.Bus            // shared org-level event bus
	store    storage.Store            // optional shared storage (nil = in-memory)
	policies []domain.Policy          // org-level policies applied to all projects
}

type projectState struct {
	project Project
	app     *web.App // lazily built on first access
	appErr  error    // build error (cached)
}

// New creates an enterprise server with no projects. Use Register to add
// projects and WithOrgAudit/WithOrgBus/WithPolicies to configure org-level
// shared state.
func New() *Server {
	return &Server{
		projects: map[string]*projectState{},
		orgAudit: governance.NewAuditLog(),
		orgBus:   eventbus.New(),
		policies: governance.DefaultPolicies(),
	}
}

// WithOrgAudit sets a custom org-level audit log (e.g. one backed by
// storage.Store via governance.AuditLog.WithStore). Default is in-memory.
func (s *Server) WithOrgAudit(a *governance.AuditLog) *Server {
	s.orgAudit = a
	return s
}

// WithOrgBus sets a custom org-level event bus. Default is a new bus.
func (s *Server) WithOrgBus(b *eventbus.Bus) *Server {
	s.orgBus = b
	return s
}

// WithPolicies sets org-level risk policies applied to all projects.
// Default is governance.DefaultPolicies().
func (s *Server) WithPolicies(p []domain.Policy) *Server {
	s.policies = p
	return s
}

// WithStore sets a shared storage backend for org-level persistence.
// When set, the org audit log can be persisted via WithStore on AuditLog.
func (s *Server) WithStore(store storage.Store) *Server {
	s.store = store
	return s
}

// Register adds a project to the enterprise server. The project's web.App
// is built lazily on first request. Returns an error if the name is
// already registered or the root is invalid.
func (s *Server) Register(name, root string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.projects[name]; exists {
		return fmt.Errorf("enterprise: project %q already registered", name)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("enterprise: invalid root %q: %w", root, err)
	}
	s.projects[name] = &projectState{
		project: Project{Name: name, Root: absRoot},
	}
	return nil
}

// Unregister removes a project from the enterprise server.
func (s *Server) Unregister(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.projects[name]; !exists {
		return fmt.Errorf("enterprise: project %q not registered", name)
	}
	delete(s.projects, name)
	return nil
}

// Projects returns all registered projects, sorted by name.
func (s *Server) Projects() []Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.projects))
	for n := range s.projects {
		names = append(names, n)
	}
	sort.Strings(names)
	result := make([]Project, 0, len(names))
	for _, n := range names {
		result = append(result, s.projects[n].project)
	}
	return result
}

// appFor returns the web.App for a project, building it lazily on first
// access. The build error is cached so repeated requests don't retry.
func (s *Server) appFor(name string) (*web.App, error) {
	s.mu.RLock()
	ps, exists := s.projects[name]
	s.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("enterprise: project %q not registered", name)
	}
	if ps.app != nil || ps.appErr != nil {
		return ps.app, ps.appErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Double-check after acquiring write lock
	if ps.app != nil || ps.appErr != nil {
		return ps.app, ps.appErr
	}
	app, err := web.New(ps.project.Root)
	ps.app = app
	ps.appErr = err
	return app, err
}

// OrgAudit returns the shared org-level audit log.
func (s *Server) OrgAudit() *governance.AuditLog { return s.orgAudit }

// OrgBus returns the shared org-level event bus.
func (s *Server) OrgBus() *eventbus.Bus { return s.orgBus }

// Store returns the shared org-level storage backend (nil if unset).
func (s *Server) Store() storage.Store { return s.store }

// authTokenEnv is the environment variable holding the enterprise server's
// shared bearer token. It must be set before the server is started.
const authTokenEnv = "KERN_AUTH_TOKEN"

// requireAuth enforces token-based authentication for every enterprise
// request. It is fail-closed:
//   - if KERN_AUTH_TOKEN is unset the server refuses to serve (503), because in
//     enterprise mode even a single unauthenticated request leaks the full
//     digital twin of every project plus the shared org audit log and policies;
//   - any request without a matching "Authorization: Bearer <token>" header is
//     rejected with 401 Unauthorized.
//
// It writes the error response and returns false when the request is not
// authorized, so callers should return immediately.
func (s *Server) requireAuth(w http.ResponseWriter, r *http.Request) bool {
	token := os.Getenv(authTokenEnv)
	if token == "" {
		http.Error(w, authTokenEnv+" must be set for enterprise mode", http.StatusServiceUnavailable)
		return false
	}
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) || strings.TrimSpace(strings.TrimPrefix(h, prefix)) != token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// ServeHTTP routes requests by project: /<project>/... delegates to the
// project's web.App; /org/... serves org-level endpoints (audit, projects,
// policies); / serves an org-level dashboard (list of projects).
//
// Every request is gated by requireAuth: in enterprise mode the server serves
// each project's full digital twin plus the shared org audit log and policies,
// so nothing is exposed without a valid bearer token (fail-closed).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuth(w, r) {
		return
	}
	path := strings.Trim(r.URL.Path, "/")
	if path == "" {
		s.serveOrgDashboard(w, r)
		return
	}
	parts := strings.SplitN(path, "/", 2)
	projectName := parts[0]
	if projectName == "org" {
		s.serveOrgAPI(w, r)
		return
	}
	app, err := s.appFor(projectName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	// Rewrite the URL to strip the project prefix, then delegate
	if len(parts) > 1 {
		r.URL.Path = "/" + parts[1]
	} else {
		r.URL.Path = "/"
	}
	app.ServeHTTP(w, r)
}

// serveOrgDashboard serves a simple HTML listing of all projects.
func (s *Server) serveOrgDashboard(w http.ResponseWriter, r *http.Request) {
	projects := s.Projects()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!DOCTYPE html><html><head><title>Kern Enterprise</title></head><body>")
	fmt.Fprintf(w, "<h1>Kern Enterprise</h1>")
	fmt.Fprintf(w, "<h2>Projects (%d)</h2><ul>", len(projects))
	for _, p := range projects {
		fmt.Fprintf(w, `<li><a href="/%s/">%s</a></li>`, p.Name, p.Name)
	}
	fmt.Fprintf(w, "</ul>")
	fmt.Fprintf(w, `<p><a href="/org/audit">Org Audit</a> | <a href="/org/policies">Org Policies</a></p>`)
	fmt.Fprintf(w, "</body></html>")
}

// serveOrgAPI serves org-level API endpoints.
func (s *Server) serveOrgAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/org")
	path = strings.Trim(path, "/")
	switch path {
	case "audit":
		s.serveOrgAudit(w, r)
	case "policies":
		s.serveOrgPolicies(w, r)
	case "projects":
		s.serveOrgProjects(w, r)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) serveOrgAudit(w http.ResponseWriter, r *http.Request) {
	entries := s.orgAudit.All()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"entries": entries,
		"count":   len(entries),
	})
}

func (s *Server) serveOrgPolicies(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"policies": s.policies,
		"count":    len(s.policies),
	})
}

func (s *Server) serveOrgProjects(w http.ResponseWriter, r *http.Request) {
	// Omit Root: never expose absolute filesystem paths to clients. Only the
	// human-friendly project names are served.
	type projectInfo struct {
		Name string `json:"name"`
	}
	projects := s.Projects()
	info := make([]projectInfo, 0, len(projects))
	for _, p := range projects {
		info = append(info, projectInfo{Name: p.Name})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"projects": info,
		"count":    len(info),
	})
}
