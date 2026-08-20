package index

import (
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

//go:embed export_graph.html
var graphFS embed.FS

// graphHTMLTmpl is the self-contained HTML/SVG visualisation template. It is
// parsed once at package init and executed with text/template (not
// html/template) so the pre-escaped JSON, title and stats HTML are inserted
// verbatim without double-escaping.
var graphHTMLTmpl = template.Must(template.ParseFS(graphFS, "export_graph.html"))

// GraphNode is a single symbol node in a neighbourhood graph.
type GraphNode struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Role      string `json:"role,omitempty"` // def, caller, callee
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Pkg       string `json:"pkg,omitempty"`       // top-level dir of File (whole-repo mode)
	Community string `json:"community,omitempty"` // label-propagation community (whole-repo mode)
}

// Edge confidence tiers describe how reliably an edge was derived.
const (
	confHigh   = "high"   // direct same-package call, resolved
	confMedium = "medium" // cross-package call, resolved to a definition
	confLow    = "low"    // unresolved/phantom reference
)

// Edge confidence labels — the standard EXTRACTED/INFERRED/AMBIGUOUS scheme
// mapped to kern's internal high/medium/low tiers for JSON/GraphML output.
const (
	confExtracted = "EXTRACTED" // deterministic from AST, no inference
	confInferred  = "INFERRED"  // resolved but requiring cross-package lookup
	confAmbiguous = "AMBIGUOUS" // unresolved/phantom reference
)

// confidenceLabel maps kernel internal tiers (high/medium/low) to the
// industry-standard EXTRACTED/INFERRED/AMBIGUOUS labels.
func confidenceLabel(conf string) string {
	switch conf {
	case confHigh:
		return confExtracted
	case confMedium:
		return confInferred
	case confLow:
		return confAmbiguous
	default:
		return confAmbiguous
	}
}

// GraphEdge is a directed edge between two graph nodes. Confidence records how
// reliably the edge was derived using kern's internal tiers (high/medium/low);
// ConfidenceLabel carries the standard EXTRACTED/INFERRED/AMBIGUOUS equivalent.
type GraphEdge struct {
	From            string `json:"from"`
	To              string `json:"to"`
	Confidence      string `json:"confidence,omitempty"`       // high/medium/low (internal)
	ConfidenceLabel string `json:"confidence_label,omitempty"` // EXTRACTED/INFERRED/AMBIGUOUS (standard)
}

// TokenStats records the token count of the full context versus the compressed
// graph/context representation, so callers can display a savings summary.
type TokenStats struct {
	FullContext   int    `json:"full_context_tokens"`
	CompactTokens int    `json:"compact_tokens"`
	SavingsPct    int    `json:"savings_percent"`
	Source        string `json:"source,omitempty"` // "graph" or "context"
}

func (t TokenStats) Summary() string {
	if t.FullContext <= 0 {
		return ""
	}
	if t.CompactTokens >= t.FullContext {
		return fmt.Sprintf("tokens: %s %d → %d (0%% saved; compact includes metadata)",
			t.Source, t.FullContext, t.CompactTokens)
	}
	return fmt.Sprintf("tokens: %s %d → %d (%d%% saved)",
		t.Source, t.FullContext, t.CompactTokens, t.SavingsPct)
}

// GraphResult is the structured neighbourhood of a symbol.
type GraphResult struct {
	Root  string      `json:"root"`
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
	Stats TokenStats  `json:"stats,omitempty"`
}

// Neighborhood returns the definition, callers, and callees of a symbol as a
// structured graph (used by --json, --graphml and --html exports).
func (ix *Index) Neighborhood(symbol string) (GraphResult, bool) {
	g := GraphResult{Root: symbol}
	defs := ix.symbolsFor(symbol)
	if len(defs) == 0 {
		if d, ok := resolveName(ix, symbol); ok {
			defs = []Symbol{d}
		} else {
			return g, false
		}
	}
	root := defs[0]
	byID := map[string]GraphNode{}
	rootID := root.FullName()
	for _, d := range defs {
		byID[d.FullName()] = GraphNode{
			ID: d.FullName(), Name: d.FullName(), Kind: d.Kind,
			Role: "def", File: d.File, Line: d.Line,
		}
	}
	for _, c := range ix.CallersFor(root) {
		byID[c] = mergeNode(byID[c], GraphNode{ID: c, Name: c, Role: "caller"})
		conf := edgeConfidence(ix, root.File, c)
		g.Edges = append(g.Edges, GraphEdge{
			From: c, To: rootID,
			Confidence:      conf,
			ConfidenceLabel: confidenceLabel(conf),
		})
	}
	for _, c := range ix.CallsFor(root) {
		byID[c] = mergeNode(byID[c], GraphNode{ID: c, Name: c, Role: "callee"})
		conf := edgeConfidence(ix, root.File, c)
		g.Edges = append(g.Edges, GraphEdge{
			From: rootID, To: c,
			Confidence:      conf,
			ConfidenceLabel: confidenceLabel(conf),
		})
	}
	// resolve file:line for caller/callee nodes that have a definition
	for id, n := range byID {
		if n.File != "" || id == rootID {
			continue
		}
		if d, ok := resolveName(ix, n.Name); ok {
			n.Kind, n.File, n.Line = d.Kind, d.File, d.Line
			byID[id] = n
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		g.Nodes = append(g.Nodes, byID[id])
	}
	g.Stats = ix.TokenSavingsForNeighborhood(g)
	return g, true
}

// WholeGraph renders the whole repository as a graph: every symbol (capped at
// limit, most-connected first), every resolved call edge between kept symbols,
// and per-symbol package and community memberships for the banded layout. Root
// stays empty so the HTML renderer picks the whole-repo branch.
func (ix *Index) WholeGraph(limit int) GraphResult {
	if limit <= 0 {
		limit = 400
	}
	labels := ix.Communities
	if len(labels) == 0 {
		labels = ix.CommunityLabels()
	}
	degree := func(id string) int {
		return len(ix.Calls[id]) + len(ix.Callers[id])
	}
	// FullName is not unique across languages (bash/python/go each define
	// "main", pack/brief/engine each define "Build"), so dedupe candidates
	// by name before capping or the degree cut would be full of collisions.
	seen := map[string]bool{}
	var cands []struct {
		s   Symbol
		deg int
	}
	for _, s := range ix.Symbols {
		id := s.FullName()
		if seen[id] {
			continue
		}
		seen[id] = true
		cands = append(cands, struct {
			s   Symbol
			deg int
		}{s, degree(id)})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].deg != cands[j].deg {
			return cands[i].deg > cands[j].deg
		}
		return cands[i].s.FullName() < cands[j].s.FullName()
	})
	if len(cands) > limit {
		cands = cands[:limit]
	}
	byID := map[string]GraphNode{}
	for _, c := range cands {
		id := c.s.FullName()
		byID[id] = GraphNode{
			ID: id, Name: id, Kind: c.s.Kind, Role: "def",
			File: c.s.File, Line: c.s.Line,
			Pkg: topDir(ix.Root, c.s.File), Community: labels[id],
		}
	}
	g := GraphResult{}
	for _, c := range cands {
		id := c.s.FullName()
		for _, callee := range ix.Calls[id] {
			if _, ok := byID[callee]; !ok {
				continue
			}
			conf := edgeConfidence(ix, c.s.File, callee)
			g.Edges = append(g.Edges, GraphEdge{
				From: id, To: callee,
				Confidence:      conf,
				ConfidenceLabel: confidenceLabel(conf),
			})
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		g.Nodes = append(g.Nodes, byID[id])
	}
	return g
}

// topDir returns the top-level directory of file relative to root, or the
// parent directory when the file sits at the top level.
func topDir(root, file string) string {
	rel := filepath.ToSlash(file)
	if dir := filepath.Dir(rel); dir != "." && dir != "/" {
		if i := strings.IndexByte(dir, '/'); i >= 0 {
			return dir[:i]
		}
		return dir
	}
	return "."
}

// computeTokenSavings compares tokens in the concatenated source files of all
// graph nodes against the compact text form (graph JSON or context text).
func computeTokenSavings(fullText, compact, source string) TokenStats {
	fullTokens := tokenize.Count(fullText)
	compactTokens := tokenize.Count(compact)
	savings := 0
	if fullTokens > 0 {
		savings = int(float64(fullTokens-compactTokens) / float64(fullTokens) * 100)
	}
	return TokenStats{
		FullContext:   fullTokens,
		CompactTokens: compactTokens,
		SavingsPct:    savings,
		Source:        source,
	}
}

// TokenSavingsForGraph computes token savings for the Graph() text output,
// comparing it against the full source file of the symbol's definition.
func (ix *Index) TokenSavingsForGraph(defFile, compact string) TokenStats {
	var fullData []byte
	if defFile != "" {
		var err error
		fullData, err = os.ReadFile(filepath.Join(ix.Root, defFile))
		if err != nil {
			fullData = nil
		}
	}
	return computeTokenSavings(string(fullData), compact, "graph")
}

// TokenSavingsForContext computes token savings for the Context() text output.
func (ix *Index) TokenSavingsForContext(defFile, compact string) TokenStats {
	var fullData []byte
	if defFile != "" {
		var err error
		fullData, err = os.ReadFile(filepath.Join(ix.Root, defFile))
		if err != nil {
			fullData = nil
		}
	}
	return computeTokenSavings(string(fullData), compact, "context")
}

// TokenSavingsForNeighborhood computes token savings for the Neighborhood JSON,
// comparing the compact JSON against the full source files of all referenced
// nodes.
func (ix *Index) TokenSavingsForNeighborhood(g GraphResult) TokenStats {
	var fullText strings.Builder
	seen := map[string]bool{}
	for _, n := range g.Nodes {
		if n.File != "" && !seen[n.File] {
			seen[n.File] = true
			if data, err := os.ReadFile(filepath.Join(ix.Root, n.File)); err == nil {
				fullText.Write(data)
			}
		}
	}
	return computeTokenSavings(fullText.String(), g.GraphJSON(), "neighborhood")
}

// resolveName finds a definition for a call target name. Exact matches win;
// a package-qualified target like "index.Build" falls back to the bare name
// ("Build") so call sites still resolve to real definitions.
func resolveName(ix *Index, name string) (Symbol, bool) {
	if defs := ix.symbolsFor(name); len(defs) > 0 {
		return defs[0], true
	}
	if i := strings.LastIndex(name, "."); i >= 0 && i+1 < len(name) {
		if defs := ix.symbolsFor(name[i+1:]); len(defs) > 0 {
			// Prefer the definition that lives under the package named by the
			// qualifier (e.g. "index.Load" should resolve to the index package,
			// not whatever Load the symbol order happens to list first). Fall
			// back to the first bare-name match for call sites.
			if i > 0 {
				pkg := name[:i]
				for _, d := range defs {
					if filepath.Base(filepath.Dir(d.File)) == pkg {
						return d, true
					}
				}
			}
			return defs[0], true
		}
	}
	return Symbol{}, false
}

// edgeConfidence reports how reliably an edge from `fromFile` to `to` was
// derived: "high" for same-package resolved calls, "medium" for cross-package
// resolved calls, "low" for unresolved references.
func edgeConfidence(ix *Index, fromFile, name string) string {
	def, ok := resolveName(ix, name)
	if !ok {
		return confLow
	}
	if samePackageDir(fromFile, def.File) {
		return confHigh
	}
	return confMedium
}

// samePackageDir reports whether two source files live in the same directory
// (a proxy for "same Go package" since Go packages map 1:1 to their directory).
func samePackageDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return filepath.Dir(a) == filepath.Dir(b)
}

func mergeNode(a, b GraphNode) GraphNode {
	if a.ID == "" {
		return b
	}
	if a.Role == "def" || b.Role == "def" {
		a.Role = "def"
	}
	if a.Role == "caller" && b.Role == "callee" {
		a.Role = "caller"
	}
	return a
}

// GraphJSON exports the neighbourhood as JSON.
func (g GraphResult) GraphJSON() string {
	b, _ := json.MarshalIndent(g, "", "  ")
	return string(b)
}

// GraphGraphML exports the neighbourhood as GraphML (XML) for tools like
// yEd, Gephi and Cytoscape.
func (g GraphResult) GraphGraphML() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<graphml xmlns="http://graphml.graphdrawing.org/xmlns">
  <key id="kind" for="node" attr.name="kind" attr.type="string"/>
  <key id="role" for="node" attr.name="role" attr.type="string"/>
  <key id="file" for="node" attr.name="file" attr.type="string"/>
  <key id="line" for="node" attr.name="line" attr.type="int"/>
  <key id="confidence" for="edge" attr.name="confidence" attr.type="string"/>
  <key id="confidence_label" for="edge" attr.name="confidence_label" attr.type="string"/>
  <graph id="kern" edgedefault="directed">
`)
	for _, n := range g.Nodes {
		fmt.Fprintf(&b, "    <node id=%q>\n      <data key=\"kind\">%s</data>\n", n.ID, xmlEsc(n.Kind))
		if n.Role != "" {
			fmt.Fprintf(&b, "      <data key=\"role\">%s</data>\n", xmlEsc(n.Role))
		}
		if n.File != "" {
			fmt.Fprintf(&b, "      <data key=\"file\">%s</data>\n", xmlEsc(n.File))
		}
		if n.Line > 0 {
			fmt.Fprintf(&b, "      <data key=\"line\">%d</data>\n", n.Line)
		}
		b.WriteString("    </node>\n")
	}
	for _, e := range g.Edges {
		if e.Confidence == confHigh {
			fmt.Fprintf(&b, "    <edge source=%q target=%q/>\n", e.From, e.To)
		} else {
			fmt.Fprintf(&b, "    <edge source=%q target=%q>\n      <data key=\"confidence\">%s</data>\n      <data key=\"confidence_label\">%s</data>\n    </edge>\n", e.From, e.To, xmlEsc(e.Confidence), xmlEsc(e.ConfidenceLabel))
		}
	}
	b.WriteString("  </graph>\n</graphml>\n")
	return b.String()
}

func xmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

func tokenStatsPanel(s TokenStats) string {
	if s.FullContext <= 0 {
		return ""
	}
	pct := s.SavingsPct
	note := ""
	if s.CompactTokens >= s.FullContext {
		pct = 0
		note = "; compact includes metadata"
	}
	return fmt.Sprintf(`<span style="color:#94a3b8;font-size:11px">\u21d2 %s %d → %d tokens (%d%% saved%s)</span>`,
		s.Source, s.FullContext, s.CompactTokens, pct, note)
}

// graphHTMLData holds the interpolated values injected into export_graph.html.
// All values are pre-escaped by the caller; text/template inserts them verbatim.
type graphHTMLData struct {
	JSON       string
	Title      string
	TokenStats string
	Colors     string
}

// kindColorJSON renders the kind->color legend as a JSON object string. The key
// order is intentionally driven by map iteration (matching the original logic);
// the set of entries is fixed and stable.
func kindColorJSON() string {
	kindColor := map[string]string{
		"func": "#3b82f6", "method": "#8b5cf6", "struct": "#ec4899",
		"interface": "#f59e0b", "type": "#14b8a6", "const": "#64748b",
		"var": "#64748b", "call": "#94a3b8",
	}
	var colors strings.Builder
	for k, v := range kindColor {
		colors.WriteString(`"`)
		colors.WriteString(k)
		colors.WriteString(`":"`)
		colors.WriteString(v)
		colors.WriteString(`",`)
	}
	return "{" + strings.TrimSuffix(colors.String(), ",") + "}"
}

// GraphHTML renders a self-contained interactive HTML/SVG visualisation of the
// neighbourhood. No external dependencies; the data is embedded as JSON and
// rendered with inline JavaScript. When Root is empty it renders the
// whole-repo mode: symbols grouped into community (or package) bands, with a
// search box to filter them.
func (g GraphResult) GraphHTML() string {
	data, err := json.Marshal(g)
	if err != nil {
		data = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	data = []byte(strings.ReplaceAll(string(data), "</", "<\\/"))
	title := g.Root
	if title == "" {
		title = fmt.Sprintf("whole repo (%d symbols, %d edges)", len(g.Nodes), len(g.Edges))
	}
	var b strings.Builder
	if err := graphHTMLTmpl.Execute(&b, graphHTMLData{
		JSON:       string(data),
		Title:      html.EscapeString(title),
		TokenStats: tokenStatsPanel(g.Stats),
		Colors:     kindColorJSON(),
	}); err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return b.String()
}
