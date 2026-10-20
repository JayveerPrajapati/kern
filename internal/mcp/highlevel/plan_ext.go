// Plan extensions (B3 context-sufficiency proof + B5 first slice, anti-stall
// campaign Phase 3). Both are deterministic, output-only sections appended to
// the rendered kern_plan text — no new MCP tools, no description changes, no
// new internal imports. They answer "do I have everything?" (which affected
// files has this session already been served?) and "what is the cheapest
// first move?" (the most-leaf, smallest, cheapest-to-verify affected file).
package highlevel

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
)

// appendPlanExtensions appends the two sections to a rendered plan. Each
// section is omitted when it has no data (no served-coverage hook for the
// sufficiency proof; no affected files for the first slice).
func appendPlanExtensions(out string, h Hooks, args map[string]any, pkt *domain.ContextPacket) string {
	files := pkt.Files
	if sufficiency := renderSufficiency(h, args, files); sufficiency != "" {
		out += "\n" + sufficiency
	}
	if first := renderFirstSlice(pkt); first != "" {
		out += "\n" + first
	}
	return out
}

// sessionLabel renders the session identifier for the sufficiency section:
// the agent part of the session key (agent_id; the |task suffix is
// truncated), or "default" when no agent_id was carried.
func sessionLabel(args map[string]any) string {
	agent := mcpargs.ArgString(args, "agent_id")
	if agent == "" {
		return "default"
	}
	return agent
}

// renderSufficiency renders the B3 section: how many of the plan's affected
// files the session has already been served, and which are still missing.
// The hook is the data source; without it (nil) the section is omitted — no
// coverage data exists to render.
func renderSufficiency(h Hooks, args map[string]any, files []domain.File) string {
	if h.ServedThisSession == nil {
		return ""
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	covered := h.ServedThisSession(paths)
	missing := make([]string, 0, len(paths))
	for _, p := range paths {
		if !covered[p] {
			missing = append(missing, p)
		}
	}
	var b strings.Builder
	b.WriteString("## Context sufficiency\n")
	if len(missing) == 0 {
		b.WriteString("ready-to-execute: true\n")
	} else {
		fmt.Fprintf(&b, "ready-to-execute: false\nmissing: %s\n", strings.Join(missing, ", "))
	}
	fmt.Fprintf(&b, "served: %d/%d affected files (session %s)", len(paths)-len(missing), len(paths), sessionLabel(args))
	return b.String()
}

// rankedFile is the deterministic first-slice pick: the file path plus its
// entry symbol and kind ("" when the file has no symbols in the packet).
type rankedFile struct {
	Path   string
	Symbol string
	Kind   string
}

// outboundDeps counts, per affected file, the packet's dependency edges that
// originate from a symbol in that file (the "blast radius" a first edit
// would carry). Edges whose From does not resolve to a packet symbol are
// skipped (deterministic — the packet is the only source).
func outboundDeps(pkt *domain.ContextPacket) map[string]int {
	byName := map[string]string{}
	for _, f := range pkt.Files {
		for _, sym := range f.Symbols {
			if sym.Name != "" {
				byName[sym.Name] = f.Path
			}
			if sym.Qualified != "" {
				byName[sym.Qualified] = f.Path
			}
		}
	}
	out := map[string]int{}
	for _, e := range pkt.Dependencies {
		if f, ok := byName[e.From]; ok {
			out[f]++
		}
	}
	return out
}

// rankFiles orders the affected files deterministically for the first slice:
// fewest outbound deps → fewest symbols → smallest LOC (Lines) → lexical
// path as the final tiebreak. The ranking uses only data the Plan workflow
// already holds (the context packet), so it needs no new intel imports.
func rankFiles(files []domain.File, outbound map[string]int) []domain.File {
	ranked := append([]domain.File(nil), files...)
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if da, db := outbound[a.Path], outbound[b.Path]; da != db {
			return da < db
		}
		if len(a.Symbols) != len(b.Symbols) {
			return len(a.Symbols) < len(b.Symbols)
		}
		if a.Lines != b.Lines {
			return a.Lines < b.Lines
		}
		return a.Path < b.Path
	})
	return ranked
}

// rankFirstSlice returns the top-ranked affected file (the deterministic
// first slice). ok is false when the packet has no files.
func rankFirstSlice(pkt *domain.ContextPacket) (rankedFile, bool) {
	if len(pkt.Files) == 0 {
		return rankedFile{}, false
	}
	outbound := outboundDeps(pkt)
	top := rankFiles(pkt.Files, outbound)[0]
	r := rankedFile{Path: top.Path}
	if len(top.Symbols) > 0 {
		r.Symbol = top.Symbols[0].Name
		r.Kind = strings.ToLower(top.Symbols[0].Kind)
	}
	return r, true
}

// renderFirstSlice renders the B5 section: the cheapest verifiable first
// edit among the affected files and how to verify it.
func renderFirstSlice(pkt *domain.ContextPacket) string {
	top, ok := rankFirstSlice(pkt)
	if !ok {
		return ""
	}
	verify := "go test ./"
	if dir := dirFor(top.Path); dir != "" {
		verify = "go test ./" + dir + "/"
	}
	var b strings.Builder
	b.WriteString("## First slice\n")
	if top.Symbol != "" {
		fmt.Fprintf(&b, "Smallest verifiable first edit: %s (%s)\none %s, verify with: %s", top.Path, top.Symbol, top.Kind, verify)
	} else {
		fmt.Fprintf(&b, "Smallest verifiable first edit: %s\nverify with: %s", top.Path, verify)
	}
	return b.String()
}

// dirFor returns the package directory of a file path ("" for a root-level
// file, so the verify command reads "go test ./").
func dirFor(path string) string {
	dir := filepath.Dir(path)
	if dir == "." {
		return ""
	}
	return dir
}
