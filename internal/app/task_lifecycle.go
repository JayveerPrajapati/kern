// Package app hosts the TaskService orchestration layer.
// Generated split of task.go by domain (see task.go for the core).
package app

import (
	"context"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/coder"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/flight"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/loop"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/planner"
	"log"
	"path/filepath"
	"sync"
	"time"
)

// transition advances the task to next and records the transition in the
// unified tamper-evident governance audit chain. It replaces direct
// t.Transition call sites so every lifecycle state change is audited exactly
// once, alongside the existing eventbus event and snapshot/artifact writes.
//
// FAILURE SEMANTICS (deliberate): the audit write is best-effort and NEVER
// rolls back or blocks the state transition. Task liveness is prioritized — a
// missing chain entry is detectable via the audit trail / chain repair, while
// a blocked task is not acceptable. A failed audit write is logged loudly so
// it surfaces in server logs.
func (s *TaskService) transition(t *agent.Task, next domain.TaskState) error {
	from := t.State
	if err := t.Transition(next); err != nil {
		return err
	}
	s.auditTransition(t, from, next)
	return nil
}

// Create makes a new Task for the given intent and submits it to the registry.
// The Task starts in CREATED state with the intent as both Input and Intent.
// Returns the created Task (a pointer into the registry, so state mutations
// are visible) or an error if submission fails.
func (s *TaskService) Create(intent string) (*agent.Task, error) {
	t := agent.NewTask("analyze", intent)
	// The persisted store owns task IDs: clear the process-local ID assigned
	// by NewTask so SubmitTask lets the store assign "t-<max+1>" under its
	// cross-process file lock. Two processes would otherwise both start at
	// t-1 and Save (replace by ID) would silently destroy one of the tasks.
	t.ID = ""
	t.Intent = intent
	t.CreatedBy = s.agentID
	t.Requester = s.agentID
	if s.platform != nil {
		t.Project = filepath.Base(s.platform.Root())
	}
	if err := s.registry.SubmitTask(t); err != nil {
		return nil, fmt.Errorf("task service: %w", err)
	}
	s.publish(eventbus.TaskCreated, t.ID, map[string]string{"intent": intent})
	return t, nil
}

// createAnalysisTask creates the task behind a read-only analysis command
// (Analyze/AnalyzeWithLens/WhatIf/Plan/Impact). Analysis commands are
// workflow-state-free by default: the task is tracked in the in-memory
// registry (ID, states, output, artifacts, audit transitions) but no task
// record is written to the persisted store unless the caller explicitly
// opted in with WithTaskPersistence(true) (F9).
func (s *TaskService) createAnalysisTask(intent string) (*agent.Task, error) {
	if s.persistTasks {
		return s.Create(intent)
	}
	return s.createEphemeral(intent)
}

// createEphemeral is the non-persisting counterpart of Create used by
// read-only analysis commands. The task is submitted to the in-memory
// registry WITHOUT the persisted backing store, so no record is written; the
// store is re-attached immediately after, so a persisted Create on the same
// service is unaffected. The task ID comes from the ephemeral "a-<n>"
// namespace — disjoint from the store's "t-<max+1>" namespace — so an
// ephemeral task can never collide with a persisted task in the same
// registry, even when one service mixes analysis and workflow (the shared
// acceptance-matrix service does exactly that). The task ID is marked
// ephemeral so persist/fail skip the store for it.
func (s *TaskService) createEphemeral(intent string) (*agent.Task, error) {
	t := agent.NewTask("analyze", intent)
	// Override the process-local "t-<n>" ID from NewTask with the ephemeral
	// "a-<n>" namespace: persisted tasks (store-assigned "t-<max+1>") and
	// ephemeral tasks can then coexist in one registry without ID clashes.
	t.ID = nextEphemeralTaskID()
	t.Intent = intent
	t.CreatedBy = s.agentID
	t.Requester = s.agentID
	if s.platform != nil {
		t.Project = filepath.Base(s.platform.Root())
	}
	if s.registry == nil {
		return nil, fmt.Errorf("task service: registry not configured")
	}
	// Detach the persisted backing store for the submit so no task record
	// is written; restore it right after so later persisted work on the
	// same service is unaffected.
	reg := s.registry
	reg.SetTaskStore(nil)
	err := reg.SubmitTask(t)
	reg.SetTaskStore(s.store)
	if err != nil {
		return nil, fmt.Errorf("task service: %w", err)
	}
	s.markEphemeral(t.ID)
	s.publish(eventbus.TaskCreated, t.ID, map[string]string{"intent": intent})
	return t, nil
}

// ephemeralSeq is the package-level counter for ephemeral analysis task IDs.
// The "a-" prefix keeps them out of the store's "t-<n>" namespace, so an
// ephemeral task can never collide with a persisted task ID in the same
// registry and the store can never hand out an ID that is already registered
// in memory.
var ephemeralSeq struct {
	sync.Mutex
	n int
}

// nextEphemeralTaskID returns the next ephemeral analysis task ID.
func nextEphemeralTaskID() string {
	ephemeralSeq.Lock()
	defer ephemeralSeq.Unlock()
	ephemeralSeq.n++
	return fmt.Sprintf("a-%d", ephemeralSeq.n)
}

// markEphemeral records a task as created without persistence (analysis
// commands), so persist/fail skip the store for exactly that task.
func (s *TaskService) markEphemeral(id string) {
	s.ephMu.Lock()
	if s.ephemeral == nil {
		s.ephemeral = map[string]bool{}
	}
	s.ephemeral[id] = true
	s.ephMu.Unlock()
}

// isEphemeral reports whether the task was created without persistence.
func (s *TaskService) isEphemeral(id string) bool {
	s.ephMu.RLock()
	_, ok := s.ephemeral[id]
	s.ephMu.RUnlock()
	return ok
}

// RunLoop is the task-scoped closed-loop entry point. It creates an
// authoritative Task for the intent, runs the closed loop at the requested
// autonomy level, records the run as an artifact, and returns the Task plus the
// loop Result so the interface layer can render it. It replaces the previous
// inline loop.NewLoop(...).Run(...) orchestration in the MCP handler: the
// service owns the loop so every interface gets task tracking and an audit
// trail. RunLoop runs the loop's default no-op stages (read-only).
//
// RunLoop is the backward-compatible command-less form; it runs with
// context.Background() so a deadline/cancel never applies. Callers that hold
// a request-derived context (web /v1/loop, MCP kern_loop) should use
// RunLoopContext so a deadline or client disconnect actually cancels the run
// between stages instead of leaving it running in the background
// (oracle-gate ctx threading).
func (s *TaskService) RunLoop(intent string, level loop.Autonomy) (*agent.Task, *loop.Result, error) {
	return s.RunLoopContext(context.Background(), intent, level)
}

// RunLoopContext is the context-aware form of RunLoop: it threads ctx into
// the closed loop so a caller deadline or cancellation stops the run BETWEEN
// stages (and before it starts) instead of leaving it running in the
// background. The loop's own stage loop checks ctx.Err() before every stage;
// the app layer checks it around task setup too. The created Task is always
// observable: a cancelled run fails the Task (FAILED) so the aborted run is
// terminal and auditable, never an orphan.
func (s *TaskService) RunLoopContext(ctx context.Context, intent string, level loop.Autonomy) (*agent.Task, *loop.Result, error) {
	return s.runLoop(ctx, intent, level, false)
}

// RunDo is the task-scoped autonomous closed-loop entry point (the "Implement
// X" path). It behaves exactly like RunLoop but additionally wires the
// autonomous coder and the LLM-driven planner into the loop's default stage
// handlers, so `kern do "add a cache layer"` drives the full
// understand→remember→plan→code→verify→protect→observe→learn loop without a
// caller-supplied StepFunc. The coder and planner use the provider-neutral LLM
// factory (KERN_LLM_PROVIDER, default local Ollama); their stage gates sit at
// >= L2 autonomy, so L0/L1 runs never invoke them.
func (s *TaskService) RunDo(intent string, level loop.Autonomy) (*agent.Task, *loop.Result, error) {
	return s.RunDoContext(context.Background(), intent, level)
}

// RunDoContext is RunDo with caller cancellation: a cancelled context stops
// the autonomous loop between stages (and before it starts) instead of
// leaving the L2 code-modifying loop running in the background after the
// caller (server shutdown, $/cancelRequest, client disconnect) believes it
// stopped. kern_loop mode=autonomous routes here.
func (s *TaskService) RunDoContext(ctx context.Context, intent string, level loop.Autonomy) (*agent.Task, *loop.Result, error) {
	return s.runLoop(ctx, intent, level, true)
}

// runLoop is the shared task-scoped closed-loop implementation behind RunLoop
// and RunDo. When autonomous is true, the loop's default code and plan stages
// are handled by the coder and planner agents (mirroring what the CLI's runDo
// previously wired inline) instead of no-op'ing.
func (s *TaskService) runLoop(ctx context.Context, intent string, level loop.Autonomy, autonomous bool) (*agent.Task, *loop.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.platform == nil {
		return nil, nil, fmt.Errorf("task service: platform not configured")
	}
	t, err := s.Create(intent)
	if err != nil {
		return nil, nil, err
	}
	// The caller's deadline/cancel may already have fired (e.g. a web request
	// whose context expired during setup): record a FAILED task so the aborted
	// run is terminal and observable, then surface the cancellation.
	if err := ctx.Err(); err != nil {
		s.fail(t, "loop cancelled: "+err.Error())
		return t, nil, err
	}
	if err := s.transition(t, domain.TaskAnalyzing); err != nil {
		s.fail(t, err.Error())
		return t, nil, err
	}
	if err := ctx.Err(); err != nil {
		s.fail(t, "loop cancelled: "+err.Error())
		return t, nil, err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "ANALYZING"})

	root := s.platform.Root()
	memStore := memory.NewMemoryStore(root)
	cfg := loop.LoopConfig{
		Root:     root,
		Level:    level,
		Mem:      memStore,
		Recorder: flight.New(root),
		// Continuous learning is default-on whenever a memory store exists:
		// the learn stage extracts recurring patterns over the store and
		// surfaces them as constraints. This single wiring point covers the
		// kern_loop/kern_do MCP tools AND the CLI `loop`/`do` commands (all
		// route through runLoop). LoopConfigs built elsewhere without a
		// memory store keep the nil-skip contract.
		Learning: learning.New(memStore),
	}
	if autonomous {
		cfg.Coder = coder.New(agent.OllamaProvider())
		cfg.Planner = planner.New(agent.OllamaProvider())
		cfg.Context = s.platform.CodeContext
	}
	l, err := loop.NewLoop(cfg)
	if err != nil {
		s.fail(t, err.Error())
		return t, nil, err
	}
	if s.bus != nil {
		l.WithBus(s.bus)
	}

	// RunContext threads ctx into the loop's per-stage cancellation checks, so
	// a deadline/disconnect stops the run between stages (oracle-gate).
	res, err := l.RunContext(ctx, intent, nil)
	if res == nil {
		res = &loop.Result{Intent: intent, Level: level}
	}
	t.Output = fmt.Sprintf("level: %s, stages: %d, deployed: %v", level, len(res.Stages), res.Deployed)
	t.AddStep(agent.Step{
		Action:     "loop",
		AgentID:    "loop-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("deployed: %v, healthy: %v", res.Deployed, res.ObservedHealthy),
		Status:     "success",
	})

	// Record the loop run as an artifact in the audit chain.
	s.recordArtifact(domain.ArtifactPlan, t.ID, "loop-engine",
		fmt.Sprintf("loop run: %s, %d stages, deployed=%v", res.Intent, len(res.Stages), res.Deployed),
		"", "loop:run")

	if err != nil {
		s.fail(t, err.Error())
		return t, res, err
	}

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, res, err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, res, nil
}

// Cancel transitions a task to CANCELLED with a reason. The task is persisted
// and a task.updated event is published. Returns an error if the task is
// already terminal or the transition is invalid.
func (s *TaskService) Cancel(taskID, reason string) error {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return err
	}
	if err := t.Cancel(reason); err != nil {
		return err
	}
	s.persist(t)
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"action": "cancel", "reason": reason})
	return nil
}

// Timeout transitions a task to FAILED, indicating it exceeded a deadline.
func (s *TaskService) Timeout(taskID string) error {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return err
	}
	if err := t.Timeout(); err != nil {
		return err
	}
	s.persist(t)
	s.publish(eventbus.TaskFailed, t.ID, map[string]string{"reason": "timeout"})
	return nil
}

// Retry reopens a FAILED task to ANALYZING. Idempotent: if the task is already
// non-terminal, it is a no-op. The task is persisted and a task.updated event
// is published.
func (s *TaskService) Retry(taskID string) (*agent.Task, error) {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return nil, err
	}
	if err := t.Retry(); err != nil {
		return nil, err
	}
	s.persist(t)
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{
		"action":       "retry",
		"retry_count":  fmt.Sprintf("%d", t.RetryCount),
		"retry_reason": t.RetryReason,
	})
	return t, nil
}

// Resume unblocks a BLOCKED task, returning it to its PriorState. Idempotent:
// if the task is already non-terminal, it is a no-op.
func (s *TaskService) Resume(taskID string) (*agent.Task, error) {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return nil, err
	}
	if err := t.Resume(); err != nil {
		return nil, err
	}
	// Full reconstruction on resume. The resumed task rehydrates
	// its ContextPacket and Plan from the persisted artifacts, so a resumed
	// task is not a shell — it carries the same working context it had when it
	// was paused/blocked. Best-effort: if reconstruction fails, resume still
	// succeeds (the task is usable with what it had).
	s.reconstructContext(t)
	s.persist(t)
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"action": "resume"})
	return t, nil
}

// reconstructContext rehydrates a task's ContextPacket and Plan from its
// persisted artifacts and rich context snapshot ( full
// reconstruction). It is best-effort and never fails the caller: it only
// restores fields that can be derived from the artifact chain or the most
// recent persisted snapshot. Existing fields (e.g. a ContextPacket already
// attached by a fresh analyze) are preserved; snapshot fields are layered on
// top when present.
func (s *TaskService) reconstructContext(t *agent.Task) {
	if t == nil {
		return
	}
	// Build a minimal packet from the artifact chain if the task has none yet.
	if t.ContextPacket == nil {
		pkt := &domain.ContextPacket{GeneratedAt: time.Now()}
		arts, err := s.arts.GetByTask(t.ID)
		if err == nil {
			for _, a := range arts {
				if a.Kind == domain.ArtifactContextPacket && a.Scope != "" {
					pkt.Task = a.Scope
					break
				}
			}
		}
		t.ContextPacket = pkt
	}
	// Layer the rich context snapshot (Goal, Decisions, Constraints, Files,
	// Tests, Risks) on top so a resumed task carries its prior working context.
	// Best-effort: only the most recent snapshot is considered.
	if s.snapshots == nil {
		return
	}
	snaps, err := s.snapshots.History(t.ID)
	if err != nil || len(snaps) == 0 {
		return
	}
	snap := snaps[len(snaps)-1]
	pkt := t.ContextPacket
	if pkt.Task == "" && snap.Goal != "" {
		pkt.Task = snap.Goal
	}
	addFacts := func(stmts []string) {
		for _, stmt := range stmts {
			if stmt == "" {
				continue
			}
			pkt.Facts = append(pkt.Facts, domain.Claim{
				Type:      domain.ClaimFact,
				Statement: stmt,
				Source:    "resume:snapshot",
			})
		}
	}
	addFacts(snap.Decisions)
	addFacts(snap.Constraints)
	addFacts(snap.Files)
	addFacts(snap.Tests)
	addFacts(snap.Risks)
}

// Pause blocks a task with a reason, recording its PriorState so Resume can
// return to it. Idempotent: if the task is already BLOCKED, it is a no-op. The
// task is persisted and a task.blocked event is published with the pause reason.
func (s *TaskService) Pause(taskID, reason string) error {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return err
	}
	if err := t.Pause(reason); err != nil {
		return err
	}
	s.persist(t)
	s.publish(eventbus.TaskBlocked, t.ID, map[string]string{"action": "pause", "reason": reason})
	return nil
}

// Rollback transitions a PR_CREATED / DEPLOYING / OBSERVING task to
// ROLLED_BACK with a reason.
func (s *TaskService) Rollback(taskID, reason string) error {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return err
	}
	if err := t.Rollback(reason); err != nil {
		return err
	}
	s.persist(t)
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"action": "rollback", "reason": reason})
	return nil
}

// HumanTakeover blocks a task and binds it to a human agent, recording the
// prior state so it can be resumed after intervention.
func (s *TaskService) HumanTakeover(taskID, agentID string) error {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return err
	}
	if err := t.HumanTakeover(agentID); err != nil {
		return err
	}
	s.persist(t)
	s.publish(eventbus.TaskBlocked, t.ID, map[string]string{"action": "human_takeover", "agent": agentID})
	return nil
}

// ReturnToAgent hands a human-takeover (BLOCKED) task back to an agent. It
// resumes the task to its prior state and reassigns the AgentID. Mirrors
// HumanTakeover's structure: get, mutate, persist, publish.
func (s *TaskService) ReturnToAgent(taskID, agentID string) error {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return err
	}
	if err := t.ReturnToAgent(agentID); err != nil {
		return err
	}
	s.persist(t)
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"action": "return_to_agent", "agent": agentID})
	return nil
}

// getTaskForMutation retrieves a task by ID from the registry or the persisted
// store, returning an error if not found.
func (s *TaskService) getTaskForMutation(taskID string) (*agent.Task, error) {
	t, ok := s.registry.GetTask(taskID)
	if ok {
		return t, nil
	}
	stored, err := s.store.Get(taskID)
	if err != nil {
		return nil, fmt.Errorf("task %s: %w", taskID, err)
	}
	return &stored, nil
}

// RunWorkflowDefault is the exit-gate entry point: Kern selects and
// coordinates the agent team WITHOUT the external caller manually sequencing
// it. The caller passes only the intent — everything else is Kern's:
// 1. Task creation (Task →).
// 2. Agent selection: the task is classified by kind and the kind-specific
// workflow (only the specialists that apply) is registered — the same
// selection RunWorkflow performs.
// 3. Team wiring: the standard specialist team (planner, architect, coder,
// reviewer, security, tester, sre) is registered on the engine's registry
// so every workflow role resolves without external setup.
// 4. Coordination: the WorkflowEngine drives the steps (session → context →
// tool call → result → artifact → Task state) in order, parking at the
// human approval gate before the first execution step.
// 5. Execution: Kern's own default step handler performs each step — the
// analyze and plan steps run the real deterministic engines (platform
// analysis + plan assembly), and the remaining role stages produce
// deterministic outcomes from the task's real plan/risk/test data.
// The human approval gate is preserved (Invariant #2): the task parks in
// WAITING_FOR_APPROVAL and the error wraps agent.ErrApprovalRequired. The
// caller extracts the approval ID via agent.ApprovalID(err), resolves it via
// CompleteApproval (or out-of-band `kern approve`), and calls
// RunWorkflowResume — the engine resumes at the gate and drives the remaining
// steps to completion. The run state (resume step + approval bindings) is
// persisted on the task and the approval decision through the project's
// approval store, so resume also works across processes.
func (s *TaskService) RunWorkflowDefault(intent string) (*agent.Task, error) {
	return s.RunWorkflowDefaultContext(context.Background(), intent)
}

// RunWorkflowDefaultContext is RunWorkflowDefault with caller cancellation:
// the context is checked before the workflow starts and before every
// workflow step (WorkflowEngine.RunContext), so a cancelled caller stops the
// run between steps instead of leaving it running in the background.
// kern_workflow routes here.
func (s *TaskService) RunWorkflowDefaultContext(ctx context.Context, intent string) (*agent.Task, error) {
	t, err := s.Create(intent)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		s.fail(t, "workflow cancelled: "+err.Error())
		return t, err
	}
	eng := s.engineForTask(t)

	// Keep the task + engine together so an approval-gated run can resume with
	// the same pair (the engine's gate/progress state lives on the instance).
	s.wfMu.Lock()
	s.workflowRuns[t.ID] = &workflowRun{task: t, engine: eng}
	s.wfMu.Unlock()

	return s.runStoredWorkflowContext(ctx, t.ID)
}

// fail marks a Task FAILED, persists it, and publishes a task.failed event.
// When the FAILED transition itself fails (the task is already terminal, or
// its current state cannot legally transition to FAILED — agent.Task.Fail
// returns ErrInvalidTransition in that case), the task is NOT marked FAILED:
// the failure is logged loudly and NEITHER the misleading task.failed event
// is published NOR the task persisted as FAILED, so downstream consumers
// never observe a FAILED task that is not actually in FAILED state. The
// success path is unchanged: a task that really transitions to FAILED is
// persisted and announced exactly as before.
func (s *TaskService) fail(t *agent.Task, errMsg string) {
	if err := t.Fail(errMsg); err != nil {
		log.Printf("kern app: task %s could not be marked FAILED: %v", t.ID, err)
		return
	}
	s.persist(t)
	s.publish(eventbus.TaskFailed, t.ID, map[string]string{"error": errMsg})
}
