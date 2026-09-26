// Per-project kern configuration from .kern/kern.yaml: optional, a missing or
// unreadable file silently yields an empty config (same graceful-degradation
// contract as .kern/config.json).
//
// Schema (YAML):
//
//	profiles:
//	  <profile-name>:
//	    exclude_patterns: ["node_modules/**", ".env"]
//	    truncate_rules:
//	      - match: "ValidationError:"
//	        keep_lines_before: 2
//	        keep_lines_after: 5
//	      - match: "console.log"
//	        action: strip_completely
//
// Only the "default" profile is applied automatically by optimize.Log when no
// explicit profile is selected.
package config

import (
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// TruncateRule describes one user-defined log-line transformation rule.
// Rules are evaluated in declaration order; the first matching rule wins.
type TruncateRule struct {
	// Match is a plain-text substring. A line that contains this string
	// (case-sensitive) triggers the rule.
	Match string `yaml:"match"`

	// Action controls what happens to matching lines:
	//   ""                  – keep the line (default; KeepLinesBefore/After still apply)
	//   "strip_completely"  – remove the line entirely from the output
	Action string `yaml:"action"`

	// KeepLinesBefore is how many lines before a match to preserve in the
	// output window (equivalent to grep -B). Ignored when Action is
	// "strip_completely".
	KeepLinesBefore int `yaml:"keep_lines_before"`

	// KeepLinesAfter is how many lines after a match to preserve in the
	// output window (equivalent to grep -A). Ignored when Action is
	// "strip_completely".
	KeepLinesAfter int `yaml:"keep_lines_after"`
}

// ProfileConfig holds compression settings for one named profile.
type ProfileConfig struct {
	// ExcludePatterns are glob patterns (relative to the project root).
	// Files matching any pattern are excluded from log/context processing.
	ExcludePatterns []string `yaml:"exclude_patterns"`

	// TruncateRules is an ordered list of per-line transformation rules.
	TruncateRules []TruncateRule `yaml:"truncate_rules"`
}

// Config is the parsed representation of .kern/kern.yaml.
type Config struct {
	// Profiles maps profile names to their config.
	// The reserved name "default" is applied automatically when no profile
	// is explicitly requested.
	Profiles map[string]ProfileConfig `yaml:"profiles"`
}

// DefaultProfile returns the "default" profile config, or a zero-value
// ProfileConfig when no default profile is defined.
func (c *Config) DefaultProfile() ProfileConfig {
	if c == nil || c.Profiles == nil {
		return ProfileConfig{}
	}
	return c.Profiles["default"]
}

// Profile returns the named profile, falling back to the default profile and
// then to a zero-value ProfileConfig when neither exists.
func (c *Config) Profile(name string) ProfileConfig {
	if c == nil || c.Profiles == nil {
		return ProfileConfig{}
	}
	if p, ok := c.Profiles[name]; ok {
		return p
	}
	return c.Profiles["default"]
}

// configCache holds parsed configs per absolute project root.
var (
	cacheMu     sync.RWMutex
	configCache = make(map[string]*Config)
)

// ResetCache clears the per-root config cache. Used in tests.
func ResetCache() {
	cacheMu.Lock()
	configCache = make(map[string]*Config)
	cacheMu.Unlock()
}

// Load reads .kern/kern.yaml from root. A missing or unreadable file returns
// an empty *Config (never nil, never an error). A malformed YAML file returns
// an empty config so the caller always has a safe value.
func Load(root string) *Config {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}

	cacheMu.RLock()
	if cfg, ok := configCache[abs]; ok {
		cacheMu.RUnlock()
		return cfg
	}
	cacheMu.RUnlock()

	cfg := parse(filepath.Join(abs, ".kern", "kern.yaml"))

	cacheMu.Lock()
	configCache[abs] = cfg
	cacheMu.Unlock()
	return cfg
}

// parse reads and parses one YAML file. Any error returns an empty *Config.
func parse(path string) *Config {
	b, err := os.ReadFile(path)
	if err != nil {
		return &Config{}
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return &Config{}
	}
	return &cfg
}
