// Package brief produces a compact onboarding digest for an agent at the
// start of a session: project map, index summary, hub symbols, entry points
// and recent kern savings. It is kern acting as the agent's "buddy" — the
// best starting context for a fresh session.
package brief

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/stats"
)

// Build renders the briefing for root. Errors are reported per section; the
// function itself only fails if the root is unusable. On a cold index it skips
// the index/architecture sections with a hint rather than building a possibly-
// huge index; callers can warm it via Warm or `kern index .` / `kern precache .`.
func Build(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	var head strings.Builder
	fmt.Fprintf(&head, "# kern buddy briefing — %s\n", abs)
	fmt.Fprintf(&head, "Generated %s. This digest gives you the best starting context.\n\n",
		time.Now().UTC().Format("2006-01-02 15:04"))

	// Render the fixed sections (index, architecture, stats, memory,
	// cheatsheet) first so the project map can be sized into the remaining
	// budget: the digest must always fit inside the MCP output sandbox
	// (24KB default) or its tail sections get clipped off in tool output.
	var tail strings.Builder
	ix, err := index.Load(root)
	if err != nil {
		tail.WriteString("## Index\n(not built yet — run `kern index .` or `kern precache .` once to enable the symbols/architecture sections)\n\n")
		ix = nil
	}
	if ix != nil {
		tail.WriteString(indexSection(ix))
		tail.WriteString(architectureSection(ix))
	}
	statsSection(&tail)
	if entries := memory.List(root); len(entries) > 0 {
		tail.WriteString("## Project memory (from past sessions)\n")
		for _, e := range entries {
			text := e.Text
			if len(text) > 400 {
				text = text[:400] + "…"
			}
			fmt.Fprintf(&tail, "- %s: %s\n", e.Time.UTC().Format("2006-01-02"), strings.ReplaceAll(text, "\n", " "))
		}
		tail.WriteString("\n")
	}
	tail.WriteString(cheatsheet)

	var b strings.Builder
	b.WriteString(head.String())
	if p, err := code.BuildProject(root, 0, 200); err == nil {
		b.WriteString("## Project map\n")
		b.WriteString(renderProjectMap(p, digestBudget-head.Len()-tail.Len()))
		b.WriteString("\n")
	}
	b.WriteString(tail.String())
	return b.String(), nil
}

// digestBudget caps the whole briefing so it fits inside the MCP output
// sandbox (24KB default) with headroom; the project map gets whatever the
// fixed sections leave. The full map stays available via kern_project_map.
const digestBudget = 21 << 10

// mapFloor is the minimum the project-map section always gets, even when the
// fixed sections are unusually large.
const mapFloor = 4 << 10

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

func indexSection(ix *index.Index) string {
	var b strings.Builder
	b.WriteString("## Index\n")
	langs := ix.Languages()
	if len(langs) > 0 {
		b.WriteString("Languages: " + strings.Join(langs, ", ") + "\n")
	}
	fmt.Fprintf(&b, "Symbols: %d · Call edges: %d · Files indexed: %d\n",
		len(ix.Symbols), len(ix.Calls), len(ix.FileHashes))

	kindCount := map[string]int{}
	for _, s := range ix.Symbols {
		kindCount[s.Kind]++
	}
	var kinds []string
	for k, n := range kindCount {
		kinds = append(kinds, fmt.Sprintf("%s %d", k, n))
	}
	sort.Strings(kinds)
	if len(kinds) > 0 {
		b.WriteString("Kinds: " + strings.Join(kinds, " · ") + "\n")
	}

	hubCount := map[string]int{}
	for sym, callers := range ix.Callers {
		if len(callers) > 1 {
			hubCount[sym] = len(callers)
		}
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
		b.WriteString("Framework endpoints (handler → route):\n")
		for _, e := range fwEntries {
			fmt.Fprintf(&b, "  %-30s %s %s\n", e.name, e.fw, e.route)
		}
	}
	b.WriteString("\n")
	return b.String()
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
	if len(ix.Calls) == 0 {
		return ""
	}
	if len(ix.Symbols) > archGateSymbols || len(ix.Calls) > archGateEdges {
		return fmt.Sprintf("## Architecture\n(skipped — %d symbols / %d call edges exceed the digest's analysis gate; use `kern graph --html` for the interactive explorer)\n\n",
			len(ix.Symbols), len(ix.Calls))
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

func statsSection(b *strings.Builder) {
	rec, err := stats.NewRecorder()
	if err != nil {
		return
	}
	s, err := rec.Summarize(7, "")
	if err != nil {
		return
	}
	fmt.Fprintf(b, "## kern savings (last 7 days)\n%d ops · %d tokens saved (%.1f%%) · ~$%.4f cost saved\n\n",
		s.Operations, s.SavedTotal, s.SavedPct, s.CostSaved)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
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
