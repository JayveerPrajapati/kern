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
		conf := edgeConfidenceFor(ix, c, rootID, root.File)
		g.Edges = append(g.Edges, GraphEdge{
			From: c, To: rootID,
			Confidence:      conf,
			ConfidenceLabel: confidenceLabel(conf),
		})
	}
	for _, c := range ix.CallsFor(root) {
		byID[c] = mergeNode(byID[c], GraphNode{ID: c, Name: c, Role: "callee"})
		conf := edgeConfidenceFor(ix, rootID, c, root.File)
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
		for _, ce := range ix.Calls[id] {
			callee := ce.Target
			if _, ok := byID[callee]; !ok {
				continue
			}
			// Prefer the parser's per-edge confidence (H/M/L) over the
			// resolution heuristic; lowercased to the internal tiers the
			// confidenceLabel switch expects.
			conf := strings.ToLower(ce.Confidence.String())
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
// ResolveDottedMethod resolves a dotted method reference ("Type.Method" or
// "pkg.Type.Method") to the method symbols whose receiver matches the
// qualifier. The index may key a Java method's FullName as
// "com.inn.rcp.ResponseWrapperFactory.build" while the user types
// "ResponseWrapperFactory.build", and overloads all share one FullName, so an
// exact map lookup cannot disambiguate. Matching tiers:
//
//  1. receiver equals the qualifier exactly ("ResponseWrapperFactory.build")
//  2. receiver is package-qualified and ends in the qualifier
//     ("com.inn.rcp.ResponseWrapperFactory.build")
//  3. the qualifier is more-qualified than the receiver (nested classes:
//     "Outer.Inner.build" where the receiver is "Inner")
//
// Overloads are all returned (in index order); callers that need one symbol
// pick the first deterministically. Returns nil when no receiver matches.
func (ix *Index) ResolveDottedMethod(qualifier, method string) []Symbol {
	var exact, suffix, nested []Symbol
	for _, s := range ix.symbolsFor(method) {
		if s.Receiver == "" {
			continue
		}
		switch {
		case s.Receiver == qualifier:
			exact = append(exact, s)
		case strings.HasSuffix(s.Receiver, "."+qualifier):
			suffix = append(suffix, s)
		case strings.Contains(qualifier, ".") && baseName(qualifier) == s.Receiver:
			nested = append(nested, s)
		}
	}
	switch {
	case len(exact) > 0:
		return exact
	case len(suffix) > 0:
		return suffix
	default:
		return nested
	}
}

func resolveName(ix *Index, name string) (Symbol, bool) {
	if defs := ix.symbolsFor(name); len(defs) > 0 {
		return defs[0], true
	}
	if i := strings.LastIndex(name, "."); i >= 0 && i+1 < len(name) {
		qualifier, method := name[:i], name[i+1:]
		// Class.method: match receivers to the qualifier deterministically so
		// an overloaded or package-qualified method name never falls through
		// to an arbitrary same-named symbol in another class.
		if matches := ix.ResolveDottedMethod(qualifier, method); len(matches) > 0 {
			return matches[0], true
		}
		// Package-qualified Go target ("index.Load"): match the qualifier to
		// the directory that actually defines the bare-name symbol. Unlike the
		// receiver path above, this is a directory match, not a receiver
		// match; when neither qualifies, a dotted name resolves to nothing
		// rather than an arbitrary same-named symbol (report K-01).
		if defs := ix.symbolsFor(method); len(defs) > 0 {
			pkg := qualifier
			for _, d := range defs {
				if filepath.Base(filepath.Dir(d.File)) == pkg {
					return d, true
				}
			}
		}
	}
	return Symbol{}, false
}

// edgeConfidenceFor returns the parser's per-edge confidence (high/medium/
// low) for a call from owner to target, matching WholeGraph's preference for
// CallEdge.Confidence over the resolution heuristic. The edge is looked up in
// the Calls map exactly as recorded (Target forms are the map's values, so an
// exact key match is authoritative). When the edge is absent from the map —
// caller entries synthesized outside per-symbol call lists, or a
// receiver-qualified form — it falls back to the directory heuristic via
// edgeConfidence, with fromFile resolving the owner's own file when possible.
func edgeConfidenceFor(ix *Index, owner, target, fallbackFile string) string {
	for _, e := range ix.Calls[owner] {
		if e.Target == target {
			return strings.ToLower(e.Confidence.String())
		}
	}
	fromFile := fallbackFile
	if d, ok := resolveName(ix, owner); ok && d.File != "" {
		fromFile = d.File
	}
	return edgeConfidence(ix, fromFile, target)
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

// EdgeConfidenceHeuristic is the exported form of edgeConfidence for the
// intel renderers' fallback path (an edge with no recorded parser confidence).
func EdgeConfidenceHeuristic(ix *Index, fromFile, name string) string {
	return edgeConfidence(ix, fromFile, name)
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

// GraphCypher exports the neighbourhood as Cypher statements for Neo4j
// interoperability (file interop only — no server push, consistent with
// kern's local-first design). Output is byte-deterministic for identical
// input: nodes are emitted sorted by full name, edges sorted by
// (source, target). Edge confidence reuses the GraphML path's internal
// high/medium/low tier mapping (GraphEdge.Confidence); GraphEdge carries no
// synthetic-edge provenance, so none is reflected here either.
func (g GraphResult) GraphCypher() string {
	var b strings.Builder
	b.WriteString("// Cypher export for Neo4j interoperability (file interop only — no server push).\n")
	nodes := append([]GraphNode(nil), g.Nodes...)
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	for _, n := range nodes {
		fmt.Fprintf(&b, "CREATE (:Symbol {name: \"%s\", kind: \"%s\", file: \"%s\", line: %d});\n",
			cypherEsc(n.Name), cypherEsc(n.Kind), cypherEsc(n.File), n.Line)
	}
	edges := append([]GraphEdge(nil), g.Edges...)
	sort.SliceStable(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	for _, e := range edges {
		fmt.Fprintf(&b, "MATCH (a:Symbol {name: \"%s\"}), (b:Symbol {name: \"%s\"}) CREATE (a)-[:CALLS {confidence: \"%s\"}]->(b);\n",
			cypherEsc(e.From), cypherEsc(e.To), cypherEsc(e.Confidence))
	}
	return b.String()
}

// cypherEsc escapes a string for a double-quoted Cypher string literal.
// Within double quotes Cypher treats only backslash and double-quote
// specially, so those are the only characters escaped.
func cypherEsc(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return r.Replace(s)
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
	JSON        string
	Title       string
	TokenStats  string
	Colors      string
	StaleBanner string
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
// search box to filter them. When any of the files backing the rendered nodes
// changed on disk since the index was built, a staleness banner is shown
// below the top bar (P1-7).
func (g GraphResult) GraphHTML(ix *Index) string {
	data, err := json.Marshal(g)
	if err != nil {
		data = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	data = []byte(strings.ReplaceAll(string(data), "</", "<\\/"))
	title := g.Root
	if title == "" {
		title = fmt.Sprintf("whole repo (%d symbols, %d edges)", len(g.Nodes), len(g.Edges))
	}
	// Cite the files backing the rendered nodes so the staleness gate can
	// spot-check them against the hashes recorded at build time.
	seen := map[string]bool{}
	var files []string
	for _, n := range g.Nodes {
		if n.File != "" && !seen[n.File] {
			seen[n.File] = true
			files = append(files, n.File)
		}
	}
	var b strings.Builder
	if err := graphHTMLTmpl.Execute(&b, graphHTMLData{
		JSON:        string(data),
		Title:       html.EscapeString(title),
		TokenStats:  tokenStatsPanel(g.Stats),
		Colors:      kindColorJSON(),
		StaleBanner: ix.StalenessBanner(files),
	}); err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return b.String()
}
