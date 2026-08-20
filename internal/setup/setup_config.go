package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// globalConfig resolves a path under the user config dir.
func globalConfig(sub ...string) func(string) string {
	return func(_ string) string {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				home = "."
			}
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(append([]string{base}, sub...)...)
	}
}

// projectConfig resolves a path inside the project root.
func projectConfig(sub ...string) func(string) string {
	return func(root string) string {
		return filepath.Join(append([]string{root}, sub...)...)
	}
}

// homeConfig resolves a path directly under the user home dir (used by agents
// that keep their config at ~/.name rather than under ~/.config).
func homeConfig(sub ...string) func(string) string {
	return func(_ string) string {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		return filepath.Join(append([]string{home}, sub...)...)
	}
}

func homeJoin(rel string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", rel)
	}
	return filepath.Join(home, rel)
}

// mergeJSON reads a JSON (or JSONC) file, inserts entry under key, and writes
// it back. JSONC comments (// line and /* block */) are stripped before
// parsing so machine-generated config like opencode.jsonc that users may
// annotate with comments still loads. A pre-existing "kern" entry is repaired
// (replaced) when it differs from the current entry — e.g. the binary path
// changed — instead of being left stale, and the file is not rewritten when
// nothing changed.
func mergeJSON(path, key string, entry map[string]any) error {
	var m map[string]any
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		cleaned := stripJSONC(data)
		if err := json.Unmarshal(cleaned, &m); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		m = map[string]any{}
	default:
		return err
	}
	if m == nil {
		m = map[string]any{}
	}
	existing, _ := m[key].(map[string]any)
	if existing == nil {
		existing = map[string]any{}
	}
	cur, _ := existing["kern"].(map[string]any)
	if mapsEqual(cur, entry) {
		return nil
	}
	existing["kern"] = entry
	m[key] = existing
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Preserve the original file's permission bits across the rewrite and the
	// backup. Configs often hold tokens and are deliberately 0600; rewriting
	// with a hardcoded 0644 would leak them world-readable. New files default
	// to 0600.
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	// Back up the existing file before rewriting so a corrupting rewrite
	// (e.g. a marshal bug or a permissions race) never destroys the user's
	// original config without recourse. The .bak is a best-effort copy;
	// a missing original (new file) has nothing to back up.
	if prev, perr := os.ReadFile(path); perr == nil && len(prev) > 0 {
		_ = os.WriteFile(path+".bak", prev, mode)
	}
	data, err = json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), mode)
}

// mapsEqual reports whether two JSON-encodable maps are semantically equal
// (marshal sorts keys, so byte equality is order-independent).
func mapsEqual(a, b map[string]any) bool {
	aj, aerr := json.Marshal(a)
	bj, berr := json.Marshal(b)
	return aerr == nil && berr == nil && string(aj) == string(bj)
}

// stripJSONC removes // line comments and /* block */ comments from JSON data,
// while preserving strings that contain those sequences. This is a minimal
// stripper — it does not validate JSON, just removes comment tokens.
func stripJSONC(data []byte) []byte {
	var out []byte
	inString := false
	escape := false
	i := 0
	for i < len(data) {
		c := data[i]
		if inString {
			out = append(out, c)
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				inString = false
			}
			i++
			continue
		}
		// Not inside a string.
		if c == '"' {
			inString = true
			out = append(out, c)
			i++
			continue
		}
		// Block comment: /* ... */
		if i+1 < len(data) && c == '/' && data[i+1] == '*' {
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			i += 2 // skip closing */
			continue
		}
		// Line comment: // ... until end of line
		if c == '/' && i+1 < len(data) && data[i+1] == '/' {
			for i < len(data) && data[i] != '\n' {
				i++
			}
			continue
		}
		out = append(out, c)
		i++
	}
	return out
}

func fileStatus(path, label string) Status {
	b, err := os.ReadFile(path)
	if err != nil {
		return Status{Agent: label, Path: path, Note: "not present"}
	}
	installed := strings.Contains(string(b), "kern")
	note := "kern entry present"
	if !installed {
		note = "file exists but no kern entry"
	}
	return Status{Agent: label, Installed: installed, Path: path, Note: note}
}

func claudeConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude.json")
}

func globalOpencodePath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "opencode", "opencode.jsonc")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "opencode", "opencode.jsonc")
}
