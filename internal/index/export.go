package index

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

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

// Edge confidence labels — the industry-standard 3-tier scheme used by
// Graphify, Code-Review-Graph and CodeGraph. These map to kern's internal
// high/medium/low tiers so the JSON/GraphML output aligns with peer tools.
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
// reliably the edge was derived using kern's internal tiers ("high" for
// same-package resolved calls, "medium" for cross-package resolved calls, "low"
// for unresolved references). ConfidenceLabel provides the industry-standard
// EXTRACTED/INFERRED/AMBIGUOUS equivalent.
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

// computeTokenSavings computes how many tokens the full source context for a
// symbol would use versus the compact text representation. The "full context"
// is the concatenation of source files for all graph nodes (the code an agent
// would read), and the "compact" form is the provided text (graph JSON or
// context text).
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
	return fmt.Sprintf(`<span style="color:#94a3b8;font-size:11px">\u21d2 %s %d → %d tokens (%d%% saved)</span>`,
		s.Source, s.FullContext, s.CompactTokens, s.SavingsPct)
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
	whole := title == ""
	if whole {
		title = fmt.Sprintf("whole repo (%d symbols, %d edges)", len(g.Nodes), len(g.Edges))
	}
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
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>kern graph: `)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</title>
<style>
  body { font-family: system-ui, sans-serif; margin: 0; background: #0f172a; color: #e2e8f0; }
  #top { padding: 10px 16px; border-bottom: 1px solid #334155; display: flex; gap: 16px; align-items: baseline; }
  #top h1 { font-size: 15px; margin: 0; }
  #top .sub { color: #94a3b8; font-size: 12px; }
  #legend { font-size: 11px; color: #94a3b8; }
  #legend span { margin-right: 10px; }
.low-edge { stroke: #dc2626; stroke-dasharray: 5,4; }
  #wrap { display: flex; }
  svg { flex: 1; height: 600px; }
  #side { width: 300px; padding: 12px; border-left: 1px solid #334155; font-size: 12px; }
  #side .title { color: #94a3b8; text-transform: uppercase; font-size: 10px; letter-spacing: 1px; }
  #side .dim { color: #94a3b8; }
  svg text { font-family: system-ui, sans-serif; }
  .node rect { cursor: pointer; transition: opacity .15s; }
  .node text { pointer-events: none; }
  .edge { stroke: #475569; stroke-width: 1.5; }
  .dim { opacity: .12; }
  #search { background: #0f172a; border: 1px solid #334155; color: #e2e8f0; font-size: 12px; padding: 4px 8px; border-radius: 6px; width: 200px; }
  #search::placeholder { color: #64748b; }
  .band-label { fill: #94a3b8; font-size: 11px; }
</style>
</head>
<body>
<div id="top"><h1>kern graph: `)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</h1><span class="sub">hover to trace edges, click for details</span>
` + tokenStatsPanel(g.Stats) + `
<input id="search" type="text" placeholder="filter symbols\u2026">
<div id="legend"><span style="color:#22c55e">\u2500\u25b6 EXTRACTED (same pkg)</span><span style="color:#f59e0b">\u2500\u25b6 INFERRED (cross pkg)</span><span style="color:#dc2626">\u2500\u2500\u25b6 AMBIGUOUS (unresolved)</span></div></div>
<div id="wrap"><svg id="svg" viewBox="0 0 1000 600"></svg><div id="side"></div></div>
<script>
const g = `)
	b.WriteString(string(data))
	b.WriteString(`;
const whole = (g.root === '');
const colors = {`)
	b.WriteString(strings.TrimSuffix(colors.String(), ","))
	b.WriteString(`};
const COL = {caller: 120, def: 360, callee: 600};
const layers = {caller: [], def: [], callee: []};
const ids = [];
g.nodes.forEach((n, i) => { n.i = i; n.rx = n.ry = 0; ids.push(n.id); layers[n.role||'callee'].push(n); });
let nodeById = {};
g.nodes.forEach(n => nodeById[n.id] = n);
function slot(n, role) {
  const col = COL[role];
  const idx = layers[role].indexOf(n);
  const nL = layers[role].length;
  const gap = nL > 1 ? 40 : 0;
  const start = 300 - ((nL - 1) * gap) / 2;
  return [col, start + idx * gap];
}
const svg = document.getElementById('svg');
const NS = 'http://www.w3.org/2000/svg';
const edgesEl = [], nodeEl = new Map(), w = new Map();
const edgemap = new Map();
function addEdge(from, to, confidence) {
  const e = whole ? document.createElementNS(NS, 'path') : document.createElementNS(NS, 'line');
  e.setAttribute('class', 'edge');
  if (confidence === 'AMBIGUOUS' || confidence === 'low') {
    e.setAttribute('stroke-dasharray', '5,4');
    e.setAttribute('stroke', '#dc2626');
  } else if (confidence === 'INFERRED' || confidence === 'medium') {
    e.setAttribute('stroke', '#f59e0b');
  }
    e.setAttribute('stroke', '#f59e0b');
  }
  const mk = document.createElementNS(NS, 'marker');
  mk.setAttribute('id', 'arrow' + (edgemap.size));
  mk.setAttribute('viewBox', '0 0 10 10'); mk.setAttribute('refX', '9'); mk.setAttribute('refY', '5');
  mk.setAttribute('markerWidth', '6'); mk.setAttribute('markerHeight', '6'); mk.setAttribute('orient', 'auto-start-reverse');
  const p = document.createElementNS(NS, 'path'); p.setAttribute('d', 'M 0 0 L 10 5 L 0 10 z'); p.setAttribute('fill', '#64748b');
  mk.appendChild(p); svg.appendChild(mk);
  e.setAttribute('marker-end', 'url(#arrow' + (edgemap.size) + ')');
  svg.appendChild(e); edgesEl.push(e);
  const key = from + '>' + to;
  if (!edgemap.has(key)) edgemap.set(key, []);
  edgemap.get(key).push(e);
}
function textWidth(s) {
  if (!w.has(s)) { const t = document.createElementNS(NS, 'text'); t.textContent = s; svg.appendChild(t); w.set(s, t.getComputedTextLength()); t.remove(); }
  return w.get(s);
}
function draw() {
  if (whole) return wholeDraw();
  g.nodes.forEach(n => {
    const [cx, cy] = slot(n, n.role || 'callee');
    const tw = Math.min(textWidth(n.id), 180);
    const rw = tw + 22, rh = 30;
    const g2 = document.createElementNS(NS, 'g');
    g2.setAttribute('class', 'node');
    const rect = document.createElementNS(NS, 'rect');
    rect.setAttribute('width', rw); rect.setAttribute('height', rh);
    rect.setAttribute('rx', 6);
    rect.setAttribute('fill', (colors[n.kind] || '#64748b') + '33');
    rect.setAttribute('stroke', colors[n.kind] || '#64748b');
    const t = document.createElementNS(NS, 'text');
    t.setAttribute('x', rw / 2); t.setAttribute('y', rh / 2 + 4);
    t.setAttribute('text-anchor', 'middle'); t.setAttribute('font-size', '11');
    t.textContent = n.id;
    g2.appendChild(rect); g2.appendChild(t);
    g2.setAttribute('transform', 'translate(' + (cx - rw / 2) + ',' + (cy - rh / 2) + ')');
    g2.addEventListener('mouseenter', () => highlight(n, true));
    g2.addEventListener('mouseleave', () => highlight(n, false));
    g2.addEventListener('click', () => detail(n));
    svg.appendChild(g2);
    nodeEl.set(n.id, g2);
    n.rx = cx; n.ry = cy; n.w = rw; n.h = rh;
  });
  g.edges.forEach(e => addEdge(e.from, e.to, e.confidence_label || e.confidence));
  edgesEl.forEach((el, i) => {});
  layout();
}
function layout() {
  g.edges.forEach(e => {
    const a = nodeById[e.from], b = nodeById[e.to];
    if (!a || !b) return;
    const lines = edgemap.get(e.from + '>' + e.to) || [];
    lines.forEach((l, i) => {
      const bend = (i - (lines.length - 1) / 2) * 12;
      if (whole) {
        const x1 = a.rx + a.w / 2, y1 = a.ry + bend;
        const x2 = b.rx - b.w / 2, y2 = b.ry + bend;
        const mx = (x1 + x2) / 2;
        l.setAttribute('d', 'M ' + x1 + ' ' + y1 + ' C ' + mx + ' ' + y1 + ' ' + mx + ' ' + y2 + ' ' + x2 + ' ' + y2);
        return;
      }
      let x1 = a.rx + (e.from === g.root ? a.w / 2 : -a.w / 2);
      let y1 = a.ry + bend;
      let x2 = b.rx + (e.to === g.root ? -b.w / 2 : b.w / 2);
      let y2 = b.ry + bend;
      l.setAttribute('x1', x1); l.setAttribute('y1', y1);
      l.setAttribute('x2', x2); l.setAttribute('y2', y2);
    });
  });
}
function wholeDraw() {
  const by = {};
  g.nodes.forEach(n => { const k = (n.community || n.pkg || '?'); (by[k] = by[k] || []).push(n); });
  const keys = Object.keys(by).sort((a, b) => by[b].length - by[a].length || a.localeCompare(b));
  const colW = 200, colH = 470, rowH = 30, pad = 14;
  let x = 40;
  const bands = [];
  keys.forEach(k => {
    const nodes = by[k];
    const pos = {};
    let col = 0, row = 0;
    nodes.forEach(n => {
      if (row >= colH / rowH) { row = 0; col++; }
      pos[n.id] = [x + col * colW + pad + 90, 40 + pad + row * rowH + 10];
      row++;
    });
    const bw = (col + 1) * colW + pad * 2;
    const band = document.createElementNS(NS, 'g');
    const r = document.createElementNS(NS, 'rect');
    r.setAttribute('x', x); r.setAttribute('y', 8); r.setAttribute('width', bw); r.setAttribute('height', 508);
    r.setAttribute('fill', 'rgba(148,163,184,0.04)'); r.setAttribute('stroke', '#334155'); r.setAttribute('rx', 8);
    const t = document.createElementNS(NS, 'text');
    t.setAttribute('x', x + 10); t.setAttribute('y', 24); t.setAttribute('class', 'band-label');
    t.textContent = k + ' (' + nodes.length + ')';
    band.appendChild(r); band.appendChild(t); svg.appendChild(band);
    bands.push({ x, w: bw, pos });
    x += bw + 24;
  });
  svg.setAttribute('viewBox', '0 0 ' + (x + 40) + ' 560');
  g.nodes.forEach(n => {
    const b = bands.find(bb => bb.pos[n.id]);
    const cx = b.pos[n.id][0], cy = b.pos[n.id][1];
    const tw = Math.min(textWidth(n.id), 170);
    const rw = tw + 16, rh = 22;
    const g3 = document.createElementNS(NS, 'g');
    g3.setAttribute('class', 'node');
    const rect = document.createElementNS(NS, 'rect');
    rect.setAttribute('width', rw); rect.setAttribute('height', rh); rect.setAttribute('rx', 5);
    rect.setAttribute('fill', (colors[n.kind] || '#64748b') + '33');
    rect.setAttribute('stroke', colors[n.kind] || '#64748b');
    const t = document.createElementNS(NS, 'text');
    t.setAttribute('x', rw / 2); t.setAttribute('y', rh / 2 + 4);
    t.setAttribute('text-anchor', 'middle'); t.setAttribute('font-size', '10');
    t.textContent = n.id;
    g3.appendChild(rect); g3.appendChild(t);
    g3.setAttribute('transform', 'translate(' + (cx - rw / 2) + ',' + (cy - rh / 2) + ')');
    g3.addEventListener('mouseenter', () => highlight(n, true));
    g3.addEventListener('mouseleave', () => highlight(n, false));
    g3.addEventListener('click', () => detail(n));
    svg.appendChild(g3);
    nodeEl.set(n.id, g3);
    n.rx = cx; n.ry = cy; n.w = rw; n.h = rh;
  });
}
function highlight(n, on) {
  const connected = new Set();
  g.edges.forEach(e => {
    if (e.from === n.id || e.to === n.id) { connected.add(e.from); connected.add(e.to); }
  });
  g.nodes.forEach(m => {
    const el = nodeEl.get(m.id);
    if (!el) return;
    if (!on || m.id === n.id || connected.has(m.id)) el.classList.remove('dim');
    else el.classList.add('dim');
  });
  edgesEl.forEach(l => { if (!on || l.__live) l.classList.remove('dim'); else l.classList.add('dim'); });
  g.edges.forEach(e => {
    const isConn = e.from === n.id || e.to === n.id;
    (edgemap.get(e.from + '>' + e.to) || []).forEach(l => l.__live = isConn);
  });
}
const side = document.getElementById('side');
function detail(n) {
  side.innerHTML = '<div class="title">' + (n.role || '') + '</div><div style="font-size:15px;margin:4px 0">' + n.id + '</div>' +
    '<div>kind: <span class="dim">' + (n.kind || '-') + '</span></div>' +
    '<div>file: <span class="dim">' + (n.file || '-') + '</span></div>' +
    '<div>line: <span class="dim">' + (n.line || '-') + '</span></div>' +
    (n.pkg ? '<div>pkg: <span class="dim">' + n.pkg + '</span></div>' : '') +
    (n.community ? '<div>community: <span class="dim">' + n.community + '</span></div>' : '') +
    '<div style="margin-top:8px" class="dim">' + n.id + ' has ' + (g.edges.filter(e => e.from === n.id).length) + ' outgoing, ' + (g.edges.filter(e => e.to === n.id).length) + ' incoming edges.</div>';
}
function legend() {
  const seen = {};
  g.nodes.forEach(n => { if (!seen[n.kind]) { seen[n.kind] = 1; const s = document.createElement('span'); s.innerHTML = '<span style="color:' + (colors[n.kind] || '#64748b') + '">\u25a0</span> ' + n.kind; document.getElementById('legend').appendChild(s); } });
}
const search = document.getElementById('search');
search.addEventListener('input', () => {
  const q = search.value.trim().toLowerCase();
  g.nodes.forEach(n => {
    const el = nodeEl.get(n.id);
    if (!el) return;
    const hit = !q || n.id.toLowerCase().includes(q) || (n.file || '').toLowerCase().includes(q);
    el.style.opacity = hit ? '1' : '0.12';
  });
  g.edges.forEach(e => {
    const a = nodeEl.get(e.from), b = nodeEl.get(e.to);
    const vis = !q || (a && a.style.opacity !== '0.12' && b && b.style.opacity !== '0.12');
    (edgemap.get(e.from + '>' + e.to) || []).forEach(l => { l.style.opacity = vis ? '1' : '0.06'; });
  });
});
draw(); legend();
if (nodeById[g.root]) detail(nodeById[g.root]);
else detail({id: 'whole repo', kind: 'index', role: 'whole repo', file: g.nodes.length + ' symbols, ' + g.edges.length + ' edges'});
</script>
</body>
</html>
`)
	return b.String()
}
