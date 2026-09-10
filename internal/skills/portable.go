// Package skills portable definitions: user-loadable skills with manifest
// metadata, validation, permission preview, and ed25519 signatures.
package skills

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// Skill is a portable skill definition: a named playbook with optional
// manifest metadata, policies, evals, and examples. Embedded kern skills
// (assets/) and user skills loaded from a directory both fit this shape.
type Skill struct {
	Name        string
	Description string
	Policies    []string      // policy names the skill engages
	Evals       []string      // evaluation/review checklist items
	Examples    []string      // example invocations
	Manifest    SkillManifest // parsed from SKILL.md frontmatter; zero when absent
	Body        string        // markdown playbook body
	Source      string        // "embedded" or the directory path it was loaded from
}

// SkillManifest is the structured frontmatter of a portable SKILL.md.
type SkillManifest struct {
	Version      string
	Author       string
	Permissions  []governance.Permission // permissions the skill requests
	Dependencies []string                // skill names this skill depends on
}

// ParseManifest parses the YAML-ish frontmatter of a SKILL.md body into a
// SkillManifest. Hand-rolled line parsing (stdlib only), mirroring
// ExtractDescriptionAndBody's tolerant style. Supported keys: version,
// author, permissions (list of "resource:action" strings), dependencies
// (list of names). Unknown keys ignored. No frontmatter ("---" opener
// absent) → zero manifest. Malformed permission entries are kept as-is (the
// caller validates via ValidateSkill).
func ParseManifest(data []byte) SkillManifest {
	s := string(data)
	if !strings.HasPrefix(s, "---") {
		return SkillManifest{}
	}
	parts := strings.SplitN(s, "---", 3)
	if len(parts) < 3 {
		return SkillManifest{}
	}
	frontmatter := parts[1]
	var m SkillManifest
	lines := strings.Split(frontmatter, "\n")
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "version:"):
			m.Version = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "version:")), "\"'")
		case strings.HasPrefix(trimmed, "author:"):
			m.Author = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "author:")), "\"'")
		case strings.HasPrefix(trimmed, "permissions:"):
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "permissions:"))
			var items []string
			if val != "" {
				items = splitList(val)
			} else {
				items = collectBlockList(lines, i)
			}
			m.Permissions = append(m.Permissions, toPermissions(items)...)
		case strings.HasPrefix(trimmed, "dependencies:"):
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "dependencies:"))
			var items []string
			if val != "" {
				items = splitList(val)
			} else {
				items = collectBlockList(lines, i)
			}
			m.Dependencies = append(m.Dependencies, items...)
		}
	}
	return m
}

// splitList splits a comma-separated inline list value.
func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// collectBlockList gathers "- item" lines following the key at line index i.
// An empty or indented line is tolerated; a non-indented line that is not a
// list item (the next top-level key, or the closing "---") ends the block.
func collectBlockList(lines []string, i int) []string {
	var out []string
	for _, line := range lines[i+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue // indented non-item: tolerate
		}
		break // new top-level key or closing ---
	}
	return out
}

// toPermissions converts "resource:action" entries to governance.Permission.
// Malformed entries are kept as-is (empty Resource or Action) so the caller's
// ValidateSkill can flag them.
func toPermissions(items []string) []governance.Permission {
	out := make([]governance.Permission, 0, len(items))
	for _, item := range items {
		res, act, ok := strings.Cut(item, ":")
		if !ok {
			out = append(out, governance.Permission{Resource: item})
			continue
		}
		out = append(out, governance.Permission{Resource: strings.TrimSpace(res), Action: strings.TrimSpace(act)})
	}
	return out
}

// LoadSkillsFromDir loads user-defined skills from dir: every immediate
// subdirectory containing a SKILL.md becomes a Skill. Name = the
// subdirectory name; Description + Body via ExtractDescriptionAndBody;
// Manifest via ParseManifest. Skills with unreadable SKILL.md are skipped
// (not fatal); a missing dir is an error. Sorted by Name.
func LoadSkillsFromDir(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		data, err := os.ReadFile(filepath.Join(dir, name, "SKILL.md"))
		if err != nil {
			continue // unreadable SKILL.md: skip, not fatal
		}
		desc, body := ExtractDescriptionAndBody(data)
		out = append(out, Skill{
			Name:        name,
			Description: desc,
			Body:        body,
			Manifest:    ParseManifest(data),
			Source:      dir,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ValidateSkill checks a skill's manifest and shape. Rules (deterministic):
// Name non-empty (required — a skill must be referenceable); when the
// manifest is present (any field set): Version if set must be non-whitespace,
// Author if set must be non-whitespace, every Permission must have
// non-empty Resource AND Action, every Dependency must be a non-empty name.
// Body may be empty (a skill can be a pure manifest). Returns nil when valid.
func ValidateSkill(s Skill) error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("skills: skill name is required")
	}
	m := s.Manifest
	present := m.Version != "" || m.Author != "" || len(m.Permissions) > 0 || len(m.Dependencies) > 0
	if !present {
		return nil
	}
	if m.Version != "" && strings.TrimSpace(m.Version) == "" {
		return errors.New("skills: manifest version must be non-whitespace when set")
	}
	if m.Author != "" && strings.TrimSpace(m.Author) == "" {
		return errors.New("skills: manifest author must be non-whitespace when set")
	}
	for i, p := range m.Permissions {
		if strings.TrimSpace(p.Resource) == "" || strings.TrimSpace(p.Action) == "" {
			return fmt.Errorf("skills: permission %d must have non-empty resource and action", i)
		}
	}
	for _, d := range m.Dependencies {
		if strings.TrimSpace(d) == "" {
			return errors.New("skills: dependency names must be non-empty")
		}
	}
	return nil
}

// PreviewPermissions returns the permissions a skill requests, deduplicated
// by "resource|action" (first occurrence wins, stable order).
func PreviewPermissions(s Skill) []governance.Permission {
	var out []governance.Permission
	seen := map[string]bool{}
	for _, p := range s.Manifest.Permissions {
		key := p.Resource + "|" + p.Action
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out
}

// Signature attests a skill's canonical payload with ed25519.
type Signature struct {
	PublicKey string `json:"public_key"` // base64 ed25519 public key
	Value     string `json:"value"`      // base64 ed25519 signature
	Timestamp string `json:"timestamp"`  // RFC3339 UTC
}

// SignSkill signs a skill's canonical payload with an ed25519 private key
// (ed25519.GenerateKey/NewKeyFromSeed supported). Errors on nil key or empty
// name. Timestamp = time.Now().UTC().Format(time.RFC3339).
func SignSkill(s Skill, key ed25519.PrivateKey) (Signature, error) {
	if key == nil {
		return Signature{}, errors.New("skills: nil signing key")
	}
	if strings.TrimSpace(s.Name) == "" {
		return Signature{}, errors.New("skills: skill name is required")
	}
	pub, ok := key.Public().(ed25519.PublicKey)
	if !ok || len(pub) != ed25519.PublicKeySize {
		return Signature{}, errors.New("skills: invalid signing key")
	}
	sig := ed25519.Sign(key, canonicalSkillPayload(s))
	return Signature{
		PublicKey: base64.StdEncoding.EncodeToString(pub),
		Value:     base64.StdEncoding.EncodeToString(sig),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// VerifySkill verifies a signature against a skill's canonical payload.
// Errors: nil signature fields, base64 decode failure, or ed25519.Verify
// failure ("skills: signature does not match skill").
func VerifySkill(s Skill, sig Signature) error {
	if sig.PublicKey == "" || sig.Value == "" {
		return errors.New("skills: signature is incomplete")
	}
	pub, err := base64.StdEncoding.DecodeString(sig.PublicKey)
	if err != nil {
		return fmt.Errorf("skills: decode signature public key: %w", err)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(sig.Value)
	if err != nil {
		return fmt.Errorf("skills: decode signature: %w", err)
	}
	// Size guard: ed25519.Verify panics on a wrong-sized public key, so a
	// malformed key must be rejected as a verification failure instead.
	if len(pub) != ed25519.PublicKeySize || len(sigBytes) != ed25519.SignatureSize {
		return errors.New("skills: signature does not match skill")
	}
	if !ed25519.Verify(pub, canonicalSkillPayload(s), sigBytes) {
		return errors.New("skills: signature does not match skill")
	}
	return nil
}

// canonicalSkillPayload returns the deterministic JSON bytes a signature
// covers: Name, Description, Policies, Evals, Examples, Manifest, Body.
// Body is INCLUDED so a signature detects any tampering with the playbook,
// not just the contract fields. json.Marshal of a struct with these fields
// (exported, in that order) is deterministic.
func canonicalSkillPayload(s Skill) []byte {
	data, err := json.Marshal(struct {
		Name        string
		Description string
		Policies    []string
		Evals       []string
		Examples    []string
		Manifest    SkillManifest
		Body        string
	}{
		Name:        s.Name,
		Description: s.Description,
		Policies:    s.Policies,
		Evals:       s.Evals,
		Examples:    s.Examples,
		Manifest:    s.Manifest,
		Body:        s.Body,
	})
	if err != nil {
		return nil
	}
	return data
}
