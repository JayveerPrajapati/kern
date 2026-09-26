// Package enterprise implements multi-project enterprise mode for kern-server
// Shared org-level policies and audit log, plus per-project
// digital-twin state (index, graph, memories, incidents) served from a single
// HTTP listener. It is additive and opt-in; the single-project web.App is
// unchanged.
package enterprise

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/orgapprovals"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// Project is a registered project in enterprise mode.
type Project struct {
	Name    string  // human-friendly project name (unique within the org)
	Root    string  // absolute path to the project root
	Profile Profile // effective enterprise profile (set at registration)
}

// Server is the multi-project enterprise server. It wraps multiple
// ProjectApp instances (one per project) under a shared org-level audit
// log, policy set, memory store, task visibility, and agent registry.
type Server struct {
	mu           sync.RWMutex
	projects     map[string]*projectState             // keyed by project name
	orgAudit     *governance.AuditLog                 // shared org-level audit
	orgBus       *eventbus.Bus                        // shared org-level event bus
	store        storage.Store                        // optional shared storage (nil = in-memory)
	policies     []domain.Policy                      // org-level policies applied to all projects
	orgRoot      string                               // org root (governance.OrgRoot); "" = no org configured
	orgMemory    *memory.MemoryStore                  // shared org-level memory
	orgAgents    map[string]*governance.AgentIdentity // shared org-level agent registry
	teamRegistry map[string]*OrgTeam                  // org-level team registry
	users        map[string]*User                     // org-level user registry (Feature Batch G)
	userAudits   map[string][]UserAuditEntry          // per-user audit trail (append-only)
	usersLoaded  bool                                 // true once the persisted registry has been replayed
	profile      Profile                              // org-level default profile
	profileCfg   ProfileConfig                        // org-level profile configuration
	// closeApp is the teardown hook for evicted cached ProjectApp instances.
	// It defaults to calling ProjectApp.Close; tests may replace it to assert
	// that eviction tears down the app (relay/bus subscriptions must not leak
	// across evictions). It is always invoked with s.mu held.
	closeApp func(app ProjectApp) error
	// newApp is the injected ProjectApp constructor (web.New at the
	// composition roots). Nil until SetAppFactory is called; appFor fails
	// closed on a nil factory so the enterprise server never builds a nil
	// app. Set once at setup, read lock-free from the build path (same
	// convention as profileCfg).
	newApp func(root string) (ProjectApp, error)
}

type projectState struct {
	project  Project
	app      ProjectApp // lazily built on first access
	appErr   error      // build error (cached)
	lastUsed time.Time
	memory   *memory.MemoryStore // per-project memory store (scoped to project root)
	building bool                // B4: one off-lock builder per project
	cond     *sync.Cond          // B4: single-flight waiters (L: s.mu)
}

// orgAllowWeakRBACEnv is the documented-unsafe escape hatch for org mode
// without KERN_RBAC_DEFAULT_DENY=1 (finding 2).
const orgAllowWeakRBACEnv = "KERN_ORG_ALLOW_WEAK_RBAC"

// orgModeErr returns an error when org mode is configured without its RBAC
// default-deny pairing. agent_id is client-asserted (untrusted), so org mode
// letting unassigned principals keep the legacy permit-all trust is a
// governance bypass; KERN_ORG_ALLOW_WEAK_RBAC=1 is the explicit unsafe
// escape hatch. Nil = org mode may start.
func orgModeErr(root string) error {
	if root == "" {
		return nil
	}
	if os.Getenv("KERN_RBAC_DEFAULT_DENY") == "1" || os.Getenv(orgAllowWeakRBACEnv) == "1" {
		return nil
	}
	return fmt.Errorf("org mode requires KERN_RBAC_DEFAULT_DENY=1 when %s is set (root %s): agent_id is client-asserted and unassigned principals would otherwise keep the legacy permit-all trust, defeating org-wins RBAC. Set KERN_RBAC_DEFAULT_DENY=1 (recommended), or set %s=1 to explicitly accept the unsafe weak-RBAC posture", governance.OrgRootEnv, root, orgAllowWeakRBACEnv)
}

// New creates an enterprise server with no projects. Use Register to add
// projects and WithOrgAudit/WithOrgBus/WithPolicies to configure org-level
// shared state. When org mode is configured (KERN_ORG_ROOT) without the RBAC
// default-deny pairing, New REFUSES to start, erroring with both env vars
// (finding 2) — org mode is opt-in, so requiring the pairing is a safe gate.
func New() (*Server, error) {
	s := &Server{
		projects:     map[string]*projectState{},
		orgAudit:     governance.NewAuditLog(),
		orgBus:       eventbus.New(),
		policies:     governance.DefaultPolicies(),
		orgMemory:    memory.WithEnvGovernance(memory.NewMemoryStore(""), ""), // org-level store (no root; governed when KERN_MEMORY_GOVERNANCE is set)
		orgAgents:    map[string]*governance.AgentIdentity{},
		teamRegistry: map[string]*OrgTeam{},
		users:        map[string]*User{},
		userAudits:   map[string][]UserAuditEntry{},
		profile:      DefaultProfile,
		profileCfg:   ProfileConfig{Profile: DefaultProfile},
		closeApp:     func(a ProjectApp) error { return a.Close() },
	}
	// Opt-in deployment-time profile selection via KERN_ENTERPRISE_PROFILE.
	if p, ok := ParseProfile(os.Getenv(enterpriseProfileEnv)); ok {
		s.profile = p
		s.profileCfg.Profile = p
	}
	// P13 stage 1 — central policy distribution: with an org root configured
	// (KERN_ORG_ROOT), the org policy document at <org-root>/.kern/
	// org-policy.json replaces the default policy set, so every project
	// firewall appFor builds is constructed from the org policy. A missing
	// document keeps the defaults (first-run state); a CORRUPT document is
	// warned loudly and the defaults are kept — the store's fail-closed
	// guarantee means the corrupt policy is never partially applied. Without
	// an org root, behavior is byte-for-byte unchanged.
	s.orgRoot = governance.OrgRoot()
	// Finding 2 pairing gate: refuse to enter org mode without the RBAC
	// default-deny posture (or the documented-unsafe escape hatch).
	if err := orgModeErr(s.orgRoot); err != nil {
		return nil, err
	}
	if s.orgRoot != "" {
		if doc, ok, err := governance.LoadOrgPolicy(s.orgRoot); err != nil {
			log.Printf("enterprise: WARNING: org policy store %s is corrupt (%v) — project firewalls will enforce the default policies until it is fixed (kern policy set --root %s)", governance.OrgPolicyPath(s.orgRoot), err, s.orgRoot)
		} else if ok {
			s.policies = doc.Policies
		}
		// P13 stage 3 — org-wide approvals: this server owns the shared org
		// audit log, so org-approval events (create/approve/reject/consume)
		// recorded by the orgapprovals package — including consumes from the
		// project consoles' deploy gates — land on the org audit trail.
		orgapprovals.SetAuditHook(s.orgAudit.Record)
	}
	return s, nil
}

// SetAppFactory injects the ProjectApp constructor used by appFor. The
// enterprise package does not import internal/web (breaking the mcp → org →
// enterprise → web transitive closure), so the composition roots — cmd/kern
// and cmd/kern-server — wire the real web.New here at startup. Without a
// factory appFor fails closed ("no app factory configured") rather than
// building a nil app.
func (s *Server) SetAppFactory(f func(root string) (ProjectApp, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.newApp = f
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

// WithOrgRoot sets the org root explicitly, overriding the KERN_ORG_ROOT
// resolution performed by New. When the root holds an org policy document it
// replaces the default policy set (same load as New); an empty root clears
// org mode. This is the programmatic entry point to the same org-root
// resolution governance.OrgRoot provides; Stages 2/3 reuse the root.
//
// WithOrgRoot REFUSES to apply an org root without the RBAC default-deny
// pairing (finding 2): org scope stays inactive and a clear error naming
// both env vars is logged — same gate as New()'s env path.
func (s *Server) WithOrgRoot(root string) *Server {
	if root != "" {
		if err := orgModeErr(root); err != nil {
			log.Printf("enterprise: refusing org mode: %v", err)
			return s // org scope stays inactive
		}
	}
	s.orgRoot = root
	if root == "" {
		return s
	}
	if doc, ok, err := governance.LoadOrgPolicy(root); err != nil {
		log.Printf("enterprise: WARNING: org policy store %s is corrupt (%v) — project firewalls will enforce the default policies until it is fixed (kern policy set --root %s)", governance.OrgPolicyPath(root), err, root)
	} else if ok {
		s.policies = doc.Policies
	}
	// P13 stage 3: same org audit hook wiring as New (see there).
	orgapprovals.SetAuditHook(s.orgAudit.Record)
	return s
}

// WriteOrgPolicy persists the given policies to the org policy store
// (<org-root>/.kern/org-policy.json) and applies them to this server's
// snapshot AND every cached project App, so newly-built AND already-built
// project firewalls pick the change up immediately. It is the write path
// behind POST /org/policies and `kern policy set`. Requires an org root;
// without one the write is refused — org scope is strictly opt-in. Returns
// the recorded content hash.
func (s *Server) WriteOrgPolicy(policies []domain.Policy) (string, error) {
	if s.orgRoot == "" {
		return "", fmt.Errorf("enterprise: no org root configured (set %s or WithOrgRoot)", governance.OrgRootEnv)
	}
	hash, err := governance.SaveOrgPolicy(s.orgRoot, policies)
	if err != nil {
		return "", err
	}
	s.applyPoliciesLocked(policies)
	return hash, nil
}

// ReloadOrgPolicy re-reads the org policy document and re-applies it to this
// server's snapshot and every cached project App — the "re-propagate"
// operation behind POST /org/policies/apply and `kern policy apply`, and the
// operation that resolves drift after an out-of-band change to the shared
// file. Requires an org root and an existing org policy document.
func (s *Server) ReloadOrgPolicy() (string, error) {
	if s.orgRoot == "" {
		return "", fmt.Errorf("enterprise: no org root configured (set %s or WithOrgRoot)", governance.OrgRootEnv)
	}
	doc, ok, err := governance.LoadOrgPolicy(s.orgRoot)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("enterprise: no org policy at %s — run kern policy set first", governance.OrgPolicyPath(s.orgRoot))
	}
	s.applyPoliciesLocked(doc.Policies)
	return doc.Hash, nil
}

// applyPoliciesLocked swaps the applied policy snapshot and pushes it into
// every cached project App. It is the single runtime propagation point:
// existing Apps are refreshed IN PLACE (a cheap assessor swap on the shared
// firewall) so a policy change never leaves a stale snapshot in a built App.
func (s *Server) applyPoliciesLocked(policies []domain.Policy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policies = policies
	for _, ps := range s.projects {
		if ps.app != nil {
			ps.app.SetPolicies(policies)
		}
	}
}

// PolicyDrift reports whether the policies this server applies to project
// firewalls drift from the org policy document on disk (e.g. after an
// operator ran `kern policy set` on the shared org root while this server was
// running). ReloadOrgPolicy resolves the drift.
func (s *Server) PolicyDrift() (governance.DriftReport, error) {
	s.mu.RLock()
	policies := s.policies
	orgRoot := s.orgRoot
	s.mu.RUnlock()
	return governance.PolicyDrift(orgRoot, policies)
}

// WithStore sets a shared storage backend for org-level persistence.
// When set, the org audit log can be persisted via WithStore on AuditLog.
func (s *Server) WithStore(store storage.Store) *Server {
	s.store = store
	return s
}

// Register adds a project to the enterprise server using the org-level
// default profile. The project's ProjectApp is built lazily on first request.
// Returns an error if the name is already registered, the root is invalid, or
// the profile's limits reject the registration.
func (s *Server) Register(name, root string) error {
	return s.RegisterWithProfile(name, root, s.profile)
}

// RegisterWithProfile adds a project to the enterprise server under an
// explicit profile, overriding the org-level default for that project. The
// project's ProjectApp is built lazily on first request. Returns an error if the
// name is already registered, the root is invalid, the profile is unknown, or
// the profile's limits reject the registration (e.g. ProfileBasic allows a
// single project).
func (s *Server) RegisterWithProfile(name, root string, p Profile) error {
	if !p.Valid() {
		return fmt.Errorf("enterprise: unknown profile %q", p)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.projects[name]; exists {
		return fmt.Errorf("enterprise: project %q already registered", name)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("enterprise: invalid root %q: %w", root, err)
	}
	// Registration limits come from the stricter of the org-level effective
	// configuration (default profile + overrides) and the explicit profile:
	// the org contract bounds every project, and a per-project profile can
	// only tighten it.
	orgFeats, pFeats := s.profileCfg.Effective(), p.Features()
	multiProject := orgFeats.MultiProject && pFeats.MultiProject
	maxProjects := orgFeats.MaxProjects
	if pFeats.MaxProjects > 0 && (maxProjects == 0 || pFeats.MaxProjects < maxProjects) {
		maxProjects = pFeats.MaxProjects
	}
	if !multiProject && len(s.projects) > 0 {
		return fmt.Errorf("enterprise: profile %q supports at most one project", p)
	}
	if maxProjects > 0 && len(s.projects) >= maxProjects {
		return fmt.Errorf("enterprise: profile %q allows at most %d projects", p, maxProjects)
	}
	s.projects[name] = &projectState{
		project: Project{Name: name, Root: absRoot, Profile: p},
		cond:    sync.NewCond(&s.mu),
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
	names := slices.Sorted(maps.Keys(s.projects))
	result := make([]Project, 0, len(names))
	for _, n := range names {
		result = append(result, s.projects[n].project)
	}
	return result
}

// appFor returns the ProjectApp for a project, building it lazily on first
// access via the injected factory (web.New at the composition roots). The
// build error is cached so repeated requests don't retry. Cached apps are
// capped (see maxProjects): when the cache is full and a new project needs
// building, the least-recently-used cached app is evicted so it can be
// rebuilt on next access. This bounds memory growth for orgs with many
// registered projects. With no factory configured it fails closed — the
// enterprise server never builds a nil app.
func (s *Server) appFor(name string) (ProjectApp, error) {
	// B4: the factory build (30-90s on large repos for the real web.New)
	// runs OFF the org-wide mutex so one project's cold build never blocks
	// every other tenant. Single-flight per project: concurrent callers for
	// the same project wait on its condition variable for the in-flight
	// builder's result.
	s.mu.Lock()
	ps, exists := s.projects[name]
	if !exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("enterprise: project %q not registered", name)
	}
	// Touch recency on every access so LRU eviction reflects true usage.
	ps.lastUsed = time.Now()
	if ps.app != nil || ps.appErr != nil {
		app, err := ps.app, ps.appErr
		s.mu.Unlock()
		return app, err
	}
	if ps.building {
		for ps.building {
			ps.cond.Wait()
		}
		app, err := ps.app, ps.appErr
		s.mu.Unlock()
		return app, err
	}
	// Cache miss: if at the app cap, evict the LRU cached app to make room.
	if s.cachedCount() >= s.maxProjects() {
		s.evictLRU()
	}
	ps.building = true
	root := ps.project.Root
	// Snapshot the applied policy set under the lock so the build below
	// constructs the firewall from a consistent policy version even if a
	// concurrent WriteOrgPolicy/ReloadOrgPolicy swaps it mid-build.
	policies := s.policies
	factory := s.newApp
	s.mu.Unlock()

	if factory == nil {
		// Fail closed: without an injected constructor the enterprise server
		// cannot build project apps. The error is cached on the project
		// state so concurrent single-flight waiters observe the same
		// failure instead of retrying.
		err := fmt.Errorf("enterprise: no app factory configured — the composition root must call SetAppFactory (e.g. web.New)")
		s.mu.Lock()
		ps.building = false
		ps.app = nil
		ps.appErr = err
		ps.cond.Broadcast()
		s.mu.Unlock()
		return nil, err
	}

	app, err := factory(root)
	if err == nil {
		// Feature Batch G: wire the org user registry's role lookup into
		// the project console so /api/approvals/approve|reject enforce the
		// org RBAC layer (approver role -> approve/reject). When the
		// profile disables UserRegistry, UserRole returns ("", false) and
		// the console falls back to the historical no-RBAC flow.
		app.SetUserRoleLookup(s.UserRole)
		// P13 stage 1: build the project firewall from the org policy (the
		// applied snapshot) instead of the defaults. With no org root the
		// snapshot IS DefaultPolicies, so behavior is byte-for-byte
		// unchanged.
		app.SetPolicies(policies)
	}

	s.mu.Lock()
	ps.building = false
	ps.app = app
	ps.appErr = err
	if err == nil {
		ps.memory = memory.WithEnvGovernance(memory.NewMemoryStore(ps.project.Root), ps.project.Root)
	}
	ps.cond.Broadcast()
	s.mu.Unlock()
	return app, err
}

// appForCached returns the cached ProjectApp for name without triggering a
// build (B4): aggregate endpoints must never serialize N full rebuilds inside
// a single request. The bool reports whether an app is cached.
func (s *Server) appForCached(name string) (ProjectApp, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, exists := s.projects[name]
	if !exists || ps.app == nil {
		return nil, false
	}
	ps.lastUsed = time.Now()
	return ps.app, true
}

// defaultMaxProjects is the default cap on cached ProjectApp instances. It bounds
// the number of lazily built apps an enterprise server holds in memory; when
// exceeded the least-recently-used cached app is evicted (and rebuilt on next
// access). Configurable via KERN_ENTERPRISE_MAX_PROJECTS.
const defaultMaxProjects = 16

// maxProjects returns the configured cap on cached ProjectApp instances. The
// KERN_ENTERPRISE_MAX_PROJECTS env var wins when set; otherwise the org-level
// profile's MaxCachedApps applies (ProfileAdvanced raises it to 64); the
// default is 16. Invalid or non-positive values fall back to the default.
func (s *Server) maxProjects() int {
	v := os.Getenv("KERN_ENTERPRISE_MAX_PROJECTS")
	if v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n >= 1 {
			return n
		}
		return defaultMaxProjects
	}
	if n := s.orgFeatures().MaxCachedApps; n > 0 {
		return n
	}
	return defaultMaxProjects
}

// cachedCount returns how many projects currently hold a built ProjectApp.
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

// evictLRU drops the cached ProjectApp of the least-recently-used project that
// currently has one built. The evicted app is torn down via the closeApp hook
// (ProjectApp.Close by default) so the relay/bus subscriptions and background
// loops New() started do not leak goroutines or sockets across evictions. The
// teardown is best-effort: an error is logged and never fails the eviction.
// The projectState (and its per-project memory store) are retained so the app
// can be rebuilt on next access. Must be called with s.mu held.
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
	// Tear down the evicted app BEFORE dropping it: without this, every
	// eviction leaks the relay listener goroutine, the socket, and the bus
	// subscription the app's New() wired.
	if err := s.closeApp(oldest.app); err != nil {
		log.Printf("enterprise: close app for project %q: %v", oldest.project.Name, err)
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
	ps.memory = memory.WithEnvGovernance(memory.NewMemoryStore(ps.project.Root), ps.project.Root)
	return ps.memory
}

// OrgAudit returns the shared org-level audit log, or nil when the org-level
// profile disables OrgAudit (e.g. ProfileBasic).
func (s *Server) OrgAudit() *governance.AuditLog {
	if !s.orgFeatures().OrgAudit {
		return nil
	}
	return s.orgAudit
}

// OrgBus returns the shared org-level event bus, or nil when the org-level
// profile disables OrgBus (e.g. ProfileBasic).
func (s *Server) OrgBus() *eventbus.Bus {
	if !s.orgFeatures().OrgBus {
		return nil
	}
	return s.orgBus
}

// Store returns the shared org-level storage backend (nil if unset).
func (s *Server) Store() storage.Store { return s.store }

// Profile returns the org-level default profile. Projects registered through
// Register inherit it; RegisterWithProfile overrides it per project.
func (s *Server) Profile() Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.profile
}

// ProfileConfig returns the org-level profile configuration, including any
// resource overrides set via WithProfileConfig.
func (s *Server) ProfileConfig() ProfileConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.profileCfg
}

// WithProfile sets the org-level default profile applied to projects
// registered through Register. An unknown profile is ignored. Returns the
// server for chaining.
func (s *Server) WithProfile(p Profile) *Server {
	if !p.Valid() {
		return s
	}
	s.mu.Lock()
	s.profile = p
	s.profileCfg.Profile = p
	s.mu.Unlock()
	return s
}

// WithProfileConfig sets the org-level profile configuration: a profile plus
// optional resource overrides (MaxProjects, MaxCachedApps, AuditRetention).
// An unknown profile is ignored; an empty Profile keeps the current one.
// Returns the server for chaining.
func (s *Server) WithProfileConfig(c ProfileConfig) *Server {
	if c.Profile != "" && !c.Profile.Valid() {
		return s
	}
	s.mu.Lock()
	if c.Profile == "" {
		c.Profile = s.profile
	}
	s.profile = c.Profile
	s.profileCfg = c
	s.mu.Unlock()
	return s
}

// Features returns the org-level feature set for the server's default
// profile, with configuration overrides applied.
func (s *Server) Features() FeatureSet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.profileCfg.Effective()
}

// orgFeatures returns the org-level feature set without acquiring the lock.
// Profiles are configured at setup time (before serving), so a lock-free read
// is safe; use this from code paths that already hold s.mu.
func (s *Server) orgFeatures() FeatureSet { return s.profileCfg.Effective() }

// ProjectProfile returns the effective profile for a project: its explicit
// profile when registered via RegisterWithProfile, otherwise the org-level
// default. The bool reports whether the project is registered.
func (s *Server) ProjectProfile(name string) (Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ps, exists := s.projects[name]
	if !exists {
		return "", false
	}
	if ps.project.Profile != "" {
		return ps.project.Profile, true
	}
	return s.profile, true
}

// ProjectFeatures returns the effective feature set for a project's profile.
// The bool reports whether the project is registered.
func (s *Server) ProjectFeatures(name string) (FeatureSet, bool) {
	p, ok := s.ProjectProfile(name)
	if !ok {
		return FeatureSet{}, false
	}
	return p.Features(), true
}

// OrgMemory returns the shared org-level memory store. Memories
// written here are visible across all projects — e.g. a lesson learned in the
// payments service is recallable when working on the orders service.
func (s *Server) OrgMemory() *memory.MemoryStore {
	if !s.orgFeatures().OrgMemory {
		return nil
	}
	return s.orgMemory
}

// RegisterAgent registers an agent identity at the org level (permissions
// apply across all projects); with an org root and a Role on the identity,
// the role is also bound in the org role store (org-wins RBAC). Errors on a
// duplicate ID or when the org-level profile disables AgentRegistry.
func (s *Server) RegisterAgent(a *governance.AgentIdentity) error {
	if !s.orgFeatures().AgentRegistry {
		return fmt.Errorf("enterprise: agent registry disabled by profile %q", s.profile)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.orgAgents[a.ID]; exists {
		return fmt.Errorf("enterprise: agent %q already registered", a.ID)
	}
	s.orgAgents[a.ID] = a
	if s.orgRoot != "" && a.Role != "" { // P13 stage 2: identity Role binds an org role
		if err := orgapprovals.AssignOrgRole(s.orgRoot, a.ID, a.Role); err != nil {
			return fmt.Errorf("enterprise: bind org role %q for agent %q: %w", a.Role, a.ID, err)
		}
	}
	return nil
}

// Agents returns all registered org-level agent identities, sorted by ID.
// Returns nil when the org-level profile disables AgentRegistry.
func (s *Server) Agents() []*governance.AgentIdentity {
	if !s.orgFeatures().AgentRegistry {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := slices.Sorted(maps.Keys(s.orgAgents))
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
// multi-repo registry. Returns nil when no repos are registered or when the
// org-level profile disables CrossProjectSearch.
func (s *Server) OrgSearch(query string, limit int) []intel.RepoHit {
	if !s.orgFeatures().CrossProjectSearch {
		return nil
	}
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
	if !strings.HasPrefix(h, prefix) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(h, prefix))
	// Constant-time compare so token timing does not leak length/prefix
	// information. ConstantTimeCompare returns 0 on length mismatch, which
	// is fine here: the fail-closed 401 is the same either way.
	if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// ServeHTTP routes requests by project: /<project>/... delegates to the
// project's ProjectApp; /org/... serves org-level endpoints (audit, projects,
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

// serveOrgDashboard serves the org admin page: a server-rendered overview of
// the org's registered projects, agents, and teams plus a footer nav to the
// org JSON endpoints. Only the cheap accessors are called — no appFor builds
// and no reindexing — so the page stays fast at any org size.
func (s *Server) serveOrgDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	orgAdminHTML(w, s.Projects(), s.Agents(), s.Teams())
}

// orgAdminCSS is the inline stylesheet for the org admin page, following the
// web console's design family (dark panels, muted headers, monospace accents).
// Fully self-contained — no external assets, no JavaScript.
const orgAdminCSS = `:root { --bg:#0f1115; --panel:#171a21; --panel2:#1c2029; --fg:#e6e8ee; --muted:#9aa3b2; --accent:#4f8cff; --border:#262b36; }
* { box-sizing:border-box; }
body { margin:0; background:var(--bg); color:var(--fg); font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif; }
header { padding:16px 28px; border-bottom:1px solid var(--border); background:var(--panel); }
header h1 { margin:0; font-size:20px; }
header p { margin:4px 0 0; color:var(--muted); font-size:12px; }
main { padding:20px 28px; display:grid; grid-template-columns:repeat(auto-fit,minmax(420px,1fr)); gap:16px; }
section.panel { background:var(--panel); border:1px solid var(--border); border-radius:8px; padding:16px; }
section.panel.wide { grid-column:1/-1; }
section.panel h2 { margin:0 0 12px; font-size:15px; text-transform:uppercase; letter-spacing:.04em; color:var(--muted); }
table { width:100%; border-collapse:collapse; font-size:12px; }
th,td { text-align:left; padding:6px 8px; border-bottom:1px solid var(--border); vertical-align:top; }
th { color:var(--muted); font-weight:600; }
a { color:var(--accent); text-decoration:none; }
a:hover { text-decoration:underline; }
code { font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace; font-size:11px; color:var(--muted); }
.muted { color:var(--muted); }
footer { padding:14px 28px; border-top:1px solid var(--border); background:var(--panel); }
footer nav { display:flex; gap:4px; flex-wrap:wrap; align-items:center; }
footer a { color:var(--muted); text-decoration:none; font-size:12px; padding:6px 12px; border-radius:6px; }
footer a:hover { color:var(--fg); background:var(--panel2); }
footer .hint { color:var(--muted); font-size:11px; margin-left:8px; }`

// orgAdminHTML writes the org admin page to w: a header with org-wide counts,
// then Projects, Agents, and Teams tables, then a footer nav linking the org
// JSON endpoints. Names, roots, and IDs come from user registration input,
// so every interpolation is HTML-escaped.
func orgAdminHTML(w io.Writer, projects []Project, agents []*governance.AgentIdentity, teams []OrgTeam) {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Kern Enterprise</title><style>`)
	b.WriteString(orgAdminCSS)
	b.WriteString(`</style></head><body>`)
	fmt.Fprintf(&b, `<header><div class="brand"><h1>Kern Enterprise</h1><p>Org admin &mdash; %d project(s), %d agent(s), %d team(s)</p></div></header>`, len(projects), len(agents), len(teams))
	b.WriteString(`<main>`)

	// Projects: full-width table (roots are long paths) with per-project
	// console links.
	fmt.Fprintf(&b, `<section class="panel wide"><h2>Projects (%d)</h2><table><tr><th>Name</th><th>Root</th></tr>`, len(projects))
	if len(projects) == 0 {
		b.WriteString(`<tr><td colspan="2" class="muted">no projects registered</td></tr>`)
	}
	for _, p := range projects {
		fmt.Fprintf(&b, `<tr><td><a href="/%s/">%s</a></td><td><code>%s</code></td></tr>`,
			html.EscapeString(p.Name), html.EscapeString(p.Name), html.EscapeString(p.Root))
	}
	b.WriteString(`</table></section>`)

	// Agents: org-level agent registry, sorted by ID (Agents() sorts).
	fmt.Fprintf(&b, `<section class="panel"><h2>Agents (%d)</h2><table><tr><th>ID</th><th>Name</th><th>Type</th></tr>`, len(agents))
	if len(agents) == 0 {
		b.WriteString(`<tr><td colspan="3" class="muted">no agents registered</td></tr>`)
	}
	for _, a := range agents {
		fmt.Fprintf(&b, `<tr><td><code>%s</code></td><td>%s</td><td>%s</td></tr>`,
			html.EscapeString(a.ID), html.EscapeString(a.Name), html.EscapeString(a.Type))
	}
	b.WriteString(`</table></section>`)

	// Teams: members and projects comma-joined inline.
	fmt.Fprintf(&b, `<section class="panel"><h2>Teams (%d)</h2><table><tr><th>ID</th><th>Name</th><th>Members</th><th>Projects</th></tr>`, len(teams))
	if len(teams) == 0 {
		b.WriteString(`<tr><td colspan="4" class="muted">no teams registered</td></tr>`)
	}
	for _, t := range teams {
		fmt.Fprintf(&b, `<tr><td><code>%s</code></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			html.EscapeString(t.ID), html.EscapeString(t.Name),
			html.EscapeString(strings.Join(t.Members, ", ")),
			html.EscapeString(strings.Join(t.Projects, ", ")))
	}
	b.WriteString(`</table></section>`)

	// Footer nav: links to the org JSON endpoints.
	b.WriteString(`</main><footer><nav>`)
	b.WriteString(`<a href="/org/audit">Org Audit</a><a href="/org/policies">Org Policies</a><a href="/org/approvals">Org Approvals</a><a href="/org/memory">Org Memory</a><a href="/org/tasks">Org Tasks</a><a href="/org/search?q=New">Org Search</a><a href="/org/agents">Org Agents</a><a href="/org/teams">Org Teams</a>`)
	b.WriteString(`<span class="hint">JSON endpoints</span>`)
	b.WriteString(`</nav></footer></body></html>`)

	_, _ = w.Write([]byte(b.String()))
}

// serveOrgAPI serves org-level API endpoints.
func (s *Server) serveOrgAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/org")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	switch parts[0] {
	case "audit":
		s.serveOrgAudit(w, r)
	case "approvals":
		// /org/approvals[/approve|/reject]: the org-wide approval surface
		// (list/create/approve/reject) — thin delegation; the handler lives
		// in the orgapprovals package.
		orgapprovals.ServeApprovals(w, r, s.orgRoot)
	case "policies":
		// /org/policies[/apply]: GET lists the applied policy set; POST
		// writes a new org policy; POST /org/policies/apply re-propagates
		// the org policy document (resolving drift).
		if len(parts) > 1 {
			if parts[1] == "apply" {
				s.serveOrgPolicyApply(w, r)
				return
			}
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
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

// orgAgentJSON is the wire shape for org agents: snake_case, aligned with
// the MCP org surface's agent rendering (id/name/type). domain.Agent has no
// json tags and is persisted as-is by stores, so the org surface renders
// through this DTO instead of retagging the domain type.
type orgAgentJSON struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Role      string    `json:"role,omitempty"` // P13 stage 2: org role binding
}

func orgAgentJSONOf(a *governance.AgentIdentity) orgAgentJSON {
	return orgAgentJSON{ID: a.ID, Name: a.Name, Type: a.Type, CreatedAt: a.CreatedAt}
}

// orgTeamJSON is the wire shape for org teams (snake_case, matching the MCP
// org team rendering).
type orgTeamJSON struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Projects []string `json:"projects"`
	Members  []string `json:"members"`
}

func orgTeamJSONOf(t OrgTeam) orgTeamJSON {
	return orgTeamJSON{ID: t.ID, Name: t.Name, Projects: t.Projects, Members: t.Members}
}

// orgPolicyJSON is the wire shape for org-level policies (snake_case).
type orgPolicyJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Rule        string `json:"rule"`
	Scope       string `json:"scope"`
	Enabled     bool   `json:"enabled"`
}

// orgMemoryJSON is the wire shape for memories served on /org/memory
// (snake_case). domain.Memory is persisted to disk with its untagged Go
// field names, so retagging the domain type would break existing stores —
// the org surface renders through this DTO instead.
type orgMemoryJSON struct {
	ID              string    `json:"id"`
	Type            string    `json:"type"`
	Content         string    `json:"content"`
	Source          string    `json:"source"`
	Scope           string    `json:"scope,omitempty"`
	Tags            []string  `json:"tags,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Subject         string    `json:"subject,omitempty"`
	Confidence      float64   `json:"confidence,omitempty"`
	Provenance      string    `json:"provenance,omitempty"`
	RelatedEntities []string  `json:"related_entities,omitempty"`
	ClaimType       string    `json:"claim_type,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	Classification  string    `json:"classification,omitempty"`
	Status          string    `json:"status,omitempty"`
}

func orgMemoryJSONOf(m domain.Memory) orgMemoryJSON {
	return orgMemoryJSON{
		ID:              m.ID,
		Type:            string(m.Type),
		Content:         m.Content,
		Source:          m.Source,
		Scope:           m.Scope,
		Tags:            m.Tags,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
		Subject:         m.Subject,
		Confidence:      m.Confidence,
		Provenance:      m.Provenance,
		RelatedEntities: m.RelatedEntities,
		ClaimType:       string(m.ClaimType),
		Reason:          m.Reason,
		Classification:  m.Classification,
		Status:          string(m.Status),
	}
}

func (s *Server) serveOrgAudit(w http.ResponseWriter, r *http.Request) {
	entries := s.orgAudit.All()
	out := make([]orgapprovals.AuditEntryJSON, 0, len(entries))
	for _, e := range entries {
		out = append(out, orgapprovals.AuditEntryJSONOf(e))
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"entries": out,
		"count":   len(out),
	}); err != nil {
		http.Error(w, "could not encode response", http.StatusInternalServerError)
	}
}

// serveOrgPolicies serves the org-level policy set.
// - GET /org/policies lists the policies currently applied to project
// firewalls (the default set when no org root is configured, the org policy
// document otherwise).
// - POST /org/policies writes a new org policy: the body is
// {"policies": [...]} (strict: unknown fields are rejected by name). The
// policies are persisted atomically to <org-root>/.kern/org-policy.json,
// audited on the shared org audit log, and applied to this server's snapshot
// AND every cached project App immediately. Requires an org root
// (KERN_ORG_ROOT). 201 with the recorded content hash on success.
// - any other method → 405.
func (s *Server) serveOrgPolicies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		applied := s.policies
		s.mu.RUnlock()
		policies := make([]orgPolicyJSON, 0, len(applied))
		for _, p := range applied {
			policies = append(policies, orgPolicyJSON{
				ID:          p.ID,
				Name:        p.Name,
				Description: p.Description,
				Rule:        p.Rule,
				Scope:       p.Scope,
				Enabled:     p.Enabled,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"policies": policies,
			"count":    len(policies),
		}); err != nil {
			http.Error(w, "could not encode response", http.StatusInternalServerError)
		}
	case http.MethodPost:
		// The write body is strict and server-owned: unknown fields (e.g. a
		// client-submitted "hash") are rejected by name, so a client can
		// never believe it set a policy the server did not record.
		var body struct {
			Policies []domain.Policy `json:"policies"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			http.Error(w, "enterprise: invalid policy body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if len(body.Policies) == 0 {
			http.Error(w, "enterprise: policies is required", http.StatusBadRequest)
			return
		}
		hash, err := s.WriteOrgPolicy(body.Policies)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.auditOrgPolicyChange("org policy written via POST /org/policies (" + hash + ")")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hash":  hash,
			"count": len(body.Policies),
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// serveOrgPolicyApply implements POST /org/policies/apply: it re-reads the
// org policy document and re-applies it to this server's snapshot and every
// cached project App — resolving drift after an out-of-band change to the
// shared file (e.g. a `kern policy set` run by another operator). 404 when
// no org policy document exists, 400 when no org root is configured.
func (s *Server) serveOrgPolicyApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	hash, err := s.ReloadOrgPolicy()
	if err != nil {
		if strings.Contains(err.Error(), "no org policy at") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.auditOrgPolicyChange("org policy re-applied via POST /org/policies/apply (" + hash + ")")
	drift, _ := s.PolicyDrift()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"hash":  hash,
		"drift": drift,
	})
}

// auditOrgPolicyChange records an org policy write/reload on the shared org
// audit log so every change to the org's governing rule set is attributable.
func (s *Server) auditOrgPolicyChange(reason string) {
	s.orgAudit.Record(governance.AuditEntry{
		AgentID:  "org-admin",
		Action:   "apply",
		Resource: "policy",
		Risk:     domain.Risk{Level: domain.RiskMedium, Score: 0.5, Factors: []string{"org policy change"}},
		Approved: true,
		Result:   "allowed",
		Policy:   "org-policy",
		Reason:   reason,
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
// cached ProjectApp architecture builder, skipping projects that fail to build.
func (s *Server) serveOrgArchitecture(w http.ResponseWriter, r *http.Request) {
	type projectArch struct {
		Project    string   `json:"project"`
		Violations []string `json:"violations"`
		OK         bool     `json:"ok"`
	}
	s.mu.Lock()
	names := make([]string, 0, len(s.projects))
	for _, p := range s.projects {
		names = append(names, p.project.Name)
	}
	s.mu.Unlock()

	// B4: the aggregate answers from CACHED apps only — a cold org must not
	// serialize up to 16 full rebuilds inside one request. Uncached projects
	// are counted as "pending" and warmed in the background (bounded to two
	// concurrent builds so a cold start cannot OOM the server).
	out := make([]projectArch, 0, len(names))
	pending := 0
	for _, name := range names {
		app, ok := s.appForCached(name)
		if !ok {
			pending++
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
		out = append(out, projectArch{Project: name, Violations: viol, OK: arch.OK})
	}

	// Async warm: kick off background builds for the uncached projects so the
	// next request finds them ready. Fire-and-forget; errors are cached on
	// the project state and surfaced by appFor.
	sem := make(chan struct{}, 2)
	for _, name := range names {
		if _, ok := s.appForCached(name); ok {
			continue
		}
		go func(n string) {
			sem <- struct{}{}
			defer func() { <-sem }()
			_, _ = s.appFor(n)
		}(name)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"architecture": out,
		"count":        len(out),
		"pending":      pending,
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
		out := make([]orgMemoryJSON, 0, len(memories))
		for _, m := range memories {
			out = append(out, orgMemoryJSONOf(m))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"memories": out,
			"count":    len(out),
		})
	case http.MethodPost:
		// Decode through the wire DTO (snake_case) and rebuild the domain
		// Memory — decoding domain.Memory directly would bind to its
		// untagged Go field names, a different contract than the GET shape.
		var body orgMemoryJSON
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "enterprise: invalid memory body: "+err.Error(), http.StatusBadRequest)
			return
		}
		m := domain.Memory{
			ID:              body.ID,
			Type:            domain.MemoryType(body.Type),
			Content:         body.Content,
			Source:          body.Source,
			Scope:           body.Scope,
			Tags:            body.Tags,
			CreatedAt:       body.CreatedAt,
			UpdatedAt:       body.UpdatedAt,
			Subject:         body.Subject,
			Confidence:      body.Confidence,
			Provenance:      body.Provenance,
			RelatedEntities: body.RelatedEntities,
			ClaimType:       domain.ClaimType(body.ClaimType),
			Reason:          body.Reason,
			Classification:  body.Classification,
			Status:          domain.MemoryStatus(body.Status),
		}
		saved, err := s.orgMemory.Add(m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(orgMemoryJSONOf(saved))
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
	_ = json.NewEncoder(w).Encode(map[string]any{
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
	_ = json.NewEncoder(w).Encode(map[string]any{
		"hits":  hits,
		"count": len(hits),
	})
}

// serveOrgAgents serves the org-level agent registry.
//   - GET  /org/agents returns all registered agent identities, each with any
//     org role binding from the org role store (Stage 2 org-scope RBAC).
//   - POST /org/agents registers a new agent (optional "role" binds an org
//     role); 400 on a bad body, 409 on a duplicate ID, 201 on success.
//   - any other method → 405.
func (s *Server) serveOrgAgents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		agents := s.Agents()
		orgRoles := orgapprovals.ListOrgRBACRoles(s.orgRoot) // org role bindings (no org root → empty)
		out := make([]orgAgentJSON, 0, len(agents))
		for _, a := range agents {
			j := orgAgentJSONOf(a)
			j.Role = orgRoles[a.ID]
			out = append(out, j)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agents": out,
			"count":  len(out),
		})
	case http.MethodPost:
		// Registration input is strict and server-owned:
		// - unknown fields are rejected by name (never silently dropped);
		// - Permissions are decoded but stripped before registering: org
		//   agents never carry enforcement grants (only id/name/type/role
		//   are kept from the body);
		// - CreatedAt is stamped server-side (never a zero value).
		var body struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
			Role string `json:"role"`
			// Permissions is decoded only so it is not rejected as an
			// unknown field; it is never stored (see above).
			Permissions []json.RawMessage `json:"permissions"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			http.Error(w, "enterprise: invalid agent body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if body.ID == "" {
			http.Error(w, "enterprise: agent id is required", http.StatusBadRequest)
			return
		}
		agent := governance.NewAgent(body.ID, body.Name, body.Type, nil)
		agent.Role = body.Role // P13 stage 2: optional org role binding
		if err := s.RegisterAgent(agent); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(orgAgentJSONOf(agent))
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
		out := make([]orgTeamJSON, 0, len(teams))
		for _, t := range teams {
			out = append(out, orgTeamJSONOf(t))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"teams": out,
			"count": len(out),
		})
	case http.MethodPost:
		// Decode through the wire DTO (snake_case, strict: unknown fields
		// are rejected by name) and rebuild the domain OrgTeam.
		var body orgTeamJSON
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			http.Error(w, "enterprise: invalid team body: "+err.Error(), http.StatusBadRequest)
			return
		}
		team := OrgTeam{ID: body.ID, Name: body.Name, Projects: body.Projects, Members: body.Members}
		if err := s.CreateTeam(team); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(orgTeamJSONOf(team))
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
		_ = json.NewEncoder(w).Encode(orgTeamJSONOf(*team))
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
	// AgentTeams returns team IDs (not team objects), so the response is
	// the plain ID list — no DTO needed.
	teams := s.AgentTeams(agentID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"agent": agentID,
		"teams": teams,
		"count": len(teams),
	})
}
