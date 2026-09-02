package context

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// architectureRules determines which governance / architecture rules apply to
// a change to the given roots.
// It is an additive, honest integration:
// 1. Every enabled governance policy from the firewall whose Scope matches the
// change scope (derived deterministically from the root files) is surfaced.
// 2. When a boundary provider is attached, each provided boundary rule that the
// change actually crosses (a forbidden dependency edge within the change's
// blast region) is surfaced as an ArchitectureRules entry.
// The result is deterministic: rules are emitted in a stable order with
// duplicates removed.
func (e *Engine) architectureRules(scope string, roots []domain.Symbol) []domain.Policy {
	seen := map[string]bool{}
	var out []domain.Policy
	add := func(p domain.Policy) {
		if !p.Enabled || p.Name == "" || seen[p.Name] {
			return
		}
		seen[p.Name] = true
		out = append(out, p)
	}

	// 1. Governance firewall policies relevant to the change scope.
	if e.firewall != nil {
		cs := changeScope(roots)
		for _, p := range e.firewall.Policies() {
			if p.Scope == "all" || p.Scope == cs {
				add(p)
			}
		}
	}

	// 2. Boundary rules actually crossed by the change.
	if e.boundaryProvider != nil {
		for _, p := range e.boundaryProvider() {
			if from, to := parseBoundary(p.Name); from == "" || to == "" {
				continue // not a boundary rule
			}
			if e.crossesBoundary(p, roots) {
				add(p)
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// changeScope derives the governance resource scope from the root symbols'
// files. It is deterministic: the lexically smallest root file wins, then the
// first matching resource heuristic.
func changeScope(roots []domain.Symbol) string {
	// Pick the deterministically smallest file among the roots.
	file := ""
	for _, r := range roots {
		if r.File != "" && (file == "" || r.File < file) {
			file = r.File
		}
	}
	switch {
	case file == "":
		return "source"
	case strings.Contains(file, "_test") || strings.Contains(file, "/test/"):
		return "tests"
	case strings.Contains(file, "_security") || strings.Contains(file, "security") ||
		strings.Contains(file, "auth") || strings.Contains(file, "secret"):
		return "security"
	case strings.Contains(file, "config") || strings.Contains(file, ".env"):
		return "config"
	default:
		return "source"
	}
}

// crossesBoundary reports whether a change to the given roots crosses the
// forbidden dependency boundary encoded in a boundary rule policy.
// A boundary policy is expected to have the shape produced by
// domain.FromBoundaryRule: Name = "boundary:<from>-><to>". It reports true when
// some dependency edge within the change's blast region goes from a directory
// matching <from> to a directory matching <to> (a forbid crossing). It only
// considers the roots' own callee edges plus their transitive region, so a
// change that doesn't touch the boundary never surfaces it.
func (e *Engine) crossesBoundary(p domain.Policy, roots []domain.Symbol) bool {
	from, to := parseBoundary(p.Name)
	if from == "" || to == "" {
		return false
	}

	// Build the raw call adjacency from the graph edges. Cross-package call
	// edges reference the callee by a qualified name (e.g. "db.Do") that does
	// not resolve to a graph node ID ("Do"), so the traversal below must run
	// over the raw edge endpoints rather than the graph's node query helpers
	// (which drop unresolvable callees).
	outgoing := map[string][]string{}
	incoming := map[string][]string{}
	for _, edge := range e.graph.Edges {
		if edge.Kind != "calls" {
			continue
		}
		outgoing[edge.From] = append(outgoing[edge.From], edge.To)
		incoming[edge.To] = append(incoming[edge.To], edge.From)
	}

	// Reachable = the roots plus every endpoint transitively reachable via
	// call edges (both directions), cycle-safe via a visited set.
	reachable := map[string]bool{}
	queue := []string{}
	for _, r := range roots {
		if r.Qualified == "" || reachable[r.Qualified] {
			continue
		}
		reachable[r.Qualified] = true
		queue = append(queue, r.Qualified)
	}
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		add := func(nb string) {
			if reachable[nb] {
				return
			}
			reachable[nb] = true
			queue = append(queue, nb)
		}
		for _, nb := range outgoing[cur] {
			add(nb)
		}
		for _, nb := range incoming[cur] {
			add(nb)
		}
	}

	byID := e.nodesByID()
	for _, edge := range e.graph.Edges {
		if edge.Kind != "calls" {
			continue
		}
		if !reachable[edge.From] || !reachable[edge.To] {
			continue
		}
		fromID, ok := resolveEdgeID(edge.From, byID)
		if !ok {
			continue
		}
		toID, ok := resolveEdgeID(edge.To, byID)
		if !ok {
			continue
		}
		fromNode, _ := byID[fromID]
		toNode, _ := byID[toID]
		if fromNode.Symbol == nil || toNode.Symbol == nil {
			continue
		}
		fromDir := filepath.Dir(fromNode.Symbol.File)
		toDir := filepath.Dir(toNode.Symbol.File)
		if fromDir == "" || toDir == "" || fromDir == toDir {
			continue
		}
		if intel.DirMatch(from, fromDir) && intel.DirMatch(to, toDir) {
			return true
		}
	}
	return false
}

// resolveEdgeID maps an edge endpoint to a graph node ID. Endpoints are usually
// node IDs already, but cross-package call edges reference the callee by a
// qualified name (e.g. "db.Do") while the graph node is the bare symbol name
// ("Do"). Resolution is deterministic: exact match first, then the bare
// identifier segment, then the symbol's Name.
func resolveEdgeID(endpoint string, byID map[string]domain.Node) (string, bool) {
	if _, ok := byID[endpoint]; ok {
		return endpoint, true
	}
	seg := endpoint
	if i := strings.LastIndexAny(seg, "./"); i >= 0 {
		seg = seg[i+1:]
	}
	if _, ok := byID[seg]; ok {
		return seg, true
	}
	for id, n := range byID {
		if n.Symbol != nil && n.Symbol.Name == seg {
			return id, true
		}
	}
	return "", false
}

// parseBoundary splits a boundary rule Name of the form
// "<from>-><to>" (after the optional "boundary:" prefix) into its two
// directory patterns. It returns ("","") when the name is malformed.
func parseBoundary(name string) (string, string) {
	name = strings.TrimPrefix(name, "boundary:")
	if i := strings.Index(name, "->"); i > 0 {
		return name[:i], name[i+2:]
	}
	return "", ""
}

// runtimeEvidence gathers recent error/notification events relevant to the
// changed symbol's scope from the optional runtime source. It returns an empty
// (non-nil) slice when no source is wired or nothing matches.
// Relevance is deterministic: an event matches if its Service equals the
// directory of a root file, or its "file" attribute equals a root file. When no
// explicit match is found, error events within the source are still surfaced
// (bounded) so the packet is never misleadingly empty. Results are sorted by
// timestamp then by ID.
func (e *Engine) runtimeEvidence(roots []domain.Symbol) []domain.Evidence {
	if e.runtimeSrc == nil {
		return []domain.Evidence{}
	}
	rootFiles := map[string]bool{}
	rootDirs := map[string]bool{}
	for _, r := range roots {
		if r.File == "" {
			continue
		}
		rootFiles[r.File] = true
		rootDirs[filepath.Dir(r.File)] = true
	}
	var matched []runtime.Event
	var errorsAll []runtime.Event
	for _, ev := range e.runtimeSrc.Events("") {
		if !ev.IsError() {
			continue
		}
		errorsAll = append(errorsAll, ev)
		if ev.Attributes["file"] != "" && rootFiles[ev.Attributes["file"]] {
			matched = append(matched, ev)
		} else if ev.Service != "" && rootDirs[ev.Service] {
			matched = append(matched, ev)
		}
	}
	// Deterministic fallback: if nothing matched, surface all error events.
	if len(matched) == 0 {
		matched = errorsAll
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if !matched[i].Timestamp.Equal(matched[j].Timestamp) {
			return matched[i].Timestamp.Before(matched[j].Timestamp)
		}
		return matched[i].ID < matched[j].ID
	})
	const maxEvidence = 20
	if len(matched) > maxEvidence {
		matched = matched[:maxEvidence]
	}
	out := make([]domain.Evidence, 0, len(matched))
	for _, ev := range matched {
		out = append(out, domain.Evidence{
			Type:      domain.EvidenceRuntime,
			Source:    e.runtimeSrc.Name(),
			Content:   runtime.FormatEvent(ev),
			Timestamp: ev.Timestamp,
		})
	}
	return out
}
