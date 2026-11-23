package main

// Phase-0 benchmark: deterministic agent-flow baselines for the Code Mode
// comparison. Each row compares a manual four-call exploration sequence with
// one existing Kern composite call, then reports model turns, output tokens,
// token reduction, and a deterministic evidence-coverage score. This is the
// baseline needed before any new orchestration tool is built.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/research"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

type agentFlowExecution struct {
	ModelTurns     int       `json:"model_turns"`
	OutputTokens   int       `json:"output_tokens"`
	BalancedTokens int       `json:"balanced_tokens,omitempty"`
	EvidenceTokens int       `json:"evidence_tokens,omitempty"`
	Latency        t07Sample `json:"latency"`
}

type agentFlowTask struct {
	Name              string             `json:"name"`
	Target            string             `json:"target"`
	ExpectedFile      string             `json:"expected_file"`
	ExpectedLine      int                `json:"expected_line"`
	Manual            agentFlowExecution `json:"manual"`
	Direct            agentFlowExecution `json:"direct"`
	ModelTurnsSaved   int                `json:"model_turns_saved"`
	TokenReductionPct int                `json:"token_reduction_pct"`
	Accuracy          float64            `json:"accuracy"`
}

type agentFlowSection struct {
	Tasks []agentFlowTask `json:"tasks"`
	Note  string          `json:"note"`
}

func measureAgentFlows(ix *index.Index) *agentFlowSection {
	if ix == nil {
		return nil
	}
	tasks := []struct {
		name   string
		target func() (index.Symbol, bool)
		direct func(index.Symbol) string
	}{
		{
			name: "symbol-understanding",
			target: func() (index.Symbol, bool) {
				return ix.FindSymbol("ExploreBudgeted")
			},
			direct: func(d index.Symbol) string {
				report, err := intel.ExploreBudgeted(ix, d.FullName(), 2, 30, "", 4000)
				if err != nil {
					return "error: " + err.Error()
				}
				return intel.RenderExploreConcise(report)
			},
		},
		{
			name: "impact-analysis",
			target: func() (index.Symbol, bool) {
				target := heavyTypeSymbol(ix)
				return ix.FindSymbol(target)
			},
			direct: func(d index.Symbol) string {
				radius, _ := intel.BlastRadius(ix, []string{d.FullName()})
				return fmt.Sprintf("target: %s (%s:%d)\naffected symbols: %d\n%s", d.FullName(), d.File, d.Line, len(radius), strings.Join(radius, "\n"))
			},
		},
		{
			name: "task-context",
			target: func() (index.Symbol, bool) {
				return ix.FindSymbol(cliSymbol(ix))
			},
			direct: func(d index.Symbol) string {
				return ix.ContextDef(d, 0)
			},
		},
		{
			name: "task-context-probe",
			target: func() (index.Symbol, bool) {
				return ix.FindSymbol("RenderExploreBalanced")
			},
			direct: func(d index.Symbol) string {
				return intel.RenderProbe(intel.Probe(ix, "RenderExploreBalanced and Probe task context", 1200))
			},
		},
		{
			name: "research-context",
			target: func() (index.Symbol, bool) {
				return ix.FindSymbol("ClassifyCommand")
			},
			direct: func(d index.Symbol) string {
				report := research.Research(ix, "Understand the guard command classifier and verify reuse path", 1200)
				text := research.RenderResearch(report)
				if report.Truncated {
					text = research.FitResearch(text, 1200)
				}
				return text
			},
		},
	}

	section := &agentFlowSection{Note: "manual baselines are task-specific: symbol-understanding = search + context + callers + impact (4 turns), impact-analysis = search + context + impact (3 turns), task-context = search + context (2 turns); direct = one existing Kern composite call; accuracy = deterministic target/file/line/nonempty evidence coverage, not semantic model accuracy"}
	for _, task := range tasks {
		d, ok := task.target()
		if !ok {
			continue
		}
		var manualOutput string
		var manualTurns int
		includeCallers := false
		includeImpact := false
		switch task.name {
		case "symbol-understanding":
			includeCallers = true
			includeImpact = true
			manualOutput = manualAgentFlow(ix, d, true, true)
			manualTurns = 4
		case "impact-analysis":
			includeImpact = true
			manualOutput = manualAgentFlow(ix, d, false, true)
			manualTurns = 3
		case "task-context":
			manualOutput = manualAgentFlow(ix, d, false, false)
			manualTurns = 2
		case "task-context-probe":
			manualOutput = manualAgentFlow(ix, d, false, false)
			manualTurns = 2
		case "research-context":
			manualOutput = manualResearchFlow(ix, d)
			manualTurns = 5
		}
		directOutput := task.direct(d)
		evidenceOutput := ""
		balancedOutput := ""
		if task.name == "symbol-understanding" {
			if report, err := intel.ExploreBudgeted(ix, d.FullName(), 2, 30, "", 4000); err == nil {
				evidenceOutput = intel.RenderExplore(report)
				balancedOutput = intel.RenderExploreBalanced(report)
			}
		}

		manualLatency := timeAgentFlow(func() string {
			return manualAgentFlow(ix, d, includeCallers, includeImpact)
		})
		directLatency := timeAgentFlow(func() string { return task.direct(d) })

		manual := agentFlowExecution{ModelTurns: manualTurns, OutputTokens: tokenize.Count(manualOutput), Latency: manualLatency}
		direct := agentFlowExecution{
			ModelTurns:     1,
			OutputTokens:   tokenize.Count(directOutput),
			BalancedTokens: tokenize.Count(balancedOutput),
			EvidenceTokens: tokenize.Count(evidenceOutput),
			Latency:        directLatency,
		}

		accuracy := agentFlowAccuracy(directOutput, d)
		tokenReduction := 0
		if manual.OutputTokens > 0 {
			tokenReduction = int(float64(manual.OutputTokens-direct.OutputTokens) / float64(manual.OutputTokens) * 100)
		}
		section.Tasks = append(section.Tasks, agentFlowTask{
			Name:              task.name,
			Target:            d.FullName(),
			ExpectedFile:      d.File,
			ExpectedLine:      d.Line,
			Manual:            manual,
			Direct:            direct,
			ModelTurnsSaved:   manual.ModelTurns - direct.ModelTurns,
			TokenReductionPct: tokenReduction,
			Accuracy:          accuracy,
		})
	}
	return section
}

func manualResearchFlow(ix *index.Index, d index.Symbol) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("search: %s (%s:%d)\n", d.FullName(), d.File, d.Line))
	b.WriteString("\ncontext:\n" + ix.ContextDef(d, 0))
	b.WriteString("\ncallers:\n" + strings.Join(ix.CallersFor(d), "\n"))
	radius, _ := intel.BlastRadius(ix, []string{d.FullName()})
	b.WriteString("\nimpact:\n" + strings.Join(radius, "\n"))
	if verify, ok := ix.FindSymbol("Engine.VerifyReuse"); ok {
		b.WriteString(fmt.Sprintf("\nverify search: %s (%s:%d)\n", verify.FullName(), verify.File, verify.Line))
		b.WriteString("verify context:\n" + ix.ContextDef(verify, 0))
	}
	return b.String()
}

func manualAgentFlow(ix *index.Index, d index.Symbol, includeCallers, includeImpact bool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("search: %s (%s:%d)\n", d.FullName(), d.File, d.Line))
	b.WriteString("\ncontext:\n" + ix.ContextDef(d, 0))
	if includeCallers {
		b.WriteString("\ncallers:\n" + strings.Join(ix.CallersFor(d), "\n"))
	}
	if includeImpact {
		radius, _ := intel.BlastRadius(ix, []string{d.FullName()})
		b.WriteString("\nimpact:\n" + strings.Join(radius, "\n"))
	}
	return b.String()
}

func timeAgentFlow(run func() string) t07Sample {
	durations := make([]time.Duration, 0, 3)
	for i := 0; i < 3; i++ {
		start := time.Now()
		run()
		durations = append(durations, time.Since(start))
	}
	return t07FromDurations(durations)
}

func agentFlowAccuracy(out string, d index.Symbol) float64 {
	checks := []bool{
		strings.Contains(out, d.FullName()),
		strings.Contains(out, d.File),
		strings.Contains(out, strconv.Itoa(d.Line)),
		strings.TrimSpace(out) != "",
	}
	passed := 0
	for _, ok := range checks {
		if ok {
			passed++
		}
	}
	return float64(passed) / float64(len(checks))
}
