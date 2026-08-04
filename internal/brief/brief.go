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
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/stats"
)

// Build renders the briefing for root. Errors are reported per section; the
// function itself only fails if the root is unusable.
func Build(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# kern buddy briefing — %s\n", abs)
	fmt.Fprintf(&b, "Generated %s. This digest gives you the best starting context.\n\n",
		time.Now().UTC().Format("2006-01-02 15:04"))

	if p, err := code.BuildProject(root, 500, 200); err == nil {
		b.WriteString("## Project map\n")
		b.WriteString(p.Render())
		b.WriteString("\n")
	}

	ix, err := index.Load(root)
	if err != nil {
		ix, err = index.Build(root)
	}
	if err == nil && ix != nil {
		b.WriteString(indexSection(ix))
	}

	statsSection(&b)

	if entries := memory.List(root); len(entries) > 0 {
		b.WriteString("## Project memory (from past sessions)\n")
		for _, e := range entries {
			text := e.Text
			if len(text) > 400 {
				text = text[:400] + "…"
			}
			fmt.Fprintf(&b, "- %s: %s\n", e.Time.UTC().Format("2006-01-02"), strings.ReplaceAll(text, "\n", " "))
		}
		b.WriteString("\n")
	}

	b.WriteString(cheatsheet)
	return b.String(), nil
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
