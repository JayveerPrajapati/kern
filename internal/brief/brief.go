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
	"github.com/JayveerPrajapati/kern/internal/topology"
)

// Options controls how the briefing is rendered.
type Options struct {
	// Map renders the full per-file project map as the trailing section
	// instead of the compact project overview. Off by default so the
	// digest stays short; the map is served on demand (`kern buddy --map`).
	Map bool

	// NoMemory skips the "Project memory" and "Recent session activity"
	// sections (kern_buddy's no_memory param). Subagent lanes that already
	// carry session context can request a memory-free digest so captures
	// never re-enter the context.
	NoMemory bool
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
	digest.WriteString(statsSection(abs))
	if !opts.NoMemory {
		digest.WriteString(memorySections(root))
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

// memorySections renders the two memory sections of the digest: "Project
// memory (from past sessions)" (the TYPED store — the one the learn and
// capture paths write) and "Recent session activity" (v1 auto captures).
// kern_buddy's no_memory option skips both via Options.NoMemory.
func memorySections(root string) string {
	var b strings.Builder
	// Project memory: read the TYPED store. Reading only the v1 lesson
	// store here is what made "Project memory" render empty for repos whose
	// v1 store holds just auto captures (P2-14): the typed store is where
	// real lessons land.
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
		b.WriteString("## Project memory (from past sessions)\n")
		for i, m := range lessons {
			if i >= projectMemoryMax {
				fmt.Fprintf(&b, "- … and %d more (full history via `kern memory`)\n", len(lessons)-projectMemoryMax)
				break
			}
			text := m.Content
			if len(text) > 400 {
				text = text[:400] + "…"
			}
			fmt.Fprintf(&b, "- %s: %s\n", m.CreatedAt.UTC().Format("2006-01-02"), strings.ReplaceAll(text, "\n", " "))
		}
		b.WriteString("\n")
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
		b.WriteString("## Recent session activity (auto captures, newest first)\n")
		for _, e := range autos {
			text := strings.ReplaceAll(e.Text, "\n", " ")
			if len(text) > 160 {
				text = text[:160] + "…"
			}
			fmt.Fprintf(&b, "- %s: %s\n", e.Time.UTC().Format("2006-01-02 15:04"), text)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// surprisingSection lists the top cross-community couplings that are not
// known bridges — the "what should I look at" onboarding signal. Omitted
// entirely when there is nothing surprising, to keep the digest budget.
func surprisingSection(ix *index.Index) string {
	edges := topology.SurprisingConnections(ix, 5)
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

// digestHubs returns the digest's "Most-called (hubs)" list: the top hubs by
// ACTUAL caller count (the section's own "(N callers)" measure), with
// 0-caller symbols dropped (QA 5.6.b). The pool is the shared topology.Hubs
// top-decile ranking (production symbols only); the digest re-ranks it by
// Callers desc so a community-weighted fan-out symbol (main with 0 callers)
// can never lead the list, then caps at the section's limit.
func digestHubs(ix *index.Index, limit int) []topology.Hub {
	pool := topology.Hubs(ix, 0)
	var hubs []topology.Hub
	for _, h := range pool {
		if h.Callers == 0 {
			continue // a symbol nothing calls is not "most-called"
		}
		hubs = append(hubs, h)
	}
	sort.Slice(hubs, func(i, j int) bool {
		if hubs[i].Callers != hubs[j].Callers {
			return hubs[i].Callers > hubs[j].Callers
		}
		if hubs[i].Calls != hubs[j].Calls {
			return hubs[i].Calls > hubs[j].Calls
		}
		return hubs[i].Symbol < hubs[j].Symbol
	})
	if len(hubs) > limit {
		hubs = hubs[:limit]
	}
	return hubs
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

	// Phase 5 / F2 (2026-10-07): "Most-called (hubs)" must show the
	// project's own production hubs, not raw call-target noise. intel.Hubs
	// is the shared, authoritative ranking (kern hubs, web dashboards): it
	// excludes test-file and testdata symbols entirely, counts only
	// production callers (test-file callers of a production symbol do not
	// inflate its hub weight — prodCallersWithFileMap), and normalizes by
	// community size. Counting raw ix.Callers here previously let
	// T.Fatalf-style test noise dominate the digest.
	//
	// QA 5.6(b): the digest ranks its hubs by ACTUAL caller count, not the
	// community-weighted score. The weighted score can rank a 0-caller
	// fan-out symbol (e.g. main — nobody calls it, it calls everything)
	// above every real hub, which reads as "most-called: main (0 callers)".
	// A symbol nothing calls is not "most-called", so 0-caller entries are
	// dropped and the list is ordered by the same "(N callers)" measure the
	// section prints.
	hubs := digestHubs(ix, 8)
	if len(hubs) > 0 {
		b.WriteString("Most-called (hubs):\n")
		for _, h := range hubs {
			fmt.Fprintf(&b, "  %s (%d callers)\n", h.Symbol, h.Callers)
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
		// QA 6.7(b): cap the entry-point line — a repo with many run/init
		// symbols must not turn the digest's index section into a long list.
		uniq := dedupe(entries)
		if len(uniq) > 8 {
			fmt.Fprintf(&b, "Entry points: %s, … (%d total)\n", strings.Join(uniq[:8], ", "), len(uniq))
		} else {
			b.WriteString("Entry points: " + strings.Join(uniq, ", ") + "\n")
		}
	}

	fwEntries, fwTotal := frameworkEntries(ix)
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
		if fwTotal > len(fwEntries) {
			fmt.Fprintf(&b, "  … and %d more endpoints (see `kern graph --entities`)\n", fwTotal-len(fwEntries))
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

// dominantLang returns the language with the most symbols in the index
// ("" on an empty index). The endpoint section ranks entries in the
// dominant language first: on a Go-primary repo the real server routes
// (net-http) must take the capped slots before JS client fetch wrappers
// (F3-1) — the old plain fw-alphabetical sort ("js-router" < "net-http")
// filled every slot with the TS SDK's client routes and hid the Go
// surface entirely.
func dominantLang(ix *index.Index) string {
	counts := map[string]int{}
	for _, s := range ix.Symbols {
		if s.Lang != "" {
			counts[s.Lang]++
		}
	}
	best, bestN := "", -1
	for l, n := range counts {
		if n > bestN {
			best, bestN = l, n
		}
	}
	return best
}

// goPrimary reports whether Go is the repo's dominant language (the
// language with the most symbols in the index).
func goPrimary(ix *index.Index) bool { return dominantLang(ix) == "go" }

type fwEntry struct {
	name, fw, route string
	primary         bool // language matches the repo's dominant language
}

// frameworkEntries collects enriched entry-point symbols (framework handlers,
// controllers, route targets) ordered by dominant-language entries first,
// then framework, then name. F2 (2026-10-07): entries defined in test files
// (or testdata fixtures) are skipped — a test fixture mirroring a real
// handler must not be presented as the digest's HTTP surface — and exact
// (name, route) duplicates collapse to one entry (the indexer can register
// the same handler twice with an unpopulated route, e.g. the live "/v1/memory"
// duplicate; route was historically never copied on some paths). F3-1
// (2026-10-08): returns the pre-cap total alongside the capped list so the
// digest can disclose what the cap hid.
func frameworkEntries(ix *index.Index) ([]fwEntry, int) {
	dom := dominantLang(ix)
	var out []fwEntry
	for _, s := range ix.Symbols {
		if !s.Entry || s.Framework == "" {
			continue
		}
		if intel.IsTestFile(s.File) || intel.IsFixtureFile(s.File) {
			continue
		}
		name := s.Name
		if s.Receiver != "" {
			name = s.Receiver + "." + name
		}
		out = append(out, fwEntry{name: name, fw: s.Framework, route: s.Route, primary: s.Lang == dom})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].primary != out[j].primary {
			return out[i].primary
		}
		if out[i].fw != out[j].fw {
			return out[i].fw < out[j].fw
		}
		if out[i].name != out[j].name {
			return out[i].name < out[j].name
		}
		return out[i].route < out[j].route
	})
	// Dedupe by (name, route), mirroring the dedupe() collapse semantics:
	// keep the first occurrence of each pair after the sort.
	seen := map[[2]string]bool{}
	uniq := out[:0]
	for _, e := range out {
		key := [2]string{e.name, e.route}
		if seen[key] {
			continue
		}
		seen[key] = true
		uniq = append(uniq, e)
	}
	out = uniq
	if len(out) > 20 {
		out = out[:20]
	}
	return out, len(uniq)
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
	arch := topology.AnalyzeArchitecture(ix)
	if len(arch.Communities) == 0 && len(arch.Coupling) == 0 {
		// The graph has local calls but they did not coalesce into detected
		// communities (e.g. very small or star-shaped graphs). Surface that
		// call structure exists rather than reporting nothing.
		return "## Architecture\n(call structure present but too small to cluster into communities)\n\n"
	}
	return renderArchitectureSection(&arch)
}

// renderArchitectureSection renders the community/coupling overview, with the
// community list capped (QA 6.7(b)) so a large repo's many communities cannot
// push the digest past its byte budget. Split from architectureSection so
// tests can exercise the cap without re-running community detection.
func renderArchitectureSection(arch *topology.Architecture) string {
	var b strings.Builder
	b.WriteString("## Architecture (communities + coupling)\n")
	// QA 6.7(b): the community list was unbounded — on a large repo hundreds
	// of communities (each with a joined package list) pushed the digest past
	// ~25KB. Cap the rendered list at the top communities by size with a
	// truncation pointer; the full graph stays one `kern graph` away.
	const maxCommunities = 12
	communities := arch.Communities
	if len(communities) > maxCommunities {
		communities = communities[:maxCommunities]
	}
	for _, c := range communities {
		fmt.Fprintf(&b, "  %-24s size %-4d hub %-24s pkgs %s\n",
			c.ID, c.Size, c.Hub, strings.Join(c.Packages, ", "))
	}
	if len(arch.Communities) > maxCommunities {
		fmt.Fprintf(&b, "  … and %d more communities (full graph via `kern graph --html`)\n",
			len(arch.Communities)-maxCommunities)
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

func statsSection(root string) string {
	rec, err := stats.NewRecorder()
	if err != nil {
		return "## kern savings\n(stats unavailable: " + err.Error() + ")\n\n"
	}
	sum, err := rec.SummarizeByRoot(30, "", root)
	if err != nil || (sum.Operations == 0 && sum.ToolCalls == 0) {
		return "## kern savings\n(no rooted project stats yet — run `kern stats` for the machine-wide view)\n\n"
	}
	var b strings.Builder
	b.WriteString("## kern savings (project, last 30 days)\n")
	fmt.Fprintf(&b, "  %d tool calls · %d optimization events · %d tokens saved (%.1f%%)\n",
		sum.ToolCalls, sum.Operations, sum.SavedTotal, sum.SavedPct)
	b.WriteString("  machine-wide view: `kern stats`\n\n")
	return b.String()
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
