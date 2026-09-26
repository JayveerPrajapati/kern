// Package app hosts the TaskService orchestration layer.
// Generated split of task.go by domain (see task.go for the core).
package app

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/calibrate"
	"github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lenses"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// Analyze creates a Task for the intent, runs the context engine, and attaches
// the ContextPacket to the Task. The Task transitions CREATED → ANALYZING →
// (COMPLETED or FAILED). Returns the Task and the rendered analysis text.
// This is the Task-tracked version of Platform.Analyze. Read-only analysis
// commands do NOT persist a task record by default (F9): the task is tracked
// in memory (states, output, artifacts, audit transitions) but the store is
// only written when the caller opts in with WithTaskPersistence(true).
// Interfaces that want stateless analysis call Platform.Analyze directly.
func (s *TaskService) Analyze(intent string) (*agent.Task, string, error) {
	t, err := s.createAnalysisTask(intent)
	if err != nil {
		return nil, "", err
	}
	return s.analyzeTask(t, intent)
}

// analyzeTask drives a Task through the ANALYZING state, runs the context
// engine, attaches the packet, and completes the Task. It is the shared
// implementation for Analyze and any future staged workflow.
func (s *TaskService) analyzeTask(t *agent.Task, change string) (*agent.Task, string, error) {
	return s.analyzeTaskOpts(t, change, true)
}

// analyzeTaskOpts is like analyzeTask but with a complete flag. When complete
// is false, the task is left in ANALYZING state (not completed) so a caller
// like Plan can continue the lifecycle (ANALYZING → PLANNING).
func (s *TaskService) analyzeTaskOpts(t *agent.Task, change string, complete bool) (*agent.Task, string, error) {
	if err := s.transition(t, domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	pkt, text, err := s.platform.Analyze(change)
	if err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}

	// Attach lifecycle results to the Task.
	t.ContextPacket = &pkt
	t.Risks = pkt.Risks
	// Attach the context engine's evidence-backed claims
	// (FACT/INFERENCE/HYPOTHESIS/RECOMMENDATION) to the Task so the analysis is
	// persisted with its evidence trail: evidence claims must be part of the
	// auditable task record, not only the rendered text.
	t.Evidence = append(t.Evidence, pkt.Facts...)
	t.Output = text
	t.AddStep(agent.Step{
		Action:     "analyze",
		AgentID:    "context-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("context packet: %d symbols, %d risks", len(pkt.Symbols), len(pkt.Risks)),
		Status:     "success",
	})

	// Emit risk.calculated so the bus carries each identified risk to
	// webhooks/audit ( event standardization).
	for _, r := range pkt.Risks {
		s.publish(eventbus.RiskCalculated, t.ID, map[string]string{
			"level":      string(r.Level),
			"mitigation": r.Mitigation,
		})
	}

	// Record the ContextPacket as the root artifact of the chain.
	s.recordArtifact(domain.ArtifactContextPacket, t.ID, "context-engine",
		"context packet: "+change, "", "context:analyze")
	// Record the AnalysisReport artifact (P10.4) linked as a child of the
	// context packet so the analysis is a typed, traceable artifact in the chain.
	s.recordArtifact(domain.ArtifactAnalysisReport, t.ID, "context-engine",
		"analysis: "+change,
		s.lastArtifactID(t.ID, domain.ArtifactContextPacket), "context:analyze")

	if complete {
		// Context-usage learning (Self-Improvement #5): the analyze outcome
		// is known here, and the task retains the packet built above. Record
		// which packet slices the outcome actually used (the resolved target
		// symbol and its defining file) so the learning pass can propose
		// shrink/review signals. Best-effort and nil-guarded — it never
		// blocks or changes the task flow.
		s.recordContextUsage(t, change)
		if err := t.Complete(text); err != nil {
			s.fail(t, err.Error())
			return t, "", err
		}
		s.persist(t)
		s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	}
	return t, text, nil
}

// AnalyzeWithLens is Analyze with a review lens: the context packet's facts
// are re-ranked by the lens priorities before rendering and task attachment.
func (s *TaskService) AnalyzeWithLens(intent, lensName string) (*agent.Task, string, error) {
	t, err := s.createAnalysisTask(intent)
	if err != nil {
		return nil, "", err
	}
	return s.analyzeTaskWithLens(t, intent, lensName)
}

// analyzeTaskWithLens drives a Task through the ANALYZING state, runs the
// context engine, re-ranks the packet facts by the review lens priorities,
// re-renders the packet, and completes the Task. It mirrors analyzeTaskOpts;
// the only difference is the lens-ordered facts.
func (s *TaskService) analyzeTaskWithLens(t *agent.Task, change, lensName string) (*agent.Task, string, error) {
	l, err := lenses.Resolve(lensName)
	if err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}
	if err := s.transition(t, domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	pkt, _, err := s.platform.Analyze(change)
	if err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}

	// Re-rank the packet facts by the review lens before rendering and
	// attachment: the lens makes the evidence a reviewer cares about surface
	// first in the analysis output.
	pkt.Facts = lenses.ApplyLens(l, pkt.Facts)
	text := context.RenderText(pkt)

	// Attach lifecycle results to the Task (mirrors analyzeTaskOpts).
	t.ContextPacket = &pkt
	t.Risks = pkt.Risks
	// Attach the context engine's evidence-backed claims to the Task so the
	// analysis is persisted with its evidence trail (lens-ordered).
	t.Evidence = append(t.Evidence, pkt.Facts...)
	t.Output = text
	t.AddStep(agent.Step{
		Action:     "analyze",
		AgentID:    "context-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("context packet: %d symbols, %d risks (lens %s)", len(pkt.Symbols), len(pkt.Risks), lensName),
		Status:     "success",
	})

	// Emit risk.calculated so the bus carries each identified risk to
	// webhooks/audit ( event standardization).
	for _, r := range pkt.Risks {
		s.publish(eventbus.RiskCalculated, t.ID, map[string]string{
			"level":      string(r.Level),
			"mitigation": r.Mitigation,
		})
	}

	// Record the ContextPacket as the root artifact of the chain.
	s.recordArtifact(domain.ArtifactContextPacket, t.ID, "context-engine",
		"context packet: "+change, "", "context:analyze")
	// Record the AnalysisReport artifact (P10.4) linked as a child of the
	// context packet so the analysis is a typed, traceable artifact in the chain.
	s.recordArtifact(domain.ArtifactAnalysisReport, t.ID, "context-engine",
		"analysis: "+change,
		s.lastArtifactID(t.ID, domain.ArtifactContextPacket), "context:analyze")

	if err := t.Complete(text); err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, text, nil
}

// WhatIf creates a Task, simulates the change, attaches the Impact to the Task,
// and completes it. Returns the Task and the rendered impact text. Read-only
// analysis commands do NOT persist a task record by default (F9); opt in with
// WithTaskPersistence(true) for an authoritative record.
func (s *TaskService) WhatIf(kind whatif.ChangeKind, change, newTarget string) (*agent.Task, string, error) {
	t, err := s.createAnalysisTask(fmt.Sprintf("what-if: %s %s", kind, change))
	if err != nil {
		return nil, "", err
	}
	if err := s.transition(t, domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	imp, text, err := s.platform.WhatIf(kind, change, newTarget)
	if err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}
	// Calibration (Feature Batch C): record the simulation as an impact-kind
	// prediction so it can later be matched against observed incidents.
	// Best-effort — recording never fails or changes the what-if path.
	s.recordWhatIfPrediction(change, imp)

	t.ImpactReport = &imp
	t.Output = text
	t.AddStep(agent.Step{
		Action:     "what-if",
		AgentID:    "whatif-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("affected: %d, risk: %s", len(imp.Affected), imp.Risk),
		Status:     "success",
	})

	// Emit code.changed when the what-if shows affected files, so the bus
	// carries the change blast radius to webhooks/audit .
	if len(imp.Files) > 0 {
		s.publish(eventbus.CodeChanged, t.ID, map[string]string{
			"affected": fmt.Sprintf("%d", len(imp.Affected)),
			"files":    fmt.Sprintf("%d", len(imp.Files)),
			"risk":     imp.Risk,
		})
	}

	// Record the ImpactReport artifact, linked to the task's context-packet
	// artifact as its parent (when one exists) to form the audit chain.
	s.recordArtifact(domain.ArtifactImpactReport, t.ID, "whatif-engine",
		fmt.Sprintf("impact: %d affected, risk=%s", len(imp.Affected), imp.Risk),
		s.lastArtifactID(t.ID, domain.ArtifactContextPacket), "whatif:simulate")
	// Record the RiskReport artifact (P10.4) so the risk assessment is a typed,
	// traceable artifact in the chain alongside the impact report.
	s.recordArtifact(domain.ArtifactRiskReport, t.ID, "whatif-engine",
		fmt.Sprintf("risk=%s", imp.Risk),
		s.lastArtifactID(t.ID, domain.ArtifactImpactReport), "whatif:risk")

	// Exit gate: what-if is Evidence aware. Attach the simulation's typed
	// claims (FACT/INFERENCE/HYPOTHESIS/RECOMMENDATION with provenance) to the
	// task so the impact estimate is persisted with its evidence trail, not
	// only as rendered text and artifacts.
	if len(imp.Claims) > 0 {
		t.Evidence = append(t.Evidence, imp.Claims...)
	}

	if err := t.Complete(text); err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, text, nil
}

// Plan creates a Task for the intent, runs the full control-plane Plan
// workflow (analyze → memory → impact → risk → architecture), and assembles a
// structured domain.Plan from the deterministic results. The Task transitions
// CREATED → ANALYZING → PLANNING → (COMPLETED or FAILED). Returns the Task,
// the Plan, and a rendered text summary.
// This realizes the : the Plan
// artifact is populated from deterministic sources (context packet, impact
// report, risk assessment, architecture rules) — the LLM may explain it, but
// the fields are not LLM guesses. Read-only analysis commands do NOT persist
// a task record by default (F9); opt in with WithTaskPersistence(true) for an
// authoritative record.
func (s *TaskService) Plan(intent string) (*agent.Task, domain.Plan, string, error) {
	t, err := s.createAnalysisTask(intent)
	if err != nil {
		return nil, domain.Plan{}, "", err
	}

	// Stage 1: analyze (CREATED → ANALYZING), without completing the task.
	t, _, err = s.analyzeTaskOpts(t, intent, false)
	if err != nil {
		return t, domain.Plan{}, "", err
	}

	// Stage 2: plan (ANALYZING → PLANNING).
	if err := s.transition(t, domain.TaskPlanning); err != nil {
		s.fail(t, err.Error())
		return t, domain.Plan{}, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "PLANNING"})

	// Stage 3: assemble the Plan from deterministic sources.
	var pkt domain.ContextPacket
	if t.ContextPacket != nil {
		pkt = *t.ContextPacket
	}
	plan := s.assemblePlan(intent, pkt)

	// Exit gate: mandatory constitution rules can block a plan before
	// execution. Validate the assembled plan against .kern/constitution.yaml;
	// a MUST/MUST_NOT violation blocks the plan (the task fails, so the plan
	// cannot proceed to execution) while SHOULD/SHOULD_NOT are non-blocking
	// warnings recorded on the task output. Missing constitution = no rules =
	// plan passes (backward compatible).
	if constitution, err := governance.LoadConstitution(s.platform.Root()); err != nil {
		s.fail(t, "constitution: "+err.Error())
		return t, domain.Plan{}, "", err
	} else if validation := governance.ValidatePlan(plan, constitution); !validation.Passed {
		msg := "plan blocked by constitution: " + validation.Violations[0].Message
		s.fail(t, msg)
		return t, domain.Plan{}, "", fmt.Errorf("%s", msg)
	}

	t.Plan = &plan
	t.Output = renderPlanText(plan)
	t.AddStep(agent.Step{
		Action:     "plan",
		AgentID:    "plan-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("plan: %d steps, risk=%s, %d affected components", len(plan.ImplementationSteps), plan.Risk, len(plan.AffectedComponents)),
		Status:     "success",
	})

	// Record the Plan artifact, linked to the context-packet artifact as its
	// parent to continue the audit chain.
	s.recordArtifact(domain.ArtifactPlan, t.ID, "plan-engine",
		fmt.Sprintf("plan: %s — %d steps, risk=%s", plan.Objective, len(plan.ImplementationSteps), plan.Risk),
		s.lastArtifactID(t.ID, domain.ArtifactContextPacket), "plan:assemble")

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, plan, "", err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, plan, t.Output, nil
}

// ImpactOption customizes an Impact computation.
type ImpactOption func(*impactOptions)

type impactOptions struct {
	strict bool
}

// ImpactStrict opts an Impact computation into strict precision mode: call
// edges whose caller language is not "resolved"-precision in the index are
// skipped as unknown rather than trusted (see kern impact --precision strict).
func ImpactStrict() ImpactOption {
	return func(o *impactOptions) { o.strict = true }
}

// Impact creates a Task for the change, runs the 11 deterministic graph
// queries from the spec, and attaches the ImpactReport to the Task.
// The Task transitions CREATED → ANALYZING → (COMPLETED or FAILED). Returns
// the Task, the ImpactReport, and a rendered text summary.
// This realizes the impact analysis contract: the impact
// report is the deterministic source — the LLM may explain it, but the data
// comes from the knowledge graph, not an LLM guess. Read-only analysis
// commands do NOT persist a task record by default (F9); opt in with
// WithTaskPersistence(true) for an authoritative record.
func (s *TaskService) Impact(change string, opts ...ImpactOption) (*agent.Task, domain.ImpactReport, string, error) {
	o := &impactOptions{}
	for _, opt := range opts {
		opt(o)
	}
	t, err := s.createAnalysisTask(change)
	if err != nil {
		return nil, domain.ImpactReport{}, "", err
	}
	if err := s.transition(t, domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, domain.ImpactReport{}, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	target, fuzzy, err := s.platform.resolveSymbol(change)
	if err != nil {
		s.fail(t, err.Error())
		return t, domain.ImpactReport{}, "", err
	}

	g := s.platform.Graph()
	rep := s.collectGraphImpact(g, target, o.strict)
	// Entity overlay (Feature 3): surface the twin entity nodes implicated by
	// the change's blast radius (the target plus the report's affected
	// symbols). Zero-cost when the twin graph carries no entities for them.
	rep.Entities = attachImpactEntities(g, impactSymbols(rep))
	s.gatherRuntimeEvidence(&rep, target)
	classifyCriticality(g, target, o.strict, &rep)
	t, rep, text, err := s.finalizeImpact(t, &rep)
	if fuzzy && text != "" {
		// The requested symbol was fuzzy-resolved to a different one: surface
		// the mapping so the substitution is never silent.
		text = fmt.Sprintf("resolved %q -> %s (fuzzy match)\n%s", change, target, text)
	}
	// Calibration (Feature Batch C): record the impact analysis as an
	// impact-kind prediction (predicted files = the file set the impact path
	// computed). Best-effort — recording never fails or changes the impact path.
	s.recordImpactPrediction(change, &rep, s.impactCitedFiles(t, &rep))
	return t, rep, text, err
}

// collectGraphImpact runs the six deterministic graph queries from the spec
// (callers, callees, services, APIs, events, tests) against the target symbol
// and returns the ImpactReport fields they populate.
func (s *TaskService) collectGraphImpact(g *intel.Graph, target string, strict bool) domain.ImpactReport {
	rep := domain.ImpactReport{Target: target}
	// 1. What calls this?
	for _, n := range g.WhoCallsPrecise(target, strict) {
		rep.WhoCalls = append(rep.WhoCalls, nodeName(n))
	}
	// 2. What does it call? Names, not nodes, so callees that do not resolve
	// to indexed nodes (e.g. "fmt.Println") survive instead of emptying the
	// section - methods that only call external code reported "What it
	// calls: 0" while `kern why` showed the edges (e2e round 2, P0-1).
	// Order direct (1-hop) callees first, then transitive-only ones.
	// The full transitive set is preserved for --json; the text renderer
	// truncates with "+N more" and collapses stdlib. Direct-first keeps the
	// actionable entries visible after truncation on hubs like loadOrBuild
	// (1 direct + 1273 transitive).
	{
		all := g.WhatDoesXDependOnNames(target, strict)
		direct := g.DirectDependsOnNames(target, strict)
		isDirect := make(map[string]bool, len(direct))
		for _, d := range direct {
			isDirect[d] = true
		}
		seen := make(map[string]bool, len(all))
		for _, d := range direct {
			if !seen[d] {
				seen[d] = true
				rep.WhatItCalls = append(rep.WhatItCalls, d)
			}
		}
		for _, c := range all {
			if !isDirect[c] && !seen[c] {
				seen[c] = true
				rep.WhatItCalls = append(rep.WhatItCalls, c)
			}
		}
	}
	// 3. What services depend on it?
	for _, n := range g.WhatServicesAffectedPrecise(target, strict) {
		rep.ServicesDepend = append(rep.ServicesDepend, nodeName(n))
	}
	// 4. Which APIs are affected?
	for _, n := range g.WhatAPIsAffectedPrecise(target, strict) {
		rep.APIsAffected = append(rep.APIsAffected, nodeName(n))
	}
	// 5. Which events are affected?
	for _, n := range g.WhatEventsAffectedPrecise(target, strict) {
		rep.EventsAffected = append(rep.EventsAffected, nodeName(n))
	}
	// 6. Which tests cover it?
	seenTests := make(map[string]bool)
	for _, n := range g.WhatTestsCoverPrecise(target, strict) {
		name := nodeName(n)
		if name != "" && !seenTests[name] {
			seenTests[name] = true
			rep.TestsCover = append(rep.TestsCover, name)
		}
	}
	return rep
}

// gatherRuntimeEvidence folds non-graph evidence into the report: affected data
// stores from the context packet's runtime evidence, related incidents from
// memory recall, and applicable architecture rules from the context packet.
func (s *TaskService) gatherRuntimeEvidence(rep *domain.ImpactReport, target string) {
	// Which data stores are affected? (from the context packet's databases)
	pkt, _ := s.platform.ctx.AnalyzeChange(target)
	for _, e := range pkt.RuntimeEvidence {
		if strings.Contains(strings.ToLower(string(e.Type)), "database") || strings.Contains(strings.ToLower(string(e.Type)), "db") {
			rep.DataStoresAffected = append(rep.DataStoresAffected, e.Content)
		}
	}
	// Which incidents are related? (from memory recall)
	if s.platform.Memory() != nil {
		ms, _ := s.platform.Memory().Recall(memory.Query{Text: target, Type: domain.MemoryIncident})
		for _, m := range ms {
			rep.IncidentsRelated = append(rep.IncidentsRelated, m.ID)
		}
	}
	// Which architecture rules apply?
	for _, p := range pkt.ArchitectureRules {
		name := p.ID
		if name == "" {
			name = p.Name
		}
		if name == "" {
			name = p.Description
		}
		if name != "" {
			rep.ArchitectureRules = append(rep.ArchitectureRules, name)
		}
	}
}

// classifyCriticality derives the report's risk level from the production
// criticality of the target symbol, falling back to the caller/service
// footprint when the graph reports no criticality tier.
func classifyCriticality(g *intel.Graph, target string, strict bool, rep *domain.ImpactReport) {
	// Risk from production criticality.
	crit := g.ProductionCriticalityPrecise(target, strict)
	switch crit {
	case "critical":
		rep.Risk = "high"
	case "high":
		rep.Risk = "high"
	case "medium":
		rep.Risk = "medium"
	default:
		if len(rep.ServicesDepend) > 0 {
			rep.Risk = "high"
		} else if len(rep.WhoCalls) > 0 {
			rep.Risk = "medium"
		} else {
			rep.Risk = "low"
		}
	}
}

// impactSymbols returns the code-symbol footprint of an ImpactReport: the
// target plus every symbol named in the report's affected sections. Used to
// surface the twin entities implicated by the change's blast radius. Test
// names are excluded — they describe coverage, not affected code.
func impactSymbols(rep domain.ImpactReport) []string {
	out := make([]string, 0, 1+len(rep.WhoCalls)+len(rep.WhatItCalls)+len(rep.ServicesDepend)+len(rep.APIsAffected)+len(rep.EventsAffected))
	out = append(out, rep.Target)
	out = append(out, rep.WhoCalls...)
	out = append(out, rep.WhatItCalls...)
	out = append(out, rep.ServicesDepend...)
	out = append(out, rep.APIsAffected...)
	out = append(out, rep.EventsAffected...)
	return out
}

// impactCitedFiles returns the files the impact report cites: the target's
// defining file, the defining file of every symbol named in the report, and
// the context packet's files. StalenessBanner spot-checks exactly these so
// the impact answer can warn when it cites drifted line numbers.
func (s *TaskService) impactCitedFiles(t *agent.Task, rep *domain.ImpactReport) []string {
	var files []string
	add := func(ref string) {
		if f := s.platform.graphNodeFile(ref); f != "" {
			files = append(files, f)
		}
	}
	add(rep.Target)
	for _, groups := range [][]string{rep.WhoCalls, rep.WhatItCalls, rep.ServicesDepend, rep.APIsAffected, rep.EventsAffected, rep.TestsCover} {
		for _, ref := range groups {
			add(ref)
		}
	}
	if t.ContextPacket != nil {
		for _, f := range t.ContextPacket.Files {
			if f.Path != "" {
				files = append(files, f.Path)
			}
		}
	}
	return files
}

// finalizeImpact stamps the completed ImpactReport onto the Task, records the
// report artifact, and completes the Task lifecycle (ANALYZING → COMPLETED).
func (s *TaskService) finalizeImpact(t *agent.Task, rep *domain.ImpactReport) (*agent.Task, domain.ImpactReport, string, error) {
	t.Impact = rep
	rep.Evidence = intel.AnchorLine(s.platform.Index(), rep.Target)
	out := renderImpactText(*rep)
	if banner := s.platform.Index().StalenessBanner(s.impactCitedFiles(t, rep)); banner != "" {
		out = banner + "\n\n" + out
	}
	// Calibration (Feature Batch C): append the per-subsystem confidence line
	// at the end of the rendered report (best-effort; omitted on any error).
	// This is the shared render path for CLI kern impact and MCP kern_impact.
	if f := s.platform.graphNodeFile(rep.Target); f != "" {
		if line := calibrate.ConfidenceLine(s.platform.Root(), calibrate.SubsystemOf(f)); line != "" {
			out = out + "\n" + line
		}
	}
	t.Output = out
	t.AddStep(agent.Step{
		Action:     "impact",
		AgentID:    "graph-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("impact: %d callers, %d services, risk=%s", len(rep.WhoCalls), len(rep.ServicesDepend), rep.Risk),
		Status:     "success",
	})
	s.recordArtifact(domain.ArtifactImpactReport, t.ID, "graph-engine",
		fmt.Sprintf("impact: %d callers, risk=%s", len(rep.WhoCalls), rep.Risk),
		s.lastArtifactID(t.ID, domain.ArtifactContextPacket), "impact:graph")

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, *rep, "", err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, *rep, t.Output, nil
}

// assemblePlan builds a domain.Plan from the deterministic context packet. It
// derives each Plan field from a deterministic source: objective/scope from
// the intent, affected components from the packet's symbols+files, risk from
// the packet's risk assessment, tests from required validation, architecture
// from the packet's architecture rules, and evidence from the packet's facts.
func (s *TaskService) assemblePlan(intent string, pkt domain.ContextPacket) domain.Plan {
	plan := domain.Plan{
		Objective: intent,
		Scope:     scopeFromPacket(pkt),
		Risk:      riskLevelString(pkt.Risks),
	}

	// Affected components: symbols + files from the context packet.
	// When the intent describes a net-new feature (e.g. "Add...", "Create..."),
	// do not report unrelated symbols or files as affected components.
	if whatif.IsNetNewFeature(intent) {
		plan.Scope = "net-new feature (no existing components affected)"
	} else {
		for _, sym := range pkt.Symbols {
			plan.AffectedComponents = append(plan.AffectedComponents, sym.Name)
		}
		for _, f := range pkt.Files {
			plan.AffectedComponents = append(plan.AffectedComponents, f.Path)
		}
	}

	// Implementation steps: deterministic scaffolding from the required
	// validation list (build, test, security, architecture), concrete
	// per-symbol steps for existing-symbol changes, and explicit change kinds
	// detected from the intent text (rename/remove). No LLM anywhere.
	if whatif.IsNetNewFeature(intent) {
		plan.ImplementationSteps = append(plan.ImplementationSteps, "Implement the new feature according to specifications.")
	} else {
		// Explicit change kinds first (prepended): a rename or removal is the
		// headline of the change, so its steps lead. The regexes run on the
		// ORIGINAL intent (case-insensitively) so captured symbol names keep
		// the user's casing.
		if m := intentRenameRe.FindStringSubmatch(intent); m != nil {
			plan.ImplementationSteps = append(plan.ImplementationSteps,
				fmt.Sprintf("Rename %s to %s (definition in the affected components above)", m[1], m[2]),
				"Update all references to "+m[1])
		}
		if m := intentRemoveRe.FindStringSubmatch(intent); m != nil {
			plan.ImplementationSteps = append(plan.ImplementationSteps, "Remove "+m[1]+" and update its callers")
		}
		// Concrete per-symbol steps (capped at 5; test-file symbols are
		// exercised by the test step below, not updated as source).
		for i, sym := range pkt.Symbols {
			if i >= 5 {
				break
			}
			if strings.Contains(sym.File, "_test") {
				continue
			}
			plan.ImplementationSteps = append(plan.ImplementationSteps,
				fmt.Sprintf("Update %s (%s:%d)", sym.Name, sym.File, sym.Line))
		}
		// Last resort: no symbols and no explicit change kind matched.
		if len(plan.ImplementationSteps) == 0 {
			plan.ImplementationSteps = append(plan.ImplementationSteps, "Implement the requested change.")
		}
	}
	for _, v := range pkt.RequiredValidation {
		switch v {
		case "build":
			plan.ImplementationSteps = append(plan.ImplementationSteps, "Ensure the project builds (go build ./...).")
		case "test":
			plan.ImplementationSteps = append(plan.ImplementationSteps, "Add/update tests for affected symbols and run go test.")
		case "security":
			plan.ImplementationSteps = append(plan.ImplementationSteps, "Run security scan (kern security) and address findings.")
		case "architecture":
			plan.ImplementationSteps = append(plan.ImplementationSteps, "Validate architecture boundaries (kern guard).")
		}
	}

	// Dependencies: keep only edges that touch the affected components (the
	// symbol names and file paths above); unrelated package edges are noise.
	filter := planFilter(pkt)
	for _, e := range pkt.Dependencies {
		// Trim any "pkg."-style qualifier on the right side before matching,
		// so an edge "pkg.A → pkg.B" touches the affected symbol "B".
		to := e.To
		if i := strings.LastIndexByte(to, '.'); i >= 0 {
			to = to[i+1:]
		}
		if edgeTouches(filter, e.From) || edgeTouches(filter, to) {
			plan.Dependencies = append(plan.Dependencies, e.From+" → "+e.To)
		}
	}

	// Tests: required validation + covering tests from the packet.
	plan.Tests = append(plan.Tests, pkt.RequiredValidation...)

	// Rollback: deterministic from risk level.
	switch plan.Risk {
	case "high":
		plan.Rollback = "High risk: revert the commit and redeploy the previous version. Verify via kern verify."
	case "medium":
		plan.Rollback = "Medium risk: revert the commit. Run the affected test suite."
	default:
		plan.Rollback = "Low risk: revert the commit."
	}

	// Security: surface if any risk factor mentions security.
	plan.Security = securityNotes(pkt.Risks)

	// Architecture: summarize architecture rules from the packet.
	plan.Architecture = architectureNotes(pkt.ArchitectureRules)

	// Deployment: deterministic from risk + affected services.
	plan.Deployment = deploymentNotes(pkt)

	// Evidence: claim statements from the packet's facts.
	for _, c := range pkt.Facts {
		plan.Evidence = append(plan.Evidence, c.Statement)
	}

	return plan
}

// intentRenameRe detects an explicit rename intent ("rename X to Y") so the
// plan can name the concrete change kind instead of a generic step. It runs
// on the original intent, case-insensitively, preserving the user's casing in
// the captured names.
var intentRenameRe = regexp.MustCompile(`(?i)rename\s+([\w.]+)\s+to\s+([\w.]+)`)

// intentRemoveRe detects an explicit removal intent ("remove X"/"delete X").
var intentRemoveRe = regexp.MustCompile(`(?i)(?:remove|delete)\s+([\w.]+)`)

// planFilter builds the set of names that mark a dependency edge as relevant
// to the plan: the affected symbols' names/qualified names and the affected
// files' paths.
func planFilter(pkt domain.ContextPacket) map[string]bool {
	set := map[string]bool{}
	for _, sym := range pkt.Symbols {
		if sym.Name != "" {
			set[sym.Name] = true
		}
		if sym.Qualified != "" {
			set[sym.Qualified] = true
		}
	}
	for _, f := range pkt.Files {
		if f.Path != "" {
			set[f.Path] = true
		}
	}
	return set
}

// edgeTouches reports whether a dependency edge endpoint touches the
// affected-component filter: exact equality or a component-bounded suffix
// match on either side (a "pkg.Sym" node ID touches the affected symbol
// "Sym"; a bare "task.go" touches the affected file "internal/app/task.go").
// The boundary check keeps short endings ("go") from suffix-matching every
// *.go file in the filter set.
func edgeTouches(set map[string]bool, end string) bool {
	if end == "" {
		return false
	}
	if set[end] {
		return true
	}
	for k := range set {
		if k == "" {
			continue
		}
		if strings.HasSuffix(end, k) && suffixBounded(end, k) {
			return true
		}
		// Reverse direction only for name-like endings (containing '.' or
		// '/'), so a bare "go" cannot touch "internal/app/task.go".
		if strings.ContainsAny(end, "./") && strings.HasSuffix(k, end) && suffixBounded(k, end) {
			return true
		}
	}
	return false
}

// suffixBounded reports whether suffix starts at a component boundary ('.' or
// '/') or at the very start of s.
func suffixBounded(s, suffix string) bool {
	i := len(s) - len(suffix)
	if i == 0 {
		return true
	}
	c := s[i-1]
	return c == '.' || c == '/'
}

// Verify creates a Task, runs verification, attaches the result, and completes
// the Task. Returns the Task and the verification result.
func (s *TaskService) Verify(types []string) (*agent.Task, verification.VerificationResult, error) {
	t, err := s.Create("verify")
	if err != nil {
		return nil, verification.VerificationResult{}, err
	}
	if err := s.transition(t, domain.TaskVerifying); err != nil {
		s.fail(t, err.Error())
		return t, verification.VerificationResult{}, err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "VERIFYING"})

	res := s.platform.Verify(types)
	t.Verification = &res
	// Cost/latency policy learning (Tier 2 #6): record this verify outcome —
	// task kind, configured model, PASS/FAIL — best-effort; learning proposes
	// via memory only and never blocks verify (same style as
	// recordCalibrationClaims).
	s.recordModelOutcome(t, res.Verdict == verification.VerdictPass || res.Verdict == verification.VerdictPassWithWarning)
	t.Output = fmt.Sprintf("verdict: %s", res.Verdict)
	t.AddStep(agent.Step{
		Action:     "verify",
		AgentID:    "verification-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("verdict: %s, summary: %s", res.Verdict, res.Summary),
		Status:     "success",
	})

	// Record the VerificationReport artifact, linked to the task's impact
	// artifact (when one exists) to continue the audit chain.
	s.recordArtifact(domain.ArtifactVerificationReport, t.ID, "verification-engine",
		fmt.Sprintf("verdict: %s, summary: %s", res.Verdict, res.Summary),
		s.lastArtifactID(t.ID, domain.ArtifactImpactReport), "verification:verify")

	// Also emit the typed sub-report artifacts (test, security,
	// architecture) so the safe-change slice's required artifact set
	// (ContextPacket, AnalysisReport, ImpactReport, RiskReport, Plan,
	// CodePatch, TestReport, SecurityReport, ArchitectureReport,
	// VerificationReport, Diff, PullRequest, Audit) is fully covered by the
	// lifecycle, not just the verification kinds.
	if res.UnitTests != nil {
		ok := res.UnitTests.OK
		s.recordArtifact(domain.ArtifactTestReport, t.ID, "verification-engine",
			fmt.Sprintf("tests: passed=%d failed=%d skipped=%d ok=%v", res.UnitTests.Passed, res.UnitTests.Failed, res.UnitTests.Skipped, ok),
			s.lastArtifactID(t.ID, domain.ArtifactVerificationReport), "verification:test")
	}
	if res.Security != nil {
		s.recordArtifact(domain.ArtifactSecurityReport, t.ID, "verification-engine",
			fmt.Sprintf("security: findings=%d critical=%d high=%d low=%d ok=%v", res.Security.Count, res.Security.Critical, res.Security.High, res.Security.Low, res.Security.OK),
			s.lastArtifactID(t.ID, domain.ArtifactTestReport), "verification:security")
	}
	if res.Architecture != nil {
		s.recordArtifact(domain.ArtifactArchitectureReport, t.ID, "verification-engine",
			fmt.Sprintf("architecture: violations=%d ok=%v", len(res.Architecture.Violations), res.Architecture.OK),
			s.lastArtifactID(t.ID, domain.ArtifactSecurityReport), "verification:architecture")
	}

	// Gate completion on the verification verdict: a failed verification must
	// never yield a COMPLETED task (reliability: verification failure → task
	// cannot become successful). Only a PASS verdict (or the non-blocking
	// PASS_WITH_WARNING) may complete; anything else fails the task.
	if res.Verdict == verification.VerdictPass || res.Verdict == verification.VerdictPassWithWarning {
		if err := t.Complete(t.Output); err != nil {
			s.fail(t, err.Error())
			return t, res, err
		}
		s.persist(t)
		s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
		return t, res, nil
	}
	s.fail(t, "verification failed: "+res.Summary)
	return t, res, fmt.Errorf("verification failed: %s", res.Summary)
}
