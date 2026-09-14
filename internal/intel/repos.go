package intel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// Repo is one registered project in the multi-repo registry.
type Repo struct {
	Name  string `json:"name"`
	Root  string `json:"root"`
	Added string `json:"added"`
}

// RepoRegistry persists a list of registered projects.
type RepoRegistry struct {
	Repos []Repo `json:"repos"`
}

func reposPath() string {
	return cache.Path("repos.json")
}

// LoadRepos reads the registry (an empty registry if none exists yet).
func LoadRepos() (*RepoRegistry, error) {
	r := &RepoRegistry{}
	b, err := os.ReadFile(reposPath())
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return r, nil
	}
	if err := json.Unmarshal(b, r); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", reposPath(), err)
	}
	return r, nil
}

func (r *RepoRegistry) Save() error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	path := reposPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Atomic write (temp file + rename) so a concurrent reader never observes
	// a partially-written registry.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Add registers a project, replacing any repo with the same name.
func (r *RepoRegistry) Add(root, name string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("cannot add %s: %w", abs, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("cannot add %s: not a directory", abs)
	}
	if name == "" {
		name = filepath.Base(abs)
	}
	out := make([]Repo, 0, len(r.Repos)+1)
	for _, existing := range r.Repos {
		if existing.Name != name {
			out = append(out, existing)
		}
	}
	out = append(out, Repo{Name: name, Root: abs, Added: time.Now().Format(time.RFC3339)})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	r.Repos = out
	return nil
}

// Remove unregisters a repo by name. Returns false if not present.
func (r *RepoRegistry) Remove(name string) bool {
	out := make([]Repo, 0, len(r.Repos))
	found := false
	for _, existing := range r.Repos {
		if existing.Name == name {
			found = true
			continue
		}
		out = append(out, existing)
	}
	if found {
		r.Repos = out
	}
	return found
}

// Get returns a registered repo by name.
func (r *RepoRegistry) Get(name string) (Repo, bool) {
	for _, existing := range r.Repos {
		if existing.Name == name {
			return existing, true
		}
	}
	return Repo{}, false
}

// RepoHit is a symbol match with the repo it came from.
type RepoHit struct {
	Repo   string       `json:"repo"`
	Root   string       `json:"root"`
	Symbol index.Symbol `json:"symbol"`
	Score  int          `json:"score"`
}

// DiscoverSubrepos scans root and subdirectories up to depth 3 for .git repositories
// and standalone project workspaces.
func DiscoverSubrepos(root string) []Repo {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	var repos []Repo
	seen := make(map[string]bool)

	// Check if root itself is a repo
	if isRepoDir(absRoot) {
		name := filepath.Base(absRoot)
		repos = append(repos, Repo{Name: name, Root: absRoot, Added: time.Now().Format(time.RFC3339)})
		seen[absRoot] = true
	}

	// Walk subdirectories up to depth 3
	_ = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path == absRoot {
			return nil
		}

		name := d.Name()
		// Skip non-workspace/cache dirs
		if name == ".git" || name == ".kern" || name == "node_modules" || name == "vendor" ||
			name == ".venv" || name == "venv" || name == "dist" || name == "build" ||
			name == "target" || name == "bin" || name == ".cache" || name == "__pycache__" {
			return filepath.SkipDir
		}

		// Calculate relative depth from absRoot
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}
		depth := strings.Count(filepath.ToSlash(rel), "/") + 1
		if depth > 3 {
			return filepath.SkipDir
		}

		if isRepoDir(path) && !seen[path] {
			seen[path] = true
			repoName := filepath.Base(path)
			repos = append(repos, Repo{
				Name:  repoName,
				Root:  path,
				Added: time.Now().Format(time.RFC3339),
			})
			return filepath.SkipDir
		}

		return nil
	})

	return repos
}

func isRepoDir(path string) bool {
	// Check for .git dir/file or .kern dir
	gitPath := filepath.Join(path, ".git")
	if st, err := os.Stat(gitPath); err == nil {
		if st.IsDir() || st.Mode().IsRegular() {
			return true
		}
	}
	kernPath := filepath.Join(path, ".kern")
	if st, err := os.Stat(kernPath); err == nil && st.IsDir() {
		return true
	}
	// Check for common project manifest files
	for _, m := range []string{"go.mod", "pom.xml", "package.json", "Cargo.toml", "pyproject.toml", "build.gradle"} {
		if _, err := os.Stat(filepath.Join(path, m)); err == nil {
			return true
		}
	}
	return false
}

// FederatedRepos returns the union of registered repos from repos.json and
// any discovered subproject workspaces in root and cwd.
func FederatedRepos(root string) []Repo {
	var all []Repo
	seen := make(map[string]bool)

	// 1. Registered repos
	reg, err := LoadRepos()
	if err == nil && reg != nil {
		for _, r := range reg.Repos {
			abs, err := filepath.Abs(r.Root)
			if err == nil {
				if !seen[abs] {
					seen[abs] = true
					all = append(all, r)
				}
			}
		}
	}

	// 2. Discover in root if provided
	if root != "" {
		for _, r := range DiscoverSubrepos(root) {
			abs, err := filepath.Abs(r.Root)
			if err == nil && !seen[abs] {
				seen[abs] = true
				all = append(all, r)
			}
		}
	}

	// 3. Discover in current working directory
	if cwd, err := os.Getwd(); err == nil && cwd != root {
		for _, r := range DiscoverSubrepos(cwd) {
			abs, err := filepath.Abs(r.Root)
			if err == nil && !seen[abs] {
				seen[abs] = true
				all = append(all, r)
			}
		}
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all
}

// SemanticSearchRepos is SearchRepos with a dense re-rank pass: the pooled
// lexical hits across all registered repos are re-ordered by cosine similarity
// between the query embedding and each symbol descriptor (see SemanticSearch).
// Returns nil when no repo matches.
func SemanticSearchRepos(query string, limit int, e SymbolEmbedder) []RepoHit {
	return SemanticSearchReposIn(".", query, limit, e)
}

// SemanticSearchReposIn runs semantic search across all federated repositories in root.
func SemanticSearchReposIn(root string, query string, limit int, e SymbolEmbedder) []RepoHit {
	if e == nil {
		return SearchReposIn(root, query, limit)
	}
	pool := SearchReposIn(root, query, limit*4)
	if len(pool) == 0 {
		return nil
	}
	qvec, err := e.EmbedText(query)
	if err != nil {
		return truncateRepoHits(pool, limit)
	}
	type scored struct {
		h    RepoHit
		cos  float64
		rank int
	}
	var list []scored
	for rank, h := range pool {
		vec, err := embedCached(e, symbolDescriptor(h.Symbol))
		if err != nil {
			continue
		}
		list = append(list, scored{h: h, cos: denseCosine(qvec, vec), rank: rank})
	}
	if len(list) == 0 {
		return truncateRepoHits(pool, limit)
	}
	const rrfK = 60
	type acc struct {
		h    RepoHit
		scor float64
	}
	byKey := map[string]acc{}
	for i, sc := range list {
		key := sc.h.Root + "|" + symbolKey(sc.h.Symbol)
		a := byKey[key]
		a.h = sc.h
		a.scor += 1 / (rrfK + float64(i) + 1)
		a.scor += 1 / (rrfK + float64(sc.rank) + 1)
		byKey[key] = a
	}
	out := make([]RepoHit, 0, len(byKey))
	for _, a := range byKey {
		out = append(out, a.h)
	}
	sort.Slice(out, func(i, j int) bool {
		ki, kj := out[i].Root+"|"+symbolKey(out[i].Symbol), out[j].Root+"|"+symbolKey(out[j].Symbol)
		if byKey[ki].scor != byKey[kj].scor {
			return byKey[ki].scor > byKey[kj].scor
		}
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		if out[i].Symbol.FullName() != out[j].Symbol.FullName() {
			return out[i].Symbol.FullName() < out[j].Symbol.FullName()
		}
		return out[i].Symbol.File < out[j].Symbol.File
	})
	return truncateRepoHits(out, limit)
}

func truncateRepoHits(in []RepoHit, limit int) []RepoHit {
	if limit >= len(in) {
		return in
	}
	return in[:limit]
}

// SearchRepos runs a ranked free-text search across every federated repo and
// returns the best hits with their repo of origin.
func SearchRepos(query string, limit int) []RepoHit {
	return SearchReposIn(".", query, limit)
}

// SearchReposIn runs ranked search across every registered repo and discovered
// subproject in root.
func SearchReposIn(root string, query string, limit int) []RepoHit {
	if limit <= 0 {
		limit = 20
	}
	repos := FederatedRepos(root)
	if len(repos) == 0 {
		return nil
	}
	var hits []RepoHit
	for _, repo := range repos {
		ix, err := ReadIndex(repo.Root)
		if err != nil || ix == nil {
			ix, err = index.LoadOrBuild(repo.Root)
			if err != nil || ix == nil {
				continue
			}
		}
		for _, rh := range RankedSearchScored(ix, query, limit) {
			rh.Repo = repo.Name
			rh.Root = repo.Root
			hits = append(hits, rh)
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Repo != hits[j].Repo {
			return hits[i].Repo < hits[j].Repo
		}
		if hits[i].Symbol.FullName() != hits[j].Symbol.FullName() {
			return hits[i].Symbol.FullName() < hits[j].Symbol.FullName()
		}
		return hits[i].Symbol.File < hits[j].Symbol.File
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// RepoNames lists registered repo names.
func RepoNames(reg *RepoRegistry) []string {
	names := make([]string, 0, len(reg.Repos))
	for _, r := range reg.Repos {
		names = append(names, r.Name)
	}
	return names
}

func repoHitString(h RepoHit) string {
	s := h.Symbol
	return fmt.Sprintf("%-12s %-10s %-7s %-24s %s:%d", h.Repo, s.Kind, s.Lang, s.FullName(), s.File, s.Line)
}

// FormatRepoHits renders cross-repo search results for the terminal.
func FormatRepoHits(hits []RepoHit) string {
	var b strings.Builder
	for _, h := range hits {
		b.WriteString(repoHitString(h))
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}
