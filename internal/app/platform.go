// Package app is the Task-centered application-services layer that sits above
// the existing engines and below every interface (MCP, CLI, REST, SDK, Web).
//
// It exists to enforce the Kern 2.0 Integration Transformation Plan's
// Architecture Invariant 1: "Interfaces do not contain core business
// orchestration." Before this package, the analyze / plan / what-if / impact /
// verify pipelines were duplicated three ways — once in internal/mcp, once in
// cmd/kern, and once in internal/web — each rebuilding the index, graph,
// memory store, firewall, and context/verification engines independently.
//
// Platform is the single shared facade. Every interface calls the same methods
// here, and Platform calls the same engines. The orchestration that was
// copy-pasted across handlers now lives in exactly one place.
//
// Construction:
//   - New(root)           — builds the index and derived state once; for
//                            one-shot callers (CLI, MCP).
//   - NewWithIndex(root, ix) — reuses a caller-owned prebuilt index; for
//                            long-lived servers (web) that already index at
//                            startup.
//
// All methods are safe for concurrent use: the index and graph are treated as
// read-only after construction, and the engines hold only read-only references.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
//
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
	// Merge the Digital Twin's non-code dimensions (API, data, messaging,
	// infra) into the code graph so engines reason over the full knowledge
	// graph, not just source. Best-effort: extraction errors are non-fatal
	// (the graph stays code-only).
	_ = twin.Merge(g, twin.NewExtractors(root, nil))

	mem := memory.NewMemoryStore(root)
	fw := governance.NewFirewall().WithAgents(governance.NewAgent(
		"context-engine", "Context Engine", "analyzer",
		[]governance.Permission{
			{Resource: "source", Action: "read"},
			{Resource: "source", Action: "write"},
			{Resource: "security", Action: "write"},
			{Resource: "tests", Action: "write"},
			{Resource: "config", Action: "write"},
			{Resource: "documentation", Action: "write"},
		},
	))

	// Wire the audit log to a file-based store so `kern audit` can read entries
	// across processes (and so a fresh server sees entries written by a prior
	// one). The CLI only ever reads these files; the running firewall writes them.
	auditDir := filepath.Join(root, ".kern", "audit")
	_ = os.MkdirAll(auditDir, 0o755)
	fw.AuditLog().WithStore(storage.NewLocal(auditDir))

	ctxEng := context.NewEngine(root, g, mem, fw).
		WithRuntimeSource(loadRuntimeSource(root)).
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
//
// If change contains whitespace, a symbol is extracted from the description
// (via whatif.ExtractSymbols); otherwise it is treated as a bare symbol name.
func (p *Platform) Analyze(change string) (domain.ContextPacket, string, error) {
	sym, err := p.resolveSymbol(change)
	if err != nil {
		return domain.ContextPacket{}, "", err
	}
	pkt, err := p.ctx.AnalyzeChange(sym)
	if err != nil {
		return domain.ContextPacket{}, "", fmt.Errorf("analyze: %w", err)
	}
	return pkt, context.RenderText(pkt), nil
}

// Risk runs the context engine against a proposed change and returns a focused
// risk view (level, factors, mitigation) rather than the full packet. Backs
// kern risk (CLI) and POST /v1/risk (REST).
func (p *Platform) Risk(change string) (domain.ContextPacket, string, error) {
	pkt, err := p.ctx.AnalyzeChange(change)
	if err != nil {
		return domain.ContextPacket{}, "", fmt.Errorf("risk: %w", err)
	}
	return pkt, renderRiskText(change, pkt), nil
}

// WhatIf simulates a hypothetical change against the knowledge graph and
// returns the deterministic impact plus rendered report. Read-only — it never
// mutates the graph or index. Shared by kern what-if / kern simulate (CLI),
// kern_what_if (MCP), and POST /v1/what-if + /v1/impact (REST).
//
// If change contains whitespace, a symbol is extracted; otherwise it is used
// as the bare target.
func (p *Platform) WhatIf(kind whatif.ChangeKind, change, newTarget string) (whatif.Impact, string, error) {
	target, err := p.resolveSymbol(change)
	if err != nil {
		return whatif.Impact{}, "", err
	}
	imp := whatif.Simulate(p.graph, whatif.Change{Kind: kind, Target: target, NewTarget: newTarget})
	return imp, renderWhatIfText(kind, change, target, imp), nil
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
// it is used as-is.
func (p *Platform) resolveSymbol(change string) (string, error) {
	if !strings.ContainsAny(change, " \t") {
		return change, nil
	}
	cands := whatif.ExtractSymbols(change)
	if len(cands) == 0 {
		return "", fmt.Errorf("could not identify a symbol in the change description. Pass a bare symbol name (e.g. 'GetMySQLDB') or include a qualified name (e.g. 'pkg.Symbol') in the description.")
	}
	return cands[0], nil
}

// loadRuntimeSource returns the runtime source used for correlation/incident.
//
// Live production adapters take precedence: if any of the env vars
// KERN_PROMETHEUS_URL, KERN_OTEL_URL, or KERN_K8S_API are set, a live polling
// adapter for the first configured endpoint is returned and the
// .kern/runtime.json snapshot is ignored. Otherwise it loads
// .kern/runtime.json when present; nil-safe otherwise.
func loadRuntimeSource(root string) runtime.Source {
	interval := runtimePollInterval()

	if url := os.Getenv("KERN_PROMETHEUS_URL"); url != "" {
		return runtime.NewLivePrometheusSource(url, interval)
	}
	if url := os.Getenv("KERN_OTEL_URL"); url != "" {
		return runtime.NewLiveOtelSource(url, interval)
	}
	if api := os.Getenv("KERN_K8S_API"); api != "" {
		return runtime.NewLiveKubernetesSource(
			api,
			os.Getenv("KERN_K8S_TOKEN"),
			os.Getenv("KERN_K8S_NAMESPACE"),
			interval,
		)
	}

	st, err := runtime.LoadJSON(filepath.Join(root, ".kern", "runtime.json"))
	if err != nil {
		return nil
	}
	return st
}

// runtimePollInterval returns the live-adapter poll interval from
// KERN_POLL_INTERVAL (a Go duration string, default 30s). Invalid values fall
// back to the default.
func runtimePollInterval() time.Duration {
	const def = 30 * time.Second
	if s := os.Getenv("KERN_POLL_INTERVAL"); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			return d
		}
	}
	return def
}

// loadBoundaryProvider surfaces .kern/boundaries.json rules as governance
// policies for the context engine (empty rule set when none present).
func loadBoundaryProvider(root string) func() []domain.Policy {
	return func() []domain.Policy {
		b, err := intel.LoadBoundaries(root)
		if err != nil {
			return nil
		}
		out := make([]domain.Policy, 0, len(b.Rules))
		for _, r := range b.Rules {
			out = append(out, domain.FromGuardRule(r))
		}
		return out
	}
}
