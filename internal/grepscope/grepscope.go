// Package grepscope decides whether a grep invocation can be answered from
// kern's symbol index scoped to one file or directory, and matches
// index-relative symbol paths against that scope. It is the single rule set
// behind two surfaces: internal/guardrule's serve plan (Tier 1/2 carry the
// scope so the plugin/CLI can answer a scoped grep from the index) and the
// `kern search --path` filter. Extracted from guardrule when that package hit
// its ARCHITECTURE.md LOC cap.
//
// The rules are deliberately strict: a scope that the index cannot honor
// exactly (globs, absolute paths, parent escapes, include/exclude filters,
// count/list/context flags) is NOT scoped-served — the caller falls back to a
// real sandboxed grep rather than answering with symbols from the wrong files.
package grepscope

import (
	"path"
	"strings"
)

// SymbolShaped reports whether s is a plausible symbol name — the same
// predicate as the opencode plugin's SYMBOL_SHAPED_RE. Regexes, punctuation
// and prose patterns are not symbol-shaped and never take the index path.
func SymbolShaped(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		ok := r == '_' || r == '.' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return false
		}
		if i == 0 && (r == '.' || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// SplitArgs splits grep-family args into the pattern (first non-flag
// argument) and the remaining non-flag arguments (file/directory targets).
// A flag takes no value in this model; flags whose value would be mistaken
// for the pattern (-e, -f, -A/-B/-C) are rejected by FlagsOK before any
// scoped serve is chosen.
func SplitArgs(args []string) (pattern string, targets []string) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") && a != "-" {
			continue
		}
		if pattern == "" {
			pattern = a
			continue
		}
		targets = append(targets, a)
	}
	return pattern, targets
}

// allowedLong lists the long grep flags whose semantics a ranked symbol
// search preserves. Anything else (--include/--exclude change the file set,
// --files-with-matches/--count change the shape) forces a real grep.
var allowedLong = map[string]bool{
	"--recursive":      true,
	"--line-number":    true,
	"--ignore-case":    true,
	"--no-ignore-case": true,
	"--no-messages":    true,
}

const allowedShort = "rRniIsHh"

// FlagsOK reports whether every grep flag is one the symbol index can honor.
func FlagsOK(args []string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") || a == "-" || a == "--" {
			continue
		}
		if strings.HasPrefix(a, "--") {
			if !allowedLong[a] {
				return false
			}
			continue
		}
		for _, c := range a[1:] {
			if !strings.ContainsRune(allowedShort, c) {
				return false
			}
		}
	}
	return true
}

// CleanScope validates a grep target as a plain repo-relative path (file or
// directory) and returns it cleaned with forward slashes. Globs, ~, absolute
// paths, parent escapes and shell metacharacters are refused: the index
// filter cannot reproduce their semantics, so the caller must fall back.
// "." is accepted and means the whole repo.
func CleanScope(target string) (string, bool) {
	if target == "" || target == "-" || strings.HasPrefix(target, "-") {
		return "", false
	}
	if strings.HasPrefix(target, "~") || path.IsAbs(target) {
		return "", false
	}
	if strings.ContainsAny(target, "*?[{\\`$\x00") {
		return "", false
	}
	clean := path.Clean(target)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return strings.TrimPrefix(clean, "./"), true
}

// Scoped reports whether args are a symbol-shaped grep with exactly one safe
// path target and only index-honorable flags. Multiple targets are refused:
// a filter on one of them would silently drop the others' matches.
func Scoped(args []string) (pattern, scope string, ok bool) {
	pattern, targets := SplitArgs(args)
	if !SymbolShaped(pattern) || len(targets) != 1 || !FlagsOK(args) {
		return "", "", false
	}
	scope, ok = CleanScope(targets[0])
	if !ok {
		return "", "", false
	}
	return pattern, scope, true
}

// Matches reports whether an index-relative symbol file is inside scope.
// An empty or "." scope matches every file.
func Matches(file, scope string) bool {
	if scope == "" || scope == "." {
		return true
	}
	f := strings.TrimPrefix(path.Clean(file), "./")
	return f == scope || strings.HasPrefix(f, scope+"/")
}
