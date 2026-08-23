// Package modernization implements the legacy modernization use case
// (spec §41 lines 3121-3169): "Analyze this monolith and propose a safe
// extraction plan." It is fully deterministic — reuses intel.Communities,
// intel.Bridges, intel.Churn for analysis. No LLM (principle 2.3).
package modernization

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// BoundedContext is a candidate extracted service/module detected by
// community detection. It groups symbols that are tightly coupled
// internally and loosely coupled to other groups.
type BoundedContext struct {
	Name         string   // derived from the dominant package/directory
	Symbols      []string // symbol IDs in this context
	FileCount    int      // number of files
	Cohesion     float64  // 0.0-1.0, internal coupling density
	OutgoingDeps int      // dependencies on other contexts (lower = better extraction candidate)
	IncomingDeps int      // dependencies from other contexts (lower = safer to extract)
	// Ownership is the owning team/owner of the context (Phase 12): derived
	// deterministically from the dominant package path prefix (e.g.
	// "internal/checkout" -> "@checkout"). Empty when undeterminable.
	Ownership string `json:"ownership,omitempty"`
	// Dependencies lists the distinct external symbol names this context calls
	// into (Phase 12 deps), a diagnostic complement to the OutgoingDeps count.
	Dependencies []string `json:"dependencies,omitempty"`
}

// Bridge is a coupling point between two bounded contexts. Extracting
// one context without addressing bridges risks breaking the other.
type Bridge struct {
	From      string   // source context name
	To        string   // target context name
	Symbols   []string // the symbols that form the bridge
	RiskLevel string   // "low", "medium", "high"
}

// ExtractionPhase is one step of a phased extraction plan. Each phase
// extracts one or more contexts with validation and rollback guidance.
type ExtractionPhase struct {
	Phase       int      // 1-based phase number
	Context     string   // the bounded context being extracted
	RiskLevel   string   // "low", "medium", "high"
	BlastRadius int      // number of symbols affected
	Bridges     []Bridge // bridges that must be addressed first
	Migration   string   // migration strategy text
	Rollback    string   // rollback strategy text
	Validation  string   // validation steps text
	// Ownership is the owning team of the extracted context (Phase 12.2).
	Ownership string `json:"ownership,omitempty"`
	// TaskID is the task that tracks this phase's extraction (Phase 12.3
	// phase-tasks). Set when the phase is materialized as a task.
	TaskID string `json:"task_id,omitempty"`
}

// ExtractionPlan is the full phased plan for modernizing a monolith.
//
// A plan with empty Contexts/Bridges/Phases but a non-empty Summary indicates
// the analysis was gated (repo exceeds index.MaxCommunitySymbols); the Summary
// carries a skip-note explaining why and how to proceed.
type ExtractionPlan struct {
	Contexts []BoundedContext  // all detected bounded contexts
	Bridges  []Bridge          // all inter-context bridges
	Phases   []ExtractionPhase // ordered extraction phases (lowest risk first)
	Summary  string            // human-readable summary
}

// Analyzer detects bounded contexts and generates extraction plans.
// It wraps the intel layer's community/bridge/churn analysis.
//
// The reusable intel primitives — intel.Communities, intel.Bridges,
// intel.Churn — all operate on an *index.Index (the v1 AST index), so the
// analyzer is constructed from an *index.Index. This keeps the analysis on
// the deterministic v1 path and avoids reimplementing community or bridge
// detection (principle 2.3: no LLM).
type Analyzer struct {
	ix *index.Index
}

// NewAnalyzer creates an Analyzer from a project's AST index — the input
// consumed by intel.Communities and intel.Bridges.
func NewAnalyzer(ix *index.Index) *Analyzer {
	return &Analyzer{ix: ix}
}

// Analyze detects bounded contexts and coupling bridges, then generates a
// phased extraction plan ordered by risk (lowest risk extracted first). It is
// fully deterministic: community and bridge detection are delegated to the
// intel layer, and every downstream score is computed from the call graph with
// stable ordering.
func (a *Analyzer) Analyze() (*ExtractionPlan, error) {
	if a == nil || a.ix == nil {
		return nil, fmt.Errorf("modernization: analyzer requires an index")
	}

	// Community detection is gated for large repos: intel.Communities returns
	// empty above index.MaxCommunitySymbols to avoid O(n*iter) latency. Detect
	// that here and return a valid plan with a skip-note instead of a silent
	// empty plan.
	if len(a.ix.Symbols) > index.MaxCommunitySymbols {
		return &ExtractionPlan{
			Summary: fmt.Sprintf("modernization analysis skipped — %d symbols exceed the %d-symbol community-detection gate; use `kern bridges` and `kern churn` for structural analysis on gated repos",
				len(a.ix.Symbols), index.MaxCommunitySymbols),
		}, nil
	}

	// 1. Communities -> candidate bounded contexts (intel reuses label
	//    propagation; no reimplementation here).
	communities := intel.Communities(a.ix)

	// symbol full-name -> owning community ID, and community ID -> display name.
	symToCtx := map[string]string{}
	idToName := map[string]string{}
	for _, c := range communities {
		name := contextName(c)
		idToName[c.ID] = name
		for _, s := range c.Symbols {
			symToCtx[s] = c.ID
		}
	}

	// 2. Compute cohesion and cross-context deps per community.
	contexts := make([]BoundedContext, len(communities))
	for i, c := range communities {
		contexts[i] = buildContext(a.ix, c)
	}

	// 3. Bridges between contexts (reuses intel.Bridges, which finds symbols
	//    called from more than one package).
	// intel.Bridges treats limit<=0 as "use default 15" and truncates. The
	// analyzer wants every coupling bridge so it can derive an accurate risk
	// level; a large limit avoids silently undercounting bridges (bug: Bridges
	// has no dedicated "unlimited" sentinel).
	bridges := a.mapBridges(intel.Bridges(a.ix, 10000), symToCtx, idToName)

	// Per-context bridge count -> phase risk level.
	bridgeCount := map[string]int{}
	for _, b := range bridges {
		bridgeCount[b.From]++
	}

	// 4. Churn scores, best-effort (0 when not a git repo / not indexed).
	churn := churnScores(a.ix)

	// 5. Sort contexts by extraction risk ascending (lowest first). The risk
	// level of a phase is derived from its bridge count, so sort on that
	// first; safety score breaks ties within the same risk band.
	order := make([]int, len(contexts))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(x, y int) bool {
		bx := bridgeCount[contexts[order[x]].Name]
		by := bridgeCount[contexts[order[y]].Name]
		if bx != by {
			return bx < by
		}
		return safetyScore(contexts[order[x]], churn) < safetyScore(contexts[order[y]], churn)
	})

	// 6. Generate one phase per context, ordered by safety.
	phases := make([]ExtractionPhase, 0, len(order))
	for i, idx := range order {
		phases = append(phases, buildPhase(i+1, contexts[idx], bridgeCount[contexts[idx].Name], bridges, contexts))
	}

	return &ExtractionPlan{
		Contexts: contexts,
		Bridges:  bridges,
		Phases:   phases,
		Summary:  buildSummary(contexts, bridges, order),
	}, nil
}

// safetyScore ranks how safe a context is to extract first: lower is safer.
// Outgoing deps weigh double (extracting a provider risks breaking dependents);
// incoming deps and churn add proportional risk.
func safetyScore(ctx BoundedContext, churn map[string]float64) float64 {
	return float64(ctx.OutgoingDeps)*2 + float64(ctx.IncomingDeps) + churn[ctx.Name]
}

// buildContext computes the metrics of one bounded context from its community.
func buildContext(ix *index.Index, c intel.Community) BoundedContext {
	set := map[string]bool{}
	for _, s := range c.Symbols {
		set[s] = true
	}
	internal, cross := internalDges(ix, set)
	cohesion := 0.0
	if internal+cross > 0 {
		cohesion = float64(internal) / float64(internal+cross)
	}
	outgoing, incoming := crossDeps(ix, set)
	return BoundedContext{
		Name:         contextName(c),
		Symbols:      append([]string(nil), c.Symbols...),
		FileCount:    contextFileCount(ix, c.Symbols),
		Cohesion:     cohesion,
		OutgoingDeps: outgoing,
		IncomingDeps: incoming,
		// Phase 12.2: derive ownership from the dominant package path and list
		// the external dependency symbols the context calls into.
		Ownership:    contextOwnership(ix, c.Symbols),
		Dependencies: contextOutDeps(ix, set),
	}
}

// contextOwnership derives an ownership tag (e.g. "@backend") from the dominant
// package path of the context's symbols. It is deterministic and best-effort:
// it takes the first path segment after internal/ (or the first directory of
// the first symbol's file) and prefixes it with "@". Empty when it cannot
// determine a package path.
func contextOwnership(ix *index.Index, syms []string) string {
	for _, s := range syms {
		file := fileOfSymbol(ix)[s]
		if file == "" {
			continue
		}
		file = filepath.ToSlash(file)
		trimmed := strings.TrimPrefix(file, "internal/")
		if trimmed == file {
			trimmed = file
		}
		if i := strings.IndexByte(trimmed, '/'); i > 0 {
			return "@" + trimmed[:i]
		}
	}
	return ""
}

// contextOutDeps returns the distinct external symbol full-names the context's
// symbols call into (one endpoint inside, one outside). Deterministic, sorted.
func contextOutDeps(ix *index.Index, set map[string]bool) []string {
	outSeen := map[string]bool{}
	for from, tos := range ix.Calls {
		fromIn := set[from]
		for _, to := range tos {
			if fromIn && !set[to] {
				outSeen[to] = true
			}
		}
	}
	out := make([]string, 0, len(outSeen))
	for s := range outSeen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// contextName derives a stable name from the dominant package directory.
func contextName(c intel.Community) string {
	if len(c.Packages) > 0 {
		base := c.Packages[0]
		if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
			base = base[i+1:]
		}
		if base != "" {
			return base
		}
	}
	return c.ID
}

// internalDges counts call edges internal to the set vs edges crossing its
// boundary (one endpoint inside, one outside).
func internalDges(ix *index.Index, set map[string]bool) (internal, cross int) {
	for from, tos := range ix.Calls {
		fromIn := set[from]
		for _, to := range tos {
			toIn := set[to]
			if fromIn && toIn {
				internal++
			} else if fromIn != toIn {
				cross++
			}
		}
	}
	return internal, cross
}

// crossDeps counts distinct outgoing (this -> other) and incoming (other ->
// this) dependency symbols across the context boundary.
func crossDeps(ix *index.Index, set map[string]bool) (outgoing, incoming int) {
	outSeen := map[string]bool{}
	inSeen := map[string]bool{}
	for from, tos := range ix.Calls {
		fromIn := set[from]
		for _, to := range tos {
			toIn := set[to]
			if fromIn && !toIn && !outSeen[to] {
				outSeen[to] = true
				outgoing++
			}
			if !fromIn && toIn && !inSeen[from] {
				inSeen[from] = true
				incoming++
			}
		}
	}
	return outgoing, incoming
}

// contextFileCount returns the number of distinct source files backing the
// context's symbols.
func contextFileCount(ix *index.Index, syms []string) int {
	fileOf := fileOfSymbol(ix)
	seen := map[string]bool{}
	for _, s := range syms {
		if f := fileOf[s]; f != "" {
			seen[f] = true
		}
	}
	return len(seen)
}

// fileOfSymbol maps symbol full names to their source file.
func fileOfSymbol(ix *index.Index) map[string]string {
	m := map[string]string{}
	for _, s := range ix.Symbols {
		m[s.FullName()] = s.File
	}
	return m
}

// churnScores returns per-context churn scores, best-effort. The intel Churn
// report requires a git repository, so a non-repo index yields all-zero scores
// (still deterministic). High churn is folded into the safety score.
func churnScores(ix *index.Index) map[string]float64 {
	out := map[string]float64{}
	rep, err := intel.Churn(ix.Root, "", "")
	if err != nil {
		return out
	}
	fileChurn := map[string]float64{}
	for _, e := range rep.Entries {
		fileChurn[e.File] = float64(e.Commits)
	}
	fileOf := fileOfSymbol(ix)
	ctxChurn := map[string]float64{}
	for _, c := range intel.Communities(ix) {
		seen := map[string]bool{}
		for _, s := range c.Symbols {
			if f := fileOf[s]; f != "" && !seen[f] {
				seen[f] = true
				ctxChurn[c.ID] += fileChurn[f]
			}
		}
	}
	return ctxChurn
}

// mapBridges converts intel.Bridge records into context-level Bridge records,
// mapping each bridge symbol to its owning context (From) and the distinct
// contexts that call into it (To). Risk levels: high for 3+ contexts, medium
// for 2, low for 1.
func (a *Analyzer) mapBridges(raw []intel.Bridge, symToCtx map[string]string, idToName map[string]string) []Bridge {
	var out []Bridge
	for _, rb := range raw {
		fromCtxID := symToCtx[rb.Symbol]
		fromName := idToName[fromCtxID]
		if fromName == "" {
			continue
		}
		// Distinct contexts that call the bridge symbol, excluding its own.
		toSeen := map[string]bool{}
		var to []string
		for _, caller := range a.ix.Callers[rb.Symbol] {
			cid, ok := symToCtx[caller]
			if !ok || cid == fromCtxID {
				continue
			}
			if name := idToName[cid]; name != "" && !toSeen[name] {
				toSeen[name] = true
				to = append(to, name)
			}
		}
		if len(to) == 0 {
			continue
		}
		sort.Strings(to)
		out = append(out, Bridge{
			From:      fromName,
			To:        strings.Join(to, ", "),
			Symbols:   []string{rb.Symbol},
			RiskLevel: bridgeRisk(len(to)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].From < out[j].From })
	return out
}

// bridgeRisk levels a bridge by how many distinct contexts it couples.
func bridgeRisk(ctxCount int) string {
	switch {
	case ctxCount >= 3:
		return "high"
	case ctxCount == 2:
		return "medium"
	default:
		return "low"
	}
}

// buildPhase constructs a single extraction phase for one context.
func buildPhase(phaseNum int, ctx BoundedContext, bridgeCount int, all []Bridge, contexts []BoundedContext) ExtractionPhase {
	phase := ExtractionPhase{
		Phase:       phaseNum,
		Context:     ctx.Name,
		RiskLevel:   phaseRisk(bridgeCount),
		BlastRadius: blastRadius(ctx, contexts),
		// Phase 12.2: carry the context ownership onto its extraction phase.
		Ownership: ctx.Ownership,
		Migration: fmt.Sprintf("Extract %s as a separate module. Update imports in %d dependent files.",
			ctx.Name, ctx.OutgoingDeps),
		Rollback:   "Revert the module extraction commit. No data migration needed (code-only).",
		Validation: fmt.Sprintf("Run full test suite. Verify %s API contracts via integration tests.", ctx.Name),
	}
	for _, b := range all {
		if b.From == ctx.Name || strings.Contains(b.To, ctx.Name) {
			phase.Bridges = append(phase.Bridges, b)
		}
	}
	return phase
}

// blastRadius counts symbols in contexts coupled to the given one — the
// blast radius if extraction breaks a dependency.
func blastRadius(ctx BoundedContext, contexts []BoundedContext) int {
	total := 0
	for _, other := range contexts {
		if other.Name == ctx.Name {
			continue
		}
		// Conservative: every other context's symbols are part of the blast
		// radius because we cannot cheaply prove they are uncoupled here.
		total += len(other.Symbols)
	}
	return total
}

// phaseRisk levels a phase by how many bridges it must first address.
func phaseRisk(bridgeCount int) string {
	switch {
	case bridgeCount >= 3:
		return "high"
	case bridgeCount >= 1:
		return "medium"
	default:
		return "low"
	}
}

// buildSummary produces a human-readable plan summary.
func buildSummary(contexts []BoundedContext, bridges []Bridge, order []int) string {
	if len(order) == 0 {
		return "Detected 0 bounded contexts. The monolith is already atomic."
	}
	first := contexts[order[0]].Name
	last := contexts[order[len(order)-1]].Name
	symbols, files := totalExtent(contexts)
	return fmt.Sprintf(
		"Detected %d bounded contexts with %d coupling bridges. Recommended %d-phase extraction: "+
			"phase 1 (lowest risk) extracts %s, phase %d (highest risk) extracts %s. "+
			"Total blast radius: %d symbols across %d files.",
		len(contexts), len(bridges), len(order), first, len(order), last, symbols, files)
}

// totalExtent sums symbols and distinct files across all contexts.
func totalExtent(contexts []BoundedContext) (symbols, files int) {
	fileSet := map[string]bool{}
	for _, ctx := range contexts {
		symbols += len(ctx.Symbols)
		for _, sym := range ctx.Symbols {
			fileSet[sym] = true
		}
	}
	return symbols, len(fileSet)
}
