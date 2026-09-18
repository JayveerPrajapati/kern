// Package risk classifies a ChangeRequest into a risk level (P1.3). The
// classification feeds the two-person approval gate: high-risk changes from
// agent sources require a human-approved request before the file gate passes.
//
// Risk is a pure function of the change surface: sensitive paths, diff size,
// and destructive operations. It carries NO policy decision — the approval
// check decides whether a level requires approval from risk.Config.
package risk

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// Level is the classified risk of a change.
type Level string

const (
	LevelLow    Level = "low"
	LevelMedium Level = "medium"
	LevelHigh   Level = "high"
)

// Indicator is one machine-readable trigger that raised the risk level.
type Indicator struct {
	Kind   string // "sensitive-path" | "large-diff" | "destructive-op"
	Detail string
	File   string
}

// Assessment is the result of classifying one ChangeRequest.
type Assessment struct {
	Level      Level
	Reasons    []string // human-readable triggers
	Indicators []Indicator
}

// Config tunes classification and the approval requirement.
type Config struct {
	// SensitivePathPatterns are glob patterns (path.Match plus "**" which
	// crosses directory separators) matched against each file path. A match
	// raises risk to at least medium. Defaults cover .kern/, key material,
	// auth/, credentials and secrets files.
	SensitivePathPatterns []string
	// MaxDiffLines is the total added+removed line threshold above which a
	// diff is large. 0/negative falls back to the default (500).
	MaxDiffLines int
	// RequireApprovalFor lists the risk levels that require approval. A level
	// NOT listed never needs approval (e.g. ["high"] means medium-risk human
	// changes pass the gate without a request).
	RequireApprovalFor []string
	// RequireForSources lists the change sources the gate applies to. A
	// change from a source NOT listed passes the gate without consulting the
	// approval store (default ["agent"]: humans are the approvers).
	RequireForSources []string
}

// DefaultConfig returns the conservative built-in tuning.
func DefaultConfig() Config {
	return Config{
		SensitivePathPatterns: []string{
			".kern/**",
			"**/*.pem",
			"**/*.key",
			"auth/**",
			"**/credentials*",
			"**/secrets*",
		},
		MaxDiffLines:       500,
		RequireApprovalFor: []string{string(LevelHigh)},
		RequireForSources:  []string{string(domain.SourceAgent)},
	}
}

// Classify evaluates a ChangeRequest and returns its risk Assessment.
//
// Rules (conservative defaults, overridable via Config):
//   - any file matching a sensitive-path glob           -> at least medium
//   - total diff lines > MaxDiffLines                    -> at least medium
//   - any OpDelete                                       -> at least medium
//   - any of the above with Source == "agent"            -> high
//   - otherwise                                          -> low
//
// Source is the escalation trigger: a human is assumed to have reviewed their
// own change; an agent change on a sensitive surface needs a second person.
func Classify(req domain.ChangeRequest, cfg Config) Assessment {
	cfg = withDefaults(cfg)

	var as Assessment
	add := func(kind, detail, file string, reason string) {
		as.Indicators = append(as.Indicators, Indicator{Kind: kind, Detail: detail, File: file})
		as.Reasons = append(as.Reasons, reason)
	}

	// 1. Sensitive paths.
	for _, fc := range req.Files {
		p := filepath.ToSlash(fc.Path)
		p = strings.TrimPrefix(p, "./")
		for _, pat := range cfg.SensitivePathPatterns {
			if matchesPath(pat, p) {
				add("sensitive-path", pat, fc.Path,
					fmt.Sprintf("path %q matches sensitive pattern %q", fc.Path, pat))
				break // one indicator per file is enough
			}
		}
	}

	// 2. Large diff.
	totalDiff := 0
	for _, fc := range req.Files {
		totalDiff += len(fc.Added) + len(fc.Removed)
	}
	if totalDiff > cfg.MaxDiffLines {
		add("large-diff", fmt.Sprintf("%d lines (max %d)", totalDiff, cfg.MaxDiffLines), "",
			fmt.Sprintf("diff is %d lines, exceeding the %d-line threshold", totalDiff, cfg.MaxDiffLines))
	}

	// 3. Destructive operations.
	for _, fc := range req.Files {
		if fc.Op == domain.OpDelete {
			add("destructive-op", "delete", fc.Path, fmt.Sprintf("deletes %q", fc.Path))
		}
	}

	level := LevelLow
	if len(as.Indicators) > 0 {
		level = LevelMedium
		if req.Source == domain.SourceAgent {
			level = LevelHigh
		}
	}
	as.Level = level
	return as
}

// RequiresApproval reports whether the level is listed in RequireApprovalFor.
// A level absent from the list (or an empty list) never requires approval.
func RequiresApproval(level Level, requireFor []string) bool {
	for _, l := range requireFor {
		if l == string(level) {
			return true
		}
	}
	return false
}

// SourceRequiresApproval reports whether the change source is subject to the
// gate. An empty list means the gate is off for every source; a source not
// listed passes without consulting the approval store.
func SourceRequiresApproval(src domain.Source, requireFor []string) bool {
	for _, s := range requireFor {
		if s == string(src) {
			return true
		}
	}
	return false
}

// withDefaults fills zero-valued Config fields with the conservative defaults.
func withDefaults(cfg Config) Config {
	if len(cfg.SensitivePathPatterns) == 0 {
		cfg.SensitivePathPatterns = DefaultConfig().SensitivePathPatterns
	}
	if cfg.MaxDiffLines <= 0 {
		cfg.MaxDiffLines = DefaultConfig().MaxDiffLines
	}
	if len(cfg.RequireApprovalFor) == 0 {
		cfg.RequireApprovalFor = DefaultConfig().RequireApprovalFor
	}
	if len(cfg.RequireForSources) == 0 {
		cfg.RequireForSources = DefaultConfig().RequireForSources
	}
	return cfg
}

// matchesPath reports whether pattern matches path at the repository root or
// at ANY directory level (fail-safe bias: a sensitive directory name — auth/,
// .kern/ — is sensitive no matter where it sits in the tree, so
// "auth/**" matches both "auth/tokens.json" and "api/auth/tokens.json").
func matchesPath(pattern, path string) bool {
	if globMatch(pattern, path) {
		return true
	}
	for i := 0; i < len(path); i++ {
		if path[i] == '/' && globMatch(pattern, path[i+1:]) {
			return true
		}
	}
	return false
}

// globMatch reports whether name matches pattern. It supports the stdlib
// path.Match syntax plus "**" which crosses directory separators (so
// "**/*.pem" matches "a/b/secret.pem" and ".kern/**" matches everything under
// .kern/). "**/" matches zero or more complete path segments (gitignore
// semantics). Implemented recursively with no external dependencies; simple
// patterns are delegated to path.Match.
func globMatch(pattern, name string) bool {
	// Fast path: no "**" -> stdlib path.Match (documented limitation: "*"
	// does not cross "/", which is what we want for non-globstar patterns).
	if !strings.Contains(pattern, "**") {
		ok, err := path.Match(pattern, name)
		return err == nil && ok
	}
	// "**/X": zero or more directories before X.
	if strings.HasPrefix(pattern, "**/") {
		rest := pattern[3:]
		if globMatch(rest, name) {
			return true
		}
		for i := 0; i < len(name); i++ {
			if name[i] == '/' && globMatch(pattern, name[i+1:]) {
				return true
			}
		}
		return false
	}
	// Bare "**" at the start: matches any prefix, including separators.
	if strings.HasPrefix(pattern, "**") {
		rest := pattern[2:]
		for i := 0; i <= len(name); i++ {
			if globMatch(rest, name[i:]) {
				return true
			}
		}
		return false
	}
	// A fixed prefix precedes the first "**": it must match a leading slice
	// of name; the rest recurses.
	idx := strings.Index(pattern, "**")
	prefix := pattern[:idx]
	for i := 0; i <= len(name); i++ {
		if ok, _ := path.Match(prefix, name[:i]); ok {
			if globMatch(pattern[idx:], name[i:]) {
				return true
			}
		}
	}
	return false
}
