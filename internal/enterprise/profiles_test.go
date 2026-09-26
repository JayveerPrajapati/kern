package enterprise

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

func TestProfileValid(t *testing.T) {
	for _, p := range []Profile{ProfileBasic, ProfileStandard, ProfileAdvanced} {
		if !p.Valid() {
			t.Errorf("profile %q should be valid", p)
		}
	}
	for _, p := range []Profile{"", "enterprise", "ULTRA", "Basic "} {
		if p.Valid() {
			t.Errorf("profile %q should be invalid", p)
		}
	}
}

func TestParseProfile(t *testing.T) {
	cases := []struct {
		in   string
		want Profile
		ok   bool
	}{
		{"basic", ProfileBasic, true},
		{"BASIC", ProfileBasic, true},
		{"  Standard  ", ProfileStandard, true},
		{"advanced", ProfileAdvanced, true},
		{"", "", false},
		{"ultra", "", false},
		{"standrd", "", false},
	}
	for _, c := range cases {
		got, ok := ParseProfile(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseProfile(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// featureLessOrEqual reports whether a's enabled booleans are a subset of b's
// and a's resource limits are at most b's where b is non-zero.
func featureLessOrEqual(t *testing.T, a, b FeatureSet) bool {
	boolFields := []struct {
		name string
		a, b bool
	}{
		{"MultiProject", a.MultiProject, b.MultiProject},
		{"OrgAudit", a.OrgAudit, b.OrgAudit},
		{"OrgBus", a.OrgBus, b.OrgBus},
		{"OrgPolicies", a.OrgPolicies, b.OrgPolicies},
		{"OrgMemory", a.OrgMemory, b.OrgMemory},
		{"AgentRegistry", a.AgentRegistry, b.AgentRegistry},
		{"TeamRegistry", a.TeamRegistry, b.TeamRegistry},
		{"UserRegistry", a.UserRegistry, b.UserRegistry},
		{"CrossProjectSearch", a.CrossProjectSearch, b.CrossProjectSearch},
		{"OrgDashboard", a.OrgDashboard, b.OrgDashboard},
	}
	for _, f := range boolFields {
		if f.a && !f.b {
			t.Logf("feature %s enabled in lower profile but not in higher", f.name)
			return false
		}
	}
	if a.MaxProjects > 0 && b.MaxProjects > 0 && a.MaxProjects > b.MaxProjects {
		t.Logf("MaxProjects %d exceeds higher profile's %d", a.MaxProjects, b.MaxProjects)
		return false
	}
	if a.MaxCachedApps > 0 && b.MaxCachedApps > 0 && a.MaxCachedApps > b.MaxCachedApps {
		t.Logf("MaxCachedApps %d exceeds higher profile's %d", a.MaxCachedApps, b.MaxCachedApps)
		return false
	}
	if a.AuditRetention > 0 && b.AuditRetention > 0 && a.AuditRetention > b.AuditRetention {
		t.Logf("AuditRetention %d exceeds higher profile's %d", a.AuditRetention, b.AuditRetention)
		return false
	}
	return true
}

func TestProfileFeatures(t *testing.T) {
	basic := ProfileBasic.Features()
	if basic.MultiProject {
		t.Error("basic profile should not enable multi-project")
	}
	if basic.OrgAudit || basic.OrgBus || basic.OrgPolicies || basic.OrgMemory ||
		basic.AgentRegistry || basic.TeamRegistry || basic.UserRegistry ||
		basic.CrossProjectSearch || basic.OrgDashboard {
		t.Error("basic profile should disable all org-level capabilities")
	}
	if basic.MaxProjects != 1 {
		t.Errorf("basic MaxProjects = %d, want 1", basic.MaxProjects)
	}

	std := ProfileStandard.Features()
	if !std.MultiProject || !std.OrgAudit || !std.OrgBus || !std.OrgPolicies ||
		!std.OrgMemory || !std.AgentRegistry || !std.TeamRegistry || !std.UserRegistry ||
		!std.CrossProjectSearch || !std.OrgDashboard {
		t.Error("standard profile should enable the full historical feature set")
	}
	if std.MaxProjects != 0 {
		t.Errorf("standard MaxProjects = %d, want 0 (unlimited)", std.MaxProjects)
	}
	if std.MaxCachedApps != defaultMaxProjects {
		t.Errorf("standard MaxCachedApps = %d, want %d", std.MaxCachedApps, defaultMaxProjects)
	}

	adv := ProfileAdvanced.Features()
	if !adv.MultiProject || !adv.OrgAudit || !adv.OrgBus || !adv.OrgPolicies ||
		!adv.OrgMemory || !adv.AgentRegistry || !adv.TeamRegistry || !adv.UserRegistry ||
		!adv.CrossProjectSearch || !adv.OrgDashboard {
		t.Error("advanced profile should enable the full feature set")
	}
	if adv.MaxCachedApps != advancedMaxCachedApps {
		t.Errorf("advanced MaxCachedApps = %d, want %d", adv.MaxCachedApps, advancedMaxCachedApps)
	}
	if adv.AuditRetention != advancedAuditRetention {
		t.Errorf("advanced AuditRetention = %d, want %d", adv.AuditRetention, advancedAuditRetention)
	}

	// Monotonicity: basic ⊂ standard ⊂ advanced.
	if !featureLessOrEqual(t, basic, std) {
		t.Error("basic features should be a subset of standard")
	}
	if !featureLessOrEqual(t, std, adv) {
		t.Error("standard features should be a subset of advanced")
	}

	// Unknown profiles disable everything.
	if unknown := Profile("bogus").Features(); unknown.MultiProject || unknown.OrgAudit {
		t.Error("unknown profile should return the zero feature set")
	}
}

func TestProfileConfigEffective(t *testing.T) {
	base := ProfileConfig{Profile: ProfileBasic}.Effective()
	if base.MaxProjects != 1 {
		t.Errorf("basic effective MaxProjects = %d, want 1", base.MaxProjects)
	}

	overridden := ProfileConfig{Profile: ProfileBasic, MaxProjects: 3, MaxCachedApps: 7}.Effective()
	if overridden.MaxProjects != 3 {
		t.Errorf("overridden MaxProjects = %d, want 3", overridden.MaxProjects)
	}
	if overridden.MaxCachedApps != 7 {
		t.Errorf("overridden MaxCachedApps = %d, want 7", overridden.MaxCachedApps)
	}
	if overridden.MultiProject {
		t.Error("overrides must not enable features the profile disables")
	}

	adv := ProfileConfig{Profile: ProfileAdvanced, AuditRetention: 200}.Effective()
	if adv.AuditRetention != 200 {
		t.Errorf("advanced AuditRetention = %d, want override 200", adv.AuditRetention)
	}
	if adv.MaxCachedApps != advancedMaxCachedApps {
		t.Errorf("advanced MaxCachedApps = %d, want profile default %d", adv.MaxCachedApps, advancedMaxCachedApps)
	}
}

func TestParseProfileConfig(t *testing.T) {
	cfg, err := ParseProfileConfig("basic")
	if err != nil || cfg.Profile != ProfileBasic {
		t.Errorf("ParseProfileConfig(basic) = (%+v, %v), want basic profile", cfg, err)
	}

	cfg, err = ParseProfileConfig("advanced:max_projects=4,audit_retention=5000")
	if err != nil {
		t.Fatalf("ParseProfileConfig(advanced spec): %v", err)
	}
	if cfg.Profile != ProfileAdvanced || cfg.MaxProjects != 4 || cfg.AuditRetention != 5000 || cfg.MaxCachedApps != 0 {
		t.Errorf("ParseProfileConfig(advanced spec) = %+v", cfg)
	}

	cfg, err = ParseProfileConfig("standard:max_cached_apps=32")
	if err != nil || cfg.Profile != ProfileStandard || cfg.MaxCachedApps != 32 {
		t.Errorf("ParseProfileConfig(standard spec) = (%+v, %v)", cfg, err)
	}

	for _, bad := range []string{"", "ultra", "basic:max_projects=abc", "basic:unknown_key=1", "basic:max_projects", "basic:max_projects=0"} {
		if _, err := ParseProfileConfig(bad); err == nil {
			t.Errorf("ParseProfileConfig(%q) should error", bad)
		}
	}
}

func TestServerDefaultProfile(t *testing.T) {
	s := mustNew(t)
	if got := s.Profile(); got != DefaultProfile {
		t.Errorf("New().Profile() = %q, want %q", got, DefaultProfile)
	}
	if got := s.ProfileConfig().Profile; got != DefaultProfile {
		t.Errorf("New().ProfileConfig().Profile = %q, want %q", got, DefaultProfile)
	}
	// Default profile preserves the historical cache cap.
	if got := s.maxProjects(); got != defaultMaxProjects {
		t.Errorf("New().maxProjects() = %d, want %d", got, defaultMaxProjects)
	}
}

func TestServerProfileFromEnv(t *testing.T) {
	t.Setenv(enterpriseProfileEnv, "advanced")
	s := mustNew(t)
	if got := s.Profile(); got != ProfileAdvanced {
		t.Errorf("env-configured Profile() = %q, want advanced", got)
	}
	if got := s.maxProjects(); got != advancedMaxCachedApps {
		t.Errorf("env-configured maxProjects() = %d, want %d", got, advancedMaxCachedApps)
	}

	t.Setenv(enterpriseProfileEnv, "bogus")
	if got := mustNew(t).Profile(); got != DefaultProfile {
		t.Errorf("invalid env profile should fall back to %q, got %q", DefaultProfile, got)
	}
}

func TestServerWithProfile(t *testing.T) {
	s := mustNew(t)
	if got := s.WithProfile(ProfileBasic).Profile(); got != ProfileBasic {
		t.Errorf("WithProfile(basic).Profile() = %q", got)
	}
	// Invalid profiles are ignored, keeping the current one.
	if got := s.WithProfile("nope").Profile(); got != ProfileBasic {
		t.Errorf("invalid WithProfile should keep basic, got %q", got)
	}
}

func TestServerWithProfileConfig(t *testing.T) {
	s := mustNew(t)
	s.WithProfileConfig(ProfileConfig{Profile: ProfileAdvanced, MaxCachedApps: 4})
	if got := s.Profile(); got != ProfileAdvanced {
		t.Errorf("WithProfileConfig Profile() = %q, want advanced", got)
	}
	if got := s.maxProjects(); got != 4 {
		t.Errorf("WithProfileConfig maxProjects() = %d, want 4", got)
	}
	if feats := s.Features(); feats.AuditRetention != advancedAuditRetention {
		t.Errorf("Features().AuditRetention = %d, want advanced default %d", feats.AuditRetention, advancedAuditRetention)
	}

	// Empty profile keeps the current one.
	s.WithProfileConfig(ProfileConfig{MaxCachedApps: 9})
	if got := s.Profile(); got != ProfileAdvanced {
		t.Errorf("empty-profile config should keep advanced, got %q", got)
	}
	if got := s.maxProjects(); got != 9 {
		t.Errorf("override maxProjects() = %d, want 9", got)
	}

	// Invalid profile config is ignored.
	if got := s.WithProfileConfig(ProfileConfig{Profile: "bogus"}).Profile(); got != ProfileAdvanced {
		t.Errorf("invalid config profile should be ignored, got %q", got)
	}
}

func TestRegisterWithProfile(t *testing.T) {
	s := mustNew(t)
	if err := s.RegisterWithProfile("proj-a", t.TempDir(), ProfileBasic); err != nil {
		t.Fatalf("RegisterWithProfile(basic): %v", err)
	}
	p, ok := s.ProjectProfile("proj-a")
	if !ok || p != ProfileBasic {
		t.Errorf("ProjectProfile(proj-a) = (%q, %v), want (basic, true)", p, ok)
	}
	if feats, ok := s.ProjectFeatures("proj-a"); !ok || feats.MaxProjects != 1 {
		t.Errorf("ProjectFeatures(proj-a) = (%+v, %v)", feats, ok)
	}

	// Plain Register inherits the org-level default profile.
	if err := s.Register("proj-b", t.TempDir()); err != nil {
		t.Fatalf("Register(proj-b): %v", err)
	}
	if p, ok := s.ProjectProfile("proj-b"); !ok || p != DefaultProfile {
		t.Errorf("ProjectProfile(proj-b) = (%q, %v), want (%q, true)", p, ok, DefaultProfile)
	}

	// Unknown profiles are rejected.
	if err := s.RegisterWithProfile("proj-c", t.TempDir(), "bogus"); err == nil {
		t.Error("RegisterWithProfile with unknown profile should error")
	}
	if _, ok := s.ProjectProfile("proj-c"); ok {
		t.Error("rejected registration must not register the project")
	}

	// Unregistered projects report not-found.
	if _, ok := s.ProjectProfile("missing"); ok {
		t.Error("ProjectProfile(missing) should report not found")
	}
}

func TestBasicProfileSingleProjectLimit(t *testing.T) {
	s := mustNew(t).WithProfile(ProfileBasic)
	if err := s.Register("only", t.TempDir()); err != nil {
		t.Fatalf("first register under basic: %v", err)
	}
	if err := s.Register("second", t.TempDir()); err == nil {
		t.Error("basic profile must reject a second project")
	} else if !strings.Contains(err.Error(), "at most one project") {
		t.Errorf("unexpected error: %v", err)
	}

	// The same limit applies to explicit basic registrations...
	s2 := mustNew(t)
	if err := s2.RegisterWithProfile("a", t.TempDir(), ProfileBasic); err != nil {
		t.Fatalf("register a as basic: %v", err)
	}
	if err := s2.RegisterWithProfile("b", t.TempDir(), ProfileBasic); err == nil {
		t.Error("second basic project must be rejected")
	}
	// ...but per-project selection can still upgrade another project.
	if err := s2.RegisterWithProfile("b", t.TempDir(), ProfileAdvanced); err != nil {
		t.Errorf("advanced project should be allowed alongside a basic one: %v", err)
	}
	if p, _ := s2.ProjectProfile("b"); p != ProfileAdvanced {
		t.Errorf("ProjectProfile(b) = %q, want advanced", p)
	}
}

func TestProfileConfigMaxProjectsLimit(t *testing.T) {
	s := mustNew(t).WithProfileConfig(ProfileConfig{Profile: ProfileStandard, MaxProjects: 2})
	for _, name := range []string{"p1", "p2"} {
		if err := s.Register(name, t.TempDir()); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	if err := s.Register("p3", t.TempDir()); err == nil {
		t.Error("profile with MaxProjects=2 must reject a third project")
	}
}

func TestProfileFeatureGating(t *testing.T) {
	// A basic server gates off every org-level capability.
	basic := mustNew(t).WithProfile(ProfileBasic)
	if got := basic.OrgAudit(); got != nil {
		t.Error("basic OrgAudit() should be nil")
	}
	if got := basic.OrgBus(); got != nil {
		t.Error("basic OrgBus() should be nil")
	}
	if got := basic.OrgMemory(); got != nil {
		t.Error("basic OrgMemory() should be nil")
	}
	if got := basic.Agents(); got != nil {
		t.Error("basic Agents() should be nil")
	}
	if got := basic.OrgSearch("query", 5); got != nil {
		t.Error("basic OrgSearch should be nil")
	}
	if got := basic.Teams(); got != nil {
		t.Error("basic Teams() should be nil")
	}
	if err := basic.RegisterAgent(governance.NewAgent("agent-1", "Agent One", "coder", nil)); err == nil {
		t.Error("basic RegisterAgent should error")
	}
	if err := basic.CreateTeam(OrgTeam{ID: "team-1", Name: "Team One"}); err == nil {
		t.Error("basic CreateTeam should error")
	}

	// The default (standard) server keeps every capability.
	std := mustNew(t)
	if std.OrgAudit() == nil || std.OrgBus() == nil || std.OrgMemory() == nil {
		t.Error("standard server should expose org audit, bus, and memory")
	}
	if std.Agents() == nil || std.Teams() == nil {
		t.Error("standard server should expose agent and team registries")
	}
	if err := std.RegisterAgent(governance.NewAgent("agent-1", "Agent One", "coder", nil)); err != nil {
		t.Errorf("standard RegisterAgent: %v", err)
	}
	if err := std.CreateTeam(OrgTeam{ID: "team-1", Name: "Team One"}); err != nil {
		t.Errorf("standard CreateTeam: %v", err)
	}
}

func TestProfileMaxCachedApps(t *testing.T) {
	s := mustNew(t).WithProfile(ProfileAdvanced)
	if got := s.maxProjects(); got != advancedMaxCachedApps {
		t.Errorf("advanced maxProjects() = %d, want %d", got, advancedMaxCachedApps)
	}
	// An explicit override beats the profile default.
	s.WithProfileConfig(ProfileConfig{Profile: ProfileAdvanced, MaxCachedApps: 3})
	if got := s.maxProjects(); got != 3 {
		t.Errorf("overridden maxProjects() = %d, want 3", got)
	}
	// The env var wins over profile configuration.
	t.Setenv("KERN_ENTERPRISE_MAX_PROJECTS", "2")
	if got := s.maxProjects(); got != 2 {
		t.Errorf("env maxProjects() = %d, want 2", got)
	}
}

func TestProjectListCarriesProfile(t *testing.T) {
	s := mustNew(t)
	if err := s.RegisterWithProfile("proj-a", t.TempDir(), ProfileAdvanced); err != nil {
		t.Fatalf("RegisterWithProfile: %v", err)
	}
	if err := s.Register("proj-b", t.TempDir()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	projects := s.Projects()
	byName := map[string]Project{}
	for _, p := range projects {
		byName[p.Name] = p
	}
	if got := byName["proj-a"].Profile; got != ProfileAdvanced {
		t.Errorf("proj-a Profile = %q, want advanced", got)
	}
	if got := byName["proj-b"].Profile; got != DefaultProfile {
		t.Errorf("proj-b Profile = %q, want %q", got, DefaultProfile)
	}
}
