// Package projectstate resolves whether a repository explicitly opted into
// kern's project-local guard. It centralizes one policy that setup, the Go
// fallback guard, and doctor must agree on: explicit config consent is
// preferred, legacy index presence remains compatible, and a valid explicit
// opt-out always wins.
package projectstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/configstrip"
)

// Source explains why a project was classified as managed or unmanaged.
type Source string

const (
	SourceExplicit  Source = "explicit"
	SourceLegacy    Source = "legacy-index"
	SourceDisabled  Source = "disabled"
	SourceUnmanaged Source = "unmanaged"
	SourceMalformed Source = "malformed"
)

// State is the resolved project-management classification.
type State struct {
	Managed bool
	Source  Source
	Root    string
	Path    string
	Err     error
}

// Result is the doctor-shaped diagnostic result without importing doctor.
type Result struct {
	Check  string
	Level  string
	Detail string
}

// Inspect walks root and its ancestors. A nearest valid .kern/config.json is
// authoritative: managed+guard.enabled means managed, anything else means an
// explicit opt-out. When no valid marker exists, the historical index files
// remain a compatibility signal. A malformed marker never disables a project
// that still has a legacy index; doctor reports the malformed file separately.
func Inspect(root string) State {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	var legacy string
	var malformedPath string
	var malformedErr error
	for {
		config := filepath.Join(abs, ".kern", "config.json")
		if data, err := os.ReadFile(config); err == nil {
			managed, perr := allowsGuard(data)
			if perr == nil {
				if managed {
					return State{Managed: true, Source: SourceExplicit, Root: abs, Path: config}
				}
				return State{Managed: false, Source: SourceDisabled, Root: abs, Path: config}
			}
			malformedPath, malformedErr = config, perr
		} else if !errors.Is(err, fs.ErrNotExist) {
			return State{Source: SourceUnmanaged, Root: abs, Path: config, Err: err}
		}
		for _, name := range []string{"index.sqlite", "index.json"} {
			p := filepath.Join(abs, ".kern", name)
			if _, err := os.Stat(p); err == nil {
				if legacy == "" {
					legacy = p
				}
				break
			}
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}
	if legacy != "" {
		state := State{Managed: true, Source: SourceLegacy, Root: filepath.Dir(filepath.Dir(legacy)), Path: legacy}
		if malformedErr != nil {
			state.Err = fmt.Errorf("%s: %w", malformedPath, malformedErr)
		}
		return state
	}
	if malformedErr != nil {
		return State{Source: SourceMalformed, Root: filepath.Dir(filepath.Dir(malformedPath)), Path: malformedPath, Err: malformedErr}
	}
	return State{Source: SourceUnmanaged}
}

// allowsGuard reports whether a config document explicitly enables project
// management and the guard. JSONC is accepted because .kern/config.json is a
// user-editable configuration surface.
func allowsGuard(data []byte) (bool, error) {
	var cfg struct {
		SchemaVersion any `json:"schema_version"`
		Managed       any `json:"managed"`
		Guard         *struct {
			Enabled any `json:"enabled"`
		} `json:"guard"`
	}
	if err := json.Unmarshal(configstrip.StripJSONC(data), &cfg); err != nil {
		return false, err
	}
	managed, ok := cfg.Managed.(bool)
	if !ok {
		return false, errors.New("managed must be a boolean")
	}
	if cfg.Guard == nil {
		return false, errors.New("guard must be an object")
	}
	enabled, ok := cfg.Guard.Enabled.(bool)
	if !ok {
		return false, errors.New("guard.enabled must be a boolean")
	}
	return managed && enabled, nil
}

// Diagnostic renders the managed-project state in doctor's result shape
// without importing doctor (that would create a package cycle).
func Diagnostic(root string) Result {
	state := Inspect(root)
	switch state.Source {
	case SourceExplicit:
		return Result{Check: "project-management", Level: "ok", Detail: "explicit managed-project marker: " + state.Path}
	case SourceLegacy:
		detail := "legacy kern index inferred project management: " + state.Path + " — run `kern setup` to write .kern/config.json"
		if state.Err != nil {
			detail += "; malformed marker: " + state.Err.Error()
		}
		return Result{Check: "project-management", Level: "warn", Detail: detail}
	case SourceDisabled:
		return Result{Check: "project-management", Level: "ok", Detail: "project guard explicitly disabled: " + state.Path}
	case SourceMalformed:
		return Result{Check: "project-management", Level: "fail", Detail: "malformed managed-project marker: " + state.Err.Error()}
	default:
		if state.Err != nil {
			return Result{Check: "project-management", Level: "fail", Detail: state.Err.Error()}
		}
		return Result{Check: "project-management", Level: "ok", Detail: "project not managed by kern"}
	}
}

// EnsureMarker creates or upgrades .kern/config.json without overwriting an
// explicit opt-out. It returns the config path and whether the file changed.
func EnsureMarker(root string) (string, bool, error) {
	dir := filepath.Join(root, ".kern")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false, err
	}
	path := filepath.Join(dir, "config.json")
	mode := fs.FileMode(0o600)
	var raw map[string]any
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		raw = map[string]any{}
	case err != nil:
		return path, false, err
	default:
		if err := json.Unmarshal(configstrip.StripJSONC(data), &raw); err != nil {
			return path, false, err
		}
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
	}
	if raw == nil {
		raw = map[string]any{}
	}
	changed := false
	if _, ok := raw["schema_version"]; !ok {
		raw["schema_version"] = 1
		changed = true
	}
	if _, ok := raw["managed"].(bool); !ok {
		raw["managed"] = true
		changed = true
	}
	guard, ok := raw["guard"].(map[string]any)
	if !ok {
		if _, exists := raw["guard"]; exists {
			return path, false, errors.New("guard must be an object")
		}
		guard = map[string]any{}
		raw["guard"] = guard
		changed = true
	}
	if _, ok := guard["enabled"].(bool); !ok {
		guard["enabled"] = true
		changed = true
	}
	if !changed {
		return path, false, nil
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return path, false, err
	}
	out = append(out, '\n')
	if err := os.WriteFile(path, out, mode); err != nil {
		return path, false, err
	}
	return path, true, nil
}
