package context

import (
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/evidence"
	"github.com/JayveerPrajapati/kern/internal/lenses"
	"github.com/JayveerPrajapati/kern/internal/skills"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// OrchestrateOptions configures one silent-pipeline run.
type OrchestrateOptions struct {
	// Budget is the token budget for the envelope render. <= 0 uses the task
	// policy's MaxTokens (or the mode's override).
	Budget int
	// Mode names a context-mode preset ("fix"|"review"|"architecture"|
	// "incident"|"explain") that overrides the task-policy family and applies
	// its lens/disclosure/budget overrides. Empty = classify the intent.
	Mode string
	// Skill names a bundled agent skill (kern-investigate, kern-safe-change,
	// kern-incident-triage) whose runbook is appended to the delivered
	// render, so the model follows the repo's own operating procedures for
	// the task class. Empty = no skill section.
	Skill string
}

// OrchestrateStage is an event kind emitted by the silent pipeline, in
// emission order (spec: TaskReceived -> TaskClassified -> PlanCreated ->
// EvidenceSelected -> BudgetApplied -> ContextDelivered).
type OrchestrateStage string

const (
	StageTaskReceived     OrchestrateStage = "task.received"
	StageTaskClassified   OrchestrateStage = "task.classified"
	StagePlanCreated      OrchestrateStage = "plan.produced"
	StageEvidenceSelected OrchestrateStage = "evidence.selected"
	StageBudgetApplied    OrchestrateStage = "budget.applied"
	StageContextDelivered OrchestrateStage = "context.delivered"
)

// EnvelopeHandle is a content-hash-sealed reference to an envelope render —
// the escalation handle carried by the context envelope (spec: retrieval
// handles). Its shape mirrors retrieval.Handle (same JSON tags, same digest
// semantics) without importing the retrieval package, so the context engine
// stays independent of the disclosure protocol's task-type dependency.
type EnvelopeHandle struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Name        string            `json:"name"`
	Source      string            `json:"source"`
	Line        int               `json:"line"`
	TokenCost   int               `json:"token_cost"`
	Confidence  float64           `json:"confidence"`
	ContentHash string            `json:"content_hash"` // SHA-256 of the render (staleness check)
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// OrchestrateResult is the outcome of one silent-pipeline run: the classified
// task, the explainable plan, the envelope identity, and a content-hash-sealed
// escalation handle. Deterministic: the same intent, repo state, and budget
// produce the same result (and the same event sequence) across runs.
type OrchestrateResult struct {
	TaskType        TaskType           `json:"task_type"`
	Mode            string             `json:"mode,omitempty"`  // context-mode preset used ("" = classified)
	Skill           string             `json:"skill,omitempty"` // bundled skill whose runbook was appended ("" = none)
	Plan            *Plan              `json:"plan"`
	EnvelopeVersion int                `json:"envelope_version"`
	SchemaVersion   string             `json:"schema_version"`
	Budget          int                `json:"budget"`
	TokenCount      int                `json:"token_count"` // delivered render: plan.TotalTokens (budget-fitted)
	Truncated       bool               `json:"truncated"`
	FittedText      string             `json:"fitted_text,omitempty"`
	Handle          *EnvelopeHandle    `json:"handle"`
	Events          []OrchestrateStage `json:"events"` // stages emitted, in order
}

// Orchestrate runs the full Principle-1 silent pipeline over an intent:
// TaskReceived -> classify -> assemble packet -> select evidence -> fit to
// budget -> stamp envelope -> build escalation handle -> ContextDelivered.
// Every stage emits an eventbus event when the engine has a bus; a nil bus
// still records the stage order in Events (deterministic). Nothing here is
// LLM-driven: classification, selection, budgeting, and hashing are all
// deterministic.
func (e *Engine) Orchestrate(intent string, opts OrchestrateOptions) (*OrchestrateResult, error) {
	if intent == "" {
		return nil, fmt.Errorf("orchestrate: intent is required")
	}
	emit := func(k eventbus.Kind, subject string, payload map[string]string) {
		if e.bus != nil {
			e.bus.Publish(eventbus.Event{Kind: k, Source: "context", Subject: subject, Payload: payload})
		}
	}
	events := []OrchestrateStage{StageTaskReceived}
	emit(eventbus.TaskReceived, intent, nil)

	// Classify.
	tt := ClassifyTask(intent)
	events = append(events, StageTaskClassified)
	emit(eventbus.TaskClassified, intent, map[string]string{"task_type": string(tt)})

	// Assemble the packet (emits context_packet.built on the same bus). The
	// intent is treated as a change description: an exact symbol lookup first,
	// then symbol candidates extracted from the text (so natural-language
	// intents assemble a packet rather than failing with symbol-not-found).
	pkt, err := e.AnalyzeChange(intent)
	if err != nil {
		for _, cand := range whatif.ExtractSymbolsIndex(intent, e.ix) {
			if cand == intent {
				continue
			}
			if pkt2, err2 := e.AnalyzeChange(cand); err2 == nil {
				pkt = pkt2
				err = nil
				break
			}
		}
	}
	if err != nil {
		// Prose fallback: intents whose words name no symbol (e.g. "explain
		// the greeting flow") still resolve through the build-time prose
		// vocab before the pipeline gives up.
		for _, hit := range e.ix.LookupProse(intent, 5) {
			if hit.Symbol == intent {
				continue
			}
			if pkt2, err2 := e.AnalyzeChange(hit.Symbol); err2 == nil {
				pkt = pkt2
				err = nil
				break
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("orchestrate: assemble: %w", err)
	}

	// Mode override: select the policy family, re-rank facts by the mode's
	// review lens, and apply its budget/disclosure overrides. Without a mode,
	// the classified task's own policy is used.
	modeName := opts.Mode
	budget := opts.Budget
	var plan *Plan
	if modeName != "" {
		m, ok := ModeFor(modeName)
		if !ok {
			return nil, fmt.Errorf("orchestrate: unknown mode %q (want %v)", modeName, ModeNames())
		}
		if m.Lens != "" {
			if l, err := lenses.Resolve(m.Lens); err == nil {
				pkt.Facts = lenses.ApplyLens(l, pkt.Facts)
			}
		}
		policy := PolicyFor(m.TaskType)
		if m.RetrievalLevel != "" {
			policy.RetrievalLevel = m.RetrievalLevel
		}
		if budget <= 0 && m.Budget > 0 {
			budget = m.Budget
		}
		plan = PlanPacketWithPolicy(&pkt, intent, policy, budget)
	} else {
		plan = PlanPacket(&pkt, intent, budget)
	}
	events = append(events, StagePlanCreated, StageEvidenceSelected, StageBudgetApplied)
	emit(eventbus.PlanProduced, intent, map[string]string{
		"task_type":  string(plan.TaskType),
		"selections": fmt.Sprintf("%d", len(plan.Selections)),
	})
	emit(eventbus.EvidenceSelected, intent, map[string]string{
		"selections":   fmt.Sprintf("%d", len(plan.Selections)),
		"total_tokens": fmt.Sprintf("%d", plan.TotalTokens),
	})
	emit(eventbus.BudgetApplied, intent, map[string]string{
		"budget":       fmt.Sprintf("%d", plan.Budget),
		"total_tokens": fmt.Sprintf("%d", plan.TotalTokens),
		"truncated":    fmt.Sprintf("%t", plan.Truncated),
	})

	// Envelope identity (PlanPacket stamps it when empty).
	envVersion := pkt.EnvelopeVersion
	if envVersion == 0 {
		envVersion = domain.EnvelopeVersionV1
	}
	schemaVersion := pkt.SchemaVersion
	if schemaVersion == "" {
		schemaVersion = "1.0.0"
	}

	// Escalation handle: a content-hash-sealed reference to the envelope
	// render. The same intent always yields the same handle ID; the content
	// hash lets a consumer reject a stale envelope deterministically. The
	// handle is not registered in the retrieval registry (its resolve path is
	// symbol-based); deeper context escalates via kern_retrieve with the
	// intent as an L1 query or the plan's task type.
	rendered := RenderPlan(plan)
	if opts.Skill != "" {
		body, err := skills.ReadSkill(opts.Skill)
		if err != nil {
			return nil, fmt.Errorf("orchestrate: unknown skill %q (available: %v)", opts.Skill, skills.SkillNames)
		}
		section := "\n\n## Skill: " + opts.Skill + "\n" + string(body)
		rendered += section
		pkt.FittedText += section
	}
	contentHash := evidence.Digest(rendered)
	h := &EnvelopeHandle{
		ID:          evidence.Digest("envelope\x00" + intent + "\x00context.orchestrate\x000"),
		Type:        "envelope",
		Name:        intent,
		Source:      "context.orchestrate",
		TokenCost:   tokenize.Count(rendered),
		Confidence:  1.0,
		ContentHash: contentHash,
		Metadata: map[string]string{
			"task_type":        string(plan.TaskType),
			"envelope_version": fmt.Sprintf("%d", envVersion),
		},
	}

	events = append(events, StageContextDelivered)
	emit(eventbus.ContextDelivered, intent, map[string]string{
		"token_count": fmt.Sprintf("%d", plan.TotalTokens),
		"handle":      h.ID,
	})

	return &OrchestrateResult{
		TaskType:        plan.TaskType, // effective policy family (mode override or classified)
		Mode:            modeName,
		Skill:           opts.Skill,
		Plan:            plan,
		EnvelopeVersion: envVersion,
		SchemaVersion:   schemaVersion,
		Budget:          plan.Budget,
		TokenCount:      plan.TotalTokens,
		Truncated:       plan.Truncated,
		// FittedText is the authoritative delivered render: RenderPlan plus
		// the skill section when one was requested (pkt.FittedText is empty
		// for many task types, so it cannot be the deliverable).
		FittedText: rendered,
		Handle:     h,
		Events:     events,
	}, nil
}
