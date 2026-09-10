// Package profiles provides deterministic output profiles that shape how
// evidence is presented without changing the evidence itself. No LLM, no I/O.
package profiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// OutputProfile shapes how evidence is presented without changing the
// evidence itself. Profiles are deterministic (no LLM).
type OutputProfile struct {
	Name     string // "machine-json" | "action-first" | "human-readable" | "debug"
	Style    string // "json" | "action-first" | "plain" | "debug"
	Format   string // "json" | "text" | "markdown"
	Language string // informational language hint ("en")
}

// Registry holds named output profiles.
type Registry struct {
	profiles map[string]OutputProfile
}

// NewRegistry creates an empty output-profile registry.
func NewRegistry() *Registry {
	return &Registry{profiles: map[string]OutputProfile{}}
}

// Register adds a profile under its Name. It errors on an empty name or a
// duplicate registration.
func (r *Registry) Register(p OutputProfile) error {
	if p.Name == "" {
		return fmt.Errorf("profile: empty name")
	}
	if _, ok := r.profiles[p.Name]; ok {
		return fmt.Errorf("profile %q already registered", p.Name)
	}
	r.profiles[p.Name] = p
	return nil
}

// Select returns the profile registered under name, and false when unknown.
func (r *Registry) Select(name string) (OutputProfile, bool) {
	p, ok := r.profiles[name]
	return p, ok
}

// List returns the registered profile names, sorted.
func (r *Registry) List() []string {
	names := make([]string, 0, len(r.profiles))
	for n := range r.profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// MachineJSONProfile wraps content in a machine-readable JSON envelope.
func MachineJSONProfile() OutputProfile {
	return OutputProfile{Name: "machine-json", Style: "json", Format: "json", Language: "en"}
}

// ActionFirstProfile reorders action-verb lines to the top of the content.
func ActionFirstProfile() OutputProfile {
	return OutputProfile{Name: "action-first", Style: "action-first", Format: "text", Language: "en"}
}

// HumanReadableProfile is the identity profile: content passes through
// unchanged (the MCP default path).
func HumanReadableProfile() OutputProfile {
	return OutputProfile{Name: "human-readable", Style: "plain", Format: "markdown", Language: "en"}
}

// DebugProfile prefixes content with a diagnostic header line.
func DebugProfile() OutputProfile {
	return OutputProfile{Name: "debug", Style: "debug", Format: "text", Language: "en"}
}

// NewRegistryWithBuiltins returns a registry pre-populated with the four
// built-in profiles: machine-json, action-first, human-readable, debug.
func NewRegistryWithBuiltins() *Registry {
	r := NewRegistry()
	_ = r.Register(MachineJSONProfile())
	_ = r.Register(ActionFirstProfile())
	_ = r.Register(HumanReadableProfile())
	_ = r.Register(DebugProfile())
	return r
}

// LoadFile reads a JSON array of OutputProfile from path. Every entry must
// have a non-empty Name and Style; a violation errors naming the file and the
// offending entry index. A missing or unreadable file is an error.
func LoadFile(path string) ([]OutputProfile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ps []OutputProfile
	if err := json.Unmarshal(b, &ps); err != nil {
		return nil, fmt.Errorf("profiles: parse %s: %w", path, err)
	}
	for i, p := range ps {
		if p.Name == "" {
			return nil, fmt.Errorf("profiles: %s entry %d: empty name", path, i+1)
		}
		if p.Style == "" {
			return nil, fmt.Errorf("profiles: %s entry %d: empty style", path, i+1)
		}
	}
	return ps, nil
}

// NewRegistryWithUserProfiles returns a registry with the built-ins plus any
// user profiles loaded from <root>/.kern/profiles.json. A user profile that
// names a built-in overrides it (the user wins); a missing or unreadable
// profiles file yields the built-ins only, with a nil error.
func NewRegistryWithUserProfiles(root string) *Registry {
	r := NewRegistryWithBuiltins()
	ps, err := LoadFile(filepath.Join(root, ".kern", "profiles.json"))
	if err != nil {
		return r
	}
	for _, p := range ps {
		if err := r.Register(p); err != nil {
			// LoadFile guarantees a non-empty Name, so the only Register
			// error here is a duplicate of a built-in: override it.
			r.profiles[p.Name] = p
		}
	}
	return r
}

// actionVerbs are the line-start verbs action-first reorders to the top.
var actionVerbs = []string{
	"create", "fix", "update", "delete", "remove", "add", "run", "build",
	"test", "deploy", "merge", "rename", "move", "copy", "install", "configure",
}

// ApplyProfile shapes content WITHOUT compression (pure shaping; the MCP
// default path must stay byte-identical when no profile is requested, so
// human-readable is identity):
//
//	json:          marshal {"profile": name, "format": format, "content": content}
//	action-first:  deterministic line reorder (action verbs to the top,
//	               "---" separator, rest in original order)
//	plain:         identity (content returned unchanged)
//	debug:         "profile: <name> style=<style> format=<format> language=<language> tokens=<n>\n"
//	               prefix then the content unchanged
//	unknown style: identity
func ApplyProfile(p OutputProfile, content string) string {
	switch p.Style {
	case "json":
		data, err := json.Marshal(struct {
			Profile string `json:"profile"`
			Format  string `json:"format"`
			Content string `json:"content"`
		}{Profile: p.Name, Format: p.Format, Content: content})
		if err != nil {
			return content
		}
		return string(data)
	case "action-first":
		return reorderActionFirst(content)
	case "debug":
		return fmt.Sprintf("profile: %s style=%s format=%s language=%s tokens=%d\n%s",
			p.Name, p.Style, p.Format, p.Language, tokenize.Count(content), content)
	default: // "plain" and unknown styles: identity
		return content
	}
}

// reorderActionFirst moves lines whose trimmed content (after an optional
// leading "-", "*", or "> ") starts with an action verb to the top, in
// original relative order, followed by a "---" separator, then the remaining
// lines in original order. With no action lines, content is returned
// unchanged.
func reorderActionFirst(content string) string {
	lines := strings.Split(content, "\n")
	var actions, rest []string
	for _, line := range lines {
		if isActionLine(line) {
			actions = append(actions, line)
		} else {
			rest = append(rest, line)
		}
	}
	if len(actions) == 0 {
		return content
	}
	reordered := append(append([]string{}, actions...), "---")
	reordered = append(reordered, rest...)
	return strings.Join(reordered, "\n")
}

// isActionLine reports whether the line's trimmed content (after an optional
// leading "-", "*", or "> ") starts with an action verb, case-insensitive,
// with a word boundary after the verb.
func isActionLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "-")
	trimmed = strings.TrimPrefix(trimmed, "*")
	trimmed = strings.TrimPrefix(trimmed, "> ")
	trimmed = strings.TrimSpace(trimmed)
	lower := strings.ToLower(trimmed)
	for _, v := range actionVerbs {
		if strings.HasPrefix(lower, v) {
			rest := lower[len(v):]
			if rest == "" || !isLetter(rest[0]) {
				return true
			}
		}
	}
	return false
}

// isLetter reports whether b is an ASCII letter (word-boundary check).
func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
