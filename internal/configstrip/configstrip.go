// Package configstrip holds the pure text transforms that remove kern-owned
// entries from agent config files (JSON/JSONC and TOML). Every function takes
// the file text and returns the new text, whether anything changed, and an
// error when the file cannot be safely edited (callers then leave it alone).
package configstrip

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// StripJSONC removes // line comments and /* block */ comments from JSON data,
// while preserving strings that contain those sequences. This is a minimal
// stripper — it does not validate JSON, just removes comment tokens.
func StripJSONC(data []byte) []byte {
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

func decodeObject(text string) (map[string]any, error) {
	clean := StripJSONC([]byte(text))
	if len(bytes.TrimSpace(clean)) == 0 {
		return nil, errors.New("file is empty")
	}
	dec := json.NewDecoder(bytes.NewReader(clean))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if m == nil {
		return nil, errors.New("not a JSON object")
	}
	return m, nil
}

func encodeObject(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return ""
	}
	return buf.String()
}

// ServerKey returns a stripper that removes the "kern" server entry from m[key].
func ServerKey(key string) func(string) (string, bool, error) {
	return func(text string) (string, bool, error) {
		m, err := decodeObject(text)
		if err != nil {
			return "", false, err
		}
		servers, _ := m[key].(map[string]any)
		if _, ok := servers["kern"]; !ok {
			return text, false, nil
		}
		delete(servers, "kern")
		if len(servers) == 0 {
			delete(m, key)
		}
		return encodeObject(m), true, nil
	}
}

// kernHookCmdRe matches the commands kern's hook writers emit.
var kernHookCmdRe = regexp.MustCompile(`(?:^|[\s"'/\\])kern(?:\.exe)?["']?\s+hook\s|\bhook\s+(?:claude-post|claude-prompt|gemini-after|gemini-prompt)\b`)

func isKernHookEntry(h map[string]any) bool {
	cmd, _ := h["command"].(string)
	name, _ := h["name"].(string)
	return strings.Contains(cmd, "kern-guard.sh") || kernHookCmdRe.MatchString(cmd) || strings.HasPrefix(name, "kern-")
}

// Hooks removes kern-owned hook commands from settings.hooks.<event>[],
// dropping groups, events and the hooks object itself when they end up empty.
func Hooks(text string) (string, bool, error) {
	m, err := decodeObject(text)
	if err != nil {
		return "", false, err
	}
	hooks, _ := m["hooks"].(map[string]any)
	changed := false
	for event, v := range hooks {
		groups, ok := v.([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(groups))
		for _, g := range groups {
			gm, ok := g.(map[string]any)
			if !ok {
				kept = append(kept, g)
				continue
			}
			inner, ok := gm["hooks"].([]any)
			if !ok {
				kept = append(kept, g)
				continue
			}
			left := make([]any, 0, len(inner))
			for _, h := range inner {
				if hm, ok := h.(map[string]any); ok && isKernHookEntry(hm) {
					changed = true
					continue
				}
				left = append(left, h)
			}
			if len(left) == len(inner) {
				kept = append(kept, g)
				continue
			}
			if len(left) > 0 {
				gm["hooks"] = left
				kept = append(kept, gm)
			}
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
	if !changed {
		return text, false, nil
	}
	if len(hooks) == 0 {
		delete(m, "hooks")
	}
	return encodeObject(m), true, nil
}

// AntigravityHook removes the top-level "kern-guard" key.
func AntigravityHook(text string) (string, bool, error) {
	m, err := decodeObject(text)
	if err != nil {
		return "", false, err
	}
	if _, ok := m["kern-guard"]; !ok {
		return text, false, nil
	}
	delete(m, "kern-guard")
	return encodeObject(m), true, nil
}

// CodexTOML removes the [mcp_servers.kern*] tables. The
// `[features] codex_hooks = true` flag is left alone: the installer only adds
// it when absent, and a user may rely on it.
func CodexTOML(text string) (string, bool, error) {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	skipping, changed := false, false
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if skipping {
			if !strings.HasPrefix(t, "[") {
				continue
			}
			skipping = false
		}
		if t == "[mcp_servers.kern]" || strings.HasPrefix(t, "[mcp_servers.kern.") {
			skipping, changed = true, true
			continue
		}
		out = append(out, ln)
	}
	if !changed {
		return text, false, nil
	}
	rest := strings.Trim(strings.Join(out, "\n"), "\r\n")
	if rest == "[features]\ncodex_hooks = true" {
		return "", true, nil // kern created the whole file
	}
	if rest != "" {
		rest += "\n"
	}
	return rest, true, nil
}
