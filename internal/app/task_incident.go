// Package app hosts the TaskService orchestration layer.
// Generated split of task.go by domain (see task.go for the core).
package app

import (
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/modernization"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"time"
)

// correlator returns the single shared correlation service for this TaskService
// It is built lazily once over the platform's runtime source so
// every lane that reasons over runtime-to-code correlation (correlate,
// investigate, deploy, observe) shares the exact same source + lookback window.
func (s *TaskService) correlator() *runtime.SharedCorrelator {
	if s.sharedCorr == nil {
		src := s.platform.RuntimeSource()
		if src == nil {
			src = runtime.NewStore()
		}
		s.sharedCorr = runtime.NewSharedCorrelator(src, incident.DefaultLookback)
	}
	return s.sharedCorr
}

// Correlate runs the runtime correlation engine against a production alert and
// records the result as a Task. It creates a Task, runs the
// Correlator + CorrelateChain, attaches the deep evidence chain
// (alert→service→deployment→commit→symbol→task/pr/agent), and records an
// incident-report artifact.
// The correlation is deterministic — the LLM may explain it, but the chain is
// derived from the runtime source and git history, not an LLM guess.
func (s *TaskService) Correlate(alert domain.Alert) (*agent.Task, runtime.CorrelationChain, string, error) {
	t, err := s.Create(fmt.Sprintf("correlate alert: %s", alert.Message))
	if err != nil {
		return nil, runtime.CorrelationChain{}, "", err
	}
	if err := t.Transition(domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, runtime.CorrelationChain{}, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	src := s.platform.RuntimeSource()
	if src == nil {
		src = runtime.NewStore()
	}
	// Use the single shared correlation service so this lane reasons
	// over the same source/window as investigate/deploy/observe.
	chain := s.correlator().CorrelateChain(alert)

	t.Output = renderCorrelationText(chain)
	t.AddStep(agent.Step{
		Action:     "correlate",
		AgentID:    "runtime-correlator",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("correlated: %d links, service=%s", len(chain.Links), chain.Service),
		Status:     "success",
	})

	s.recordArtifact(domain.ArtifactIncidentReport, t.ID, "runtime-correlator",
		fmt.Sprintf("correlation: %d links, service=%s", len(chain.Links), chain.Service),
		"", "correlate:runtime")

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, chain, "", err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, chain, t.Output, nil
}

// InvestigateIncident runs the full incident workflow : IngestAlert
// → Correlate → RootCause. It wraps the incident.Engine through TaskService so
// the incident lifecycle (Task, Artifacts, Events) is recorded on the
// authoritative Task.
// The incident engine reuses Task, Artifact, Event, Policy, Memory, Evidence,
// and Verification — it does not create a separate lifecycle framework.
func (s *TaskService) InvestigateIncident(alert domain.Alert) (*agent.Task, *domain.Incident, string, error) {
	t, err := s.Create(fmt.Sprintf("investigate incident: %s", alert.Message))
	if err != nil {
		return nil, nil, "", err
	}
	if err := t.Transition(domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, nil, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	src := s.platform.RuntimeSource()
	if src == nil {
		src = runtime.NewStore()
	}
	// Use the single shared correlation service so this lane reasons
	// over the same source/window as correlate/deploy/observe.
	shared := s.correlator()
	eng, err := incident.NewEngineWithGraph(s.platform.Root(), s.platform.Graph(), src, s.platform.Memory(), s.platform.Firewall())
	if err != nil {
		s.fail(t, err.Error())
		return t, nil, "", err
	}
	eng.WithSharedCorrelator(shared)
	if s.bus != nil {
		eng.WithBus(s.bus)
	}

	inc := eng.IngestAlert(alert)
	eng.Correlate(inc)
	eng.RootCause(inc)

	t.Output = renderIncidentText(inc)
	t.AddStep(agent.Step{
		Action:     "investigate",
		AgentID:    "incident-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("incident: %s, status: %s, hypotheses: %d", inc.ID, inc.Status, len(inc.Hypotheses)),
		Status:     "success",
	})

	s.recordArtifact(domain.ArtifactIncidentReport, t.ID, "incident-engine",
		fmt.Sprintf("incident: %s — %s", inc.ID, inc.Status),
		"", "incident:investigate")
	if inc.RootCause != nil {
		s.recordArtifact(domain.ArtifactRootCauseReport, t.ID, "incident-engine",
			fmt.Sprintf("root cause: %s", inc.RootCause.Summary),
			s.lastArtifactID(t.ID, domain.ArtifactIncidentReport), "incident:rootcause")
	}

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, inc, "", err
	}
	s.persist(t)
	s.publish(eventbus.IncidentCreated, inc.ID, map[string]string{"task": t.ID, "service": inc.AffectedService})
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, inc, t.Output, nil
}

// RemediateIncident drives the candidate-fix pipeline end to end:
// the controlled incident (alert) is correlated and root-caused, the human
// approval gate is exercised, the candidate fix is applied in a sandbox,
// verified (build), and turned into a remediation PR. It is the app-layer
// counterpart to the incident engine's FixAndPR — the missing production
// entry that makes "controlled incident becomes a verified remediation PR"
// ( exit gate) reachable from the CLI/MCP/web.
// apply is the fix applier: it receives the sandbox worktree directory and
// must write the fix there (never the live repo). branch is the PR head
// branch. approver is the human identity granting the approval gate
// (Invariant #2: high-risk fixes require human approval; the decision is
// recorded in the approval workflow + bus). Returns the task, the remediated
// incident (status FIX_VERIFIED/PR_CREATED), and a rendered summary.
func (s *TaskService) RemediateIncident(alert domain.Alert, apply func(workDir string) error, branch, approver string) (*agent.Task, *domain.Incident, string, error) {
	t, err := s.Create(fmt.Sprintf("remediate incident: %s", alert.Message))
	if err != nil {
		return nil, nil, "", err
	}
	if err := t.Transition(domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, nil, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	src := s.platform.RuntimeSource()
	if src == nil {
		src = runtime.NewStore()
	}
	// Reason over the same shared correlation service as every
	// other runtime lane.
	shared := s.correlator()
	eng, err := incident.NewEngineWithGraph(s.platform.Root(), s.platform.Graph(), src, s.platform.Memory(), s.platform.Firewall())
	if err != nil {
		s.fail(t, err.Error())
		return t, nil, "", err
	}
	eng.WithSharedCorrelator(shared)
	eng.WithPRProvider(s.prProvider)
	if s.bus != nil {
		eng.WithBus(s.bus)
	}

	// 11.2 — Correlation + 11.3 — Root cause (hypothesis / evidence / confidence).
	inc := eng.IngestAlert(alert)
	eng.Correlate(inc)
	eng.RootCause(inc)

	// 11.4 — approval gate: a production remediation requires human approval.
	ap := eng.RequestApproval(inc, s.agentID, "remediate production incident")
	if _, err := eng.Approve(ap.ID, approver); err != nil {
		s.fail(t, "approval: "+err.Error())
		return t, inc, "", err
	}
	s.publish(eventbus.ApprovalRequested, ap.ID, map[string]string{"incident": inc.ID, "action": "incident.remediate"})
	s.publish(eventbus.TaskApproved, t.ID, map[string]string{"approval": ap.ID})

	// 11.4 — sandbox → verify → PR (risk gate + build inside ApplyAndVerifyFix).
	diff, err := eng.FixAndPR(inc, apply, branch)
	if err != nil {
		s.fail(t, err.Error())
		return t, inc, "", err
	}

	t.Output = renderIncidentText(inc)
	t.AddStep(agent.Step{
		Action:     "remediate",
		AgentID:    "incident-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("incident: %s → %s, diff=%d chars, PR=%s", inc.ID, inc.Status, len(diff), inc.PRURL),
		Status:     "success",
	})
	s.recordArtifact(domain.ArtifactDiff, t.ID, "incident-engine",
		fmt.Sprintf("remediation diff for %s (%d chars)", inc.ID, len(diff)),
		s.lastArtifactID(t.ID, domain.ArtifactRootCauseReport), "incident:fix")
	if inc.Verification != "" {
		s.recordArtifact(domain.ArtifactVerificationReport, t.ID, "incident-engine",
			inc.Verification, s.lastArtifactID(t.ID, domain.ArtifactDiff), "incident:verify")
	}
	s.recordArtifact(domain.ArtifactPullRequest, t.ID, "pr-engine",
		fmt.Sprintf("remediation PR for %s: %s (#%d)", inc.ID, inc.PRURL, inc.PRNumber),
		s.lastArtifactID(t.ID, domain.ArtifactVerificationReport), "incident:pr")

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, inc, "", err
	}
	s.persist(t)
	s.publish(eventbus.IncidentResolved, inc.ID, map[string]string{"task": t.ID, "status": string(inc.Status), "pr": inc.PRURL})
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, inc, t.Output, nil
}

// Learn extracts recurring patterns from engineering memory and records them as
// a Task. It wraps the learning.Extractor through TaskService so the
// learning lifecycle (Change → Outcome → Pattern → Memory) is auditable.
// Patterns are promoted to memory only when they meet the threshold (evidence-
// based promotion). The LLM may explain patterns but does not create them.
func (s *TaskService) Learn(threshold int) (*agent.Task, []learning.Pattern, string, error) {
	if threshold <= 0 {
		threshold = 3
	}
	t, err := s.Create("extract learning patterns")
	if err != nil {
		return nil, nil, "", err
	}
	if err := t.Transition(domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, nil, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	extractor := learning.New(s.platform.Memory())
	patterns, err := extractor.Patterns()
	if err != nil {
		s.fail(t, err.Error())
		return t, nil, "", err
	}
	surfaced, err := extractor.Surface(threshold)
	if err != nil {
		s.fail(t, err.Error())
		return t, nil, "", err
	}

	t.Output = renderLearningText(patterns, surfaced, threshold)
	t.AddStep(agent.Step{
		Action:     "learn",
		AgentID:    "learning-extractor",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("patterns: %d total, %d surfaced (threshold=%d)", len(patterns), len(surfaced), threshold),
		Status:     "success",
	})

	s.recordArtifact(domain.ArtifactMemoryEntry, t.ID, "learning-extractor",
		fmt.Sprintf("learning: %d patterns, %d surfaced", len(patterns), len(surfaced)),
		"", "learning:extract")

	// Promote surfaced patterns to memory.
	for _, p := range surfaced {
		extractor.Remember(p)
	}

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, patterns, "", err
	}
	s.persist(t)
	s.publish(eventbus.LessonRecorded, t.ID, map[string]string{"patterns": fmt.Sprintf("%d", len(surfaced))})
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, patterns, t.Output, nil
}

// Modernize runs the legacy modernization analysis and records it as a Task
// It wraps the modernization.Analyzer through TaskService so each
// modernization phase becomes an auditable Task with artifacts.
// The analysis connects communities → bridges → churn → candidate boundaries →
// impact → risk → migration plan → executable tasks. Each extraction phase
// becomes a Task or Task Group.
func (s *TaskService) Modernize() (*agent.Task, modernization.ExtractionPlan, string, error) {
	t, err := s.Create("modernization analysis")
	if err != nil {
		return nil, modernization.ExtractionPlan{}, "", err
	}
	if err := t.Transition(domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, modernization.ExtractionPlan{}, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	analyzer := modernization.NewAnalyzer(s.platform.Index())
	planPtr, err := analyzer.Analyze()
	if err != nil {
		s.fail(t, err.Error())
		return t, modernization.ExtractionPlan{}, "", err
	}
	plan := *planPtr

	t.Output = renderModernizationText(plan)
	t.AddStep(agent.Step{
		Action:     "modernize",
		AgentID:    "modernization-analyzer",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("modernization: %d contexts, %d bridges, %d phases", len(plan.Contexts), len(plan.Bridges), len(plan.Phases)),
		Status:     "success",
	})

	s.recordArtifact(domain.ArtifactArchitectureReport, t.ID, "modernization-analyzer",
		fmt.Sprintf("modernization: %d contexts, %d phases", len(plan.Contexts), len(plan.Phases)),
		"", "modernization:analyze")

	// / exit gate: modernization is Task aware. Materialize each
	// extraction phase as its own task (Task Group → Tasks), linked to this
	// plan task, so the phases are individually tracked, auditable, and
	// resumable instead of living only inside the plan text.
	if len(plan.Phases) > 0 {
		if phaseTasks, perr := s.ModernizePhaseTasks(plan, t.ID); perr != nil {
			s.publish(eventbus.TaskFailed, t.ID, map[string]string{"error": "modernize phase tasks: " + perr.Error()})
		} else {
			t.AddStep(agent.Step{
				Action:     "modernize-phase-tasks",
				AgentID:    "modernization-analyzer",
				StartedAt:  t.UpdatedAt,
				FinishedAt: time.Now(),
				Result:     fmt.Sprintf("materialized %d phase tasks", len(phaseTasks)),
				Status:     "success",
			})
		}
	}

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, plan, "", err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, plan, t.Output, nil
}

// ModernizePhaseTasks materializes each extraction phase as its own task
// ( one task per phase, not a single task for the whole plan). Each
// phase-task records an artifact and is linked by a parent reference to the
// plan task. It returns the created phase tasks.
func (s *TaskService) ModernizePhaseTasks(plan modernization.ExtractionPlan, parentTaskID string) ([]*agent.Task, error) {
	var out []*agent.Task
	for i := range plan.Phases {
		phase := &plan.Phases[i]
		pt, err := s.Create(fmt.Sprintf("modernize phase %d: extract %s", phase.Phase, phase.Context))
		if err != nil {
			return out, err
		}
		pt.Scope = "service:" + phase.Context
		pt.CreatedBy = "modernization-analyzer"
		// Link the phase task to its plan task so the audit trail can trace a
		// phase back to the plan that produced it .
		pt.ParentID = parentTaskID
		phase.TaskID = pt.ID
		if err := pt.Transition(domain.TaskCompleted); err == nil {
			pt.Output = renderModernizePhaseText(*phase)
			s.persist(pt)
		}
		// Record an artifact for the phase (a phase task is an auditable unit).
		s.recordArtifact(domain.ArtifactArchitectureReport, pt.ID, "modernization-analyzer",
			fmt.Sprintf("phase %d: extract %s (risk %s)", phase.Phase, phase.Context, phase.RiskLevel),
			s.lastArtifactID(parentTaskID, domain.ArtifactArchitectureReport), "modernization:phase")
		s.publish(eventbus.TaskCreated, pt.ID, map[string]string{"kind": "modernize-phase", "parent": parentTaskID})
		out = append(out, pt)
	}
	return out, nil
}
