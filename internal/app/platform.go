// Package app is the Task-centered application-services layer that sits above
// the existing engines and below every interface (MCP, CLI, REST, SDK, Web).
// It exists to enforce the Kern 2.0
// Architecture Invariant 1: "Interfaces do not contain core business
// orchestration." Before this package, the analyze / plan / what-if / impact /
// verify pipelines were duplicated three ways — once in internal/mcp, once in
// cmd/kern, and once in internal/web — each rebuilding the index, graph,
// memory store, firewall, and context/verification engines independently.
// Platform is the single shared facade. Every interface calls the same methods
// here, and Platform calls the same engines. The orchestration that was
// copy-pasted across handlers now lives in exactly one place.
// Construction:
// - New(root)           — builds the index and derived state once; for
// one-shot callers (CLI, MCP).
// - NewWithIndex(root, ix) — reuses a caller-owned prebuilt index; for
// long-lived servers (web) that already index at
// startup.
// All methods are safe for concurrent use: the index and graph are treated as
// read-only after construction, and the engines hold only read-only references.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/storage"
	"github.com/JayveerPrajapati/kern/internal/twin"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// Platform is the shared application-services facade. It owns the prebuilt
// index, the twin-merged knowledge graph, the memory store, the governance
// firewall, and the context / verification engines constructed from them.
// One Platform per project root. Long-lived servers (web) build it once at
// startup and reuse it across requests; one-shot callers (CLI, MCP) build it
// per invocation. Either way, the orchestration is identical.
type Platform struct {
	root  string
	ix    *index.Index
	graph *intelligence.Graph
	mem   *memory.MemoryStore
	fw    *governance.Firewall
	ctx   *context.Engine
	ver   *verification.Engine
	rtSrc runtime.Source // optional runtime source for correlation/incident
	bus   *eventbus.Bus  // optional event publisher; nil = no-op
}

// WithBus attaches an optional event bus. When non-nil, Platform publishes
// repository.indexed at construction and the verification engine publishes
// security.finding / architecture.violation events. A nil bus is a no-op.
// Must be called before NewWithGraph for the repository.indexed event to fire.
func (p *Platform) WithBus(b *eventbus.Bus) *Platform {
	p.bus = b
	if p.ver != nil {
		p.ver.WithBus(b)
	}
	return p
}

// Bus returns the Platform's event bus (may be nil).
func (p *Platform) Bus() *eventbus.Bus { return p.bus }

// New builds the index for root and returns a Platform with all engines
// prebuilt and shared. It is the one-shot constructor for CLI and MCP callers.
// The index, graph, memory, firewall, and engines are built exactly once here;
// callers must not rebuild them.
func New(root string) (*Platform, error) {
	ix, err := index.Build(root)
	if err != nil {
		return nil, fmt.Errorf("app: index: %w", err)
	}
	return NewWithIndex(root, ix)
}

// NewWithIndex is the server constructor: it reuses a caller-owned prebuilt
// index (e.g. one built at web.New startup) instead of rebuilding it. This is
// the hot-path constructor for long-lived servers that already indexed at
// startup — it avoids the per-request re-index bottleneck.
func NewWithIndex(root string, ix *index.Index) (*Platform, error) {
	g := intelligence.FromIndex(ix)
	return NewWithGraph(root, ix, &g)
}

// NewWithGraph is the server constructor for callers that own their own graph
// pointer (e.g. web.App, which swaps the graph value in place when the index
// goes stale). The caller's *intelligence.Graph must outlive the Platform;
// Platform stores the pointer (not a copy) so the context engine — which also
// holds this pointer — sees any in-place swap the caller performs.
func NewWithGraph(root string, ix *index.Index, g *intelligence.Graph) (*Platform, error) {
	// The runtime source is loaded once and wired into BOTH the Digital Twin's
	// runtime extractor (so the merged knowledge graph carries production
	// runtime nodes) and the context engine (runtime evidence). Reusing one
	// source avoids creating a duplicate live poller when env adapters are set.
	rtSrc := loadRuntimeSource(root)

	// Merge the Digital Twin's non-code dimensions (API, data, messaging,
	// infra, runtime) into the code graph so engines reason over the full
	// knowledge graph, not just source. Best-effort: extraction errors are
	// non-fatal (the graph stays code-only).
	_ = twin.Merge(g, twin.NewExtractors(root, rtSrc))

	mem := memory.NewMemoryStore(root)
	fw := governance.NewFirewallWithApprovalStore(root).WithAgents(governance.NewAgent(
		"context-engine", "Context Engine", "analyzer",
		[]governance.Permission{
			{Resource: "source", Action: "read"},
			{Resource: "source", Action: "write"},
			{Resource: "security", Action: "write"},
			{Resource: "tests", Action: "write"},
			{Resource: "config", Action: "write"},
			{Resource: "documentation", Action: "write"},
		},
	), governance.NewAgent(
		// "kern" is the default control-plane operator identity (TaskService's
		// default agent, used by `kern run`, kern_run MCP, and `kern do`). It
		// must be registered so the policy precheck passes its
		// identity gate for the standard intent→resource/action pairs; an
		// unregistered agent fails closed. DEPLOY stays gated by the separate
		// KERN_ALLOW_DEPLOY + deploy firewall rules — it is NOT granted here.
		"kern", "Kern Control Plane", "operator",
		[]governance.Permission{
			{Resource: "repository", Action: "read"},
			{Resource: "repository", Action: "write"},
			{Resource: "repository", Action: "scan"},
			{Resource: "repository", Action: "audit"},
			{Resource: "source", Action: "read"},
			{Resource: "source", Action: "write"},
			{Resource: "security", Action: "scan"},
			{Resource: "security", Action: "write"},
			{Resource: "tests", Action: "write"},
			{Resource: "config", Action: "write"},
			{Resource: "documentation", Action: "write"},
			{Resource: "context", Action: "read"},
			{Resource: "context", Action: "write"},
		},
	))

	// Wire the audit log to a file-based store so `kern audit` can read entries
	// across processes (and so a fresh server sees entries written by a prior
	// one). The CLI only ever reads these files; the running firewall writes them.
	auditDir := filepath.Join(root, ".kern", "audit")
	_ = os.MkdirAll(auditDir, 0o755)
	fw.AuditLog().WithStore(storage.NewLog(auditDir)).WithLockPath(filepath.Join(auditDir, ".lock"))
	// Replay persisted entries into memory, then verify the tamper-evident
	// chain. A broken chain is evidence to investigate — not a reason to refuse
	// to start: warn loudly on stderr and continue. When NOTHING verifies —
	// not even the first entry against an empty chain head — the entries were
	// written by an older kern version with a different hash format: a
	// migration. That is also the exact signature of a deliberate full-chain
	// rewrite, so this case warns loudly like any other break.
	if n, err := fw.AuditLog().Replay(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: audit log replay failed: %v\n", err)
	} else if n > 0 {
		if brk, verified := fw.AuditLog().VerifyChainReport(); brk >= 0 {
			if verified == 0 {
				fmt.Fprintf(os.Stderr, "WARNING: audit log chain verification could not verify ANY entries (0 of %d) — this indicates either a legacy version migration or a full-chain rewrite; if this is not a known upgrade, investigate before trusting governance records (kern audit)\n", n)
			} else {
				fmt.Fprintf(os.Stderr, "WARNING: audit log tamper chain verification FAILED at entry %d of %d — governance records may have been modified; investigate before trusting them (kern audit)\n", brk+1, n)
			}
		}
	}

	ctxEng := context.NewEngine(root, g, mem, fw).
		WithRuntimeSource(rtSrc).
		WithBoundaryProvider(loadBoundaryProvider(root))

	verEng := verification.NewEngineWithIndex(root, ix)

	p := &Platform{
		root:  root,
		ix:    ix,
		graph: g,
		mem:   mem,
		fw:    fw,
		ctx:   ctxEng,
		ver:   verEng,
	}
	// Emit repository.indexed so the bus carries the indexing milestone to
	// webhooks/audit. The bus may be nil at construction (set later via
	// WithBus), so we publish via a deferred check on the returned Platform.
	if p.bus != nil {
		p.bus.Publish(eventbus.Event{
			Kind:    eventbus.RepositoryIndexed,
			Source:  "app",
			Subject: root,
			Payload: map[string]int{"files": len(ix.FileHashes), "symbols": len(ix.Symbols)},
		})
	}
	return p, nil
}

// Root returns the project root this Platform was built for.
func (p *Platform) Root() string { return p.root }

// Index returns the shared prebuilt index. Callers must treat it as read-only.
func (p *Platform) Index() *index.Index { return p.ix }

// Graph returns the shared twin-merged knowledge graph. Read-only after build.
func (p *Platform) Graph() *intelligence.Graph { return p.graph }

// Memory returns the shared engineering memory store.
func (p *Platform) Memory() *memory.MemoryStore { return p.mem }

// Firewall returns the shared governance firewall.
func (p *Platform) Firewall() *governance.Firewall { return p.fw }

// RuntimeSource returns the optional runtime source (for correlation/incident).
// May be nil when no runtime.json was loaded.
func (p *Platform) RuntimeSource() runtime.Source { return p.rtSrc }

// WithRuntimeSource attaches a runtime source (for correlation/incident).
func (p *Platform) WithRuntimeSource(src runtime.Source) *Platform {
	p.rtSrc = src
	return p
}

// ContextEngine returns the shared context engine. Interfaces should prefer
// calling Analyze / Risk instead of using the engine directly, but the accessor
// is exposed for handlers that need engine-specific configuration (e.g. token
// budgets).
func (p *Platform) ContextEngine() *context.Engine { return p.ctx }

// VerificationEngine returns the shared verification engine. Interfaces should
// prefer calling Verify instead of using the engine directly.
func (p *Platform) VerificationEngine() *verification.Engine { return p.ver }

// Analyze runs the context engine against a proposed change and returns the
// assembled ContextPacket plus its rendered text. This is the single
// implementation of the "analyze this proposed change" workflow shared by
// kern analyze (CLI), kern_analyze (MCP), and POST /v1/analyze (REST).
// If change contains whitespace, a symbol is extracted from the description
// (via whatif.ExtractSymbols); otherwise it is treated as a bare symbol name.
func (p *Platform) Analyze(change string) (domain.ContextPacket, string, error) {
	sym, err := p.resolveSymbol(change)
	if err != nil {
		return domain.ContextPacket{}, "", err
	}
	pkt, err := p.analyzeChangeResolvable(sym)
	if err != nil {
		// The first extracted candidate was not resolvable in the graph (e.g.
		// the flagship request "Add tenant-aware caching to UserService"
		// extracts "tenant" first). Try every candidate in order and return the
		// first that resolves, so a natural-language request naming several
		// identifiers works whenever at least one names a real symbol. Only if
		// NONE resolve do we surface the first candidate's error.
		cands := whatif.ExtractSymbols(change)
		for _, cand := range cands {
			if cand == sym {
				continue
			}
			if pkt2, err2 := p.analyzeChangeResolvable(cand); err2 == nil {
				return pkt2, context.RenderText(pkt2), nil
			}
		}
		return domain.ContextPacket{}, "", fmt.Errorf("analyze: %w", err)
	}
	return pkt, context.RenderText(pkt), nil
}

// analyzeChangeResolvable analyzes a proposed change, trying the change text
// itself and then each extracted symbol candidate until one resolves in the
// graph. Natural-language requests that name several identifiers work whenever
// at least one names a real symbol; the raw error of the first attempt is
// returned only when nothing resolves.
func (p *Platform) analyzeChangeResolvable(change string) (domain.ContextPacket, error) {
	pkt, err := p.ctx.AnalyzeChange(change)
	if err == nil {
		return pkt, nil
	}
	for _, cand := range whatif.ExtractSymbols(change) {
		if cand == change {
			continue
		}
		if pkt2, err2 := p.ctx.AnalyzeChange(cand); err2 == nil {
			return pkt2, nil
		}
	}
	return domain.ContextPacket{}, err
}

// Orchestrate runs the silent context pipeline (classify -> assemble -> select
// evidence -> budget -> stamp envelope -> escalation handle) over an intent and
// returns the deterministic result. opts.Budget <= 0 uses the task policy's
// default token budget; opts.Mode overrides the policy family with a named
// context mode. Backs kern_orchestrate (MCP) and kern orchestrate (CLI).
func (p *Platform) Orchestrate(intent string, opts context.OrchestrateOptions) (*context.OrchestrateResult, error) {
	return p.ctx.Orchestrate(intent, opts)
}

// Risk runs the context engine against a proposed change and returns a focused
// risk view (level, factors, mitigation) rather than the full packet. Backs
// kern risk (CLI) and POST /v1/risk (REST).
func (p *Platform) Risk(change string) (domain.ContextPacket, string, error) {
	pkt, err := p.analyzeChangeResolvable(change)
	if err != nil {
		return domain.ContextPacket{}, "", fmt.Errorf("risk: %w", err)
	}
	return pkt, renderRiskText(change, pkt), nil
}

// WhatIf simulates a hypothetical change against the knowledge graph and
// returns the deterministic impact plus rendered report. Read-only — it never
// mutates the graph or index. Shared by kern what-if / kern simulate (CLI),
// kern_what_if (MCP), and POST /v1/what-if + /v1/impact (REST).
// If change contains whitespace, a symbol is extracted; otherwise it is used
// as the bare target.
func (p *Platform) WhatIf(kind whatif.ChangeKind, change, newTarget string) (whatif.Impact, string, error) {
	target, err := p.resolveSymbol(change)
	if err != nil {
		return whatif.Impact{}, "", err
	}
	imp := whatif.Simulate(p.graph, whatif.Change{Kind: kind, Target: target, NewTarget: newTarget})
	// What-if output previously omitted the architecture, memory,
	// and runtime evidence dimensions. Populate them from the platform's own
	// deterministic sources so the impact is complete.
	populateWhatIfEvidence(p, &imp, target)
	imp.Evidence = intel.AnchorLine(p.ix, target)
	return imp, renderWhatIfText(kind, change, target, imp), nil
}

// populateWhatIfEvidence fills the Impact's architecture/historical/runtime
// evidence dimensions from the platform's firewall and memory. All
// sources are deterministic; a nil store/firewall leaves the dimension empty.
func populateWhatIfEvidence(p *Platform, imp *whatif.Impact, target string) {
	// Architecture violations: ask the firewall about the affected resource.
	// A denial surfaces as a violation; an allowed (or unknown-agent) call
	// leaves the dimension empty.
	if p.fw != nil {
		if _, _, _, fwErr := p.fw.Check("whatif", target, "modify"); fwErr != nil {
			imp.ArchitectureViolations = append(imp.ArchitectureViolations, fwErr.Error())
		}
	}
	// Boundary violations: check the affected files against the
	// .kern/boundaries.json guardrails when the platform has an index and a
	// non-empty rule set is configured (same guard semantics as `kern guard`).
	// A missing boundaries.json is fail-open — no rule set, no entries.
	if p.ix != nil {
		b, bErr := intel.LoadBoundaries(p.root)
		if bErr == nil && b != nil && len(b.Rules) > 0 {
			for _, v := range intel.CheckBoundaries(p.ix, b, imp.Files) {
				imp.ArchitectureViolations = append(imp.ArchitectureViolations,
					fmt.Sprintf("boundary: %s -> %s forbidden by rule %s -> %s (%s)", v.CallerFile, v.CalleeFile, v.RuleFrom, v.RuleTo, v.CallerFile))
			}
			// Dedupe against entries the firewall block already added.
			imp.ArchitectureViolations = dedupeStrings(imp.ArchitectureViolations)
		}
	}
	// Historical evidence: recall incident lessons related to the target so
	// prior incidents on this symbol/service inform the impact estimate.
	if p.mem != nil {
		recalled, _ := p.mem.Recall(memory.Query{Type: domain.MemoryIncident, Text: target, Limit: 3})
		for _, m := range recalled {
			imp.HistoricalEvidence = append(imp.HistoricalEvidence, m.Content)
		}
	}
	// Runtime evidence: surface error telemetry from the platform's runtime
	// source (when wired). A nil source leaves the dimension unchanged (empty).
	if p.rtSrc != nil {
		imp.RuntimeEvidence = append(imp.RuntimeEvidence, p.runtimeEvidenceFor(target)...)
	}
}

// dedupeStrings removes duplicate strings, keeping the first occurrence, and
// preserves order.
func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// runtimeEvidenceFor returns the target's runtime error evidence from the
// platform's runtime source, mirroring context.runtimeEvidence.
// A nil source returns nil. Relevance is deterministic: an event matches when
// its "file" attribute equals the target symbol's file, or its Service equals
// the target's directory; when nothing matches, all error events are surfaced
// (bounded) so the dimension is never misleadingly empty when a source is
// wired. Results are sorted by timestamp then ID and capped at maxEvidence.
func (p *Platform) runtimeEvidenceFor(target string) []string {
	if p.rtSrc == nil {
		return nil
	}
	targetFile := ""
	targetDir := ""
	if sym, ok := p.ix.FindSymbol(target); ok && sym.File != "" {
		targetFile = sym.File
		targetDir = filepath.Dir(sym.File)
	}
	var matched []runtime.Event
	var errorsAll []runtime.Event
	for _, ev := range p.rtSrc.Events("") {
		if !ev.IsError() {
			continue
		}
		errorsAll = append(errorsAll, ev)
		if targetFile != "" && ev.Attributes["file"] == targetFile {
			matched = append(matched, ev)
		} else if ev.Service != "" && ev.Service == targetDir {
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
	out := make([]string, 0, len(matched))
	for _, ev := range matched {
		out = append(out, runtime.FormatEvent(ev))
	}
	return out
}

// Verify runs the verification engine against the requested types (default
// "build,test") and returns the unified result. Shared by kern verify (CLI),
// kern_verify (MCP), and POST /v1/verify (REST).
func (p *Platform) Verify(types []string) verification.VerificationResult {
	if len(types) == 0 {
		types = []string{"build", "test"}
	}
	return p.ver.Verify(types)
}

// resolveSymbol normalizes a change description into a bare symbol name. If
// the input contains whitespace it is treated as a natural-language
// description and a symbol is extracted via whatif.ExtractSymbols; otherwise
// it is used as-is. When multiple candidates are extracted, the first that
// actually resolves in the index wins, so prose such as "what breaks if I
// remove the translate function from cmaas_controller?" lands on `translate`,
// not on the lead verb `breaks` (report A8).
func (p *Platform) resolveSymbol(change string) (string, error) {
	if !strings.ContainsAny(change, " \t") {
		return change, nil
	}
	cands := whatif.ExtractSymbols(change)
	if len(cands) == 0 {
		return "", fmt.Errorf("could not identify a symbol in the change description: pass a bare symbol name (e.g. 'GetMySQLDB') or include a qualified name (e.g. 'pkg.Symbol') in the description")
	}
	// Prefer the first candidate that exists in the graph; keep extraction
	// order as the tiebreaker.
	if p.graph != nil {
		for _, c := range cands {
			if p.graph.Resolvable(c) {
				return c, nil
			}
		}
		// None resolve in this project's index — fail with a hint instead of
		// analysing a word from prose and reporting a misleading 0-caller
		// impact (report A8).
		return "", fmt.Errorf("no symbol named %q was found in this project's index (candidates: %s): pass a concrete exported name (e.g. %q) or a qualified name (e.g. 'pkg.Symbol')",
			cands[0], strings.Join(cands, ", "), cands[0])
	}
	return cands[0], nil
}

// loadRuntimeSource returns the runtime source used for correlation/incident.
// Resolution lives in runtime.LoadSource (env/config live adapters first,
// then the .kern/runtime.json snapshot) so the CLI (kern runtime status,
// kern review --runtime) and the platform share one resolution path.
func loadRuntimeSource(root string) runtime.Source {
	return runtime.LoadSource(root)
}

// loadBoundaryProvider surfaces .kern/boundaries.json rules as governance
// policies for the context engine (empty rule set when none present).
func loadBoundaryProvider(root string) func() []domain.Policy {
	return func() []domain.Policy {
		b, err := intel.LoadBoundaries(root)
		if err != nil {
			return nil
		}
		if b == nil {
			// No boundaries configured — nothing to enforce.
			return nil
		}
		out := make([]domain.Policy, 0, len(b.Rules))
		for _, r := range b.Rules {
			out = append(out, domain.FromGuardRule(r))
		}
		return out
	}
}

// CodeContext bounds: the grounding bundle never exceeds these, so the
// coder prompt stays well within model context on large repos.
const (
	codeContextMaxFiles      = 8
	codeContextMaxFileBytes  = 12 * 1024
	codeContextMaxImpactSyms = 20
)

// planFileMention matches file paths a plan text may name (source files and
// common config formats). The caller filters candidates against the repo.
var planFileMention = regexp.MustCompile(`[\w./~-]+\.(go|py|ts|tsx|js|jsx|java|rs|rb|php|cs|c|cpp|h|hpp|kt|swift|scala|sh|sql|proto)`)

// CodeContext assembles the grounded project context for the autonomous
// coder (C1): the contents of the files most relevant to the intent — the
// blast-radius files of any symbols the intent or plan mention, plus files
// the plan names explicitly — and the impact set of those symbols (what
// depends on them). Best-effort by design: an intent that resolves to no
// symbol and a plan naming no existing file yields empty context, and the
// coder runs ungrounded (intent + plan only) as before.
func (p *Platform) CodeContext(intent, plan string) (string, error) {
	// 1. Candidate symbols mentioned in the intent or plan text.
	var syms []string
	seenSym := map[string]bool{}
	for _, text := range []string{intent, plan} {
		for _, c := range whatif.ExtractSymbols(text) {
			if !seenSym[c] {
				seenSym[c] = true
				syms = append(syms, c)
			}
		}
	}

	// 2. Files: plan-named paths first (the strongest signal — the planner
	// names what to touch), then the blast radius of each resolved symbol.
	fileSet := map[string]bool{}
	var planFiles, blastFiles []string
	addPlanFile := func(f string) {
		if f != "" && !fileSet[f] {
			fileSet[f] = true
			planFiles = append(planFiles, f)
		}
	}
	addBlastFile := func(f string) {
		if f != "" && !fileSet[f] {
			fileSet[f] = true
			blastFiles = append(blastFiles, f)
		}
	}
	var impact []string
	for _, s := range syms {
		id, err := p.resolveSymbol(s)
		if err != nil {
			continue
		}
		addBlastFile(p.graphNodeFile(id))
		for _, n := range p.graph.WhatDependsOn(id) {
			if n.Symbol != nil {
				addBlastFile(n.Symbol.File)
			}
			if len(impact) < codeContextMaxImpactSyms && n.ID != id {
				impact = append(impact, n.ID)
			}
		}
	}
	for _, m := range planFileMention.FindAllString(plan, -1) {
		if st, err := os.Stat(filepath.Join(p.root, m)); err == nil && !st.IsDir() {
			addPlanFile(m)
		}
	}
	fileList := append(planFiles, blastFiles...)

	// 3. Render the bundle (ordered: symbol blast first, plan mentions last).
	if len(fileList) == 0 {
		return "", nil
	}
	if len(fileList) > codeContextMaxFiles {
		fileList = fileList[:codeContextMaxFiles]
	}
	var b strings.Builder
	if len(impact) > 0 {
		fmt.Fprintf(&b, "Impact set (symbols that depend on the changed roots; a change may break them): %s\n\n",
			strings.Join(impact, ", "))
	}
	for _, f := range fileList {
		data, err := os.ReadFile(filepath.Join(p.root, f))
		if err != nil {
			continue // unreadable (binary, permissions): skip, do not fail
		}
		if len(data) > codeContextMaxFileBytes {
			data = append(data[:codeContextMaxFileBytes], []byte("\n... (truncated)")...)
		}
		fmt.Fprintf(&b, "<context-file path=%q>\n%s\n</context-file>\n\n", f, string(data))
	}
	return b.String(), nil
}

// graphNodeFile returns the defining file of the graph node for the given
// reference, or "" when no such node exists. The reference may be a bare
// symbol name ("NewFileStore" — how resolveSymbol returns it) or a
// package-scoped node ID ("internal/governance.NewFileStore"); both forms
// are matched against the node ID and the symbol's qualified name.
func (p *Platform) graphNodeFile(ref string) string {
	for _, n := range p.graph.Nodes {
		if n.ID == ref {
			if n.Symbol != nil {
				return n.Symbol.File
			}
			if n.File != nil {
				return n.File.Path
			}
			return ""
		}
		if n.Symbol != nil && n.Symbol.Qualified == ref && n.Symbol.File != "" {
			// A bare qualified form can match several same-named symbols in
			// different packages; only return when unambiguous.
			dup := false
			for _, m := range p.graph.Nodes {
				if m.ID != n.ID && m.Symbol != nil && m.Symbol.Qualified == ref {
					dup = true
					break
				}
			}
			if !dup {
				return n.Symbol.File
			}
			return ""
		}
	}
	return ""
}
