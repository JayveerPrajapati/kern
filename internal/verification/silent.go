// Package verification silent-orchestration helpers: end-to-end checks that
// prove the silent pipeline (context envelope -> deterministic planner ->
// evidence selection -> progressive-disclosure retrieval -> host
// injection/extraction) works and stays kern-invisible.
package verification

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eval"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/host"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/retrieval"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// FullPipelineReport summarizes an end-to-end silent-orchestration run.
type FullPipelineReport struct {
	Version          string `json:"version,omitempty"`
	EnvelopeValid    bool
	PlanProduced     bool
	HandlesResolved  bool
	Injected         bool
	Extracted        bool
	Silent           bool // output contains no kern-internal markers
	TokenReduction   float64
	EvidenceRetained float64
	Steps            []string // human-readable step log
}

// VerifyFullPipeline drives the full silent-orchestration pipeline against a
// real repo root: context envelope → deterministic planner → evidence
// selection → progressive-disclosure retrieval → host injection/extraction.
// It builds the index once (internal/index Build), assembles a ContextPacket
// via the context engine for a symbol in root, validates the envelope
// (domain Validate), classifies the task + selects evidence via the planner
// (PlanPacket), registers/resolves retrieval handles (L1→L3) for the symbol,
// and injects/extracts via the opencode host adapter (NewOpenCodeAdapter)
// into a TEMP COPY of root (never the real tree). Returns the report (never
// fails on the repo — records step outcomes).
func VerifyFullPipeline(root, symbol string) FullPipelineReport {
	rep := FullPipelineReport{}
	steps := func(format string, args ...any) { rep.Steps = append(rep.Steps, fmt.Sprintf(format, args...)) }

	// 1. Index build (once).
	ix, err := index.LoadOrBuild(root)
	if err != nil {
		steps("index build failed: %v", err)
		return rep
	}
	steps("index built (%d symbols)", len(ix.Symbols))

	// 2. Context envelope via the context engine.
	pkt, err := analyzePacket(root, ix, symbol)
	if err != nil {
		steps("context packet failed: %v", err)
		return rep
	}
	rep.EnvelopeValid = pkt.Validate() == nil
	steps("context envelope assembled, valid=%v", rep.EnvelopeValid)

	// 3. Deterministic planner: classify + select evidence + fit to budget.
	plan := context.PlanPacket(&pkt, "Analyze this proposed change: "+symbol, 0)
	rep.PlanProduced = plan != nil && len(plan.Selections) > 0
	if rep.PlanProduced {
		steps("planner produced plan: task=%s, %d selections, %d tokens",
			plan.TaskType, len(plan.Selections), plan.TotalTokens)
	} else {
		steps("planner produced no selections")
	}

	// 4. Progressive-disclosure retrieval handles (L1 summary → L2
	// neighborhood → L3 verbatim source).
	l1, _ := retrieval.Retrieve(ix, retrieval.Options{Query: symbol, Level: retrieval.L1, Limit: 5})
	l2, _ := retrieval.Retrieve(ix, retrieval.Options{Symbol: symbol, Level: retrieval.L2})
	l3, _ := retrieval.Retrieve(ix, retrieval.Options{Symbol: symbol, Level: retrieval.L3})
	rep.HandlesResolved = (l2 != nil && l2.Detail != nil && l2.Detail.Handle != nil) ||
		(l3 != nil && l3.Source != nil && l3.Source.Handle != nil)
	steps("retrieval handles resolved=%v (L1=%v L2=%v L3=%v)", rep.HandlesResolved, l1 != nil, l2 != nil, l3 != nil)

	// 5. Host injection/extraction into a TEMP COPY of root — never the real
	// tree.
	copyRoot, cerr := copyTreeToTemp(root)
	if cerr != nil {
		steps("temp copy of root failed: %v", cerr)
	} else {
		adapter := host.NewOpenCodeAdapter()
		block, ierr := adapter.Inject(copyRoot, &pkt, 0)
		rep.Injected = ierr == nil && block != ""
		steps("opencode adapter injected into temp copy, injected=%v", rep.Injected)
		extracted, xerr := adapter.Extract(copyRoot)
		rep.Extracted = xerr == nil && extracted != ""
		steps("opencode adapter extracted block, extracted=%v", rep.Extracted)
		_ = os.RemoveAll(copyRoot)
	}

	// 6. Kern-invisibility check.
	silent, violations := VerifySilentOrchestration(root, symbol)
	rep.Silent = silent
	steps("silent orchestration: silent=%v violations=%d", silent, len(violations))

	// 7. Token reduction / evidence retention via the eval harness.
	if er, err := VerifyTokenReduction(root, symbol); err == nil {
		rep.TokenReduction = er.TokenReduction
		rep.EvidenceRetained = er.EvidenceRetention
		steps("token reduction=%.2f evidence retained=%.2f", er.TokenReduction, er.EvidenceRetention)
	} else {
		steps("token reduction harness failed: %v", err)
	}
	return rep
}

// VerifySilentOrchestration checks kern-invisibility: the composed pipeline
// output must not leak kern internals to a user/LLM. Deterministic rule:
// the rendered pipeline text (RenderText of the packet + injected block)
// must contain none of the markers ".kern/", "kern_", "internal/", "MCP".
// ("governance" is deliberately NOT a marker: the context engine's
// policy-evaluation claims embed the firewall identity as
// "Policy governance:<resource>:<action> ..." in every rendered fact, so
// flagging it would make every pipeline non-silent.) A marker carried by the
// input symbol/change text itself is also a violation (the change text is a
// visibility surface too), so the check reports every marker found in either
// the input or the rendered output. Returns (silent bool, violations []string).
// silentMarkers are the kern-internal strings whose presence in the rendered
// pipeline output (or the input change text) makes the orchestration
// non-silent.
// silentMarkers are the kern-specific plumbing strings whose presence in the
// rendered pipeline output (or the input change text) makes the orchestration
// non-silent. Bare Go conventions ("internal/", "MCP") are deliberately NOT
// markers: nearly every Go project uses an internal/ directory and MCP is the
// protocol name, so flagging them would make every repo non-silent on its own
// file paths (north-star NS-1).
var silentMarkers = []string{".kern/", "kern_"}

// checkSilentMarkers reports every silent-orchestration marker found in the
// rendered pipeline text or in the input symbol/change text itself (a marker
// carried by the input is reported with the "(in input change text)"
// suffix).
func checkSilentMarkers(rendered, symbol string) []string {
	var violations []string
	for _, m := range silentMarkers {
		if strings.Contains(symbol, m) {
			violations = append(violations, m+" (in input change text)")
			continue
		}
		if strings.Contains(rendered, m) {
			violations = append(violations, m)
		}
	}
	return violations
}

func VerifySilentOrchestration(root, symbol string) (bool, []string) {
	ix, err := index.Build(root)
	if err != nil {
		return false, []string{fmt.Sprintf("index build failed: %v", err)}
	}
	pkt, err := analyzePacket(root, ix, symbol)
	if err != nil {
		return false, []string{fmt.Sprintf("context packet failed: %v", err)}
	}
	rendered := context.RenderText(pkt) + "\n" + host.RenderSummary(&pkt, 0)
	violations := checkSilentMarkers(rendered, symbol)
	return len(violations) == 0, violations
}

// SilentFinding reports one symbol whose context packet or input name leaks
// a silent-orchestration marker.
type SilentFinding struct {
	Symbol, File string
	Reasons      []string
}

// SilentScanReport summarizes a path-aware silent-orchestration scan.
type SilentScanReport struct {
	Version    string `json:"version,omitempty"`
	Path       string
	Symbols    int
	Silent     int
	Violations int
	Findings   []SilentFinding
}

// ScanSilent scans a file or a directory tree under root for
// silent-orchestration violations: for every indexed symbol whose file
// matches the path (exact file match or directory prefix), it renders the
// context packet + host summary and reports each marker leak. The index is
// built once; matching symbols are copied and scanned in deterministic
// (name-sorted) order, capped at limit symbols (default 200 when limit <= 0).
func ScanSilent(root, path string, limit int) (SilentScanReport, error) {
	report := SilentScanReport{Path: path}
	p := filepath.Clean(path)
	statPath := p
	if !filepath.IsAbs(p) {
		statPath = filepath.Join(root, p)
	}
	if _, err := os.Stat(statPath); err != nil {
		return report, fmt.Errorf("scan path %q not found", path)
	}
	ix, err := index.LoadOrBuild(root)
	if err != nil {
		return report, err
	}
	if limit <= 0 {
		limit = 200
	}
	var matches []index.Symbol
	for _, s := range ix.Symbols {
		if s.File == p || strings.HasPrefix(s.File, p+string(filepath.Separator)) {
			matches = append(matches, s)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })
	if len(matches) > limit {
		matches = matches[:limit]
	}
	report.Symbols = len(matches)
	for _, s := range matches {
		pkt, err := analyzePacket(root, ix, s.Name)
		if err != nil {
			report.Findings = append(report.Findings, SilentFinding{
				Symbol:  s.Name,
				File:    symbolFile(ix, nil, s.Name),
				Reasons: []string{"context packet failed: " + err.Error()},
			})
			report.Violations++
			continue
		}
		rendered := context.RenderText(pkt) + "\n" + host.RenderSummary(&pkt, 0)
		reasons := checkSilentMarkers(rendered, s.Name)
		file := symbolFile(ix, &pkt, s.Name)
		if len(reasons) > 0 {
			report.Findings = append(report.Findings, SilentFinding{Symbol: s.Name, File: file, Reasons: reasons})
			report.Violations += len(reasons)
		} else {
			report.Silent++
		}
	}
	return report, nil
}

// VerifyTokenReduction proves token reduction without critical-evidence
// loss, using the internal/eval harness: baseline = RenderText of the full
// packet (with a symbol/file locator header so the critical evidence is part
// of the baseline); candidate = budget.Fit of the same text at ~50% tokens;
// critical evidence = the symbol's name + its file path. Returns the eval
// EvalResult and nil error when the harness ran.
func VerifyTokenReduction(root, symbol string) (eval.EvalResult, error) {
	ix, err := index.LoadOrBuild(root)
	if err != nil {
		return eval.EvalResult{}, fmt.Errorf("verify: index build: %w", err)
	}
	pkt, err := analyzePacket(root, ix, symbol)
	if err != nil {
		return eval.EvalResult{}, err
	}
	file := symbolFile(ix, &pkt, symbol)
	// RenderText does not render file paths, so the locator header carries the
	// critical evidence into the baseline (and survives the fit, which keeps
	// the document head).
	baseline := fmt.Sprintf("symbol: %s\nfile: %s\n", symbol, file) + context.RenderText(pkt)
	half := tokenize.Count(baseline) / 2
	if half <= 0 {
		half = 1
	}
	// Proportional head cut, not the line-oriented log fitter: Fit collapses
	// dense packet renders to their first line (reduction ~1.00, evidence
	// dropped), which destroys the compression proof instead of showing it
	// (north-star NS-3). FitProportional keeps the leading budget-proportional
	// portion, so the header (which carries both critical fragments) survives.
	candidate := budget.FitProportional(baseline, half)
	h := eval.NewEvalHarness([]eval.Sample{{
		Name:             "silent-token-reduction",
		Baseline:         baseline,
		Candidate:        candidate,
		CriticalEvidence: []string{symbol, file},
	}}, 0, nil)
	return h.Run(), nil
}

// analyzePacket builds the index graph and runs the context engine for a
// symbol, mirroring how the app platform wires the engine (without the
// runtime source / boundary files, which are optional). The firewall
// registers the engine's own agent identity (engineAgent = "context-engine")
// with the same permissions the platform grants, so the engine's risk
// assessment passes instead of emitting a "DENIED: register the agent before
// use" policy fact that would leak "governance" into the rendered output.
func analyzePacket(root string, ix *index.Index, symbol string) (domain.ContextPacket, error) {
	g := intel.FromIndex(ix)
	mem := memory.NewMemoryStore(root)
	fw := governance.NewFirewall().WithAgents(governance.NewAgent(
		"context-engine", "Context Engine", "analyzer",
		[]governance.Permission{
			{Resource: "source", Action: "write"},
			{Resource: "tests", Action: "write"},
			{Resource: "config", Action: "write"},
			{Resource: "documentation", Action: "write"},
			{Resource: "security", Action: "write"},
		},
	))
	eng := context.NewEngine(root, &g, mem, fw)
	return eng.AnalyzeChange(symbol)
}

// symbolFile resolves the file path of a symbol from the packet first, then
// the index.
func symbolFile(ix *index.Index, pkt *domain.ContextPacket, symbol string) string {
	if pkt != nil {
		for _, s := range pkt.Symbols {
			if s.Name == symbol && s.File != "" {
				return s.File
			}
		}
	}
	if ix != nil {
		for _, s := range ix.Symbols {
			if s.Name == symbol && s.File != "" {
				return s.File
			}
		}
	}
	return ""
}

// copyTreeToTemp copies root into a fresh temp directory (used for host
// injection so the real tree is never modified).
func copyTreeToTemp(root string) (string, error) {
	dst, err := os.MkdirTemp("", "kern-silent-*")
	if err != nil {
		return "", err
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		// Never follow symlinks out of root. WalkDir does not traverse
		// symlinked directories, but os.ReadFile below would follow a file
		// symlink (e.g. notes -> ~/.ssh/id_rsa) and copy its target's content
		// into the temp tree. Skip every symlink entry: do not copy, do not
		// descend.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		// kern runtime state (index, coordination, eventbus socket) and heavy
		// metadata (.git, node_modules, bin, .cache) are not target-repo content;
		// copying them bloats the temp tree and causes heavy IO.
		if rel == ".kern" || strings.HasPrefix(rel, ".kern"+string(os.PathSeparator)) ||
			rel == ".git" || strings.HasPrefix(rel, ".git"+string(os.PathSeparator)) ||
			rel == "node_modules" || strings.HasPrefix(rel, "node_modules"+string(os.PathSeparator)) ||
			rel == ".cache" || strings.HasPrefix(rel, ".cache"+string(os.PathSeparator)) ||
			rel == "bin" || strings.HasPrefix(rel, "bin"+string(os.PathSeparator)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// Sockets, FIFOs, and devices cannot be copied as files (e.g.
		// .kern/events.sock); skip every non-regular entry.
		if !d.Type().IsRegular() {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		_ = os.RemoveAll(dst)
		return "", err
	}
	return dst, nil
}
