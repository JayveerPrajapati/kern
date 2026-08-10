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
	return os.WriteFile(path, append(data, '\n'), 0o644)
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

// SemanticSearchRepos is SearchRepos with a dense re-rank pass: the pooled
// lexical hits across all registered repos are re-ordered by cosine similarity
// between the query embedding and each symbol descriptor (see SemanticSearch).
// Returns nil when the embedder is unavailable or no repo matches.
func SemanticSearchRepos(query string, limit int, e SymbolEmbedder) []RepoHit {
	if e == nil {
		return SearchRepos(query, limit)
	}
	pool := SearchRepos(query, limit*4)
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

// SearchRepos runs a ranked free-text search across every registered repo and
// returns the best hits with their repo of origin. Repos whose index cannot be
// built are skipped.
func SearchRepos(query string, limit int) []RepoHit {
	if limit <= 0 {
		limit = 20
	}
	reg, err := LoadRepos()
	if err != nil || len(reg.Repos) == 0 {
		return nil
	}
	var hits []RepoHit
	for _, repo := range reg.Repos {
		ix, err := ReadIndex(repo.Root)
		if err != nil || ix == nil {
			continue
		}
		for _, s := range RankedSearch(ix, query, limit) {
			hits = append(hits, RepoHit{Repo: repo.Name, Root: repo.Root, Symbol: s})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
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
