// Package whatif implements the What-If / Simulation capability: "What will
// happen if I change/remove this?" It applies a hypothetical change to an
// in-memory copy of the knowledge graph, recomputes the transitively affected
// set, and produces a deterministic scenario impact report (affected symbols,
// files, services, tests) plus a risk and a recommendation. It is read-only —
// it never mutates the real graph or the index.
package whatif

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/evidence"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
)

// ChangeKind is the kind of hypothetical change being simulated.
type ChangeKind string

const (
	// RemoveSymbol simulates deleting a symbol (and its outgoing edges).
	RemoveSymbol ChangeKind = "remove_symbol"
	// ChangeDependency simulates a symbol newly depending on another target
	// (e.g. an API now calling a different service).
	ChangeDependency ChangeKind = "change_dependency"
	// AddSymbol simulates adding a new symbol to the graph.
	AddSymbol ChangeKind = "add_symbol"
	// ChangeSignature simulates modifying a symbol's signature (params, return type).
	ChangeSignature ChangeKind = "change_signature"
	// AddDependency simulates adding a new call/dependency edge.
	AddDependency ChangeKind = "add_dependency"
	// RemoveDependency simulates removing an existing call/dependency edge.
	RemoveDependency ChangeKind = "remove_dependency"
	// SplitService simulates splitting a service into two.
	SplitService ChangeKind = "split_service"
	// MoveModule simulates moving a module to a different package.
	MoveModule ChangeKind = "move_module"
	// ChangeInfra simulates a change to infrastructure (e.g. DB → cache).
	ChangeInfra ChangeKind = "change_infra"
	// RenameSymbol simulates renaming a symbol across the codebase.
	RenameSymbol ChangeKind = "rename_symbol"
)

// Change is a hypothetical change to the knowledge graph.
type Change struct {
	Kind      ChangeKind
	Target    string // qualified symbol being changed / removed
	NewTarget string // for ChangeDependency / AddDependency: the target the dependency points at
}

// Alternative is a different way to achieve the same goal with a lower risk.
type Alternative struct {
	Description string // a different way to achieve the same goal with lower risk
	Risk        string // "low" | "medium" | "high"
}

// Impact is the deterministic result of simulating a change.
type Impact struct {
	Change         Change
	Affected       []string       // transitively affected symbol qualified names
	Files          []string       // distinct files containing affected symbols
	Services       []string       // affected service names (from WhatServicesAffected)
	Tests          []string       // tests that cover the affected (from WhatTestsCover)
	Isolated       bool           // true when nothing depends on the change (low risk)
	Risk           string         // "low" | "medium" | "high"
	Recommendation string         // deterministic, evidence-based
	Claims         []domain.Claim // typed claims produced by the simulation (e.g. RECOMMENDATION)
	Alternatives   []Alternative  // lower-risk ways to achieve the same goal
	Mitigations    []string       // concrete steps to reduce the risk of the change
	Confidence     float64        // 0..1 confidence in the impact estimate
	Databases      []string       // databases affected by the change
	// The following fields exist so the Impact shape stays stable even when
	// whatif cannot populate them without external data.
	ArchitectureViolations []string // architecture rules violated by the change
	HistoricalEvidence     []string // relevant past incidents/lessons
	RuntimeEvidence        []string // runtime telemetry evidence
	Method                 string   // analysis method used, e.g. "graph-traversal"
	Summary                string   // human-readable summary of the impact; notes pipeline limitations
	// Facts are the known, deterministic facts the simulation established about
	// the target change (kind, target, affected set size, risk, confidence).
	Facts []string `json:"facts,omitempty"`
	// Limitations are the pipeline/evidence gaps that bound the impact estimate
	// (missing runtime/historical evidence, or change kinds that need twin data).
	Limitations []string `json:"limitations,omitempty"`
}

// Simulate applies the change to the graph (in memory) and returns the impact.
// g is the canonical knowledge graph; it is never mutated. It runs
// deterministically (no LLM).
func Simulate(g *intelligence.Graph, c Change) Impact {
	if g == nil {
		return Impact{Change: c, Risk: "low", Isolated: true}
	}
	imp := Impact{Change: c}

	byID := map[string]domain.Node{}
	affected := map[string]bool{}
	files := map[string]bool{}

	// graphByID indexes the canonical graph so a target symbol (which may be
	// referenced for AddSymbol/ChangeSignature/RemoveDependency) can be located
	// independently of the affected set.
	graphByID := map[string]domain.Node{}
	for _, n := range g.Nodes {
		graphByID[n.ID] = n
	}

	collect := func(ns []domain.Node) {
		for _, n := range ns {
			if n.ID == "" {
				continue
			}
			affected[n.ID] = true
			if byID[n.ID].ID == "" {
				byID[n.ID] = n
			}
			if f := nodeFile(n); f != "" {
				files[f] = true
			}
		}
	}
	addNode := func(n domain.Node) { collect([]domain.Node{n}) }

	// Step 2 — traverse the graph to build the affected set, which depends on
	// the kind of change being simulated.
	createsCycle := false
	switch c.Kind {
	case AddSymbol:
		// A brand-new symbol has no dependents yet; it is isolated.
		// Files = the target's containing file when it resolves in the graph.
		if n, ok := graphByID[c.Target]; ok {
			addNode(n)
		}
	case ChangeSignature:
		// Callers of the symbol may break. affected = transitively what
		// depends on the target (same shape as RemoveSymbol), and Files also
		// includes the symbol's own file.
		collect(g.WhatDependsOn(c.Target))
		if n, ok := graphByID[c.Target]; ok {
			addNode(n)
		}
	case AddDependency:
		// Adding a dependency does not break existing dependents unless it
		// creates a cycle (Target now depends on NewTarget while NewTarget
		// already transitively depends on Target).
		if c.NewTarget != "" {
			for _, dn := range g.WhatDoesXDependOn(c.NewTarget) {
				if dn.ID == c.Target {
					createsCycle = true
					break
				}
			}
		}
	case RemoveDependency:
		// The symbol whose dependency was removed may itself break.
		if n, ok := graphByID[c.Target]; ok {
			addNode(n)
		}
	case RenameSymbol:
		// The symbol still exists after the rename, but every caller must be
		// updated to the new name. affected = callers + the symbol itself.
		collect(g.WhatDependsOn(c.Target))
		if n, ok := graphByID[c.Target]; ok {
			addNode(n)
		}
	case SplitService, MoveModule, ChangeInfra:
		// Higher-level operations that cannot be fully simulated from the call
		// graph alone (they need twin/context data). Surface the code-graph
		// impact of the named symbol/module and note the limitation in Summary.
		imp.Summary = fmt.Sprintf("high-level change type '%s' requires twin/context data for full simulation; showing code-graph impact only", c.Kind)
		collect(g.WhatDependsOn(c.Target))
		if n, ok := graphByID[c.Target]; ok {
			addNode(n)
		}
	default:
		// RemoveSymbol / ChangeDependency — everything that depends on the
		// changed symbol. For ChangeDependency, changing what a target depends
		// on does not affect the new target's other callers, so the affected
		// set must NOT include WhatDependsOn(c.NewTarget).
		collect(g.WhatDependsOn(c.Target))
	}

	for id := range affected {
		imp.Affected = append(imp.Affected, id)
	}
	sort.Strings(imp.Affected)

	for f := range files {
		imp.Files = append(imp.Files, f)
	}
	sort.Strings(imp.Files)

	for _, n := range g.WhatServicesAffected(c.Target) {
		if nm := nodeName(n); nm != "" {
			imp.Services = append(imp.Services, nm)
		}
	}
	sort.Strings(imp.Services)

	for _, n := range g.WhatTestsCover(c.Target) {
		if nm := nodeName(n); nm != "" {
			imp.Tests = append(imp.Tests, nm)
		}
	}
	sort.Strings(imp.Tests)

	// Databases reachable from the changed symbol.
	imp.Databases = databasesAffected(g, c.Target)
	sort.Strings(imp.Databases)

	// The architecture/historical/runtime evidence dimensions are populated by
	// callers with external data; whatif itself leaves them empty.
	imp.ArchitectureViolations = []string{}
	imp.HistoricalEvidence = []string{}
	imp.RuntimeEvidence = []string{}
	imp.Method = "graph-traversal"

	// Deterministic risk heuristic.
	imp.Isolated = len(imp.Affected) == 0
	switch {
	case createsCycle:
		imp.Risk = "high"
	case len(imp.Services) > 0 || len(imp.Affected) > 10:
		imp.Risk = "high"
	case len(imp.Affected) > 0:
		imp.Risk = "medium"
	default:
		imp.Risk = "low"
	}

	// Lower-risk alternatives.
	imp.Alternatives = alternatives(c)

	// Mitigations.
	imp.Mitigations = mitigations(c, imp)

	// Confidence in the estimate.
	imp.Confidence = confidence(c, imp)

	imp.Recommendation = recommend(c, imp)
	imp.Claims = []domain.Claim{recommendationClaim(c, imp)}
	imp.Facts = facts(c, imp)
	imp.Limitations = limitations(c, imp)
	return imp
}

// alternatives generates 1-2 deterministic lower-risk ways to achieve the same
// goal, based on the kind of change being made.
func alternatives(c Change) []Alternative {
	switch c.Kind {
	case RemoveSymbol:
		return []Alternative{
			{Description: "deprecate the symbol instead of removing it", Risk: "low"},
		}
	case ChangeSignature:
		return []Alternative{
			{Description: "add a new signature and keep the old one as deprecated", Risk: "low"},
		}
	case AddDependency:
		return []Alternative{
			{Description: "introduce an interface to decouple the dependency", Risk: "low"},
		}
	case RemoveDependency:
		return []Alternative{
			{Description: "keep the dependency behind a feature flag and remove it gradually", Risk: "medium"},
		}
	case ChangeDependency:
		return []Alternative{
			{Description: "add the new dependency path alongside the old one and migrate callers incrementally", Risk: "medium"},
		}
	case AddSymbol:
		return []Alternative{
			{Description: "add the symbol with a minimal, backward-compatible signature", Risk: "low"},
		}
	default:
		return nil
	}
}

// mitigations returns concrete, deterministic steps to reduce the risk of the
// change based on its assessed risk.
func mitigations(c Change, imp Impact) []string {
	switch imp.Risk {
	case "high":
		return []string{
			"break the change into smaller PRs",
			"add tests for affected symbols first",
		}
	case "medium":
		return []string{
			"update documentation for affected callers",
		}
	default:
		return []string{
			"no mitigation needed",
		}
	}
}

// confidence scores how much to trust the impact estimate: the smaller the
// affected set, the more confident the deterministic estimate.
func confidence(c Change, imp Impact) float64 {
	switch {
	case len(imp.Affected) == 0:
		return 0.95
	case len(imp.Affected) < 5:
		return 0.80
	case len(imp.Affected) < 20:
		return 0.60
	default:
		return 0.40
	}
}

// facts returns the known, deterministic facts the simulation established about
// the change: what kind of change it is, what it targets, how much of the graph
// it touches, and the assessed risk and confidence.
func facts(c Change, imp Impact) []string {
	var f []string
	f = append(f, "change kind: "+string(c.Kind))
	f = append(f, "target: "+c.Target)
	if c.NewTarget != "" {
		f = append(f, "new target: "+c.NewTarget)
	}
	f = append(f, "affected symbols: "+itoa(len(imp.Affected)))
	f = append(f, "affected files: "+itoa(len(imp.Files)))
	if len(imp.Services) > 0 {
		f = append(f, "affected services: "+strings.Join(imp.Services, ", "))
	}
	f = append(f, "risk: "+imp.Risk)
	f = append(f, "confidence: "+fmt.Sprintf("%0.2f", imp.Confidence))
	f = append(f, "isolated: "+boolStr(imp.Isolated))
	return f
}

// limitations enumerates the pipeline/evidence gaps that bound the impact
// estimate. The whatif simulation is graph-traversal only, so runtime and
// historical evidence are always absent; high-level change kinds additionally
// need twin/context data that is not available to the pure code-graph pass.
func limitations(c Change, imp Impact) []string {
	var l []string
	switch c.Kind {
	case SplitService, MoveModule, ChangeInfra:
		l = append(l, "high-level change kind requires twin/context data for full simulation; only code-graph impact shown")
	}
	if len(imp.RuntimeEvidence) == 0 {
		l = append(l, "no runtime telemetry evidence available to the simulation")
	}
	if len(imp.HistoricalEvidence) == 0 {
		l = append(l, "no historical incident/lesson evidence available to the simulation")
	}
	return l
}

// boolStr renders a bool as a lowercase "true"/"false" for deterministic facts.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func recommend(c Change, imp Impact) string {
	var b strings.Builder
	// Open with a kind-specific verb so the recommendation reads naturally
	// for every change type, not just removals. Previously every non-remove
	// kind rendered as "Changing X to depend on <empty>", garbling the text
	// when NewTarget was unset (add/change/rename/move/split/infra).
	switch c.Kind {
	case RemoveSymbol:
		b.WriteString("Removing " + c.Target)
	case AddSymbol:
		b.WriteString("Adding " + c.Target)
	case ChangeSignature:
		b.WriteString("Changing the signature of " + c.Target)
	case RenameSymbol:
		b.WriteString("Renaming " + c.Target)
	case MoveModule:
		b.WriteString("Moving " + c.Target)
	case SplitService:
		b.WriteString("Splitting " + c.Target)
	case ChangeInfra:
		b.WriteString("Changing the infrastructure of " + c.Target)
	case ChangeDependency, AddDependency, RemoveDependency:
		// These are the only kinds that have a meaningful NewTarget.
		if c.NewTarget != "" {
			b.WriteString("Changing " + c.Target + " to depend on " + c.NewTarget)
		} else {
			b.WriteString("Changing the dependencies of " + c.Target)
		}
	default:
		b.WriteString("Changing " + c.Target)
	}
	switch {
	case imp.Isolated:
		b.WriteString(" is isolated: nothing transitively depends on it. Safe to proceed with normal verification.")
	case len(imp.Services) > 0:
		b.WriteString(" affects service(s) " + strings.Join(imp.Services, ", ") + " — treat as high risk; run full verification and require human approval before deploy.")
	case len(imp.Affected) > 10:
		b.WriteString(" affects " + itoa(len(imp.Affected)) + " symbols — high blast radius; require human approval.")
	default:
		// The "run the affected tests (...)" tail only reads well when
		// there are actually affected tests; otherwise drop the empty
		// parenthetical.
		tests := strings.Join(imp.Tests, ", ")
		b.WriteString(" affects " + itoa(len(imp.Affected)) + " symbols across " + itoa(len(imp.Files)) + " file(s)")
		if tests != "" {
			b.WriteString("; run the affected tests (" + tests + ")")
		}
		b.WriteString(".")
	}
	return b.String()
}

func recommendationClaim(c Change, imp Impact) domain.Claim {
	conf := evidence.ConfidenceCertain
	if imp.Risk == "high" {
		conf = evidence.ConfidenceHigh
	} else if imp.Risk == "medium" {
		conf = evidence.ConfidenceModerate
	}
	content := "kind: " + string(c.Kind) + "\n" + "affected:\n" + strings.Join(imp.Affected, "\n")
	return evidence.FromRecommendation(imp.Recommendation, c.Target, "whatif:simulate", conf, domain.Evidence{
		Type:      domain.EvidenceGraph,
		Source:    "whatif",
		Content:   content,
		Digest:    evidence.Digest(content),
		Timestamp: time.Now(),
	})
}

// nodeFile returns the file path for a node (symbol or file node).
func nodeFile(n domain.Node) string {
	if n.Symbol != nil && n.Symbol.File != "" {
		return n.Symbol.File
	}
	if n.File != nil && n.File.Path != "" {
		return n.File.Path
	}
	return ""
}

// nodeName returns a display name for a node: the underlying symbol name, or
// the node label.
func nodeName(n domain.Node) string {
	if n.Symbol != nil && n.Symbol.Name != "" {
		return n.Symbol.Name
	}
	return n.Label
}

// databasesAffected returns the database/table nodes reachable from the changed
// symbol over call edges. The code-only graph emits only symbol/file/module
// nodes, so this is empty for code-only graphs — callers that attach twin data
// nodes (Kind "db" or "table") to the graph will surface them here.
func databasesAffected(g *intelligence.Graph, target string) []string {
	// Forward-reachability from the changed symbol over call edges.
	reachable := map[string]bool{}
	frontier := []string{target}
	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]
		if reachable[cur] {
			continue
		}
		reachable[cur] = true
		for _, e := range g.Edges {
			if e.From == cur && e.Kind == "calls" && !reachable[e.To] {
				frontier = append(frontier, e.To)
			}
		}
	}
	var out []string
	for _, n := range g.Nodes {
		if n.Kind != "db" && n.Kind != "table" {
			continue
		}
		if reachable[n.ID] {
			if nm := nodeName(n); nm != "" {
				out = append(out, nm)
			}
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// WhatIf is an alias for Simulate. It remains for backward compatibility.
func WhatIf(g *intelligence.Graph, c Change) Impact {
	return Simulate(g, c)
}

// itoa is a minimal integer formatter (stdlib-only, avoids strconv).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
