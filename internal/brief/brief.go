// Package brief produces a compact onboarding digest for an agent at the
// start of a session: index summary, hub symbols, entry points, architecture,
// recent kern savings and project memory — plus a compact project overview.
// The full per-file project map is available on request (Options.Map, the
// `kern buddy --map` flag). It is kern acting as the agent's "buddy" — the
// best starting context for a fresh session.
package brief

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/stats"
)

// Options controls how the briefing is rendered.
type Options struct {
	// Map renders the full per-file project map as the trailing section
	// instead of the compact project overview. Off by default so the
	// digest stays short; the map is served on demand (`kern buddy --map`).
	Map bool
}

// Build renders the concise briefing for root (the default `kern buddy`
// digest). Errors are reported per section; the function itself only fails
// if the root is unusable. On a cold index it skips the index/architecture
// sections with a hint rather than building a possibly-huge index; callers
// can warm it via Warm or `kern index .` / `kern precache .`.
func Build(root string) (string, error) {
	return BuildWithOptions(root, Options{})
}

// BuildWithOptions renders the briefing with the given options (e.g. the
// full per-file project map on request via Options.Map).
func BuildWithOptions(root string, opts Options) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	var head strings.Builder
	fmt.Fprintf(&head, "# kern buddy briefing — %s\n", abs)
	fmt.Fprintf(&head, "Generated %s. This digest gives you the best starting context.\n\n",
		time.Now().Format("2006-01-02 15:04 MST"))

	// The useful digest sections (index, architecture, surprising
	// connections, savings, memory, how-to-use) render FIRST so a huge
	// project map can never push them out of the MCP output sandbox
	// (24KB default). The project map is the optional trailing section.
	var digest strings.Builder
	ix, err := index.Load(root)
	if err != nil {
		digest.WriteString("## Index\n(not built yet — run `kern index .` or `kern precache .` once to enable the symbols/architecture sections)\n\n")
		ix = nil
	}
	if ix != nil {
		digest.WriteString(indexSection(ix))
		digest.WriteString(architectureSection(ix))
		digest.WriteString(surprisingSection(ix))
	}
	statsSection(&digest)
	// Project memory (from past sessions): read the TYPED store — the one
	// the learn and capture paths write (loop learn, learning extractor,
	// episodic lessons). Reading only the v1 lesson store here is what made
	// "Project memory" render empty for repos whose v1 store holds just
	// auto captures (P2-14): the typed store is where real lessons land.
	mems, err := memory.NewMemoryStore(root).CurrentMemories("")
	if err != nil {
		mems = nil // best-effort: a broken typed store must not fail the digest
	}
	var lessons []domain.Memory
	for _, m := range mems {
		// Auto captures (raw prompts, tool outcomes) are session context,
		// not project lessons — never inject them into a session digest.
		// Typed entries from the learn paths carry Source "loop"/"learning"
		// (never "auto"), so this is a defensive guard.
		if m.Source == "auto" {
			continue
		}
		lessons = append(lessons, m)
	}
	// Near-duplicate lessons (the same prompt, tool outcome or file edit
	// recorded once per call) are noise, not signal: normalize each entry
	// and keep only the most recent occurrence per normalized text, then
	// cap the rendered list so the digest stays compact.
	lessons = dedupeLessons(lessons)
	if len(lessons) > 0 {
		digest.WriteString("## Project memory (from past sessions)\n")
		for i, m := range lessons {
			if i >= projectMemoryMax {
				fmt.Fprintf(&digest, "- … and %d more (full history via `kern memory`)\n", len(lessons)-projectMemoryMax)
				break
			}
			text := m.Content
			if len(text) > 400 {
				text = text[:400] + "…"
			}
			fmt.Fprintf(&digest, "- %s: %s\n", m.CreatedAt.UTC().Format("2006-01-02"), strings.ReplaceAll(text, "\n", " "))
		}
		digest.WriteString("\n")
	}

	// Recent session activity: auto captures live in the v1 lesson store
	// (hook/plugin conversation capture) — the cross-session trail (edited
	// files, failed commands, user direction). Surfacing the most recent
	// ones lets a fresh session or subagent lane see what just happened
	// instead of re-deriving it — bounded so the digest stays compact and
	// stale activity ages out of the window. memory.List is newest-first,
	// so the head of the slice is the most recent activity.
	const recentActivityMax = 10
	var autos []memory.Entry
	for _, e := range memory.List(root) {
		if e.Source == "auto" {
			autos = append(autos, e)
		}
	}
	if len(autos) > recentActivityMax {
		autos = autos[:recentActivityMax]
	}
	if len(autos) > 0 {
		digest.WriteString("## Recent session activity (auto captures, newest first)\n")
		for _, e := range autos {
			text := strings.ReplaceAll(e.Text, "\n", " ")
			if len(text) > 160 {
				text = text[:160] + "…"
			}
			fmt.Fprintf(&digest, "- %s: %s\n", e.Time.UTC().Format("2006-01-02 15:04"), text)
		}
		digest.WriteString("\n")
	}
	digest.WriteString(cheatsheet)

	var b strings.Builder
	b.WriteString(head.String())
	b.WriteString(digest.String())
	// The project map is the trailing section: a compact overview by
	// default, the full per-file map on request (--map).
	if p, err := code.BuildProject(root, 0, 200); err == nil {
		b.WriteString("## Project map\n")
		if opts.Map {
			b.WriteString(renderProjectMap(p, fullMapBudget))
		} else {
			b.WriteString(renderProjectOverview(p))
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// ProjectMap renders the full per-file project map for root — the same
// renderer and byte budget `kern buddy --map` uses, exposed so the
// `kern project_map` command (the one the digest's truncation pointer
// recommends) serves the identical output.
func ProjectMap(root string) (string, error) {
	p, err := code.BuildProject(root, 0, 200)
	if err != nil {
		return "", err
	}
	return renderProjectMap(p, fullMapBudget), nil
}

// surprisingSection lists the top cross-community couplings that are not
// known bridges — the "what should I look at" onboarding signal. Omitted
// entirely when there is nothing surprising, to keep the digest budget.
func surprisingSection(ix *index.Index) string {
	edges := intel.SurprisingConnections(ix, 5)
	if len(edges) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Surprising connections\n")
	for _, e := range edges {
		fmt.Fprintf(&b, "- %s → %s (communities %q → %q, %d edge(s), %s:%d)\n",
			e.Caller, e.Callee, e.CallerCommunity, e.CalleeCommunity, e.Edges, e.File, e.Line)
	}
	b.WriteString("\n")
	return b.String()
}

// fullMapBudget bounds the explicit --map per-file listing so a request for
// the full map still cannot produce unbounded output; 1MB is effectively
// "every file" for any real repo (each file summary renders ~65-200 bytes).
const fullMapBudget = 1 << 20

// renderProjectMap renders the project map, dropping whole file summaries
// past the byte budget (never cutting mid-file) with a pointer to the full
// map tool.
func renderProjectMap(p *code.Project, budget int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Project: %s (%d files", p.Root, len(p.Files))
	if p.CacheHit > 0 {
		fmt.Fprintf(&b, ", %d from cache", p.CacheHit)
	}
	b.WriteString(")\n")
	shown := 0
	for _, f := range p.Files {
		r := f.Render()
		if r == "" {
			continue
		}
		if b.Len()+len(r)+2 > budget {
			break
		}
		b.WriteString(r)
		b.WriteString("\n")
		shown++
	}
	if shown < len(p.Files) {
		fmt.Fprintf(&b, "… %d more files — full map via `kern project_map`\n", len(p.Files)-shown)
	}
	return b.String()
}

// renderProjectOverview renders a compact project summary — top-level
// directories with file counts plus a handful of entry-point symbols — in
// place of the full per-file map. It keeps the default digest short; the
// full map is one `kern buddy --map` (or `kern project_map`) away.
func renderProjectOverview(p *code.Project) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Project: %s (%d files", p.Root, len(p.Files))
	if p.CacheHit > 0 {
		fmt.Fprintf(&b, ", %d from cache", p.CacheHit)
	}
	b.WriteString(")\n")
	type dirCount struct {
		dir string
		n   int
	}
	counts := map[string]int{}
	for _, f := range p.Files {
		d := filepath.Dir(f.Path)
		if d == "." {
			d = "/"
		}
		counts[d]++
	}
	dirs := make([]dirCount, 0, len(counts))
	for d, n := range counts {
		dirs = append(dirs, dirCount{d, n})
	}
	sort.Slice(dirs, func(i, j int) bool {
		if dirs[i].n != dirs[j].n {
			return dirs[i].n > dirs[j].n
		}
		return dirs[i].dir < dirs[j].dir
	})
	if len(dirs) > 12 {
		dirs = dirs[:12]
	}
	for _, d := range dirs {
		fmt.Fprintf(&b, "  %-28s %d files\n", d.dir, d.n)
	}
	if len(dirs) < len(counts) {
		fmt.Fprintf(&b, "  … %d more directories\n", len(counts)-len(dirs))
	}
	if entries := entryPoints(p); len(entries) > 0 {
		b.WriteString("Entry points: " + strings.Join(entries, ", ") + "\n")
	}
	b.WriteString("Full per-file map: kern buddy --map (or kern project_map)\n")
	return b.String()
}

// entryPoints collects the conventional entry-point symbol names from the
// project summaries. It needs no index, so the compact overview renders even
// on a cold cache.
func entryPoints(p *code.Project) []string {
	var entries []string
	for _, f := range p.Files {
		for _, sym := range f.Symbols {
			switch sym.Name {
			case "main", "init", "run", "Main", "Run":
				entries = append(entries, sym.Name)
			}
		}
	}
	entries = dedupe(entries)
	if len(entries) > 8 {
		entries = entries[:8]
	}
	return entries
}

// Warm builds and persists the AST index for root so the next Build call
// renders the full digest without a cold pipeline. A fresh cached index is a
// no-op. Used by the MCP kern_buddy handler and kern precache.
func Warm(root string) error {
	ix, err := index.Load(root)
	if err == nil && ix != nil && !ix.Stale() {
		return nil
	}
	ix, err = index.Build(root)
	if err != nil {
		return err
	}
	return ix.Save()
}

// callEdges returns the total number of directed caller→callee edges in an
// index. A symbol that calls three helpers contributes three edges, not
// one row.
func callEdges(ix *index.Index) int {
	n := 0
	for _, callees := range ix.Calls {
		n += len(callees)
	}
	return n
}

func indexSection(ix *index.Index) string {
	var b strings.Builder
	b.WriteString("## Index\n")
	langs := ix.Languages()
	if len(langs) > 0 {
		b.WriteString("Languages: " + strings.Join(langs, ", ") + "\n")
	}
	fmt.Fprintf(&b, "Symbols: %d · Call edges: %d · Files indexed: %d\n",
		len(ix.Symbols), callEdges(ix), len(ix.FileHashes))

	kindCount := map[string]int{}
	for _, s := range ix.Symbols {
		kindCount[s.Kind]++
	}
	var kinds []string
	for k, n := range kindCount {
		kinds = append(kinds, fmt.Sprintf("%s %d", k, n))
	}
	slices.Sort(kinds)
	if len(kinds) > 0 {
		b.WriteString("Kinds: " + strings.Join(kinds, " · ") + "\n")
	}

	// Phase 5 (2026-10-07): "Most-called (hubs)" must show the project's own
	// production hubs, not raw call-target noise. Counting every ix.Callers
	// target made EXTERNAL symbols — T.Fatalf, len, T.Errorf, strings.Contains,
	// called mostly from _test.go files and undefined in this repo — dominate
	// the digest. Rank like intel.Hubs instead: only targets DEFINED in the
	// index whose preferred definition is a production file (not a test file,
	// not a testdata/ fixture) count.
	defFile := map[string]string{}
	for _, s := range ix.Symbols {
		if s.File == "" {
			continue
		}
		full := s.FullName()
		if cur, ok := defFile[full]; ok && !intel.IsTestFile(cur) && !intel.IsFixtureFile(cur) {
			continue // a production definition is already recorded
		}
		defFile[full] = s.File
	}
	hubCount := map[string]int{}
	for sym, callers := range ix.Callers {
		if len(callers) <= 1 {
			continue
		}
		f := defFile[sym]
		if f == "" || intel.IsTestFile(f) || intel.IsFixtureFile(f) {
			continue
		}
		hubCount[sym] = len(callers)
	}
	if len(hubCount) > 0 {
		type hub struct {
			name string
			n    int
		}
		var hubs []hub
		for sym, n := range hubCount {
			hubs = append(hubs, hub{sym, n})
		}
		sort.Slice(hubs, func(i, j int) bool {
			if hubs[i].n != hubs[j].n {
				return hubs[i].n > hubs[j].n
			}
			return hubs[i].name < hubs[j].name
		})
		if len(hubs) > 8 {
			hubs = hubs[:8]
		}
		b.WriteString("Most-called (hubs):\n")
		for _, h := range hubs {
			fmt.Fprintf(&b, "  %s (%d callers)\n", h.name, h.n)
		}
	}

	var entries []string
	for _, s := range ix.Symbols {
		name := s.Name
		if s.Receiver != "" {
			name = s.Receiver + "." + name
		}
		if name == "main" || name == "init" || name == "run" || name == "Main" || name == "Run" {
			entries = append(entries, name)
		}
	}
	if len(entries) > 0 {
		b.WriteString("Entry points: " + strings.Join(dedupe(entries), ", ") + "\n")
	}

	fwEntries := frameworkEntries(ix)
	if len(fwEntries) > 0 {
		// The framework id comes from the indexer's entry-point heuristic
		// and can claim a JS framework ("js-router") for routes that are
		// really Go http-mux handlers — or test fixtures mirroring them —
		// so don't present framework identity we cannot verify. On a
		// Go-primary repo the digest's HTTP surface is Go's: render the
		// section generically with "http" labels.
		b.WriteString("HTTP endpoints (handler → route):\n")
		for _, e := range fwEntries {
			fmt.Fprintf(&b, "  %-30s %-10s %s\n", e.name, httpEndpointLabel(ix, e.fw), e.route)
		}
	}
	b.WriteString("\n")
	return b.String()
}

// httpEndpointLabel returns the framework label for the digest's
// HTTP-endpoint section. The indexer's entry-point heuristic assigns
// framework ids (e.g. "js-router") from source patterns; on a Go-primary
// repo those claims cannot be trusted — the routes a newcomer sees belong
// to Go http-mux handlers, not a JS router — so the digest renders "http"
// and stops claiming a framework identity it cannot verify. On repos where
// Go is not the dominant language the heuristic ids are kept.
func httpEndpointLabel(ix *index.Index, fw string) string {
	if goPrimary(ix) {
		return "http"
	}
	return fw
}

// goPrimary reports whether Go is the repo's dominant language (the
// language with the most symbols in the index).
func goPrimary(ix *index.Index) bool {
	counts := map[string]int{}
	for _, s := range ix.Symbols {
		if s.Lang != "" {
			counts[s.Lang]++
		}
	}
	if len(counts) == 0 {
		return false
	}
	best, bestN := "", -1
	for l, n := range counts {
		if n > bestN {
			best, bestN = l, n
		}
	}
	return best == "go"
}

type fwEntry struct {
	name, fw, route string
}

// frameworkEntries collects enriched entry-point symbols (framework handlers,
// controllers, route targets) ordered by framework then name.
func frameworkEntries(ix *index.Index) []fwEntry {
	var out []fwEntry
	for _, s := range ix.Symbols {
		if !s.Entry || s.Framework == "" {
			continue
		}
		name := s.Name
		if s.Receiver != "" {
			name = s.Receiver + "." + name
		}
		out = append(out, fwEntry{name: name, fw: s.Framework, route: s.Route})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].fw != out[j].fw {
			return out[i].fw < out[j].fw
		}
		return out[i].name < out[j].name
	})
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

// archGateSymbols and archGateEdges cap the architecture section: community
// detection runs label propagation over the whole call graph, which takes
// minutes on very large repos. The digest gates it off; per-symbol graph
// tools still serve analysis on demand.
const (
	archGateSymbols = 20000
	archGateEdges   = 40000
)

// architectureSection adds the community/coupling overview so the onboarding
// digest doubles as architecture discovery. Skipped for huge graphs.
func architectureSection(ix *index.Index) string {
	edges := callEdges(ix)
	if edges == 0 {
		return ""
	}
	if len(ix.Symbols) > archGateSymbols || edges > archGateEdges {
		return compactArchitectureFallback(ix, edges)
	}
	arch := intel.AnalyzeArchitecture(ix)
	if len(arch.Communities) == 0 && len(arch.Coupling) == 0 {
		// The graph has local calls but they did not coalesce into detected
		// communities (e.g. very small or star-shaped graphs). Surface that
		// call structure exists rather than reporting nothing.
		return "## Architecture\n(call structure present but too small to cluster into communities)\n\n"
	}
	var b strings.Builder
	b.WriteString("## Architecture (communities + coupling)\n")
	for _, c := range arch.Communities {
		fmt.Fprintf(&b, "  %-24s size %-4d hub %-24s pkgs %s\n",
			c.ID, c.Size, c.Hub, strings.Join(c.Packages, ", "))
	}
	if len(arch.Coupling) > 0 {
		b.WriteString("coupling warnings (cross-community call bundles):\n")
		shown := arch.Coupling
		if len(shown) > 5 {
			shown = shown[:5]
		}
		for _, e := range shown {
			fmt.Fprintf(&b, "  %-24s <-> %-24s %4d edges\n", e.From, e.To, e.Count)
		}
	}
	b.WriteString("\n")
	return b.String()
}

// compactArchitectureFallback renders the cheap package-count overview when
// the full community analysis is gated off for very large repos. It uses
// only aggregations already present in the index (symbol → file → package
// dir), never graph analysis, so the gate still bounds analysis cost while
// the digest keeps its most useful newcomer section.
func compactArchitectureFallback(ix *index.Index, edges int) string {
	type pkgCount struct {
		dir string
		n   int
	}
	counts := map[string]int{}
	for _, s := range ix.Symbols {
		d := filepath.Dir(s.File)
		if d == "." {
			d = "/"
		}
		counts[d]++
	}
	pkgs := make([]pkgCount, 0, len(counts))
	for d, n := range counts {
		pkgs = append(pkgs, pkgCount{d, n})
	}
	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].n != pkgs[j].n {
			return pkgs[i].n > pkgs[j].n
		}
		return pkgs[i].dir < pkgs[j].dir
	})
	var b strings.Builder
	b.WriteString("## Architecture (compact — full community analysis gated)\n")
	fmt.Fprintf(&b, "  %d packages/subsystems · %d symbols · %d call edges — over the digest's analysis gate; use `kern graph --html` for the interactive explorer\n",
		len(pkgs), len(ix.Symbols), edges)
	for i, p := range pkgs {
		if i >= 5 {
			break
		}
		fmt.Fprintf(&b, "  %-28s %d symbols\n", p.dir, p.n)
	}
	if len(pkgs) > 5 {
		fmt.Fprintf(&b, "  … %d more packages\n", len(pkgs)-5)
	}
	b.WriteString("\n")
	return b.String()
}

func statsSection(b *strings.Builder) {
	rec, err := stats.NewRecorder()
	if err != nil {
		return
	}
	s, err := rec.Summarize(7, "")
	if err != nil {
		return
	}
	// M9: the ops/tokens/$ numbers come from the machine-wide metrics
	// recorder (persisted in the global cache, shared by every project on
	// this machine) — never present them as project-scoped metrics. The
	// prefix makes the scope unambiguous.
	fmt.Fprintf(b, "## kern savings (last 7 days)\nmachine-wide (all projects): %d ops · %d tokens saved (%.1f%%) · ~$%.4f cost saved\n\n",
		s.Operations, s.SavedTotal, s.SavedPct, s.CostSaved)
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	cp := append([]string(nil), in...)
	slices.Sort(cp)
	return slices.Compact(cp)
}

// projectMemoryMax caps the rendered "Project memory" list so a long
// cross-session trail stays a compact digest section, not a debug dump.
const projectMemoryMax = 10

// normalizeLessonText trims and collapses whitespace so near-duplicate
// lessons (the same text with different wrapping or spacing) share one key.
func normalizeLessonText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// dedupeLessons collapses near-duplicate memories to their most recent
// occurrence: entries whose normalized text matches are reduced to the one
// with the newest CreatedAt, preserving the input's newest-first order.
func dedupeLessons(in []domain.Memory) []domain.Memory {
	if len(in) == 0 {
		return nil
	}
	mostRecent := map[string]domain.Memory{}
	for _, m := range in {
		key := normalizeLessonText(m.Content)
		if key == "" {
			continue
		}
		if prev, ok := mostRecent[key]; !ok || m.CreatedAt.After(prev.CreatedAt) {
			mostRecent[key] = m
		}
	}
	out := make([]domain.Memory, 0, len(mostRecent))
	for _, m := range in {
		key := normalizeLessonText(m.Content)
		best, ok := mostRecent[key]
		if !ok {
			continue
		}
		if best.CreatedAt.Equal(m.CreatedAt) && best.Content == m.Content {
			out = append(out, m)
			delete(mostRecent, key)
		}
	}
	return out
}

const cheatsheet = `## How to use kern in this session
- kern compresses your tool output automatically when it is large, and is
  available as first-class tools (opencode) or MCP tools (any agent).
- Ask kern for context, not whole files: kern context <symbol>, kern graph
  <symbol>, kern ast "pattern".
- Paste large logs/errors through kern and read the compressed result.
- Track savings with kern stats; adjust with kern optimize --llm <model>.
`
