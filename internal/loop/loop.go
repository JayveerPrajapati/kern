package loop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/coder"
	"github.com/JayveerPrajapati/kern/internal/deployment"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/flight"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/planner"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

// StepFunc is the caller-provided implementation for the creative/approval
// stages of the loop (plan, code, deploy). It receives the current stage, the
// intent, the sandbox worktree (when the stage operates on code) and the
// accumulating result, and returns a textual output. Deterministic internal
// stages (intent, remember, verify, protect, observe, learn) run internally and
// are never delegated to the step.
type StepFunc func(stage, intent string, wt *execution.Worktree, res *Result) (string, error)

// LoopConfig configures a closed loop instance.
type LoopConfig struct {
	Root          string                       // repository root
	Level         Autonomy                     // autonomy gate (L0-L5)
	Service       string                       // service observed after deploy
	Since         time.Time                    // observe window start (0 → now - ObserveWindow)
	ObserveWindow time.Duration                // default observe lookback when Since is zero
	Source        runtime.Source               // production source for observe
	Mem           *memory.MemoryStore          // engineering memory for learn
	Incidents     *incident.Store              // incident store for deploy outcome
	Appr          *governance.ApprovalWorkflow // human approval for deploy
	Scope         string                       // memory scope tag for learned patterns

	// Deployer optionally wires a deployment.Deployer for the deploy stage.
	// When nil, the loop resolves one from the environment at NewLoop time:
	// KERN_DEPLOY_COMMAND set → ShellDeployer (real external deploy), unset →
	// NoopDeployer (simulated success). Either way the stage still requires
	// KERN_ALLOW_DEPLOY=1 to proceed.
	Deployer deployment.Deployer

	// Learning optionally wires the continuous-learning pattern extractor
	// (internal/learning) into the loop: after each lesson, recurring patterns
	// above PatternThreshold are surfaced as constraints. Nil skips this wiring.
	Learning *learning.Extractor
	// PatternThreshold is the minimum count a recurring pattern must reach
	// before it is surfaced as a constraint. Values <= 0 mean 1 (surface all).
	PatternThreshold int

	// Recorder optionally wires the AI flight recorder (internal/flight) into
	// the loop: every stage run is appended to the recorder log, so Workflow E
	// (GOVERN AI ENGINEERING) can be answered from a persisted, auditable
	// record. Nil skips.
	Recorder *flight.Recorder

	// Coder optionally wires the autonomous coding agent (internal/coder) as
	// the default code-stage handler, used when the caller's StepFunc is nil.
	Coder *coder.Agent

	// Planner optionally wires the LLM-driven planner (internal/planner) as
	// the default plan-stage handler, used when the caller's StepFunc is nil.
	// When both Planner and Coder are set, the loop uses LLM-driven plan→code
	// (L3+ autonomy). When neither is set, stages are no-ops (L0-L2 read-only).
	Planner *planner.Agent
}

// StageResult is the outcome of one loop stage.
type StageResult struct {
	Stage  string
	Status string // "ok" | "error" | "skipped:below-autonomy"
	Output string
}

// Result is the outcome of a closed-loop run.
type Result struct {
	Intent          string
	Level           Autonomy
	Stages          []StageResult
	Diff            string
	Deployed        bool
	ObservedHealthy bool
	Remembered      []domain.Memory // memories recalled in the remember stage
	Protected       bool            // true when the protect/approval gate ran and granted
	Learned         *domain.Memory
}

// Loop drives the continuous closed loop.
type Loop struct {
	cfg LoopConfig
	bus *eventbus.Bus // optional event publisher; nil = no-op
}

// NewLoop returns a loop over the given config.
func NewLoop(cfg LoopConfig) (*Loop, error) {
	if cfg.Root == "" {
		return nil, errors.New("loop: root required")
	}
	if cfg.Source == nil {
		cfg.Source = runtime.NewStore()
	}
	if cfg.Since.IsZero() {
		w := cfg.ObserveWindow
		if w <= 0 {
			w = time.Hour
		}
		cfg.Since = time.Now().Add(-w)
	}
	if cfg.Scope == "" {
		cfg.Scope = "project"
	}
	if cfg.Deployer == nil {
		cfg.Deployer = deployment.NewDeployerFromEnv()
	}
	return &Loop{cfg: cfg}, nil
}

// Level returns the configured autonomy level.
func (l *Loop) Level() Autonomy { return l.cfg.Level }

// WithBus attaches an optional event bus. When non-nil, the loop publishes
// deployment.started/completed/rolled_back, observe.healthy and
// learning.lesson_recorded. A nil bus is a no-op.
func (l *Loop) WithBus(b *eventbus.Bus) *Loop {
	l.bus = b
	return l
}

// publish delivers a bus event. A nil bus is a no-op.
func (l *Loop) publish(ev eventbus.Event) {
	if l.bus == nil {
		return
	}
	if ev.Source == "" {
		ev.Source = "loop"
	}
	l.bus.Publish(ev)
}

// Run executes the closed loop for an intent: the deterministic stages (verify,
// observe, learn) run internally; plan/code/deploy are delegated to step.
// Stages the autonomy level does not permit are skipped (never jumped). The
// loop runs in a sandbox worktree and never mutates the live repository.
func (l *Loop) Run(intent string, step StepFunc) (*Result, error) {
	res := &Result{Intent: intent, Level: l.cfg.Level}
	if step == nil {
		if l.cfg.Coder != nil || l.cfg.Planner != nil {
			step = l.defaultStep()
		} else {
			step = func(string, string, *execution.Worktree, *Result) (string, error) {
				return "", nil
			}
		}
	}

	wt, err := execution.NewWorktree(l.cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("loop: worktree: %w", err)
	}
	defer wt.Cleanup()

	for _, st := range []string{stageIntent, stageRemember, stagePlan, stageCode, stageVerify, stageProtect, stageDeploy, stageObserve, stageLearn} {
		if !l.cfg.Level.AllowsStage(st) {
			res.Stages = append(res.Stages, StageResult{Stage: st, Status: "skipped:below-autonomy"})
			continue
		}

		var out string
		var err error
		switch st {
		case stageRemember:
			// Recall engineering memory relevant to the intent (read-only).
			if l.cfg.Mem != nil {
				mems, merr := l.cfg.Mem.Recall(memory.Query{Text: intent, Limit: 5})
				if merr == nil && len(mems) > 0 {
					parts := make([]string, 0, len(mems))
					for _, m := range mems {
						parts = append(parts, m.Content)
					}
					out = strings.Join(parts, "; ")
					res.Remembered = mems
				} else {
					out = "no relevant memories"
				}
			} else {
				out = "memory not configured"
			}
		case stageProtect:
			// Invoke governance approval before deploy; fail-closed if denied.
			// A nil workflow (cfg.Appr == nil) means no approval gate.
			if l.cfg.Appr != nil {
				ap, apprErr := l.requestApproval(intent)
				if apprErr != nil {
					err = apprErr
					out = "approval denied: " + apprErr.Error()
				} else {
					out = "approved " + ap.ID
					res.Protected = true
				}
			} else {
				out = "governance not configured"
			}
		case stageCode:
			out, err = step(st, intent, wt, res)
			if err == nil {
				if d, derr := wt.Diff(); derr == nil {
					res.Diff = d
				}
			}
		case stageVerify:
			v := verification.NewEngine(wt.Dir()).Verify([]string{"build", "test", "security", "architecture", "dependency"})
			out = v.Summary
			if v.Verdict != verification.VerdictPass {
				err = errors.New("verify: " + v.Summary)
			}
		case stageDeploy:
			// Phase 9: production mutation is disabled by default. The deploy
			// stage only runs when KERN_ALLOW_DEPLOY=1 is explicitly set, so
			// a local console or loop run cannot accidentally trigger a
			// production deployment without operator opt-in. Without it, the
			// stage is skipped (not failed) so read-only loops still complete.
			if os.Getenv("KERN_ALLOW_DEPLOY") != "1" {
				out = "deploy skipped: KERN_ALLOW_DEPLOY not set (production mutation disabled by default)"
				break
			}
			l.publish(eventbus.Event{Kind: eventbus.DeploymentStarted, Subject: l.cfg.Service, Payload: map[string]string{"service": l.cfg.Service}})
			dres, derr := l.cfg.Deployer.Deploy(context.Background(), deployment.DeployRequest{
				Service:     l.cfg.Service,
				Version:     "loop",
				ProjectRoot: l.cfg.Root,
			})
			if derr != nil {
				err = fmt.Errorf("deploy: %w", derr)
				res.Deployed = false
				out = "deploy failed: " + derr.Error()
				l.publish(eventbus.Event{Kind: eventbus.DeploymentFailed, Subject: l.cfg.Service, Payload: map[string]string{"service": l.cfg.Service, "error": derr.Error()}})
				l.publish(eventbus.Event{Kind: eventbus.DeploymentRolledBack, Subject: l.cfg.Service, Payload: map[string]string{"service": l.cfg.Service, "error": derr.Error()}})
			} else if !dres.Success {
				err = errors.New("deploy failed: " + dres.Output)
				res.Deployed = false
				out = dres.Output
				l.publish(eventbus.Event{Kind: eventbus.DeploymentFailed, Subject: l.cfg.Service, Payload: map[string]string{"service": l.cfg.Service, "error": dres.Output}})
				l.publish(eventbus.Event{Kind: eventbus.DeploymentRolledBack, Subject: l.cfg.Service, Payload: map[string]string{"service": l.cfg.Service, "error": dres.Output}})
			} else {
				res.Deployed = true
				out = dres.Output
				l.publish(eventbus.Event{Kind: eventbus.DeploymentCompleted, Subject: l.cfg.Service, Payload: map[string]string{"service": l.cfg.Service, "version": dres.Version}})
			}
		case stageObserve:
			res.ObservedHealthy = l.observeHealthy()
			out = fmt.Sprintf("observed %v", res.ObservedHealthy)
			l.publish(eventbus.Event{Kind: eventbus.ObserveHealthy, Subject: l.cfg.Service, Payload: map[string]string{"service": l.cfg.Service, "healthy": fmt.Sprintf("%v", res.ObservedHealthy)}})
		case stageLearn:
			m, lerr := l.learn(intent, res.ObservedHealthy, res.Deployed)
			if lerr != nil {
				err = lerr
			} else {
				res.Learned = &m
				out = "learned " + m.ID
				l.publish(eventbus.Event{Kind: eventbus.LessonRecorded, Subject: m.ID, Payload: map[string]string{"scope": l.cfg.Scope}})
			}
		default: // intent, plan
			out, err = step(st, intent, wt, res)
		}

		status := "ok"
		if err != nil {
			status = "error"
		}
		res.Stages = append(res.Stages, StageResult{Stage: st, Status: status, Output: out})
		// Persist an auditable action log for this stage (Workflow E). Nil-safe.
		if rec := l.cfg.Recorder; rec != nil {
			_, _ = rec.Record(flight.Record{
				AgentID:  "loop",
				TaskID:   "loop:" + intent,
				Action:   st,
				Context:  intent,
				Result:   out,
				Status:   status,
				Approved: st != stageDeploy || res.Deployed,
			})
		}
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

// coderStep returns a StepFunc that delegates the code stage to the wired
// coder.Agent. For non-code stages it returns empty (the loop's internal
// stages handle those). This is used when cfg.Coder is set and the caller
// did not supply a StepFunc.
func (l *Loop) coderStep() StepFunc {
	return func(stage, intent string, wt *execution.Worktree, res *Result) (string, error) {
		if stage != stageCode {
			return "", nil
		}
		// The plan is whatever the plan stage produced, if available.
		plan := ""
		for _, s := range res.Stages {
			if s.Stage == stagePlan {
				plan = s.Output
				break
			}
		}
		result, err := l.cfg.Coder.Code(intent, plan, wt)
		if err != nil {
			if err == coder.ErrNoProvider {
				return "coder: no LLM provider configured", err
			}
			return fmt.Sprintf("coder: %v", err), err
		}
		if result.Passed {
			res.Diff = result.Diff
			return fmt.Sprintf("coder: passed in %d round(s) (%.2fs)", len(result.Rounds), result.TotalTime.Seconds()), nil
		}
		return fmt.Sprintf("coder: %d round(s), verification did not pass", len(result.Rounds)), fmt.Errorf("coder: verification failed")
	}
}

// defaultStep returns a StepFunc that delegates the plan stage to the wired
// planner.Agent (when configured) and the code stage to the wired coder.Agent
// (when configured). Stages without a wired agent return empty. This is used
// when cfg.Planner or cfg.Coder is set and the caller did not supply a
// StepFunc — enabling L3+ autonomy without caller-provided handlers.
func (l *Loop) defaultStep() StepFunc {
	return func(stage, intent string, wt *execution.Worktree, res *Result) (string, error) {
		switch stage {
		case stagePlan:
			if l.cfg.Planner == nil {
				return "", nil
			}
			// Collect recalled memories to inform the plan.
			var memories []string
			for _, m := range res.Remembered {
				memories = append(memories, m.Content)
			}
			plan, err := l.cfg.Planner.Plan(intent, memories)
			if err != nil {
				// A missing provider is non-fatal: return an empty plan and
				// let the loop continue (the coder, if wired, still codes).
				if err == planner.ErrNoProvider {
					return "planner: no LLM provider configured", nil
				}
				return fmt.Sprintf("planner: %v", err), err
			}
			return plan, nil

		case stageCode:
			if l.cfg.Coder == nil {
				return "", nil
			}
			return l.coderStep()(stage, intent, wt, res)

		default:
			// intent and other non-creative stages: no-op.
			return "", nil
		}
	}
}

// requestApproval requests governance for the deploy (spec PROTECT). It
// returns an error if approval is denied (fail-closed); otherwise (granted or
// pending) it returns the approval and the deploy may proceed. Requires
// cfg.Appr to be non-nil.
func (l *Loop) requestApproval(intent string) (domain.Approval, error) {
	// Create a human-in-the-loop approval for the change. The ID is carried
	// through so operators can correlate the decision back to the originating
	// action via the workflow.
	ap := l.cfg.Appr.Request(intent, "loop", "deploy verified change")
	// Fail-closed: re-read the approval to pick up any decision already made.
	cur, err := l.cfg.Appr.Get(ap.ID)
	if err != nil {
		return ap, fmt.Errorf("loop: approval lookup: %w", err)
	}
	if cur.Status != "approved" {
		if cur.Status == "rejected" {
			return cur, fmt.Errorf("governance: approval %q rejected: %s", cur.ID, cur.Reason)
		}
		return cur, fmt.Errorf("governance: approval %q not approved (status: %s)", cur.ID, cur.Status)
	}
	return cur, nil
}

// observeHealthy reports whether the configured service produced no error
// events since the observe-window start. It is deterministic and read-only.
func (l *Loop) observeHealthy() bool {
	from := l.cfg.Since
	if from.IsZero() {
		from = time.Now().Add(-time.Hour)
	}
	seen := false
	for _, e := range l.cfg.Source.Events(l.cfg.Service) {
		if e.Timestamp.Before(from) {
			continue
		}
		seen = true
		if e.IsError() {
			return false
		}
	}
	// No telemetry in the window is indistinguishable from health; only report
	// healthy when there is actual event data to judge.
	return seen
}

// learn records a memory capturing the loop outcome (Continuous Learning).
func (l *Loop) learn(intent string, healthy, deployed bool) (domain.Memory, error) {
	status := "ok"
	if !healthy {
		status = "unhealthy"
	}
	safe := sanitizeIntent(intent)
	content := fmt.Sprintf("loop %s: %q → %s (level %s)", l.cfg.Level, safe, status, l.cfg.Level)
	m := domain.Memory{
		Type:    domain.MemoryLesson,
		Content: content,
		Source:  "loop",
		Scope:   l.cfg.Scope,
		Tags:    []string{"loop", "learn", l.cfg.Level.String()},
	}
	// Episodic (raw event) is recorded before the lesson (derived takeaway).
	episodic := domain.Memory{
		Type:    domain.MemoryEpisodic,
		Content: fmt.Sprintf("episodic: intent=%q level=%s deployed=%v observed_healthy=%v", safe, l.cfg.Level, deployed, healthy),
		Source:  "loop",
		Scope:   l.cfg.Scope,
		Tags:    []string{"loop", "episodic", l.cfg.Level.String()},
	}
	if l.cfg.Mem == nil {
		return m, errors.New("loop: learn requires a memory store")
	}
	// A failed episodic write does not stop the lesson from being recorded.
	if _, err := l.cfg.Mem.Add(episodic); err != nil {
		return m, err
	}
	stored, err := l.cfg.Mem.Add(m)
	if err != nil {
		return stored, err
	}
	// Continuous learning (G2): run the pattern extractor over accumulated
	// memory and surface recurring patterns above threshold as constraints.
	// Additive and nil-safe: with no extractor wired this is a no-op.
	if ex := l.cfg.Learning; ex != nil {
		if perr := l.surfacePatterns(ex); perr != nil {
			// Best-effort: surface the failure via the bus, never fail the loop.
			l.publish(eventbus.Event{Kind: eventbus.PatternSurfaced, Subject: "learning", Payload: map[string]string{"error": perr.Error()}})
		}
	}
	return stored, nil
}

// surfacePatterns runs the continuous-learning extractor over memory and writes
// a synthesized MemoryConstraint (via Extractor.Remember) for every recurring
// pattern whose count meets PatternThreshold. It is additive and best-effort: a
// single write failure does not stop the other patterns, and the first error is
// returned so the caller can surface it without failing the loop.
func (l *Loop) surfacePatterns(ex *learning.Extractor) error {
	threshold := l.cfg.PatternThreshold
	if threshold <= 0 {
		threshold = 1
	}
	patterns, err := ex.Surface(threshold)
	if err != nil {
		return err
	}
	var firstErr error
	for _, p := range patterns {
		_, werr := ex.Remember(p)
		if werr != nil {
			if firstErr == nil {
				firstErr = werr
			}
			continue
		}
		l.publish(eventbus.Event{Kind: eventbus.PatternSurfaced, Subject: p.Key, Payload: map[string]string{"count": fmt.Sprintf("%d", p.Count)}})
	}
	return firstErr
}

// sanitizeIntent neutralizes untrusted intent text before it is stored in
// engineering memory. It collapses newlines to spaces, strips control
// characters, and truncates to a reasonable length so a crafted intent cannot
// inject arbitrary content into the lesson or episodic records.
func sanitizeIntent(intent string) string {
	const maxLen = 200
	s := strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r':
			return ' '
		case r < 0x20 || r == 0x7f: // control characters
			return -1
		default:
			return r
		}
	}, intent)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return s
}
