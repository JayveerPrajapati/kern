// Package ignore implements gitignore-style ignore matching for kern's own
// walks: a .kernignore file takes precedence over .gitignore, and the union
// of both (plus kern's hardcoded defaults) decides what kern pack and kern
// project map skip. Matching is pure Go, deterministic, and needs no git
// binary. Both root-level and nested ignore files are honored with git's
// subtree semantics: a rule in directory d applies only under d, and deeper
// rules (both ignores and negations) take precedence over shallower ones.
package ignore

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type rule struct {
	negated  bool
	dirOnly  bool
	anchored bool
	re       *regexp.Regexp
}

// scopedRule is one compiled pattern together with the slash-relative
// directory it lives in ("" = the ignore file's root).
type scopedRule struct {
	dir string
	rule
}

// Matcher is a compiled set of ignore rules from every directory in the tree.
type Matcher struct {
	rules []scopedRule
}

// maxRules bounds a pathological repository full of ignore files.
const maxRules = 2000

// Load reads every .gitignore and .kernignore under root (root first, then
// deeper directories in walk order) and compiles their rules. Within a
// directory .kernignore is applied after .gitignore so it takes precedence
// (last match wins). VCS directories are never descended into. Files that
// cannot be read are skipped; the returned matcher ignores nothing when no
// rules were found.
func Load(root string) *Matcher {
	m := &Matcher{}
	abs, err := filepath.Abs(root)
	if err != nil {
		return m
	}
	_ = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path != abs && vcsDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}
		if name != ".gitignore" && name != ".kernignore" {
			return nil
		}
		if len(m.rules) >= maxRules {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		dir := ""
		if d := filepath.Dir(path); d != abs {
			if rel, rerr := filepath.Rel(abs, d); rerr == nil {
				dir = filepath.ToSlash(rel)
			}
		}
		for _, line := range strings.Split(string(data), "\n") {
			if r, ok := parseLine(line); ok {
				m.rules = append(m.rules, scopedRule{dir: dir, rule: r})
			}
		}
		return nil
	})
	return m
}

// vcsDirs are never descended into, matching kern's hardcoded defaults.
var vcsDirs = map[string]bool{".git": true, ".hg": true, ".svn": true, ".kern": true}

// parseLine compiles one ignore line into a rule. Returns ok=false for blank
// lines and comments. Trailing CR is stripped so CRLF ignore files work.
func parseLine(line string) (rule, bool) {
	line = strings.TrimSuffix(line, "\r")
	if line == "" || strings.HasPrefix(line, "#") {
		return rule{}, false
	}
	r := rule{}
	if strings.HasPrefix(line, `\#`) {
		line = line[1:]
	}
	if strings.HasPrefix(line, "!") {
		r.negated = true
		line = line[1:]
	}
	if line == "" {
		return rule{}, false
	}
	if strings.HasPrefix(line, "/") {
		r.anchored = true
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		r.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	hasSlash := strings.Contains(line, "/")
	if line == "" {
		return rule{}, false
	}
	body := globToRegexp(line)
	prefix := "(?:^|.*/)"
	suffix := "(?:/|$)"
	switch {
	case r.anchored, hasSlash:
		// " /foo " or a slash anywhere anchors to the ignore file's root.
		prefix = "^"
		suffix = end(r.dirOnly)
	}
	// Use Compile (not MustCompile): a malformed ignore line (e.g. "[]" or
	// "[z-a]") must not panic the whole indexer. Drop the bad rule instead.
	re, err := regexp.Compile(prefix + body + suffix)
	if err != nil {
		return rule{}, false
	}
	r.re = re
	return r, true
}

func end(dirOnly bool) string {
	if dirOnly {
		return "(?:/|$)"
	}
	return "$"
}

// globToRegexp translates the gitignore glob subset (*, **, ?, [..], backslash
// escapes) into an anchored regexp body.
func globToRegexp(p string) string {
	var b strings.Builder
	i := 0
	for i < len(p) {
		switch c := p[i]; c {
		case '*':
			if i+1 < len(p) && p[i+1] == '*' {
				// ** is only special when followed by '/' (matches zero or
				// more complete path segments). In any other position git
				// treats ** as two consecutive * wildcards, which is
				// equivalent to a single * (matches within one segment).
				if i+2 < len(p) && p[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 3
					continue
				}
				// ** without a trailing / collapses to a single * per git:
				// it matches within a single path segment, never across '/'.
				b.WriteString("[^/]*")
				i++
				continue
			}
			b.WriteString("[^/]*")
			i++
		case '?':
			b.WriteString("[^/]")
			i++
		case '[':
			close := strings.IndexByte(p[i:], ']')
			if close < 0 {
				b.WriteString(`\[`)
				i++
				continue
			}
			// Glob negation [!...] (and its git synonym [^...]) must become
			// the regexp class [^...]; otherwise '!' stays a literal member
			// and the class means the opposite of the pattern.
			body := p[i+1 : i+close]
			if strings.HasPrefix(body, "!") || strings.HasPrefix(body, "^") {
				body = "^" + body[1:]
			}
			b.WriteString("[" + body + "]")
			i += close + 1
		case '\\':
			if i+1 < len(p) {
				b.WriteString(regexp.QuoteMeta(string(p[i+1])))
				i += 2
			} else {
				b.WriteString(`\\`)
				i++
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
			i++
		}
	}
	return b.String()
}

// Ignored reports whether a root-relative path (slash-separated) is excluded.
// A rule only applies to paths inside its own directory (subtree semantics).
// Directory-only patterns exclude every path beneath the matching directory.
// The last matching rule wins, so a deeper negation re-includes an earlier
// ignore (and a deeper ignore beats a shallower one).
// Git semantics: a negation pattern ("!…") cannot re-include a file if any of
// its ancestor directories is excluded by an earlier rule, because git does
// not descend into excluded directories. This prevents "build/keep.go" from
// being re-included when "build/" is ignored.
func (m *Matcher) Ignored(rel string) bool {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
	if rel == "" {
		return false
	}
	ignored := false
	for _, sr := range m.rules {
		sub, ok := under(sr.dir, rel)
		if !ok || !sr.re.MatchString(sub) {
			continue
		}
		if sr.negated && ignored {
			// Git cannot re-include a file under an excluded directory.
			if m.ancestorExcluded(rel) {
				continue
			}
		}
		ignored = !sr.negated
	}
	return ignored
}

// ancestorExcluded reports whether any ancestor directory of rel is excluded
// by a non-negated rule. In git, excluded directories are not descended into,
// so a negation pattern cannot re-include files beneath them.
func (m *Matcher) ancestorExcluded(rel string) bool {
	for {
		slash := strings.LastIndexByte(rel, '/')
		if slash <= 0 {
			return false
		}
		rel = rel[:slash] // parent directory
		for _, sr := range m.rules {
			if sr.negated {
				continue
			}
			sub, ok := under(sr.dir, rel)
			if ok && sr.re.MatchString(sub) {
				return true
			}
		}
	}
}

// under returns rel relativized to dir (dir "" means root) and whether rel
// lives inside dir.
func under(dir, rel string) (string, bool) {
	if dir == "" {
		return rel, true
	}
	if rel == dir {
		return "", true
	}
	if strings.HasPrefix(rel, dir+"/") {
		return rel[len(dir)+1:], true
	}
	return "", false
}
