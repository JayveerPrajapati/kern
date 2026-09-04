// Package enterprise implements multi-project enterprise mode for kern-server
// Shared org-level policies and audit log, plus per-project
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/memory"
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
// log, policy set, memory store, task visibility, and agent registry.
type Server struct {
	mu           sync.RWMutex
	projects     map[string]*projectState             // keyed by project name
	orgAudit     *governance.AuditLog                 // shared org-level audit
	orgBus       *eventbus.Bus                        // shared org-level event bus
	store        storage.Store                        // optional shared storage (nil = in-memory)
	policies     []domain.Policy                      // org-level policies applied to all projects
	orgMemory    *memory.MemoryStore                  // shared org-level memory
	orgAgents    map[string]*governance.AgentIdentity // shared org-level agent registry
	teamRegistry map[string]*OrgTeam                  // org-level team registry
}

type projectState struct {
	project  Project
	app      *web.App // lazily built on first access
	appErr   error    // build error (cached)
	lastUsed time.Time
	memory   *memory.MemoryStore // per-project memory store (scoped to project root)
}

// New creates an enterprise server with no projects. Use Register to add
// projects and WithOrgAudit/WithOrgBus/WithPolicies to configure org-level
// shared state.
func New() *Server {
	return &Server{
		projects:     map[string]*projectState{},
		orgAudit:     governance.NewAuditLog(),
		orgBus:       eventbus.New(),
		policies:     governance.DefaultPolicies(),
		orgMemory:    memory.NewMemoryStore(""), // in-memory org-level store (no root)
		orgAgents:    map[string]*governance.AgentIdentity{},
		teamRegistry: map[string]*OrgTeam{},
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
// Cached apps are capped (see maxProjects): when the cache is full and a new
// project needs building, the least-recently-used cached app is evicted so it
// can be rebuilt on next access. This bounds memory growth for orgs with many
// registered projects.
func (s *Server) appFor(name string) (*web.App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, exists := s.projects[name]
	if !exists {
		return nil, fmt.Errorf("enterprise: project %q not registered", name)
	}
	// Touch recency on every access so LRU eviction reflects true usage.
	ps.lastUsed = time.Now()
	if ps.app != nil || ps.appErr != nil {
		return ps.app, ps.appErr
	}
	// Cache miss: if at the app cap, evict the LRU cached app to make room.
	if s.cachedCount() >= s.maxProjects() {
		s.evictLRU()
	}
	app, err := web.New(ps.project.Root)
	ps.app = app
	ps.appErr = err
	if err == nil {
		ps.memory = memory.NewMemoryStore(ps.project.Root)
	}
	return app, err
}

// defaultMaxProjects is the default cap on cached web.App instances. It bounds
// the number of lazily built apps an enterprise server holds in memory; when
// exceeded the least-recently-used cached app is evicted (and rebuilt on next
// access). Configurable via KERN_ENTERPRISE_MAX_PROJECTS.
const defaultMaxProjects = 16

// maxProjects returns the configured cap on cached web.App instances, read from
// KERN_ENTERPRISE_MAX_PROJECTS (default 16). Invalid or non-positive values fall
// back to the default.
func (s *Server) maxProjects() int {
	v := os.Getenv("KERN_ENTERPRISE_MAX_PROJECTS")
	if v == "" {
		return defaultMaxProjects
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return defaultMaxProjects
	}
	return n
}

// cachedCount returns how many projects currently hold a built web.App.
// Must be called with s.mu held.
func (s *Server) cachedCount() int {
	n := 0
	for _, ps := range s.projects {
		if ps.app != nil {
			n++
		}
	}
	return n
}

// evictLRU drops the cached web.App of the least-recently-used project that
// currently has one built. The projectState (and its per-project memory store)
// are retained so the app can be rebuilt on next access. Must be called with
// s.mu held.
func (s *Server) evictLRU() {
	var oldest *projectState
	var oldestT time.Time
	for _, ps := range s.projects {
		if ps.app == nil {
			continue
		}
		if oldest == nil || ps.lastUsed.Before(oldestT) {
			oldest = ps
			oldestT = ps.lastUsed
		}
	}
	if oldest == nil {
		return
	}
	oldest.app = nil
	oldest.appErr = nil
}

// projectMemory returns the per-project memory store for name, creating it
// lazily on first access. Returns nil if the project is not registered. The
// per-project store is scoped to the project's root, so lessons written to one
// project are not visible from another's per-project memory (cross-project
// lessons live in the org-level store instead).
func (s *Server) projectMemory(name string) *memory.MemoryStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, exists := s.projects[name]
	if !exists {
		return nil
	}
	if ps.memory != nil {
		return ps.memory
	}
	ps.memory = memory.NewMemoryStore(ps.project.Root)
	return ps.memory
}

// OrgAudit returns the shared org-level audit log.
func (s *Server) OrgAudit() *governance.AuditLog { return s.orgAudit }

// OrgBus returns the shared org-level event bus.
func (s *Server) OrgBus() *eventbus.Bus { return s.orgBus }

// Store returns the shared org-level storage backend (nil if unset).
func (s *Server) Store() storage.Store { return s.store }

// OrgMemory returns the shared org-level memory store. Memories
// written here are visible across all projects — e.g. a lesson learned in the
// payments service is recallable when working on the orders service.
func (s *Server) OrgMemory() *memory.MemoryStore { return s.orgMemory }

// RegisterAgent registers an agent identity at the org level. The
// agent's permissions apply across all projects. Returns an error if an agent
// with the same ID is already registered.
func (s *Server) RegisterAgent(a *governance.AgentIdentity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.orgAgents[a.ID]; exists {
		return fmt.Errorf("enterprise: agent %q already registered", a.ID)
	}
	s.orgAgents[a.ID] = a
	return nil
}

// Agents returns all registered org-level agent identities, sorted by ID.
func (s *Server) Agents() []*governance.AgentIdentity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.orgAgents))
	for id := range s.orgAgents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*governance.AgentIdentity, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.orgAgents[id])
	}
	return out
}

// OrgTasks aggregates tasks across all projects. Returns a map of
// project name → task list. Projects whose app hasn't been built yet are
// skipped (they have no tasks yet).
func (s *Server) OrgTasks() map[string][]map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string][]map[string]any{}
	for name, ps := range s.projects {
		if ps.app == nil {
			continue
		}
		// Access the task registry via the web App's public handler data.
		// We use the /v1/tasks endpoint's data shape for consistency.
		tasks := ps.app.ListTasks()
		for _, t := range tasks {
			out[name] = append(out[name], map[string]any{
				"id":     t.ID,
				"state":  string(t.State),
				"intent": t.Intent,
				"type":   t.Type,
			})
		}
	}
	return out
}

// OrgSearch performs cross-project symbol search. It delegates to
// intel.SearchRepos, which searches across all repos registered in the kern
// multi-repo registry. Returns nil when no repos are registered.
func (s *Server) OrgSearch(query string, limit int) []intel.RepoHit {
	if limit <= 0 {
		limit = 20
	}
	return intel.SearchRepos(query, limit)
}

// authTokenEnv is the environment variable holding the enterprise server's
// shared bearer token. It must be set before the server is started.
const authTokenEnv = "KERN_AUTH_TOKEN"

// requireAuth enforces token-based authentication for every enterprise
// request. It is fail-closed:
// - if KERN_AUTH_TOKEN is unset the server refuses to serve (503), because in
// enterprise mode even a single unauthenticated request leaks the full
// digital twin of every project plus the shared org audit log and policies;
// - any request without a matching "Authorization: Bearer <token>" header is
// rejected with 401 Unauthorized.
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
	fmt.Fprintf(w, `<p><a href="/org/audit">Org Audit</a> | <a href="/org/policies">Org Policies</a> | <a href="/org/memory">Org Memory</a> | <a href="/org/tasks">Org Tasks</a> | <a href="/org/search?q=New">Org Search</a> | <a href="/org/agents">Org Agents</a> | <a href="/org/teams">Org Teams</a></p>`)
	fmt.Fprintf(w, "</body></html>")
}

// serveOrgAPI serves org-level API endpoints.
func (s *Server) serveOrgAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/org")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	switch parts[0] {
	case "audit":
		s.serveOrgAudit(w, r)
	case "policies":
		s.serveOrgPolicies(w, r)
	case "projects":
		s.serveOrgProjects(w, r)
	case "memory":
		s.serveOrgMemory(w, r)
	case "tasks":
		s.serveOrgTasks(w, r)
	case "search":
		s.serveOrgSearch(w, r)
	case "repository":
		s.serveOrgRepositories(w, r)
	case "architecture":
		s.serveOrgArchitecture(w, r)
	case "agents":
		// /org/agents[/{id}/teams]
		if len(parts) == 3 && parts[2] == "teams" {
			s.serveOrgAgentTeams(w, r, parts[1])
			return
		}
		s.serveOrgAgents(w, r)
	case "teams":
		// /org/teams[/{id}]
		if len(parts) > 1 {
			s.serveOrgTeam(w, r, parts[1])
			return
		}
		s.serveOrgTeams(w, r)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) serveOrgAudit(w http.ResponseWriter, r *http.Request) {
	entries := s.orgAudit.All()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"entries": entries,
		"count":   len(entries),
	}); err != nil {
		http.Error(w, "could not encode response", http.StatusInternalServerError)
	}
}

func (s *Server) serveOrgPolicies(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"policies": s.policies,
		"count":    len(s.policies),
	}); err != nil {
		http.Error(w, "could not encode response", http.StatusInternalServerError)
	}
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
	if err := json.NewEncoder(w).Encode(map[string]any{
		"projects": info,
		"count":    len(info),
	}); err != nil {
		http.Error(w, "could not encode response", http.StatusInternalServerError)
	}
}

// serveOrgRepositories lists the repositories (projects) registered at the
// org level ( .3 "repository"). It mirrors the project list but is
// exposed under the canonical "repository" resource name the spec requires.
func (s *Server) serveOrgRepositories(w http.ResponseWriter, r *http.Request) {
	type repoInfo struct {
		Name string `json:"name"`
	}
	projects := s.Projects()
	info := make([]repoInfo, 0, len(projects))
	for _, p := range projects {
		info = append(info, repoInfo{Name: p.Name})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"repositories": info,
		"count":        len(info),
	}); err != nil {
		http.Error(w, "could not encode response", http.StatusInternalServerError)
	}
}

// serveOrgArchitecture aggregates the architecture report across every project
// ( .3 "architecture"). It is intentionally cheap: it returns the
// per-project root/name and a violations count by delegating to each project's
// cached web.App architecture builder, skipping projects that fail to build.
func (s *Server) serveOrgArchitecture(w http.ResponseWriter, r *http.Request) {
	type projectArch struct {
		Project    string   `json:"project"`
		Violations []string `json:"violations"`
		OK         bool     `json:"ok"`
	}
	out := make([]projectArch, 0, len(s.projects))
	for _, p := range s.projects {
		app, err := s.appFor(p.project.Name)
		if err != nil {
			continue
		}
		arch, aerr := app.ArchitectureReport()
		if aerr != nil {
			continue
		}
		viol := make([]string, 0, len(arch.Violations))
		for _, v := range arch.Violations {
			viol = append(viol, v.Symbol)
		}
		out = append(out, projectArch{Project: p.project.Name, Violations: viol, OK: arch.OK})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"architecture": out,
		"count":        len(out),
	}); err != nil {
		http.Error(w, "could not encode response", http.StatusInternalServerError)
	}
}

// serveOrgMemory serves the org-level memory store.
// - GET /org/memory lists org-level memories. With ?project=<name> it lists
// that project's per-project memory store instead (falling back to org
// memory is NOT done here — an unknown project is a 404, so clients can
// distinguish a missing project from an empty store). POST /org/memory
// always writes to the shared org-level store (cross-project lessons).
func (s *Server) serveOrgMemory(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		store := s.orgMemory
		if project := r.URL.Query().Get("project"); project != "" {
			ps := s.projectMemory(project)
			if ps == nil {
				http.Error(w, fmt.Sprintf("enterprise: project %q not registered", project), http.StatusNotFound)
				return
			}
			store = ps
		}
		memories, err := store.List("")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"memories": memories,
			"count":    len(memories),
		})
	case http.MethodPost:
		var m domain.Memory
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		saved, err := s.orgMemory.Add(m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(saved)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// serveOrgTasks serves aggregated task visibility across all projects
// Returns a map of project name → task list.
func (s *Server) serveOrgTasks(w http.ResponseWriter, r *http.Request) {
	tasks := s.OrgTasks()
	total := 0
	for _, list := range tasks {
		total += len(list)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"projects": tasks,
		"total":    total,
	})
}

// serveOrgSearch serves cross-project symbol search. It searches
// across all repos registered in the kern multi-repo registry.
func (s *Server) serveOrgSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		http.Error(w, "query parameter 'q' is required", http.StatusBadRequest)
		return
	}
	hits := s.OrgSearch(q, 20)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"hits":  hits,
		"count": len(hits),
	})
}

// serveOrgAgents serves the org-level agent registry.
// - GET  /org/agents returns all registered agent identities.
// - POST /org/agents registers a new agent from a JSON AgentIdentity body;
// 400 on a bad body, 409 on a duplicate ID, 201 with the created agent on
// success.
// - any other method → 405.
func (s *Server) serveOrgAgents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		agents := s.Agents()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"agents": agents,
			"count":  len(agents),
		})
	case http.MethodPost:
		var agent governance.AgentIdentity
		if err := json.NewDecoder(r.Body).Decode(&agent); err != nil {
			http.Error(w, "enterprise: invalid agent body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if agent.ID == "" {
			http.Error(w, "enterprise: agent id is required", http.StatusBadRequest)
			return
		}
		if err := s.RegisterAgent(&agent); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(agent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// serveOrgTeams serves the team registry.
// - GET /org/teams lists all teams as {"teams": [...], "count": N}.
// - POST /org/teams creates a team from a JSON OrgTeam body; 400 on a bad
// body, 409 on a duplicate/validation error, 201 with the created team on
// success.
func (s *Server) serveOrgTeams(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		teams := s.Teams()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"teams": teams,
			"count": len(teams),
		})
	case http.MethodPost:
		var team OrgTeam
		if err := json.NewDecoder(r.Body).Decode(&team); err != nil {
			http.Error(w, "enterprise: invalid team body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.CreateTeam(team); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(team)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// serveOrgTeam serves a single team by ID.
// - GET /org/teams/{id} returns one team (404 if unknown).
// - DELETE /org/teams/{id} removes it (404 if unknown, 204 on success).
func (s *Server) serveOrgTeam(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		team, ok := s.Team(id)
		if !ok {
			http.Error(w, fmt.Sprintf("enterprise: team %q not found", id), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(team)
	case http.MethodDelete:
		if err := s.RemoveTeam(id); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// serveOrgAgentTeams serves the teams a given agent belongs to
// ( .3, optional): GET /org/agents/{id}/teams. Returns 404 when the
// agent ID is unknown (fail-closed).
func (s *Server) serveOrgAgentTeams(w http.ResponseWriter, r *http.Request, agentID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	_, known := s.orgAgents[agentID]
	s.mu.RUnlock()
	if !known {
		http.Error(w, fmt.Sprintf("enterprise: agent %q not found", agentID), http.StatusNotFound)
		return
	}
	teams := s.AgentTeams(agentID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"agent": agentID,
		"teams": teams,
		"count": len(teams),
	})
}
