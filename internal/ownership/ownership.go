// Package ownership parses CODEOWNERS files and maps file paths to their
// owning teams/people. It supports both GitHub CODEOWNERS syntax
// (.github/CODEOWNERS or root CODEOWNERS) and Gerrit OWNERS (per-directory
// OWNERS files).
package ownership

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Rule maps a path pattern to its owners. Patterns follow CODEOWNERS
// syntax: "*" matches everything, "src/" matches a directory tree,
// "*.go" matches by extension, "src/foo.go" matches an exact path.
type Rule struct {
	Pattern string   // the path pattern as written in CODEOWNERS
	Owners  []string // @team-handles or email addresses
}

// Map is the parsed ownership map. Lookup(path) returns the owners of
// the most specific matching rule.
type Map struct {
	rules []Rule // ordered by specificity (most specific last in file, first in lookup)
	root  string // repository root
}

// Parse parses a CODEOWNERS file from the given path and returns a Map.
// The root is used to resolve relative paths in the map. A missing file
// returns an empty Map and no error (ownership is optional).
func Parse(codeownersPath, root string) (*Map, error) {
	f, err := os.Open(codeownersPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &Map{root: root}, nil
		}
		return nil, err
	}
	defer f.Close()

	m := &Map{root: root}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		rule := Rule{Pattern: fields[0], Owners: fields[1:]}
		m.rules = append(m.rules, rule)
	}
	return m, scanner.Err()
}

// ParseFromRepo finds and parses CODEOWNERS files in a repository root.
// It checks: .github/CODEOWNERS, CODEOWNERS (root), and docs/CODEOWNERS.
// Returns the first match found, or an empty Map if none exist.
func ParseFromRepo(root string) (*Map, error) {
	candidates := []string{
		filepath.Join(root, ".github", "CODEOWNERS"),
		filepath.Join(root, "CODEOWNERS"),
		filepath.Join(root, "docs", "CODEOWNERS"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return Parse(p, root)
		}
	}
	return &Map{root: root}, nil
}

// Lookup returns the owners of the given file path (relative to root).
// It returns the owners from the most specific matching rule. If no rule
// matches, it returns nil.
func (m *Map) Lookup(path string) []string {
	var bestMatch *Rule
	bestScore := -1
	for i := range m.rules {
		score := matchScore(m.rules[i].Pattern, path)
		if score > bestScore {
			bestScore = score
			bestMatch = &m.rules[i]
		}
	}
	if bestMatch == nil {
		return nil
	}
	return bestMatch.Owners
}

// matchScore returns a specificity score for a pattern against a path.
// Higher = more specific. 0 = no match.
// Patterns:
// "*"              → matches everything, score 1
// "*.go"           → matches by extension, score 2
// "src/"           → matches directory prefix, score 3 + directory depth
// "src/foo.go"     → exact path, score 100 (always wins)
// Directory depth is added to the base so that "src/auth/" is more specific
// than "src/", while any exact path still beats any directory match.
func matchScore(pattern, path string) int {
	if pattern == "*" {
		return 1
	}
	// Extension match (*.go, *.ts)
	if strings.HasPrefix(pattern, "*.") {
		ext := pattern[1:] // ".go"
		if strings.HasSuffix(path, ext) {
			return 2
		}
		return 0
	}
	// Directory prefix (src/ or src/*)
	if strings.HasSuffix(pattern, "/") || strings.HasSuffix(pattern, "/*") {
		dir := strings.TrimSuffix(pattern, "*")
		dir = strings.TrimSuffix(dir, "/")
		if strings.HasPrefix(path, dir+"/") {
			depth := strings.Count(dir, "/") + 1
			return 3 + depth
		}
		return 0
	}
	// Exact path
	if pattern == path {
		return 100
	}
	return 0
}

// OwnersByFile returns a map from file path to owners for the given files.
// Files with no owning rule are omitted from the map.
func (m *Map) OwnersByFile(paths []string) map[string][]string {
	result := make(map[string][]string)
	for _, p := range paths {
		if owners := m.Lookup(p); len(owners) > 0 {
			result[p] = owners
		}
	}
	return result
}

// Teams returns the deduplicated, sorted list of all owner handles in the map.
func (m *Map) Teams() []string {
	seen := map[string]bool{}
	for _, r := range m.rules {
		for _, o := range r.Owners {
			seen[o] = true
		}
	}
	teams := make([]string, 0, len(seen))
	for t := range seen {
		teams = append(teams, t)
	}
	sort.Strings(teams)
	return teams
}
