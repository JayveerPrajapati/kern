// In-package test platform: a faithful PlatformAPI implementation built over
// the same engines app.Platform wires (index.LoadOrBuild → twin-merged graph
// → memory → firewall → context engine → verification engine). The moved
// end-to-end suites (vertical/safe-change/phase10/benchmark/policy/persistence)
// construct TaskService over this platform because tasklife tests cannot
// import internal/app (app imports tasklife). Method bodies mirror
// internal/app/platform.go and internal/app/correlate_code.go so the tests
// exercise the same behavior as a production platform.
package tasklife

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/calibrate"
	"github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/guard"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/storage"
	tok "github.com/JayveerPrajapati/kern/internal/tokenize"
	"github.com/JayveerPrajapati/kern/internal/twin"
	"github.com/JayveerPrajapati/kern/internal/verdict"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// testPlatform mirrors app.Platform's fields and method surface.
type testPlatform struct {
	root  string
	ix    *index.Index
	graph *intel.Graph
	mem   *memory.MemoryStore
	fw    *governance.Firewall
	ctx   *context.Engine
	ver   *verification.Engine
	rtSrc runtime.Source
}

// newTestPlatform builds a test platform for root, mirroring app.New.
func newTestPlatform(t *testing.T, root string) *testPlatform {
	t.Helper()
	ix, err := index.LoadOrBuild(root)
	if err != nil {
		t.Fatalf("newTestPlatform: index: %v", err)
	}
	return newTestPlatformWithIndex(t, root, ix)
}

// newTestPlatformWithIndex builds a test platform over a caller-owned index,
// mirroring app.NewWithIndex → NewWithGraph.
func newTestPlatformWithIndex(t *testing.T, root string, ix *index.Index) *testPlatform {
	t.Helper()
	gp := intel.FromIndex(ix)
	g := &gp
	rtSrc := runtime.LoadSource(root)
	_ = twin.Merge(g, twin.NewExtractors(root, rtSrc))
	mem := memory.NewMemoryStore(root)
	fw := governance.NewFirewallWithApprovalStore(root).WithAgents(
		governance.NewAgent("context-engine", "Context Engine", "analyzer",
			[]governance.Permission{
				{Resource: "source", Action: "read"},
				{Resource: "source", Action: "write"},
				{Resource: "security", Action: "write"},
				{Resource: "tests", Action: "write"},
				{Resource: "config", Action: "write"},
				{Resource: "documentation", Action: "write"},
			}),
		governance.NewAgent("kern", "Kern Control Plane", "operator",
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
			}),
	)
	// Wire the audit log to a file-based store and replay/verify the
	// tamper-evident chain, mirroring app.NewWithGraph.
	auditDir := filepath.Join(root, ".kern", "audit")
	_ = os.MkdirAll(auditDir, 0o755)
	fw.AuditLog().WithStore(storage.NewLog(auditDir)).WithLockPath(filepath.Join(auditDir, ".lock"))
	if n, err := fw.AuditLog().Replay(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: audit log replay failed: %v\n", err)
	} else if n > 0 {
		if brk, verified := fw.AuditLog().VerifyChainReport(); brk >= 0 {
			if verified == 0 {
				fmt.Fprintf(os.Stderr, "WARNING: audit log chain verification could not verify ANY entries (%d of %d) — this indicates either a legacy version migration or a full-chain rewrite; if this is not a known upgrade, investigate before trusting governance records (kern audit)\n", n, n)
			} else {
				fmt.Fprintf(os.Stderr, "WARNING: audit log tamper chain verification FAILED at entry %d of %d — governance records may have been modified; investigate before trusting them (kern audit)\n", brk+1, n)
			}
		}
	}
	ctxEng := context.NewEngine(root, g, mem, fw).
		WithRuntimeSource(rtSrc).
		WithBoundaryProvider(testBoundaryProvider(root)).
		WithIndex(ix)
	verEng := verification.NewEngineWithIndex(root, ix)
	return &testPlatform{root: root, ix: ix, graph: g, mem: mem, fw: fw, ctx: ctxEng, ver: verEng, rtSrc: rtSrc}
}
func testBoundaryProvider(root string) func() []domain.Policy {
	return func() []domain.Policy {
		b, err := guard.LoadBoundaries(root)
		if err != nil {
			return nil
		}
		if b == nil {
			return nil
		}
		out := make([]domain.Policy, 0, len(b.Rules))
		for _, r := range b.Rules {
			out = append(out, domain.FromGuardRule(r))
		}
		return out
	}
}

func (p *testPlatform) Root() string                   { return p.root }
func (p *testPlatform) Index() *index.Index            { return p.ix }
func (p *testPlatform) Graph() *intel.Graph            { return p.graph }
func (p *testPlatform) Memory() *memory.MemoryStore    { return p.mem }
func (p *testPlatform) Firewall() *governance.Firewall { return p.fw }
func (p *testPlatform) RuntimeSource() runtime.Source  { return p.rtSrc }
func (p *testPlatform) ContextEngine() *context.Engine { return p.ctx }

// WithRuntimeSource attaches a runtime source (mirrors Platform.WithRuntimeSource).
func (p *testPlatform) WithRuntimeSource(src runtime.Source) *testPlatform {
	p.rtSrc = src
	return p
}

// Analyze mirrors Platform.Analyze.
func (p *testPlatform) Analyze(change string) (domain.ContextPacket, string, error) {
	sym, fuzzy, err := resolveSymbol(p, change)
	if err != nil {
		return domain.ContextPacket{}, "", err
	}
	pkt, err := p.analyzeChangeResolvable(sym)
	if err != nil {
		cands := whatif.ExtractSymbolsIndex(change, p.ix)
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
	text := context.RenderText(pkt)
	if fuzzy && text != "" {
		text = fmt.Sprintf("resolved %q -> %s (fuzzy match)\n%s", change, sym, text)
	}
	return pkt, text, nil
}

// analyzeChangeResolvable mirrors Platform.analyzeChangeResolvable.
func (p *testPlatform) analyzeChangeResolvable(change string) (domain.ContextPacket, error) {
	pkt, err := p.ctx.AnalyzeChange(change)
	if err == nil {
		return pkt, nil
	}
	for _, cand := range whatif.ExtractSymbolsIndex(change, p.ix) {
		if cand == change {
			continue
		}
		if pkt2, err2 := p.ctx.AnalyzeChange(cand); err2 == nil {
			return pkt2, nil
		}
	}
	return domain.ContextPacket{}, err
}

// Risk mirrors Platform.Risk.
func (p *testPlatform) Risk(change string) (domain.ContextPacket, string, error) {
	pkt, err := p.analyzeChangeResolvable(change)
	if err != nil {
		return domain.ContextPacket{}, "", fmt.Errorf("risk: %w", err)
	}
	return pkt, RenderRiskText(change, pkt), nil
}

// WhatIf mirrors Platform.WhatIf.
func (p *testPlatform) WhatIf(kind whatif.ChangeKind, change, newTarget string) (whatif.Impact, string, error) {
	target, fuzzy, err := resolveSymbol(p, change)
	if err != nil {
		return whatif.Impact{}, "", err
	}
	imp := whatif.Simulate(p.graph, whatif.Change{Kind: kind, Target: target, NewTarget: newTarget})
	syms := make([]string, 0, len(imp.Affected)+1)
	syms = append(syms, target)
	syms = append(syms, imp.Affected...)
	imp.Entities = AttachWhatIfEntities(p.graph, syms)
	p.populateWhatIfEvidence(&imp, target)
	imp.Evidence = intel.AnchorLine(p.ix, target)
	text := RenderWhatIfText(kind, change, target, imp)
	if fuzzy && text != "" {
		text = fmt.Sprintf("resolved %q -> %s (fuzzy match)\n%s", change, target, text)
	}
	if f := graphNodeFile(p.graph, target); f != "" {
		if line := calibrate.ConfidenceLine(p.root, calibrate.SubsystemOf(f)); line != "" {
			text = text + "\n" + line
		}
	}
	return imp, text, nil
}

// populateWhatIfEvidence mirrors Platform.populateWhatIfEvidence.
func (p *testPlatform) populateWhatIfEvidence(imp *whatif.Impact, target string) {
	if p.fw != nil {
		if _, _, _, fwErr := p.fw.Check("whatif", target, "modify"); fwErr != nil {
			imp.ArchitectureViolations = append(imp.ArchitectureViolations, fwErr.Error())
		}
	}
	if p.ix != nil {
		b, bErr := guard.LoadBoundaries(p.root)
		if bErr == nil {
			if b == nil {
				b = guard.InferBoundaries(p.ix)
			}
			if b != nil && len(b.Rules) > 0 {
				for _, v := range guard.CheckBoundaries(p.ix, b, imp.Files) {
					imp.ArchitectureViolations = append(imp.ArchitectureViolations,
						fmt.Sprintf("boundary: %s -> %s forbidden by rule %s -> %s (%s)", v.CallerFile, v.CalleeFile, v.RuleFrom, v.RuleTo, v.CallerFile))
				}
				imp.ArchitectureViolations = dedupeStrings(imp.ArchitectureViolations)
			}
		}
	}
	if p.mem != nil {
		recalled, _ := p.mem.Recall(memory.Query{Type: domain.MemoryIncident, Text: target, Limit: 3})
		for _, m := range recalled {
			imp.HistoricalEvidence = append(imp.HistoricalEvidence, m.Content)
		}
	}
	if p.rtSrc != nil {
		imp.RuntimeEvidence = append(imp.RuntimeEvidence, p.runtimeEvidenceFor(target)...)
	}
}

// dedupeStrings mirrors Platform.dedupeStrings.
func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return make([]string, 0)
	}
	cp := append([]string(nil), in...)
	slices.Sort(cp)
	return slices.Compact(cp)
}

// runtimeEvidenceFor mirrors Platform.runtimeEvidenceFor.
func (p *testPlatform) runtimeEvidenceFor(target string) []string {
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

// Verify mirrors Platform.Verify.
func (p *testPlatform) Verify(types []string, opts ...verification.Option) verdict.VerificationResult {
	if len(types) == 0 {
		types = []string{"build", "test"}
	}
	eng := p.ver
	if len(opts) > 0 {
		e := *p.ver
		for _, o := range opts {
			o(&e)
		}
		eng = &e
	}
	return eng.Verify(types)
}

// CorrelateCode mirrors Platform.CorrelateCode (app/correlate_code.go).
func (p *testPlatform) CorrelateCode(alert domain.Alert) (Correlation, error) {
	src := p.RuntimeSource()
	if src == nil {
		src = runtime.NewStore()
	}
	shared := runtime.NewSharedCorrelator(src, incident.DefaultLookback)
	corr := shared.Correlate(alert)
	chain := shared.CorrelateChain(alert)
	out := Correlation{
		Alert:           alert,
		AffectedService: corr.AffectedService,
	}
	out.RuntimeEvidence = runtimeEvidenceLines(corr, src.Name())
	if corr.AffectedService != "" {
		files, syms, conf := p.serviceToCode(corr.AffectedService, chain)
		out.ImplicatedFiles = files
		out.ImplicatedSymbols = syms
		out.Confidence = conf
	} else {
		out.Confidence = "low"
	}
	inc := domain.Incident{Alert: alert, AffectedService: corr.AffectedService, Title: alert.Message}
	if pb, ok := incident.FindPlaybook(p.Root(), inc); ok {
		out.PlaybookSignature = pb.Signature
		out.PlaybookSteps = pb.Steps
	}
	return out, nil
}

// serviceToCode mirrors Platform.serviceToCode (app/correlate_code.go).
func (p *testPlatform) serviceToCode(svc string, chain runtime.CorrelationChain) (files, syms []string, confidence string) {
	svc = strings.Trim(filepath.ToSlash(svc), "/")
	if svc == "" {
		return nil, nil, "low"
	}
	prefix := svc + "/"
	fileSet := map[string]bool{}
	var exact, heuristic []string
	addFile := func(list []string, fp string) []string {
		if !fileSet[fp] {
			fileSet[fp] = true
			list = append(list, fp)
		}
		return list
	}
	// 1) Exact directory mapping from graph file nodes.
	for _, n := range p.Graph().Nodes {
		if n.Kind != "file" || n.File == nil {
			continue
		}
		fp := filepath.ToSlash(n.File.Path)
		if fp == svc || strings.HasPrefix(fp, prefix) {
			exact = addFile(exact, fp)
		}
	}
	// 2) Twin entity mapping: service-owned nodes (service/api/deployment)
	// and error nodes whose edges reach file nodes ("defined_in"/"caused_by").
	twinFiles := map[string]bool{}
	for _, n := range p.Graph().Nodes {
		owned := false
		switch {
		case n.Kind == "service" && n.Service != nil && n.Service.Name == svc:
			owned = true
		case n.Kind == "api" && n.API != nil && n.API.Service == svc:
			owned = true
		case n.Kind == "deployment" && strings.Contains(n.Label, svc):
			owned = true
		case n.Kind == "error":
			owned = true // error nodes link to files via caused_by edges below
		}
		if !owned {
			continue
		}
		for _, e := range p.Graph().Edges {
			if e.From == n.ID && strings.HasPrefix(e.To, "file:") {
				if fp := strings.TrimPrefix(e.To, "file:"); fp != "" {
					twinFiles[fp] = true
				}
			}
		}
	}
	for fp := range twinFiles {
		heuristic = addFile(heuristic, fp)
	}
	// 3) Implicated symbols: graph symbol nodes defined in an implicated
	// file, plus the deterministic symbol references the runtime correlation
	// chain already resolved.
	symSet := map[string]bool{}
	for _, n := range p.Graph().Nodes {
		if n.Kind != "symbol" || n.Symbol == nil {
			continue
		}
		if fileSet[filepath.ToSlash(n.Symbol.File)] {
			id := n.ID
			if id == "" {
				id = n.Symbol.Qualified
			}
			if id != "" {
				symSet[id] = true
			}
		}
	}
	for _, l := range chain.Links {
		if l.Stage == "symbol" {
			symSet[l.ID] = true
		}
	}
	if len(exact) > 0 {
		confidence = "high"
	} else {
		confidence = "low"
	}
	merged := append(exact, heuristic...)
	sort.Strings(merged)
	files = slices.Compact(merged)
	syms = make([]string, 0, len(symSet))
	for s := range symSet {
		syms = append(syms, s)
	}
	sort.Strings(syms)
	return files, syms, confidence
}

// runtimeEvidenceLines mirrors app's runtimeEvidenceLines.
func runtimeEvidenceLines(corr runtime.Correlation, source string) []string {
	var out []string
	var depl []string
	for _, d := range corr.Deployments {
		depl = append(depl, d.Version+"@"+shortSHA8(d.CommitSHA))
	}
	line := fmt.Sprintf("%d errors, %d logs, %d metrics, %d spans; deployments %v",
		len(corr.ErrorEvents), len(corr.LogEvents), len(corr.MetricEvents), len(corr.TraceSpans), depl)
	if source != "" {
		line = source + ": " + line
	}
	out = append(out, line)
	for _, e := range corr.ErrorEvents {
		out = append(out, "error "+e.ID+": "+e.Message)
	}
	return out
}

// shortSHA8 mirrors app's shortSHA8.
func shortSHA8(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
}

// CodeContext bounds (mirror app/platform.go).
const (
	testCodeContextMaxFiles      = 8
	testCodeContextMaxFileBytes  = 12 * 1024
	testCodeContextMaxImpactSyms = 20
)

// testPlanFileMention mirrors app's planFileMention.
var testPlanFileMention = regexp.MustCompile(`[\w./~-]+\.(go|py|ts|tsx|js|jsx|java|rs|rb|php|cs|c|cpp|h|hpp|kt|swift|scala|sh|sql|proto)`)

// CodeContext mirrors Platform.CodeContext.
func (p *testPlatform) CodeContext(intent, plan string) (string, error) {
	var syms []string
	seenSym := map[string]bool{}
	for _, text := range []string{intent, plan} {
		for _, c := range whatif.ExtractSymbolsIndex(text, p.ix) {
			if !seenSym[c] {
				seenSym[c] = true
				syms = append(syms, c)
			}
		}
	}
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
		id, _, err := resolveSymbol(p, s)
		if err != nil {
			continue
		}
		addBlastFile(graphNodeFile(p.graph, id))
		for _, n := range p.graph.WhatDependsOn(id) {
			if n.Symbol != nil {
				addBlastFile(n.Symbol.File)
			}
			if len(impact) < testCodeContextMaxImpactSyms && n.ID != id {
				impact = append(impact, n.ID)
			}
		}
	}
	for _, m := range testPlanFileMention.FindAllString(plan, -1) {
		if st, err := os.Stat(filepath.Join(p.root, m)); err == nil && !st.IsDir() {
			addPlanFile(m)
		}
	}
	fileList := append(planFiles, blastFiles...)
	if len(fileList) == 0 {
		return "", nil
	}
	if len(fileList) > testCodeContextMaxFiles {
		fileList = fileList[:testCodeContextMaxFiles]
	}
	var b strings.Builder
	if len(impact) > 0 {
		fmt.Fprintf(&b, "Impact set (symbols that depend on the changed roots; a change may break them): %s\n\n",
			strings.Join(impact, ", "))
	}
	for _, f := range fileList {
		data, err := os.ReadFile(filepath.Join(p.root, f))
		if err != nil {
			continue
		}
		if len(data) > testCodeContextMaxFileBytes {
			data = append(data[:testCodeContextMaxFileBytes], []byte("\n... (truncated)")...)
		}
		fmt.Fprintf(&b, "<context-file path=%q>\n%s\n</context-file>\n\n", f, string(data))
	}
	out := b.String()
	if max := testCodeContextMaxTokens(); max > 0 && tok.Count(out) > max {
		out = budget.Fit(out, max)
	}
	return out, nil
}

// testCodeContextMaxTokens mirrors app's codeContextMaxTokens.
func testCodeContextMaxTokens() int {
	v := strings.TrimSpace(os.Getenv("KERN_CODE_CONTEXT_MAX_TOKENS"))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
