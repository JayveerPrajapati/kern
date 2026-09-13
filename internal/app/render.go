package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/modernization"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// renderRiskText produces the focused risk view (level, factors, mitigation)
// matching the historical kern risk CLI output so callers see no behavioral
// change after the migration to Platform.
func renderRiskText(change string, pkt domain.ContextPacket) string {
	var b strings.Builder
	fmt.Fprintf(&b, "RISK for: %s\n", change)
	if len(pkt.Risks) == 0 {
		fmt.Fprintln(&b, "no risks identified")
		return b.String()
	}
	for _, r := range pkt.Risks {
		fmt.Fprintf(&b, "%s\n", r.Level)
		for _, f := range r.Factors {
			fmt.Fprintf(&b, "  factor: %s\n", f)
		}
		if r.Mitigation != "" {
			fmt.Fprintf(&b, "  mitigation: %s\n", r.Mitigation)
		}
	}
	return b.String()
}

// renderWhatIfText produces the what-if / impact report matching the historical
// whatif.SimulateRender output so CLI and MCP callers see no behavioral change
// after the migration to Platform.
func renderWhatIfText(kind whatif.ChangeKind, change, target string, imp whatif.Impact) string {
	var b strings.Builder
	fmt.Fprintf(&b, "change: %s %s\n", kind, target)
	if target != change {
		desc := change
		if len(desc) > 60 {
			desc = desc[:60]
		}
		fmt.Fprintf(&b, "  (extracted from: %s)\n", desc)
	}
	fmt.Fprintf(&b, "affected: %d\n", len(imp.Affected))
	fmt.Fprintf(&b, "files: %d\n", len(imp.Files))
	fmt.Fprintf(&b, "services: %d\n", len(imp.Services))
	fmt.Fprintf(&b, "tests: %d\n", len(imp.Tests))
	if len(imp.BrokenCallSites) > 0 {
		fmt.Fprintf(&b, "Broken call sites: %s\n", strings.Join(imp.BrokenCallSites, ", "))
	}
	if len(imp.UntestedAffected) > 0 {
		fmt.Fprintf(&b, "Untested affected: %s\n", strings.Join(imp.UntestedAffected, ", "))
	}
	fmt.Fprintf(&b, "risk: %s\n", imp.Risk)
	fmt.Fprintf(&b, "recommendation: %s\n", imp.Recommendation)
	if imp.Evidence != "" {
		fmt.Fprintf(&b, "%s\n", imp.Evidence)
	}
	for _, c := range imp.Claims {
		fmt.Fprintf(&b, "claim[%s] %s (%.1f): %s\n", c.Type, c.Provenance, c.Confidence, c.Statement)
	}
	return b.String()
}

// renderPlanText renders a domain.Plan as the text output for kern plan / MCP
// kern_plan / REST /v1/plan. The 12 spec fields are rendered as labelled
// sections so the output is machine-parseable and human-readable.
func renderPlanText(p domain.Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PLAN\n")
	fmt.Fprintf(&b, "Objective: %s\n", p.Objective)
	fmt.Fprintf(&b, "Scope: %s\n", p.Scope)
	fmt.Fprintf(&b, "Risk: %s\n", p.Risk)
	fmt.Fprintf(&b, "Affected components: %d\n", len(p.AffectedComponents))
	for _, c := range p.AffectedComponents {
		fmt.Fprintf(&b, "  - %s\n", c)
	}
	fmt.Fprintf(&b, "Implementation steps:\n")
	for i, step := range p.ImplementationSteps {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, step)
	}
	if len(p.Dependencies) > 0 {
		fmt.Fprintf(&b, "Dependencies:\n")
		for _, d := range p.Dependencies {
			fmt.Fprintf(&b, "  - %s\n", d)
		}
	}
	fmt.Fprintf(&b, "Rollback: %s\n", p.Rollback)
	if len(p.Tests) > 0 {
		fmt.Fprintf(&b, "Tests:\n")
		for _, t := range p.Tests {
			fmt.Fprintf(&b, "  - %s\n", t)
		}
	}
	if p.Security != "" {
		fmt.Fprintf(&b, "Security: %s\n", p.Security)
	}
	if p.Architecture != "" {
		fmt.Fprintf(&b, "Architecture: %s\n", p.Architecture)
	}
	if p.Deployment != "" {
		fmt.Fprintf(&b, "Deployment: %s\n", p.Deployment)
	}
	if len(p.Evidence) > 0 {
		fmt.Fprintf(&b, "Evidence: %d claims\n", len(p.Evidence))
	}
	return b.String()
}

// scopeFromPacket derives a deterministic scope string from the context packet.
func scopeFromPacket(pkt domain.ContextPacket) string {
	if len(pkt.Files) == 0 && len(pkt.Symbols) == 0 {
		return "unknown (no symbols or files resolved)"
	}
	parts := make([]string, 0, 2)
	if len(pkt.Symbols) > 0 {
		parts = append(parts, fmt.Sprintf("%d symbols", len(pkt.Symbols)))
	}
	if len(pkt.Files) > 0 {
		parts = append(parts, fmt.Sprintf("%d files", len(pkt.Files)))
	}
	return strings.Join(parts, ", ")
}

// riskLevelString returns the highest risk level from a slice of risks as a
// lowercase string. Returns "low" when no risks are present.
func riskLevelString(risks []domain.Risk) string {
	if len(risks) == 0 {
		return "low"
	}
	highest := domain.RiskLow
	for _, r := range risks {
		switch r.Level {
		case domain.RiskCritical:
			return "high"
		case domain.RiskHigh:
			highest = domain.RiskHigh
		case domain.RiskMedium:
			if highest != domain.RiskHigh {
				highest = domain.RiskMedium
			}
		}
	}
	switch highest {
	case domain.RiskHigh:
		return "high"
	case domain.RiskMedium:
		return "medium"
	default:
		return "low"
	}
}

// securityNotes extracts security-related risk factors into a summary string.
func securityNotes(risks []domain.Risk) string {
	var notes []string
	for _, r := range risks {
		for _, f := range r.Factors {
			lower := strings.ToLower(f)
			if strings.Contains(lower, "security") || strings.Contains(lower, "secret") || strings.Contains(lower, "injection") || strings.Contains(lower, "crypto") {
				notes = append(notes, f)
			}
		}
	}
	if len(notes) == 0 {
		return "No security-specific risks identified."
	}
	return strings.Join(notes, "; ")
}

// architectureNotes summarizes the architecture rules from the context packet.
func architectureNotes(rules []domain.Policy) string {
	if len(rules) == 0 {
		return "No architecture rules apply."
	}
	parts := make([]string, 0, len(rules))
	for _, r := range rules {
		parts = append(parts, r.ID+": "+r.Description)
	}
	return fmt.Sprintf("%d rule(s) apply: %s", len(rules), strings.Join(parts, "; "))
}

// deploymentNotes derives deployment considerations from risk + affected services.
func deploymentNotes(pkt domain.ContextPacket) string {
	risk := riskLevelString(pkt.Risks)
	switch risk {
	case "high":
		return "High risk: deploy behind a feature flag with gradual rollout. Monitor for 24h."
	case "medium":
		return "Medium risk: deploy during low-traffic window. Monitor for 1h."
	default:
		return "Low risk: standard deployment."
	}
}

// nodeName extracts a display name from a domain.Node. It prefers the symbol's
// qualified name, then the file path, then the label, then the ID.
func nodeName(n domain.Node) string {
	if n.Symbol != nil && n.Symbol.Qualified != "" {
		return n.Symbol.Qualified
	}
	if n.Symbol != nil && n.Symbol.Name != "" {
		return n.Symbol.Name
	}
	if n.File != nil && n.File.Path != "" {
		return n.File.Path
	}
	if n.Label != "" {
		return n.Label
	}
	return n.ID
}

// maxImpactListItems caps how many entries of any impact section the text
// renderer prints. Counts stay exact; overflow collapses to a "+N more (use
// --json for full list)" line, mirroring the concise what-if style (P1-4).
// Full data is preserved in ImpactReport for --json / MCP / REST.
const maxImpactListItems = 20

// stdlibPkgs is the Go standard-library top-level package set used to collapse
// transitive stdlib fan-out in "What it calls" (P1-4: loadOrBuild fanned to
// 1274 entries incl. strings.*/os.*/fmt.*). Only these exact first-segment
// names collapse; internal packages (index, app, domain, ...) are never in
// this set so project calls are always shown in full.
var stdlibPkgs = map[string]bool{
	"archive": true, "bufio": true, "bytes": true, "cmp": true, "compress": true,
	"container": true, "context": true, "crypto": true, "database": true,
	"debug": true, "embed": true, "encoding": true, "errors": true, "expvar": true,
	"flag": true, "fmt": true, "go": true, "hash": true, "html": true,
	"image": true, "io": true, "iter": true, "log": true, "maps": true,
	"math": true, "mime": true, "net": true, "os": true, "path": true,
	"plugin": true, "reflect": true, "regexp": true, "runtime": true,
	"slices": true, "sort": true, "strconv": true, "strings": true, "sync": true,
	"syscall": true, "testing": true, "text": true, "time": true,
	"unicode": true, "unsafe": true,
	// Common stdlib-adjacent / runtime packages that pollute transitive
	// fan-out the same way (atomic, filepath, base64, ast, token, etc.).
	"atomic": true, "filepath": true, "base64": true, "ast": true, "token": true,
	"parser": true, "scanner": true, "strconv2": true, "hex": true, "json": true,
	"url": true, "http": true, "exec": true, "signal": true, "sitter": true,
}

// stdlibPkgOf reports the stdlib package prefix of a callee name ("strings"
// for "strings.Contains", "base64" for "base64.StdEncoding.AppendDecode").
// It returns false for project calls and bare names without a qualifier.
func stdlibPkgOf(name string) (string, bool) {
	i := strings.Index(name, ".")
	if i <= 0 {
		return "", false
	}
	pkg := name[:i]
	// Strip a receiver-style qualifier ("AuditLog.mu.Lock" -> not stdlib:
	// "AuditLog" is not in the set, so this correctly returns false).
	if stdlibPkgs[pkg] {
		return pkg, true
	}
	return "", false
}

// renderImpactList prints at most maxImpactListItems entries, then a "+N more"
// overflow line. Counts in the section header stay exact.
func renderImpactList(b *strings.Builder, items []string) {
	shown := items
	if len(items) > maxImpactListItems {
		shown = items[:maxImpactListItems]
	}
	for _, c := range shown {
		fmt.Fprintf(b, "  - %s\n", c)
	}
	if len(items) > len(shown) {
		fmt.Fprintf(b, "  ... +%d more (use --json for full list)\n", len(items)-len(shown))
	}
}

// renderWhatItCalls prints project calls first (already direct-first ordered
// by collectGraphImpact), collapsing stdlib fan-out into one summary line so
// hubs like loadOrBuild drop from 1274 lines to <25. Full lists stay in --json.
func renderWhatItCalls(b *strings.Builder, items []string) {
	var proj []string
	stdlibByPkg := map[string]int{}
	stdlibTotal := 0
	for _, c := range items {
		if pkg, ok := stdlibPkgOf(c); ok {
			stdlibByPkg[pkg]++
			stdlibTotal++
		} else {
			proj = append(proj, c)
		}
	}
	shown := proj
	hiddenProj := 0
	if len(proj) > maxImpactListItems {
		shown = proj[:maxImpactListItems]
		hiddenProj = len(proj) - len(shown)
	}
	for _, c := range shown {
		fmt.Fprintf(b, "  - %s\n", c)
	}
	if stdlibTotal > 0 {
		type kv struct {
			k string
			v int
		}
		var tops []kv
		for k, v := range stdlibByPkg {
			tops = append(tops, kv{k, v})
		}
		sort.Slice(tops, func(i, j int) bool {
			if tops[i].v != tops[j].v {
				return tops[i].v > tops[j].v
			}
			return tops[i].k < tops[j].k
		})
		n := 5
		if len(tops) < n {
			n = len(tops)
		}
		parts := make([]string, 0, n)
		for _, kv := range tops[:n] {
			parts = append(parts, fmt.Sprintf("%s (%d)", kv.k, kv.v))
		}
		fmt.Fprintf(b, "  - stdlib: %d calls collapsed (%s)\n", stdlibTotal, strings.Join(parts, ", "))
	}
	if hiddenProj > 0 {
		fmt.Fprintf(b, "  ... +%d more project calls (use --json for full list)\n", hiddenProj)
	}
}

// renderImpactText renders a domain.ImpactReport as the text output for kern
// impact (CLI), kern_impact (MCP), and POST /v1/impact (REST). The 11 spec
// questions are rendered as labelled sections.
func renderImpactText(r domain.ImpactReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "IMPACT for: %s\n", r.Target)
	fmt.Fprintf(&b, "Risk: %s\n", r.Risk)
	fmt.Fprintf(&b, "What calls this: %d\n", len(r.WhoCalls))
	renderImpactList(&b, r.WhoCalls)
	fmt.Fprintf(&b, "What it calls: %d\n", len(r.WhatItCalls))
	renderWhatItCalls(&b, r.WhatItCalls)
	fmt.Fprintf(&b, "Services that depend on it: %d\n", len(r.ServicesDepend))
	renderImpactList(&b, r.ServicesDepend)
	fmt.Fprintf(&b, "APIs affected: %d\n", len(r.APIsAffected))
	renderImpactList(&b, r.APIsAffected)
	fmt.Fprintf(&b, "Data stores affected: %d\n", len(r.DataStoresAffected))
	renderImpactList(&b, r.DataStoresAffected)
	fmt.Fprintf(&b, "Events affected: %d\n", len(r.EventsAffected))
	renderImpactList(&b, r.EventsAffected)
	fmt.Fprintf(&b, "Tests that cover it: %d\n", len(r.TestsCover))
	renderImpactList(&b, r.TestsCover)
	if len(r.IncidentsRelated) > 0 {
		fmt.Fprintf(&b, "Incidents related: %d\n", len(r.IncidentsRelated))
		renderImpactList(&b, r.IncidentsRelated)
	}
	if len(r.ArchitectureRules) > 0 {
		fmt.Fprintf(&b, "Architecture rules: %d\n", len(r.ArchitectureRules))
		renderImpactList(&b, r.ArchitectureRules)
	}
	// A change target that resolved to nothing (no callers, no callees, no
	// tests) is almost always an ambiguous or unindexed symbol, not a truly
	// isolated leaf. Warn instead of silently reporting "low risk" (F-2).
	if len(r.WhoCalls) == 0 && len(r.WhatItCalls) == 0 && len(r.TestsCover) == 0 {
		fmt.Fprintf(&b, "\nWARN: no callers, callees, or tests resolved for %q — the change target may be ambiguous or not indexed.\n", r.Target)
		fmt.Fprintf(&b, "      Qualify the symbol (e.g. Server.dispatch) or run kern_search to confirm the exact name, then re-run.\n")
	}
	if r.Evidence != "" {
		fmt.Fprintf(&b, "\n%s\n", r.Evidence)
	}
	return b.String()
}

// renderCorrelationText renders a runtime correlation chain as text.
func renderCorrelationText(chain runtime.CorrelationChain) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CORRELATION for alert: %s\n", chain.Alert.ID)
	if chain.Service != "" {
		fmt.Fprintf(&b, "Affected service: %s\n", chain.Service)
	}
	fmt.Fprintf(&b, "Evidence chain (%d links):\n", len(chain.Links))
	for _, link := range chain.Links {
		fmt.Fprintf(&b, "  %s → %s\n", link.Stage, link.ID)
	}
	return b.String()
}

// renderIncidentText renders an incident's current state as text.
func renderIncidentText(inc *domain.Incident) string {
	var b strings.Builder
	fmt.Fprintf(&b, "INCIDENT: %s\n", inc.ID)
	fmt.Fprintf(&b, "Service: %s\n", inc.AffectedService)
	fmt.Fprintf(&b, "Status: %s\n", inc.Status)
	fmt.Fprintf(&b, "Severity: %s\n", inc.Severity)
	fmt.Fprintf(&b, "Hypotheses: %d\n", len(inc.Hypotheses))
	for _, h := range inc.Hypotheses {
		fmt.Fprintf(&b, "  [%s] %s (confidence: %s, score: %.2f)\n", h.Source, h.Statement, h.Confidence, h.Score)
	}
	if inc.RootCause != nil {
		fmt.Fprintf(&b, "Root cause: %s\n", inc.RootCause.Summary)
	} else {
		b.WriteString("Root cause: none identified\n")
	}
	return b.String()
}

// renderLearningText renders learning patterns as text.
func renderLearningText(patterns []learning.Pattern, surfaced []learning.Pattern, threshold int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "LEARNING\n")
	fmt.Fprintf(&b, "Total patterns: %d\n", len(patterns))
	fmt.Fprintf(&b, "Surfaced (threshold=%d): %d\n", threshold, len(surfaced))
	if len(surfaced) > 0 {
		fmt.Fprintf(&b, "\nSurfaced patterns:\n")
		for _, p := range surfaced {
			fmt.Fprintf(&b, "  [count=%d] %s\n", p.Count, p.Key)
			if p.Recommendation != "" {
				fmt.Fprintf(&b, "    recommendation: %s\n", p.Recommendation)
			}
		}
	}
	return b.String()
}

// renderModernizationText renders a modernization extraction plan as text.
func renderModernizationText(plan modernization.ExtractionPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "MODERNIZATION PLAN\n")
	fmt.Fprintf(&b, "Bounded contexts: %d\n", len(plan.Contexts))
	fmt.Fprintf(&b, "Bridges: %d\n", len(plan.Bridges))
	fmt.Fprintf(&b, "Extraction phases: %d\n", len(plan.Phases))
	if plan.Summary != "" {
		fmt.Fprintf(&b, "Summary: %s\n", plan.Summary)
	}
	for _, phase := range plan.Phases {
		fmt.Fprintf(&b, "\nPhase %d: %s (risk: %s)\n", phase.Phase, phase.Context, phase.RiskLevel)
		if len(phase.Bridges) > 0 {
			fmt.Fprintf(&b, "  Bridges: %d\n", len(phase.Bridges))
		}
	}
	return b.String()
}

// renderModernizePhaseText renders a single extraction phase as a short,
// auditable summary used as the phase task's output .
func renderModernizePhaseText(phase modernization.ExtractionPhase) string {
	var b strings.Builder
	fmt.Fprintf(&b, "MODERNIZE PHASE %d\n", phase.Phase)
	fmt.Fprintf(&b, "Context: %s\n", phase.Context)
	if phase.Ownership != "" {
		fmt.Fprintf(&b, "Ownership: %s\n", phase.Ownership)
	}
	fmt.Fprintf(&b, "Risk: %s | Blast radius: %d symbols\n", phase.RiskLevel, phase.BlastRadius)
	if phase.TaskID != "" {
		fmt.Fprintf(&b, "Task: %s\n", phase.TaskID)
	}
	return b.String()
}
