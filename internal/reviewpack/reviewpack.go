// Package reviewpack builds immutable, deterministic review packs
// (blueprint KERN-P2-001): one evidence packet that can be reviewed by a
// human, one model, or several council members, without re-deriving
// evidence per reviewer. Everything in a pack is derived deterministically
// from the repository, the task text, and the assembled context packet —
// no LLM, no network.
//
// A pack contains: repository commit + dirty-state hash, the task,
// planner-selected evidence with reasons, relevant symbols with call
// paths, changed code, tests, project constraints, observed claims,
// unverified assumptions, and exact token counts. The ContentHash seals
// the pack: two builds over the same state produce byte-identical packs.
package reviewpack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	kernctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lenses"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// SchemaVersion is the review-pack artifact schema version.
const SchemaVersion = 1

// Options configures pack construction.
type Options struct {
	// Lens names the review lens applied to the packet facts before
	// planning ("", "security", "performance", ...). Unknown lens -> error.
	Lens string
	// MaxTokens is the planner budget. <= 0 uses the policy default.
	MaxTokens int
	// MaxSymbols caps the relevant-symbols section (default 12).
	MaxSymbols int
	// DiffBytes caps the raw diff preview (default 8 KiB).
	DiffBytes int
}

// ReviewPack is the immutable artifact. JSON-tagged; field order is stable
// so the content hash is reproducible across builds.
type ReviewPack struct {
	SchemaVersion int          `json:"schema_version"`
	ContentHash   string       `json:"content_hash"`
	GeneratedAt   time.Time    `json:"generated_at"`
	Root          string       `json:"root"`
	Task          string       `json:"task"`
	Lens          string       `json:"lens"`
	Commit        string       `json:"commit"`
	DirtyHash     string       `json:"dirty_hash"`
	ChangedFiles  []string     `json:"changed_files"`
	DiffStat      string       `json:"diff_stat"`
	DiffPreview   string       `json:"diff_preview"`
	Evidence      []Selection  `json:"evidence"`
	Symbols       []SymbolRef  `json:"symbols"`
	Tests         []TestRef    `json:"tests"`
	Constraints   []Constraint `json:"constraints"`
	Claims        []ClaimRef   `json:"claims"`
	Assumptions   []ClaimRef   `json:"assumptions"`
	TokenCount    int          `json:"token_count"`
	Sections      []Section    `json:"sections"`
}

// Selection is a planner-selected evidence item with its reason. It mirrors
// context.Selection's fields so the pack stays aligned with the planner.
type Selection struct {
	Type    string  `json:"type"`
	Source  string  `json:"source"`
	Weight  float64 `json:"weight"`
	Reason  string  `json:"reason"`
	Tokens  int     `json:"tokens"`
	Content string  `json:"content"`
}

// SymbolRef is one relevant symbol with its neighborhood.
type SymbolRef struct {
	Name        string   `json:"name"`
	Qualified   string   `json:"qualified,omitempty"`
	Kind        string   `json:"kind,omitempty"`
	File        string   `json:"file"`
	Line        int      `json:"line"`
	Callers     int      `json:"callers"`
	Callees     int      `json:"callees"`
	BlastRadius int      `json:"blast_radius"`
	BlastFiles  []string `json:"blast_files,omitempty"`
	Path        []string `json:"path,omitempty"`
}

// TestRef references a test gap or a changed test file.
type TestRef struct {
	Symbol  string `json:"symbol,omitempty"`
	Kind    string `json:"kind"` // "gap" or "changed"
	File    string `json:"file"`
	Line    int    `json:"line,omitempty"`
	Callers int    `json:"callers,omitempty"`
}

// Constraint is one project constraint (architecture rule).
type Constraint struct {
	Name    string `json:"name"`
	Rule    string `json:"rule,omitempty"`
	Enabled bool   `json:"enabled"`
}

// ClaimRef is a condensed claim: type, status, statement, evidence count.
type ClaimRef struct {
	Type      string `json:"type"`
	Status    string `json:"status"`
	Statement string `json:"statement"`
	Evidence  int    `json:"evidence"`
}

// Section reports the token count of one rendered pack section.
type Section struct {
	Name   string `json:"name"`
	Tokens int    `json:"tokens"`
}

// Build assembles a review pack from an assembled context packet and an
// index. root is the repository root; task is the task under review.
// pkt may be mutated (lens re-rank + envelope stamping), mirroring the
// planner's contract.
func Build(root, task string, pkt *domain.ContextPacket, ix *index.Index, opts Options) (*ReviewPack, error) {
	if task == "" {
		return nil, fmt.Errorf("reviewpack: task is required")
	}
	if pkt == nil {
		return nil, fmt.Errorf("reviewpack: context packet is required")
	}
	if ix == nil {
		return nil, fmt.Errorf("reviewpack: index is required")
	}
	if opts.MaxSymbols <= 0 {
		opts.MaxSymbols = 12
	}
	if opts.DiffBytes <= 0 {
		opts.DiffBytes = 8 << 10
	}

	p := &ReviewPack{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Root:          root,
		Task:          task,
	}

	// Lens: re-rank packet facts before planning (never drops claims).
	if opts.Lens != "" {
		l, err := lenses.Resolve(opts.Lens)
		if err != nil {
			return nil, err
		}
		pkt.Facts = lenses.ApplyLens(l, pkt.Facts)
		p.Lens = l.Name
	}

	// Planner: selected evidence with machine-generated reasons, budget-fit.
	plan := kernctx.PlanPacket(pkt, task, opts.MaxTokens)
	for _, s := range plan.Selections {
		p.Evidence = append(p.Evidence, Selection{
			Type:    string(s.Type),
			Source:  s.Source,
			Weight:  s.Weight,
			Reason:  s.Reason,
			Tokens:  s.Tokens,
			Content: s.Content,
		})
	}

	// Repository state: commit + dirty-state hash + changed code.
	commit, changed, stat, preview := repoState(root, opts.DiffBytes)
	p.Commit = commit
	p.ChangedFiles = changed
	p.DiffStat = stat
	p.DiffPreview = preview
	p.DirtyHash = dirtyHash(changed, stat)

	// Relevant symbols with call paths.
	p.Symbols = collectSymbols(ix, pkt.Symbols, opts.MaxSymbols)

	// Tests: gaps for the relevant symbols + changed test files.
	p.Tests = collectTests(ix, p.Symbols, changed)

	// Project constraints.
	for _, rule := range pkt.ArchitectureRules {
		p.Constraints = append(p.Constraints, Constraint{
			Name:    rule.Name,
			Rule:    strings.TrimSpace(rule.Rule),
			Enabled: rule.Enabled,
		})
		if len(p.Constraints) >= 10 {
			break
		}
	}

	// Observed claims vs unverified assumptions.
	for _, c := range pkt.Facts {
		ref := ClaimRef{Type: string(c.Type), Status: string(c.Status), Statement: c.Statement, Evidence: len(c.Evidence)}
		if isObserved(c) {
			p.Claims = append(p.Claims, ref)
			if len(p.Claims) >= 20 {
				break
			}
		}
	}
	for _, c := range pkt.Facts {
		if !isObserved(c) {
			p.Assumptions = append(p.Assumptions, ClaimRef{
				Type: string(c.Type), Status: string(c.Status), Statement: c.Statement, Evidence: len(c.Evidence),
			})
			if len(p.Assumptions) >= 10 {
				break
			}
		}
	}

	// Exact token counts: full render, then per-section, then seal.
	p.TokenCount = tokenize.Count(renderBody(p))
	p.Sections = sectionCounts(p)
	p.ContentHash = contentHash(p)
	return p, nil
}

// isObserved reports whether a claim counts as observed (vs unverified
// assumption): observed/verified_derived status, or unclassified with
// supporting evidence.
func isObserved(c domain.Claim) bool {
	switch c.Status {
	case domain.ClaimStatusObserved, domain.ClaimStatusVerifiedDerived:
		return true
	case domain.ClaimStatusReported, domain.ClaimStatusInferred, domain.ClaimStatusStale:
		return false
	}
	return len(c.Evidence) > 0
}

// repoState returns commit hash, changed files, diff stat and a capped raw
// diff preview. Any git failure degrades to empty values (pack stays
// deterministic; non-repo roots produce an empty state).
func repoState(root string, diffBytes int) (commit string, changed []string, stat, preview string) {
	changed, err := intel.ChangedFiles(root)
	if err != nil || len(changed) == 0 {
		return shortCommit(root), nil, "", ""
	}
	sort.Strings(changed)
	if out, err := gitOutput(root, "diff", "HEAD", "--stat"); err == nil {
		stat = strings.TrimSpace(out)
	}
	if out, err := gitOutput(root, "diff", "HEAD"); err == nil {
		preview = capBytes(out, diffBytes)
	}
	return shortCommit(root), changed, stat, preview
}

// shortCommit returns the short HEAD hash, or "" outside a git repo.
func shortCommit(root string) string {
	out, err := gitOutput(root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// gitOutput runs git in root with a 2s timeout and returns combined output.
func gitOutput(root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// dirtyHash seals the dirty state: sha256 over sorted changed file names
// plus the diff stat. "" for a clean tree.
func dirtyHash(changed []string, stat string) string {
	h := sha256.New()
	for _, f := range changed {
		h.Write([]byte(f))
		h.Write([]byte{'\n'})
	}
	h.Write([]byte(stat))
	return hex.EncodeToString(h.Sum(nil))
}

func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... [diff truncated]"
}

// collectSymbols resolves the packet's relevant symbols against the index
// and enriches each with callers/callees/blast radius; call paths link
// consecutive symbols via shortest-path.
func collectSymbols(ix *index.Index, syms []domain.Symbol, max int) []SymbolRef {
	var out []SymbolRef
	hops := 0
	for i, s := range syms {
		if len(out) >= max {
			break
		}
		ref := SymbolRef{Name: s.Name, Qualified: s.Qualified, Kind: s.Kind, File: s.File, Line: s.Line}
		if def, ok := ix.ResolveName(s.Name); ok {
			ref.Kind = def.Kind
			ref.File = def.File
			ref.Line = def.Line
		}
		if rep, err := intel.Explore(ix, s.Name, 1, 25); err == nil {
			ref.Callers = len(rep.Callers)
			ref.Callees = len(rep.Callees)
			ref.BlastRadius = len(rep.BlastRadius)
			ref.BlastFiles = rep.BlastFiles
			if len(ref.BlastFiles) > 5 {
				ref.BlastFiles = ref.BlastFiles[:5]
			}
		}
		// Call path from the previous symbol to this one.
		if i > 0 && hops < 30 {
			path := intel.ShortestPath(ix, syms[i-1].Name, s.Name)
			if len(path) > 1 {
				ref.Path = path
				hops += len(path)
			}
		}
		out = append(out, ref)
	}
	return out
}

// collectTests gathers test gaps for the relevant symbols plus changed test
// files.
func collectTests(ix *index.Index, syms []SymbolRef, changed []string) []TestRef {
	var out []TestRef
	want := make(map[string]bool, len(syms))
	for _, s := range syms {
		want[s.Name] = true
	}
	for _, g := range intel.TestGaps(ix, 100) {
		if want[g.Symbol] {
			out = append(out, TestRef{Symbol: g.Symbol, Kind: "gap", File: g.File, Line: g.Line, Callers: g.Callers})
		}
	}
	for _, f := range changed {
		if strings.HasSuffix(f, "_test.go") || strings.Contains(f, "_test.") {
			out = append(out, TestRef{Kind: "changed", File: f})
		}
	}
	return out
}

// contentHash seals the pack: sha256 over the canonical JSON with the
// ContentHash field blanked and GeneratedAt zeroed (a timestamp is
// provenance metadata, not content — two builds over the same state must
// produce identical hashes).
func contentHash(p *ReviewPack) string {
	cp := *p
	cp.ContentHash = ""
	cp.GeneratedAt = time.Time{}
	b, err := json.Marshal(&cp)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// sectionCounts computes per-section token counts over renderBody. The
// "header" section absorbs the remainder so the section sums equal
// TokenCount exactly.
func sectionCounts(p *ReviewPack) []Section {
	total := tokenize.Count(renderBody(p))
	var secs []Section
	sum := 0
	parts := [][2]string{
		{"evidence", renderEvidence(p)},
		{"symbols", renderSymbols(p)},
		{"tests", renderTests(p)},
		{"constraints", renderConstraints(p)},
		{"claims", renderClaims(p)},
		{"assumptions", renderAssumptions(p)},
	}
	for _, part := range parts {
		n := tokenize.Count("=== " + part[0] + " ===\n" + part[1])
		secs = append(secs, Section{Name: part[0], Tokens: n})
		sum += n
	}
	if r := total - sum; r != 0 {
		secs = append(secs, Section{Name: "header", Tokens: r})
	}
	return secs
}
