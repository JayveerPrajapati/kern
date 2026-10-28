// Package explorebudget renders the budgeted kern_explore surface: the same
// report intel.RenderExplore produces, bounded to a max_tokens budget at
// ASSEMBLY time (QA 2026-10-19 item 2). The cap is not just a source-fitting
// budget — optional sections (stale banner, callee skeletons, stats panel,
// evidence) are dropped first, then the caller/callee/blast/file lists are
// trimmed with an explicit "+N more (raise max_tokens)" marker, and the
// definition core (symbol header + source) is never dropped. It was
// extracted from internal/intel so crossing intel's cap forces a family out
// (ARCHITECTURE.md doctrine) instead of cap-raising.
package explorebudget

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/calleefilter"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// Render renders the report bounded to maxTokens tokens. maxTokens <= 0
// renders the full report unchanged.
func Render(r *intel.ExploreReport, maxTokens int) string {
	if r == nil || maxTokens <= 0 || tokenize.Count(intel.RenderExplore(r)) <= maxTokens {
		return intel.RenderExplore(r)
	}
	// Sections in canonical output order, built up front so the budget sees
	// real sizes.
	var notes strings.Builder
	if r.Symbol != r.Resolved {
		how := "fuzzy match"
		switch {
		case r.Resolved == r.Symbol || strings.HasSuffix(r.Resolved, "."+r.Symbol):
			how = "exact name"
		case strings.ContainsAny(r.Symbol, "./") && strings.HasSuffix(r.Symbol, "."+simpleName(r.Resolved)):
			how = "package-qualified match"
		}
		fmt.Fprintf(&notes, "resolved %s -> %s (%s)\n", r.Symbol, r.Resolved, how)
	}
	if len(r.Alternatives) > 0 {
		advice := "Pass a qualified name (Type.Method or dir/pkg.Symbol) to choose."
		if strings.ContainsAny(r.Symbol, "./") {
			advice = "Pass a package-qualified name (dir/pkg.Symbol) to choose."
		}
		fmt.Fprintf(&notes, "note: %q is ambiguous — showing %s; other definitions: %s. %s\n",
			r.Symbol, r.Resolved, strings.Join(r.Alternatives, ", "), advice)
	}
	head := fmt.Sprintf("symbol: %s (%s %s:%d)\n\n", r.Resolved, r.Definition.Kind, r.Definition.File, r.Definition.Line)
	callees := filterNoiseCallees(r, r.Callees)
	lists := []*listSec{
		newListSec(fmt.Sprintf("== callers (%d, unique simple names of direct callers) ==\n", len(r.Callers)), r.Callers, r.CallerConf, r.CallerSynth, r.CallerLocs),
		newListSec(fmt.Sprintf("== callees (%d) ==\n", len(callees)), callees, r.CalleeConf, r.CalleeSynth, nil),
		newListSec(fmt.Sprintf("== blast radius (%d symbols, %d files) ==\n", len(r.BlastRadius), len(r.BlastFiles)), r.BlastRadius, nil, nil, nil),
		newListSec("== affected files ==\n", r.BlastFiles, nil, nil, nil),
	}
	// The textual-reference safety net is part of the answer, not an optional
	// panel: budget it like the other lists so a tight budget trims it with an
	// explicit marker instead of silently deleting the only evidence that a
	// non-callable symbol is referenced.
	if r.ReferenceConfidence != "" {
		lists = append(lists, newListSec(fmt.Sprintf("== references (%d files, %s confidence) ==\n", len(r.ReferenceFiles), r.ReferenceConfidence), r.ReferenceFiles, nil, nil, nil))
	}
	var optionals []string
	if r.StaleBanner != "" {
		optionals = append(optionals, r.StaleBanner+"\n\n")
	}
	if len(r.CalleeSkels) > 0 {
		optionals = append(optionals, "\n"+strings.Join(r.CalleeSkels, "\n")+"\n")
	}
	if r.Stats != nil && r.Stats.Summary() != "" {
		optionals = append(optionals, "\n"+r.Stats.Summary()+"\n")
	}
	if r.Evidence != "" {
		optionals = append(optionals, "\n"+r.Evidence+"\n")
	}

	// Drop optional sections lowest-first (evidence, stats, skeletons,
	// banner) until the rest fits — the QA drop order.
	core := tokenize.Count(notes.String()+head) + tokenize.Count("== source ==\n")
	srcTok := tokenize.Count(r.Source)
	listTok := 0
	for _, ls := range lists {
		listTok += ls.fullTok
	}
	optTok := 0
	for _, o := range optionals {
		optTok += tokenize.Count(o)
	}
	over := core + listTok + srcTok + optTok - maxTokens
	kept := len(optionals)
	for kept > 0 && over > 0 {
		kept--
		over -= tokenize.Count(optionals[kept])
	}

	// Re-fit the source, reserving half the remaining room for the lists.
	room := maxTokens - core
	for _, o := range optionals[:kept] {
		room -= tokenize.Count(o)
	}
	var src string
	if room < 64 {
		src = budget.FitCode(r.Source, room)
	} else {
		reserve := room / 2
		if reserve > 512 {
			reserve = 512
		}
		src = budget.FitCode(r.Source, room-reserve)
	}
	sourceSec := "== source ==\n" + src
	if !strings.HasSuffix(src, "\n") {
		sourceSec += "\n"
	}

	// Assemble: definition core first, then lists trimmed into the room that
	// remains, then the surviving optional sections.
	rb := &renderBudget{max: maxTokens}
	rb.add(notes.String())
	rb.add(head)
	rb.add(sourceSec)
	for _, ls := range lists {
		ls.trimToFit(rb.room())
		rb.add(ls.render())
	}
	for _, o := range optionals[:kept] {
		rb.add(o)
	}
	return strings.TrimSuffix(rb.b.String(), "\n")
}

// RenderExplain splits the budget: ~70% for the report body, the rest for
// the why section (doc deduped against the source, caller list trimmed), so
// an explicit explain is never swallowed by the body's lists.
func RenderExplain(r *intel.ExploreReport, info intel.WhyInfo, maxTokens int) string {
	if r == nil || maxTokens <= 0 {
		return intel.RenderExploreExplain(r, info)
	}
	bodyMax := maxTokens * 7 / 10
	if bodyMax < 128 {
		bodyMax = maxTokens
	}
	body := Render(r, bodyMax)
	room := maxTokens - tokenize.Count(body)
	if room < 16 {
		return body
	}
	if why := intel.FormatWhyBudgeted(info, r.Source, room); why != "" {
		return body + "\n\n== why ==\n" + why
	}
	return body
}

// renderBudget tracks token spend while assembling a budgeted render.
type renderBudget struct {
	max    int
	tokens int
	b      strings.Builder
}

func (rb *renderBudget) room() int { return rb.max - rb.tokens }

func (rb *renderBudget) add(s string) {
	if s == "" {
		return
	}
	rb.b.WriteString(s)
	rb.tokens += tokenize.Count(s)
}

// listSec is a list section trimmable to a token allowance with an explicit
// overflow marker ("+N more (raise max_tokens)").
type listSec struct {
	header, marker string
	rows           []string
	rowToks        []int
	headerTok      int
	fullTok        int
	kept           int
	dropped        bool
}

func newListSec(header string, names []string, conf, synth, locs map[string]string) *listSec {
	ls := &listSec{header: header, headerTok: tokenize.Count(header)}
	if len(names) == 0 {
		ls.rows = []string{"(none)\n"}
	} else {
		ls.rows = make([]string, len(names))
		for i, n := range names {
			ls.rows[i] = row(n, conf, synth, locs)
		}
	}
	ls.rowToks = make([]int, len(ls.rows))
	for i, r := range ls.rows {
		ls.rowToks[i] = tokenize.Count(r)
		ls.fullTok += ls.rowToks[i]
	}
	ls.fullTok += ls.headerTok
	ls.kept = len(ls.rows)
	return ls
}

// trimToFit keeps as many rows as fit within avail tokens (the header stays;
// the marker is reserved whenever rows are cut); the section drops when even
// its header cannot fit.
func (ls *listSec) trimToFit(avail int) {
	if avail <= 0 || ls.headerTok > avail {
		ls.dropped = true
		ls.kept = 0
		return
	}
	total, kept := ls.headerTok, 0
	marker := ""
	for i, rt := range ls.rowToks {
		rest := len(ls.rowToks) - i - 1
		m := ""
		if rest > 0 {
			m = fmt.Sprintf("+%d more (raise max_tokens)\n", rest)
		}
		if total+rt+tokenize.Count(m) > avail {
			break
		}
		total += rt
		kept++
		marker = m
	}
	ls.kept, ls.marker = kept, marker
}

func (ls *listSec) render() string {
	if ls.dropped {
		return ""
	}
	return ls.header + strings.Join(ls.rows[:ls.kept], "") + ls.marker
}

// simpleName strips the package qualifier from a name ("pkg.Func" → "Func"),
// mirroring intel's display convention.
func simpleName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// filterNoiseCallees drops builtin/builder-chain callee names the
// record-time filter would have excluded (render-layer belt-and-braces for
// indexes that predate it) — the same rule intel.RenderExplore applies.
func filterNoiseCallees(r *intel.ExploreReport, callees []string) []string {
	if len(callees) == 0 {
		return callees
	}
	goChain := r.Definition.Lang == "" || r.Definition.Lang == "go"
	filtered := make([]string, 0, len(callees))
	for _, c := range callees {
		if !calleefilter.IsNoiseCalleeFor(c, goChain, r.Definition.File) {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

// row renders one labelled list row — the same shape as intel's
// joinLinesConf rows ("Name [LABEL] — file:line (SYNTHESIZED: ...)").
func row(n string, conf, synth, locs map[string]string) string {
	var b strings.Builder
	b.WriteString(n)
	if label := conf[n]; label != "" {
		b.WriteString(" [" + label + "]")
	}
	if loc := locs[n]; loc != "" {
		b.WriteString(" — " + loc)
	}
	if s := synth[n]; s != "" {
		b.WriteString(" (SYNTHESIZED: " + s + ")")
	}
	b.WriteString("\n")
	return b.String()
}
