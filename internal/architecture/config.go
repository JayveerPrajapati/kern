package architecture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/JayveerPrajapati/kern/internal/intel"
)

// maxConfigSize caps the size of an architecture spec file (1MB). Oversized
// files are skipped as if absent — they are treated as no config rather than
// parsed, avoiding pathological memory/CPU on a crafted file.
const maxConfigSize = 1 << 20

type Config struct {
	Version string  `yaml:"version"`          // "1"
	Name    string  `yaml:"name,omitempty"`   // optional label
	Layers  []Layer `yaml:"layers,omitempty"` // named layers (optional)
	Rules   []Rule  `yaml:"rules"`            // the actual governance rules
}

// Layer is a named group of directory patterns with an optional dependency
// constraint (the layers it is permitted to depend on).
type Layer struct {
	Name    string   `yaml:"name"`              // e.g. "presentation"
	Paths   []string `yaml:"paths"`             // directory patterns, e.g. ["web/**"]
	Depends []string `yaml:"depends,omitempty"` // layer names it may depend on; empty = no constraint
}

// Rule declares one governance decision about a directed dependency edge,
// keyed either by directory patterns (from/to) or by named layers
// (layer_from/layer_to), or both.
type Rule struct {
	ID          string `yaml:"id,omitempty"`
	Description string `yaml:"description,omitempty"`
	From        string `yaml:"from,omitempty"`       // directory/package pattern
	To          string `yaml:"to,omitempty"`         // directory/package pattern
	Action      string `yaml:"action"`               // "forbid" | "allow"
	Severity    string `yaml:"severity,omitempty"`   // "error" (default) | "warning"
	LayerFrom   string `yaml:"layer_from,omitempty"` // layer name reference
	LayerTo     string `yaml:"layer_to,omitempty"`   // layer name reference
}

// Violation mirrors intel.Violation plus the rule that fired and its severity.
type Violation struct {
	intel.Violation
	RuleID   string // rule that fired
	Severity string // "error" | "warning"
}

// DefaultPath returns where the governance spec lives for a root.
func DefaultPath(root string) string {
	return filepath.Join(root, ".kern", "architecture.yaml")
}

// Load reads architecture.yaml (or .json) from <root>/.kern. A missing file
// returns a Config with no rules (everything permitted); a malformed file is
// an error (fail-closed).
func Load(root string) (*Config, error) {
	// Prefer the .yaml spec; fall back to .json for the same document.
	for _, ext := range []string{".yaml", ".json"} {
		path := filepath.Join(root, ".kern", "architecture"+ext)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if info.Size() > maxConfigSize {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if ext == ".json" {
			var cfg Config
			if err := json.Unmarshal(data, &cfg); err != nil {
				return nil, fmt.Errorf("invalid %s: %w", path, err)
			}
			return &cfg, nil
		}
		cfg, err := decodeYAML(data)
		if err != nil {
			return nil, fmt.Errorf("invalid %s: %w", path, err)
		}
		return cfg, nil
	}
	return &Config{}, nil
}

// --- YAML decode (minimal, fixed-schema, stdlib-only) ---

// decodeYAML parses a serialized Config (JSON is a strict subset) by hand,
// since the default build has no YAML library.
func decodeYAML(data []byte) (*Config, error) {
	generic, err := parseYAML(data)
	if err != nil {
		return nil, err
	}
	if generic == nil {
		return &Config{}, nil
	}
	return decodeConfig(generic)
}

// decodeConfig walks a generic YAML document and type-checks it into a Config.
// It fails closed: any unexpected shape or type is an error.
func decodeConfig(root interface{}) (*Config, error) {
	cfg := &Config{}
	// Accept an empty/rootless document.
	if root == nil {
		return cfg, nil
	}
	m, ok := root.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("architecture config must be a mapping, got %T", root)
	}
	cfg.Version = scalarString(m["version"])
	if cfg.Version != "" && cfg.Version != "1" {
		return nil, fmt.Errorf("unsupported architecture version %q (want \"1\")", cfg.Version)
	}
	cfg.Name = scalarString(m["name"])

	if v, ok := m["layers"]; ok {
		items, ok := v.([]interface{})
		if !ok {
			return nil, fmt.Errorf("layers must be a list")
		}
		for i, li := range items {
			lm, ok := li.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("layers[%d] must be a mapping", i)
			}
			l := Layer{
				Name:    scalarString(lm["name"]),
				Paths:   toStringSlice(lm["paths"]),
				Depends: toStringSlice(lm["depends"]),
			}
			cfg.Layers = append(cfg.Layers, l)
		}
	}
	if v, ok := m["rules"]; ok {
		items, ok := v.([]interface{})
		if !ok {
			return nil, fmt.Errorf("rules must be a list")
		}
		for i, ri := range items {
			rm, ok := ri.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("rules[%d] must be a mapping", i)
			}
			r := Rule{
				ID:          scalarString(rm["id"]),
				Description: scalarString(rm["description"]),
				From:        scalarString(rm["from"]),
				To:          scalarString(rm["to"]),
				Action:      scalarString(rm["action"]),
				Severity:    scalarString(rm["severity"]),
				LayerFrom:   scalarString(rm["layer_from"]),
				LayerTo:     scalarString(rm["layer_to"]),
			}
			if r.Action != "forbid" && r.Action != "allow" {
				return nil, fmt.Errorf("rules[%d]: action must be \"forbid\" or \"allow\", got %q", i, r.Action)
			}
			cfg.Rules = append(cfg.Rules, r)
		}
	}
	return cfg, nil
}

// toStringSlice converts a decoded value to a []string. Accepts a list of
// scalars, a single scalar string, or nil.
func toStringSlice(v interface{}) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s := scalarString(e); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		if s := scalarString(v); s != "" {
			return []string{s}
		}
	}
	return nil
}

// scalarString returns a string for a scalar value, or "" for nil.
func scalarString(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}
