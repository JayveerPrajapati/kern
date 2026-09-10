// Profile system for enterprise mode.
//
// Profiles define tiers of enterprise functionality. Each tier maps to a
// FeatureSet describing which org-level capabilities are enabled and which
// resource limits apply. A server has an org-level default profile (applied
// to every project registered through Register) and each project may override
// it with its own profile via RegisterWithProfile.

package enterprise

import (
	"fmt"
	"strconv"
	"strings"
)

// Profile identifies an enterprise configuration tier.
type Profile string

const (
	// ProfileBasic enables minimal enterprise features: a single project with
	// per-project state only. Shared org-level capabilities (audit log, event
	// bus, policies, memory, agent registry, team registry, cross-project
	// search) are disabled.
	ProfileBasic Profile = "basic"
	// ProfileStandard enables the standard enterprise feature set: multiple
	// projects under one listener with a shared org-level audit log, event
	// bus, policies, memory, agent registry, team registry, and cross-project
	// search. It is the default profile and matches the historical behavior
	// of the enterprise server.
	ProfileStandard Profile = "standard"
	// ProfileAdvanced enables the full enterprise feature set: everything in
	// ProfileStandard plus larger resource allowances (more cached web.App
	// instances) and bounded per-project audit retention for compliance.
	ProfileAdvanced Profile = "advanced"
)

// DefaultProfile is applied to a server — and to every project registered
// through Register — unless overridden via WithProfile/WithProfileConfig or
// RegisterWithProfile. It preserves the historical enterprise behavior.
const DefaultProfile = ProfileStandard

// Resource allowances that set ProfileAdvanced apart from ProfileStandard.
const (
	advancedMaxCachedApps  = 64
	advancedAuditRetention = 10000
)

// FeatureSet describes the enterprise capabilities a profile enables.
// Resource limits use 0 to mean "unlimited" (or "use the profile default"
// when carried inside a ProfileConfig override).
type FeatureSet struct {
	MultiProject       bool // serve multiple projects from one listener
	OrgAudit           bool // shared org-level audit log
	OrgBus             bool // shared org-level event bus
	OrgPolicies        bool // org-level policies applied to all projects
	OrgMemory          bool // shared org-level memory store
	AgentRegistry      bool // org-level agent identities
	TeamRegistry       bool // org-level teams
	CrossProjectSearch bool // cross-project symbol search (OrgSearch)
	OrgDashboard       bool // org admin dashboard routes
	MaxProjects        int  // max registered projects (0 = unlimited)
	MaxCachedApps      int  // cap on cached web.App instances (0 = default 16)
	AuditRetention     int  // audit entries retained per project (0 = unlimited)
}

var (
	basicFeatures = FeatureSet{
		// Single project, per-project state only. All org-level capabilities
		// are off.
		MultiProject:  false,
		MaxProjects:   1,
		MaxCachedApps: 1,
	}
	standardFeatures = FeatureSet{
		// Everything the enterprise server historically provides.
		MultiProject:       true,
		OrgAudit:           true,
		OrgBus:             true,
		OrgPolicies:        true,
		OrgMemory:          true,
		AgentRegistry:      true,
		TeamRegistry:       true,
		CrossProjectSearch: true,
		OrgDashboard:       true,
		MaxCachedApps:      defaultMaxProjects, // 16, the historical default
	}
	advancedFeatures = FeatureSet{
		MultiProject:       true,
		OrgAudit:           true,
		OrgBus:             true,
		OrgPolicies:        true,
		OrgMemory:          true,
		AgentRegistry:      true,
		TeamRegistry:       true,
		CrossProjectSearch: true,
		OrgDashboard:       true,
		MaxCachedApps:      advancedMaxCachedApps,
		AuditRetention:     advancedAuditRetention,
	}
)

// Valid reports whether p is one of the known profile names.
func (p Profile) Valid() bool {
	switch p {
	case ProfileBasic, ProfileStandard, ProfileAdvanced:
		return true
	}
	return false
}

// Features returns the FeatureSet enabled by p. An unknown profile returns
// the zero FeatureSet (everything disabled), matching Valid() == false.
func (p Profile) Features() FeatureSet {
	switch p {
	case ProfileBasic:
		return basicFeatures
	case ProfileStandard:
		return standardFeatures
	case ProfileAdvanced:
		return advancedFeatures
	}
	return FeatureSet{}
}

// String returns the canonical lowercase name of the profile.
func (p Profile) String() string { return string(p) }

// ParseProfile converts a case-insensitive profile name into a Profile. The
// bool reports whether the name was recognized.
func ParseProfile(s string) (Profile, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "basic":
		return ProfileBasic, true
	case "standard":
		return ProfileStandard, true
	case "advanced":
		return ProfileAdvanced, true
	}
	return "", false
}

// ProfileConfig configures a profile with optional resource overrides.
// A zero-valued override means "use the profile default".
type ProfileConfig struct {
	Profile        Profile
	MaxProjects    int // override for the max-registered-projects limit
	MaxCachedApps  int // override for the cached web.App cap
	AuditRetention int // override for per-project audit retention
}

// Effective returns the FeatureSet for the configured profile with any
// non-zero overrides applied on top.
func (c ProfileConfig) Effective() FeatureSet {
	f := c.Profile.Features()
	if c.MaxProjects > 0 {
		f.MaxProjects = c.MaxProjects
	}
	if c.MaxCachedApps > 0 {
		f.MaxCachedApps = c.MaxCachedApps
	}
	if c.AuditRetention > 0 {
		f.AuditRetention = c.AuditRetention
	}
	return f
}

// ParseProfileConfig parses a profile configuration spec of the form
//
//	"name"                              e.g. "basic"
//	"name:key=value,key=value"          e.g. "advanced:max_projects=4,audit_retention=5000"
//
// Recognized keys are max_projects, max_cached_apps, and audit_retention.
// Unknown keys, values, and profile names are rejected.
func ParseProfileConfig(spec string) (ProfileConfig, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ProfileConfig{}, fmt.Errorf("enterprise: empty profile config")
	}
	name := spec
	overrides := ""
	if i := strings.IndexByte(spec, ':'); i >= 0 {
		name, overrides = strings.TrimSpace(spec[:i]), strings.TrimSpace(spec[i+1:])
	}
	p, ok := ParseProfile(name)
	if !ok {
		return ProfileConfig{}, fmt.Errorf("enterprise: unknown profile %q", name)
	}
	cfg := ProfileConfig{Profile: p}
	if overrides == "" {
		return cfg, nil
	}
	for _, kv := range strings.Split(overrides, ",") {
		key, val, found := strings.Cut(strings.TrimSpace(kv), "=")
		if !found {
			return ProfileConfig{}, fmt.Errorf("enterprise: profile config %q: expected key=value", kv)
		}
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil || n < 1 {
			return ProfileConfig{}, fmt.Errorf("enterprise: profile config %q: value must be a positive integer", kv)
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "max_projects":
			cfg.MaxProjects = n
		case "max_cached_apps":
			cfg.MaxCachedApps = n
		case "audit_retention":
			cfg.AuditRetention = n
		default:
			return ProfileConfig{}, fmt.Errorf("enterprise: profile config %q: unknown key %q", kv, key)
		}
	}
	return cfg, nil
}

// enterpriseProfileEnv selects the org-level default profile at deployment
// time without code changes. It is opt-in: unset or invalid values fall back
// to DefaultProfile.
const enterpriseProfileEnv = "KERN_ENTERPRISE_PROFILE"
