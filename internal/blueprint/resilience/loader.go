package resilience

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// scenarioParamKeys lists the accepted params keys for the "http" kind.
// The loader rejects any other key so a typo cannot silently change (or
// weaken) a declared fault — the same hard-validation philosophy as the
// policy loader.
var httpParamKeys = map[string]bool{
	"status":        true,
	"delay_seconds": true,
	"path":          true,
}

// scenarioTopKeys lists the accepted per-scenario keys.
var scenarioTopKeys = map[string]bool{
	"id":     true,
	"kind":   true,
	"params": true,
}

// Load reads declarative resilience scenarios from .blueprint/scenarios/*.yaml
// under repoRoot and returns them. A missing scenarios directory (or no YAML
// files) yields an empty list with no error; an unreadable or invalid file is
// a hard error. Scenario ids must be unique across the YAML files AND the
// built-in scenarios (DefaultScenarios).
func Load(repoRoot string) ([]Scenario, error) {
	pattern := filepath.Join(repoRoot, ".blueprint", "scenarios", "*.yaml")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("resilience: glob scenarios %s: %w", pattern, err)
	}
	if len(paths) == 0 {
		return nil, nil
	}

	// Scenario ids are unique across ALL scenarios: built-ins + YAML.
	seen := map[string]bool{}
	for _, s := range DefaultScenarios() {
		seen[s.ID()] = true
	}

	var scenarios []Scenario
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("resilience: read scenarios %s: %w", path, err)
		}
		parsed, err := parseScenariosFile(data, seen)
		if err != nil {
			return nil, fmt.Errorf("resilience: %s: %w", path, err)
		}
		scenarios = append(scenarios, parsed...)
	}
	return scenarios, nil
}

// LoadAll returns the built-in scenarios plus the YAML-declared ones for
// repoRoot. A missing scenarios directory yields just the built-ins with a nil
// error.
func LoadAll(repoRoot string) ([]Scenario, error) {
	declared, err := Load(repoRoot)
	if err != nil {
		return nil, err
	}
	return append(DefaultScenarios(), declared...), nil
}

// parseScenariosFile parses and validates one scenarios YAML document. seen
// tracks every accepted id (built-ins seeded by the caller) so duplicate ids
// are rejected.
func parseScenariosFile(data []byte, seen map[string]bool) ([]Scenario, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse scenarios: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, nil // empty file → no scenarios
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("scenarios file must be a mapping")
	}

	var scenarios []Scenario
	for i := 0; i+1 < len(root.Content); i += 2 {
		keyNode := root.Content[i]
		valNode := root.Content[i+1]
		if keyNode.Value != "scenarios" {
			return nil, fmt.Errorf("unknown top-level key %q (valid keys: scenarios)", keyNode.Value)
		}
		if valNode.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("scenarios must be a list")
		}
		for _, item := range valNode.Content {
			s, err := parseScenario(item, seen)
			if err != nil {
				return nil, err
			}
			scenarios = append(scenarios, s)
		}
	}
	return scenarios, nil
}

// parseScenario validates one scenario entry and constructs it. Unknown
// top-level scenario keys and unknown kinds are hard errors.
func parseScenario(n *yaml.Node, seen map[string]bool) (Scenario, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("scenario must be a mapping")
	}
	var id, kind string
	var params *yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		if !scenarioTopKeys[key] {
			return nil, fmt.Errorf("unknown scenario key %q (valid keys: id, kind, params)", key)
		}
		switch key {
		case "id":
			id = n.Content[i+1].Value
		case "kind":
			kind = n.Content[i+1].Value
		case "params":
			params = n.Content[i+1]
		}
	}
	if id == "" {
		return nil, fmt.Errorf("scenario missing id")
	}
	if seen[id] {
		return nil, fmt.Errorf("duplicate scenario id %q", id)
	}
	seen[id] = true

	switch kind {
	case "http":
		return parseHTTPScenario(id, params)
	case "":
		return nil, fmt.Errorf("scenario %q missing kind", id)
	default:
		return nil, fmt.Errorf("unknown scenario kind %q for %q (valid kinds: http)", kind, id)
	}
}

// parseHTTPScenario validates the params mapping for an http scenario,
// rejecting unknown keys, and delegates per-param validation to NewHTTPFault.
func parseHTTPScenario(id string, params *yaml.Node) (*HTTPFault, error) {
	if params == nil {
		return nil, fmt.Errorf("scenario %q: missing params", id)
	}
	if params.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("scenario %q: params must be a mapping", id)
	}
	var status, delay int
	var path string
	for i := 0; i+1 < len(params.Content); i += 2 {
		key := params.Content[i].Value
		val := params.Content[i+1]
		if !httpParamKeys[key] {
			return nil, fmt.Errorf("scenario %q: unknown param %q (valid params: status, delay_seconds, path)", id, key)
		}
		switch key {
		case "status":
			if err := val.Decode(&status); err != nil {
				return nil, fmt.Errorf("scenario %q: param status: %w", id, err)
			}
		case "delay_seconds":
			if err := val.Decode(&delay); err != nil {
				return nil, fmt.Errorf("scenario %q: param delay_seconds: %w", id, err)
			}
		case "path":
			if err := val.Decode(&path); err != nil {
				return nil, fmt.Errorf("scenario %q: param path: %w", id, err)
			}
		}
	}
	fault, err := NewHTTPFault(id, status, delay, path)
	if err != nil {
		return nil, fmt.Errorf("scenario %q: %w", id, err)
	}
	return fault, nil
}
