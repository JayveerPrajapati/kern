// Package tokstats is the token-stats/savings family extracted from
// internal/index (Phase 4 of the architecture campaign): the TokenStats
// type, the single SavingsPercent formula, the file-token-count cache and
// the TokenSavingsFor{Graph,Context,Neighborhood} render helpers. It
// depends only on stdlib and internal/tokenize — it never imports
// internal/index, so any subsystem can render honest savings footers
// without dragging the index in. Signatures are narrowed at the call sites:
// the graph/context helpers take a root dir plus file paths/strings instead
// of *index.Index.
package tokstats

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// TokenStats records the token count of the full context versus the compressed
// graph/context representation, so callers can display a savings summary.
// Baseline labels what the denominator (FullContext) actually measures — the
// thing the savings percentage is relative to — so a reader can never mistake
// a single-file baseline for an all-files one (finding V1): every rendered
// percentage carries its baseline.
type TokenStats struct {
	FullContext   int    `json:"full_context_tokens"`
	CompactTokens int    `json:"compact_tokens"`
	SavingsPct    int    `json:"savings_percent"`
	Source        string `json:"source,omitempty"`   // "graph" or "context"
	Baseline      string `json:"baseline,omitempty"` // what FullContext measures, e.g. "3 files read raw"
}

// baselineLabel returns the denominator label to render with a percentage,
// falling back to the source name so no "%" is ever printed without a
// denominator (L2/honest reporting).
func (t TokenStats) baselineLabel() string {
	if t.Baseline != "" {
		return t.Baseline
	}
	if t.Source != "" {
		return t.Source
	}
	return "input"
}

// Summary renders a one-line token-savings summary, or "" when no savings
// apply (e.g. an empty or non-positive full-context count). The percentage is
// always annotated with its denominator ("vs 3 files read raw"), and the
// no-savings guard keeps the "compact includes metadata" note instead of a
// bare "0% saved" claim.
func (t TokenStats) Summary() string {
	if t.FullContext <= 0 {
		return ""
	}
	if t.CompactTokens >= t.FullContext {
		return fmt.Sprintf("tokens: %s %d → %d (0%% vs %s; compact includes metadata)",
			t.Source, t.FullContext, t.CompactTokens, t.baselineLabel())
	}
	return fmt.Sprintf("tokens: %s %d → %d (%d%% vs %s)",
		t.Source, t.FullContext, t.CompactTokens, t.SavingsPct, t.baselineLabel())
}

// SavingsPercent is the SINGLE token-savings formula for the TokenStats
// family (finding L2): the integer-truncated percentage of tokens removed
// when compact replaces full, floor((full - compact) / full * 100). full <= 0
// yields 0 so a missing/empty denominator never produces a nonsense value.
// Every savings renderer (TokenStatsFromCounts, intel's savingsPct, the
// savings-report golden) delegates here so no two surfaces can drift.
func SavingsPercent(full, compact int) int {
	if full <= 0 {
		return 0
	}
	return int(float64(full-compact) / float64(full) * 100)
}

// ComputeTokenSavings compares tokens in the concatenated source files of all
// graph nodes against the compact text form (graph JSON or context text). The
// optional baseline labels the denominator for the rendered percentage (the
// report's golden passes the naive all-files baseline).
func ComputeTokenSavings(fullText, compact, source string, baseline ...string) TokenStats {
	base := ""
	if len(baseline) > 0 {
		base = baseline[0]
	}
	return TokenStatsFromCounts(tokenize.Count(fullText), tokenize.Count(compact), source, base)
}

// TokenStatsFromCounts assembles a TokenStats from already-computed token
// counts. Kept separate from ComputeTokenSavings so cached file-token counts
// (see fileTokenCount) can build the footer without re-reading or re-tokenizing
// the definition file on every query.
func TokenStatsFromCounts(fullTokens, compactTokens int, source, baseline string) TokenStats {
	return TokenStats{
		FullContext:   fullTokens,
		CompactTokens: compactTokens,
		SavingsPct:    SavingsPercent(fullTokens, compactTokens),
		Source:        source,
		Baseline:      baseline,
	}
}

// FilesReadRaw labels a multi-file denominator: the token count of n distinct
// source files read in full (the naive paste a compact output replaces).
func FilesReadRaw(n int) string {
	if n == 1 {
		return "1 file read raw"
	}
	return fmt.Sprintf("%d files read raw", n)
}

// savingsCacheKey identifies one cached file-token count: the absolute file
// path plus the file's mtime AND size at the moment it was tokenized (V2,
// deep-dive B2: mtime alone misses a same-mtime edit; size catches the common
// case for free on the same stat call). mtime+size is the invalidation
// signal — the same trust model reuseByMtime uses — so a content edit that
// changes neither is a residual cosmetic risk on a savings FOOTER only (the
// token counts themselves are BPE, not a chars/4 heuristic).
type savingsCacheKey struct {
	path  string
	mtime int64
	size  int64
}

// savingsCache is a small bounded memo of definition-file token counts used by
// the token-savings footers. Computing one costs a file read plus a full
// BPE tokenize (~35ms on real files) purely for a cosmetic footer, so the
// count is cached until the file's mtime or size changes. The compact-text
// half of the savings computation is query-specific and cheap, so it is never
// cached.
type savingsCache struct {
	mu      sync.Mutex
	entries map[savingsCacheKey]int
}

// savingsCacheCap bounds the memo. Entries are evicted arbitrarily (any live
// entry repopulates on the next miss), keeping memory bounded without the
// complexity of an LRU.
const savingsCacheCap = 64

// fileTokenSavingsCache is the process-wide memo. It is safe to share across
// indexes: a file's token count is a pure function of the file on disk, and
// the key includes the absolute path, so projects never collide.
var fileTokenSavingsCache = &savingsCache{entries: map[savingsCacheKey]int{}}

// fileTokenCount returns the token count of the file at path, memoized by
// (path, mtime, size). A missing or unreadable file counts as 0, matching the
// previous behavior of TokenSavingsForGraph (a failed read yielded nil
// fullData). The stat is redone on every call, so an edit that bumps the
// mtime or changes the size is observed on the next query; the read +
// tokenize happens only on a miss. The stat/read pair is not atomic, but a
// concurrent edit between them can only leave an unreachable stale entry
// (the next call stats the new mtime/size and misses it), never a wrong hit.
func fileTokenCount(path string) int {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	var mtime, size int64
	if err == nil {
		mtime = fi.ModTime().UnixNano()
		size = fi.Size()
	}
	key := savingsCacheKey{path: path, mtime: mtime, size: size}
	fileTokenSavingsCache.mu.Lock()
	if n, ok := fileTokenSavingsCache.entries[key]; ok {
		fileTokenSavingsCache.mu.Unlock()
		return n
	}
	fileTokenSavingsCache.mu.Unlock()

	// Miss: read + tokenize outside the lock so concurrent queries never
	// serialize on the expensive part.
	data, rerr := os.ReadFile(path)
	n := 0
	if rerr == nil {
		n = tokenize.Count(string(data))
	}
	fileTokenSavingsCache.mu.Lock()
	if len(fileTokenSavingsCache.entries) >= savingsCacheCap {
		for k := range fileTokenSavingsCache.entries {
			delete(fileTokenSavingsCache.entries, k)
			break
		}
	}
	fileTokenSavingsCache.entries[key] = n
	fileTokenSavingsCache.mu.Unlock()
	return n
}

// TokenSavingsForGraph computes token savings for the Graph() text output,
// comparing the compact graph against the full source of every DISTINCT node
// definition file the graph references (the definitions' files plus the files
// that define its callers and callees — what the graph's "all node sources"
// claim covers; finding V1). The denominator is labeled with the file count so
// the percentage can never be read as a single-file baseline. Per-file token
// counts come from the mtime-keyed fileTokenCount cache, so summing N files
// costs N cached lookups plus at most N real reads. root is the index root
// the (root-relative) file paths resolve against; the caller collects the
// distinct node files and passes them in.
func TokenSavingsForGraph(root string, files []string, compact string) TokenStats {
	fullTokens := 0
	seen := map[string]bool{}
	for _, f := range files {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		fullTokens += fileTokenCount(filepath.Join(root, f))
	}
	return TokenStatsFromCounts(fullTokens, tokenize.Count(compact), "graph", FilesReadRaw(len(seen)))
}

// TokenSavingsForContext computes token savings for the Context() text output.
// The denominator is the SINGLE definition file read raw (a context slice
// covers one symbol's definition window), labeled with the file name so it is
// never mistaken for an all-files baseline. defFile is root-relative.
func TokenSavingsForContext(root, defFile, compact string) TokenStats {
	var fullData []byte
	baseline := ""
	if defFile != "" {
		var err error
		fullData, err = os.ReadFile(filepath.Join(root, defFile))
		if err != nil {
			fullData = nil
		} else {
			baseline = "file read raw (" + defFile + ")"
		}
	}
	return ComputeTokenSavings(string(fullData), compact, "context", baseline)
}

// TokenSavingsForNeighborhood computes token savings for the Neighborhood JSON,
// comparing the compact JSON against the full source files of all referenced
// nodes, labeled with the distinct file count. files must be the distinct
// node files in NODE ORDER (the same walk the original index method used): the
// full-text baseline concatenates them, and a different order can change the
// BPE count when a token spans a file boundary.
func TokenSavingsForNeighborhood(root string, files []string, compact string) TokenStats {
	var fullText strings.Builder
	seen := map[string]bool{}
	for _, f := range files {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		if data, err := os.ReadFile(filepath.Join(root, f)); err == nil {
			fullText.Write(data)
		}
	}
	return ComputeTokenSavings(fullText.String(), compact, "neighborhood", FilesReadRaw(len(seen)))
}

// TokenStatsPanel renders the token-savings line injected into the exported
// graph HTML (self-contained stats panel below the top bar). It returns ""
// for an empty full context — the same no-savings rules as Summary.
func TokenStatsPanel(s TokenStats) string {
	if s.FullContext <= 0 {
		return ""
	}
	pct := s.SavingsPct
	note := ""
	if s.CompactTokens >= s.FullContext {
		pct = 0
		note = "; compact includes metadata"
	}
	return fmt.Sprintf(`<span style="color:#94a3b8;font-size:11px">\u21d2 %s %d → %d tokens (%d%% vs %s%s)</span>`,
		s.Source, s.FullContext, s.CompactTokens, pct, s.baselineLabel(), note)
}
