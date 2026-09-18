package retrieval

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/evidence"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// Level is the progressive disclosure tier: each level adds detail on top of
// the previous one.
type Level int

const (
	L1 Level = 1 // index summary: names/kinds/token costs
	L2 Level = 2 // neighborhood: callers/callees/tests/files
	L3 Level = 3 // source: verbatim context
)

// Options configures a Retrieve call.
type Options struct {
	Query     string               // L1: search query (required for L1)
	Symbol    string               // L2/L3: symbol name (required)
	Level     Level                // requested disclosure level
	Limit     int                  // L1 result cap (default 10, 0→10)
	Depth     int                  // L2 blast-radius depth (0 = unlimited)
	MaxNodes  int                  // L2 node cap (0 = unlimited)
	Lines     int                  // L3 context lines around definition (default 12, 0→12)
	MaxTokens int                  // render budget (0 = no fitting)
	Embedder  intel.SymbolEmbedder // optional semantic rerank (nil = deterministic only)
}

// L1Item is one search hit from the index summary level.
type L1Item struct {
	Handle     *Handle `json:"handle"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	File       string  `json:"file"`
	Line       int     `json:"line"`
	TokenCost  int     `json:"token_cost"`
	Confidence float64 `json:"confidence"`
}

// Detail is the L2 neighborhood of a symbol: its direct call flow and the
// files its blast radius touches.
type Detail struct {
	Handle  *Handle  `json:"handle"`
	Callers []string `json:"callers"`
	Callees []string `json:"callees"`
	Tests   []string `json:"tests"` // callees living in *_test.go files
	Files   []string `json:"files"` // distinct blast files
}

// Source is the L3 verbatim context of a symbol.
type Source struct {
	Handle *Handle `json:"handle"`
	Text   string  `json:"text"`
}

// Result is the outcome of a Retrieve call at one disclosure level.
type Result struct {
	Level     Level    `json:"level"`
	Query     string   `json:"query,omitempty"`
	Items     []L1Item `json:"items,omitempty"`
	Detail    *Detail  `json:"detail,omitempty"`
	Source    *Source  `json:"source,omitempty"`
	Tokens    int      `json:"tokens"`              // tokenize.Count of rendered text
	Truncated bool     `json:"truncated,omitempty"` // true when MaxTokens forced trimming
	MaxTokens int      `json:"-"`                   // render budget (0 = no fitting); Render fits to it
}

// DefaultRegistry is the package-level handle registry: L1 retrieval registers
// every returned handle here so a later MCP resolve step can map a handle ID
// back to the symbol it describes.
var DefaultRegistry = NewRegistry()

// Retrieve fetches the requested disclosure level for opts.Query (L1) or
// opts.Symbol (L2/L3) against ix.
func Retrieve(ix *index.Index, opts Options) (*Result, error) {
	if ix == nil {
		return nil, fmt.Errorf("index is required")
	}
	switch opts.Level {
	case L1:
		return retrieveL1(ix, opts)
	case L2:
		return retrieveL2(ix, opts)
	case L3:
		return retrieveL3(ix, opts)
	default:
		return nil, fmt.Errorf("unsupported level: %d", opts.Level)
	}
}

// parseLevel maps a planner retrieval level ("l1"|"l2"|"l3") to a Level.
// Unknown strings default to L2 (matches planner.RetrievalLevelFor's default).
func parseLevel(s string) Level {
	switch s {
	case "l1":
		return L1
	case "l3":
		return L3
	default:
		return L2
	}
}

// RetrieveForTask retrieves the planner-configured disclosure level for a
// task type against a symbol: the level comes from
// context.RetrievalLevelFor (documentation→l1, refactor→l3, everything else
// l2). L1 is search-based, so the symbol is used as the query; L2/L3 retrieve
// the symbol's neighborhood/source directly. budget <= 0 keeps all content.
// An unknown symbol errors with "symbol %q not found in index".
func RetrieveForTask(ix *index.Index, symbol, taskType string, budget int) (*Result, error) {
	level := parseLevel(context.RetrievalLevelFor(context.TaskType(taskType)))
	opts := Options{
		Symbol:    symbol,
		Level:     level,
		MaxTokens: budget,
	}
	if level == L1 {
		opts.Query = symbol
	}
	res, err := Retrieve(ix, opts)
	if err != nil {
		if strings.Contains(err.Error(), "unknown symbol") || strings.Contains(err.Error(), "not found") {
			return nil, fmt.Errorf("symbol %q not found in index", symbol)
		}
		return nil, err
	}
	if level == L1 && len(res.Items) == 0 {
		return nil, fmt.Errorf("symbol %q not found in index", symbol)
	}
	return res, nil
}

// confidenceScore maps the parser's string confidence tiers to a numeric
// 0.0-1.0 score so Handle/L1Item can carry a float64 confidence. This is the
// single documented deviation from the spec: index.Symbol.Confidence is the
// string type index.Confidence ("HIGH"/"MEDIUM"/"LOW"), not a float64, so the
// direct assignment the spec calls for cannot compile. The mapping mirrors
// kern's evidence confidence convention (Certain = 1.0).
func confidenceScore(c index.Confidence) float64 {
	switch c {
	case index.ConfidenceHigh:
		return 1.0
	case index.ConfidenceMedium:
		return 0.5
	case index.ConfidenceLow:
		return 0.2
	default:
		return 0.0
	}
}

func retrieveL1(ix *index.Index, opts Options) (*Result, error) {
	if strings.TrimSpace(opts.Query) == "" {
		return nil, fmt.Errorf("query is required for level 1")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	var syms []index.Symbol
	if opts.Embedder != nil {
		syms = intel.SemanticSearch(ix, opts.Query, limit, opts.Embedder)
	} else {
		syms = intel.RankedSearch(ix, opts.Query, limit)
	}
	r := &Result{Level: L1, Query: opts.Query}
	for _, s := range syms {
		tokenCost := tokenize.Count(s.Name + " " + s.Kind + " " + s.File)
		contentHash := evidence.Digest(ix.Context(s.Name, 0))
		h := NewHandle(TypeSymbol, s.Name, s.File, s.Line, tokenCost, confidenceScore(s.Confidence), contentHash)
		DefaultRegistry.Register(h)
		r.Items = append(r.Items, L1Item{
			Handle:     h,
			Name:       s.Name,
			Kind:       s.Kind,
			File:       s.File,
			Line:       s.Line,
			TokenCost:  tokenCost,
			Confidence: confidenceScore(s.Confidence),
		})
	}
	r.Tokens = tokenize.Count(Render(r))
	r.MaxTokens = opts.MaxTokens
	return r, nil
}

func retrieveL2(ix *index.Index, opts Options) (*Result, error) {
	if strings.TrimSpace(opts.Symbol) == "" {
		return nil, fmt.Errorf("symbol is required for level %d", L2)
	}
	rep, err := intel.Explore(ix, opts.Symbol, opts.Depth, opts.MaxNodes)
	if err != nil {
		return nil, err
	}
	d := &Detail{
		Callers: rep.Callers,
		Callees: rep.Callees,
		Files:   rep.BlastFiles,
	}
	for _, c := range rep.Callees {
		if isTestCallee(ix, c) {
			d.Tests = append(d.Tests, c)
		}
	}
	var sb strings.Builder
	tokenCost := writeL2Body(&sb, opts.Symbol, d)
	h := NewHandle(TypeSymbol, opts.Symbol, rep.Definition.File, rep.Definition.Line,
		tokenCost, confidenceScore(rep.Definition.Confidence), evidence.Digest(rep.Source))
	d.Handle = h
	DefaultRegistry.Register(h)
	r := &Result{Level: L2, Detail: d}
	r.Tokens = tokenize.Count(Render(r))
	r.MaxTokens = opts.MaxTokens
	return r, nil
}

func retrieveL3(ix *index.Index, opts Options) (*Result, error) {
	if strings.TrimSpace(opts.Symbol) == "" {
		return nil, fmt.Errorf("symbol is required for level %d", L3)
	}
	resolved, ok := intel.Resolve(ix, opts.Symbol)
	if !ok {
		return nil, fmt.Errorf("unknown symbol: %s", opts.Symbol)
	}
	def, ok := lookupSymbol(ix, resolved)
	if !ok {
		return nil, fmt.Errorf("unknown symbol: %s", opts.Symbol)
	}
	lines := opts.Lines
	if lines <= 0 {
		lines = 12
	}
	text := ix.Context(resolved, lines)
	truncated := false
	if opts.MaxTokens > 0 {
		if tokenize.Count(text) > opts.MaxTokens {
			truncated = true
		}
		text = budget.FitCode(text, opts.MaxTokens)
	}
	h := NewHandle(TypeSymbol, opts.Symbol, def.File, def.Line,
		tokenize.Count(text), confidenceScore(def.Confidence), evidence.Digest(text))
	DefaultRegistry.Register(h)
	r := &Result{Level: L3, Source: &Source{Handle: h, Text: text}, Truncated: truncated}
	r.Tokens = tokenize.Count(Render(r))
	return r, nil
}

// isTestCallee reports whether any symbol named name is defined in a
// *_test.go file.
func isTestCallee(ix *index.Index, name string) bool {
	for _, s := range ix.Symbols {
		if (s.Name == name || s.FullName() == name) && strings.HasSuffix(s.File, "_test.go") {
			return true
		}
	}
	return false
}

// lookupSymbol returns the first symbol matching a bare or full name.
func lookupSymbol(ix *index.Index, name string) (index.Symbol, bool) {
	for _, s := range ix.Symbols {
		if s.Name == name || s.FullName() == name {
			return s, true
		}
	}
	return index.Symbol{}, false
}

// Render renders a result as compact text: a level header, the payload, and
// a footer token count.
func Render(r *Result) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	payload := 0
	switch r.Level {
	case L1:
		total := 0
		for _, it := range r.Items {
			total += it.TokenCost
		}
		payload = total
		fmt.Fprintf(&b, "== level 1: %s (%d matches, ~%d tokens) ==\n", r.Query, len(r.Items), total)
		for _, it := range r.Items {
			id := it.Handle.ID
			if len(id) > 8 {
				id = id[:8]
			}
			fmt.Fprintf(&b, "handle %s %s %s %s:%d ~%dtok conf %g\n",
				id, it.Name, it.Kind, it.File, it.Line, it.TokenCost, it.Confidence)
		}
	case L2:
		if r.Detail == nil || r.Detail.Handle == nil {
			return ""
		}
		payload = writeL2Body(&b, r.Detail.Handle.Name, r.Detail)
	case L3:
		if r.Source == nil || r.Source.Handle == nil {
			return ""
		}
		fmt.Fprintf(&b, "== level 3: %s ==\n", r.Source.Handle.Name)
		b.WriteString(r.Source.Text)
		if !strings.HasSuffix(r.Source.Text, "\n") {
			b.WriteString("\n")
		}
		payload = tokenize.Count(b.String())
	default:
		return ""
	}
	// Budget fit (all levels): when MaxTokens is set, the rendered payload
	// (header + items/neighborhood/source) is deterministically compacted to
	// the budget. L3 already fits its source text at retrieve time; L1/L2
	// fit here — fitting the final render means post-governance filtering of
	// the items is reflected in the budget, not bypassed by a precomputed
	// blob. The footer (and its truncation marker) is appended after
	// the fit so it always survives the trim.
	text := b.String()
	if r.MaxTokens > 0 && tokenize.Count(text) > r.MaxTokens {
		r.Truncated = true
		text = budget.FitCode(text, r.MaxTokens)
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += fmt.Sprintf("~%d tokens", payload)
	if r.Truncated {
		text += " (truncated)"
	}
	return text
}

// writeL2Body writes the L2 header plus its non-empty sections into b and
// returns the token count of what was written.
func writeL2Body(b *strings.Builder, symbol string, d *Detail) int {
	fmt.Fprintf(b, "== level 2: %s ==\n", symbol)
	writeSection(b, "callers", d.Callers)
	writeSection(b, "callees", d.Callees)
	writeSection(b, "tests", d.Tests)
	writeSection(b, "files", d.Files)
	return tokenize.Count(b.String())
}

func writeSection(b *strings.Builder, name string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "== %s (%d) ==\n", name, len(items))
	for _, it := range items {
		b.WriteString(it)
		b.WriteString("\n")
	}
}
