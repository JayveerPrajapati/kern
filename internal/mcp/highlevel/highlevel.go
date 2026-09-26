// Package highlevel owns the high-level control-plane MCP tool bodies
// (kern_analyze, kern_plan, kern_execute, kern_verify, kern_incident,
// kern_what_if, kern_impact, kern_agents, kern_loop, kern_run, kern_workflow,
// kern_correlate, kern_learn, kern_modernize, kern_audit, kern_approve) as
// plain functions. Each handler routes through the app-layer TaskService
// (platform resolved via the injected PlatformFor hook) so every surface
// agrees on tasks, governance and artifacts; Audit and Approve additionally
// consult the server's governance service through injected hooks.
package highlevel

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/loop"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/meta"
	rootpkg "github.com/JayveerPrajapati/kern/internal/mcp/root"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/profiles"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// Hooks provides dependencies from the owning MCP server.
type Hooks struct {
	// PlatformFor resolves the shared application Platform for a project root
	// (adapter wires the server's platformFor). Nearly every highlevel
	// handler needs it; Audit does not.
	PlatformFor func(ctx context.Context, root string) (*app.Platform, error)
	// Audit returns the tamper-evident audit-log entries for a root (adapter
	// wires governance.ReadAuditTrail with an empty taskID). Used by
	// Audit only.
	Audit func(ctx context.Context, root string) ([]governance.AuditEntry, error)
	// PendingApprovals lists the pending approvals for a root (adapter wires
	// governance.NewFileStore(root).Pending). Used by Approve only.
	PendingApprovals func(ctx context.Context, root string) ([]domain.Approval, error)
}

// platform resolves the shared application Platform for a root through the
// injected hook. It fails closed when the adapter did not wire the hook.
func platform(ctx context.Context, h Hooks, root string) (*app.Platform, error) {
	if h.PlatformFor == nil {
		return nil, fmt.Errorf("highlevel: PlatformFor hook is not wired")
	}
	return h.PlatformFor(ctx, root)
}

// audit returns the audit-log entries for a root through the injected hook.
func audit(ctx context.Context, h Hooks, root string) ([]governance.AuditEntry, error) {
	if h.Audit == nil {
		return nil, fmt.Errorf("highlevel: Audit hook is not wired")
	}
	return h.Audit(ctx, root)
}

// pendingApprovals lists the pending approvals for a root through the
// injected hook.
func pendingApprovals(ctx context.Context, h Hooks, root string) ([]domain.Approval, error) {
	if h.PendingApprovals == nil {
		return nil, fmt.Errorf("highlevel: PendingApprovals hook is not wired")
	}
	return h.PendingApprovals(ctx, root)
}

// Analyze implements kern_analyze: runs the analysis through TaskService
// (optionally lensed), applies a profile wrapper when requested, and reports
// the authoritative persisted Task record.
func Analyze(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	change := mcpargs.ArgString(args, "change")
	if change == "" {
		return "", fmt.Errorf("change is required")
	}
	p, err := platform(ctx, h, root)
	if err != nil {
		return "", err
	}
	// MCP is the AI-agent surface: create an authoritative Task record so
	// the analysis is queryable via kern task <id> and the lifecycle
	// (context packet, risks, evidence) is persisted. The task ID is
	// appended to the output so the caller can reference it later. Task
	// persistence is unconditional here — this surface promises the record.
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider()).WithTaskPersistence(true)
	var t *agent.Task
	var text string
	if lensName := mcpargs.ArgString(args, "lens"); lensName != "" {
		t, text, err = ts.AnalyzeWithLens(change, lensName)
	} else {
		t, text, err = ts.Analyze(change)
	}
	if err != nil {
		return "", err
	}
	out := "ANALYSIS for: " + change + "\n" + text + fmt.Sprintf("\n[task: %s — state: %s]\n", t.ID, t.State)
	if profileName := mcpargs.ArgString(args, "profile"); profileName != "" {
		p, ok := profiles.NewRegistryWithUserProfiles(root).Select(profileName)
		if !ok {
			return "", fmt.Errorf("unknown profile %q", profileName)
		}
		out = profiles.ApplyProfile(p, out)
	}
	return out, nil
}

// Plan implements kern_plan: produces a structured domain.Plan via the
// control-plane Plan workflow and reports the authoritative persisted Task.
func Plan(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	change := mcpargs.ArgString(args, "change")
	if change == "" {
		return "", fmt.Errorf("change is required")
	}
	p, err := platform(ctx, h, root)
	if err != nil {
		return "", err
	}
	// kern_plan now produces a structured domain.Plan via the
	// control-plane Plan workflow (analyze → memory → impact → risk →
	// architecture → plan artifact), distinct from kern_analyze. The
	// authoritative Task record is persisted (queryable via kern task <id>).
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider()).WithTaskPersistence(true)
	t, plan, text, err := ts.Plan(change)
	if err != nil {
		return "", err
	}
	return "PLAN for: " + change + "\n" + text + fmt.Sprintf("\n[task: %s — state: %s — %d steps, risk=%s]\n", t.ID, t.State, len(plan.ImplementationSteps), plan.Risk), nil
}

// Execute implements kern_execute: applies a patch through TaskService.Execute
// and returns the masked output and diff.
func Execute(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	patch := mcpargs.ArgString(args, "patch")
	if patch == "" {
		return "", fmt.Errorf("patch is required")
	}
	// kern_execute now routes through TaskService.Execute so an
	// authoritative Task is created, governance is centralized (not
	// per-call-site), and the diff is recorded as an artifact.
	p, err := platform(ctx, h, root)
	if err != nil {
		return "", err
	}
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
	t, diff, err := ts.Execute(patch)
	if err != nil {
		return "", err
	}
	// Verify the worktree build so the execute output includes the verdict.
	var eb strings.Builder
	// Mask PII/secrets in the execute output and diff before returning them
	// to the caller (same gate as kern_exec/kern_sandbox).
	fmt.Fprintf(&eb, "%s\n", pii.Mask(t.Output).Text)
	fmt.Fprintf(&eb, "diff:\n%s\n", pii.Mask(diff).Text)
	fmt.Fprintf(&eb, "\n[task: %s — state: %s]\n", t.ID, t.State)
	return eb.String(), nil
}

// Verify implements kern_verify: runs the requested verification types
// through TaskService.Verify, with up-front type validation, the exec
// firewall for command-running types, and a rendered compact verdict on the
// fail path.
func Verify(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	typesArg := mcpargs.ArgString(args, "types")
	if typesArg == "" {
		typesArg = "build"
	}
	var types []string
	for _, t := range strings.Split(typesArg, ",") {
		if t = strings.TrimSpace(t); t != "" {
			types = append(types, t)
		}
	}
	// Opt-in compliance checks (cve/license/secrets bool args): each true
	// arg appends its check to the requested types — the compliance trio
	// runs ONLY when explicitly requested, never by default.
	if mcpargs.ArgBool(args, "cve") {
		types = append(types, "cve")
	}
	if mcpargs.ArgBool(args, "license") {
		types = append(types, "license")
	}
	if mcpargs.ArgBool(args, "secrets") {
		types = append(types, "secrets")
	}
	// QA: reject garbage types up front — a token the engine cannot run
	// (e.g. types=123, a number coerced to "123") must error BEFORE any
	// check runs instead of degrading into a vacuous "summary: PASS"
	// where every sub-check is silently skipped. Empty stays defaulted
	// to "build" above.
	if err := meta.ValidateVerifyTypes(types); err != nil {
		return "", err
	}
	// Governance: only the check types that actually execute host
	// commands (build/test/e2e/static-analysis/performance, and the CI
	// adapter) must pass the exec firewall — fail closed. The index-based
	// checks (architecture, security, dependency) never shell out, so they
	// must run WITHOUT the exec allowlist in a default MCP environment.
	// The concrete verify command is bound to any approval and persisted
	// under the resolved project root, so a HIGH/CRITICAL denial creates a
	// command-bound approval `kern approve` can resolve out-of-band
	// (oracle-gate: the legacy empty-command gate created an unresolvable
	// in-memory approval).
	if meta.VerifyTypesExec(types) {
		if err := governance.CheckExecCommand("kern verify "+strings.Join(types, " "), rootpkg.ResolveRoot(root)); err != nil {
			return "", fmt.Errorf("%w (set KERN_ALLOW_EXEC=1 or configure KERN_TOOLS allowlist)", err)
		}
	}
	p, err := platform(ctx, h, root)
	if err != nil {
		return "", err
	}
	// kern_verify now routes through TaskService so the
	// verification is recorded as an artifact on an authoritative Task.
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
	verifyStart := time.Now()
	t, v, err := ts.Verify(types)
	// Telemetry: one verification sample per kern_verify call (the server
	// recorder is long-lived; surfaces in kern stats performance).
	metrics.Default().RecordVerification(time.Since(verifyStart))
	if err != nil {
		// A FAIL verdict is a valid verification outcome, not an MCP
		// error: surface the typed verdict and per-check status so the
		// caller sees what failed instead of a bare error.
		if v.Verdict != "" || v.Build != nil || v.UnitTests != nil || v.Security != nil || v.Architecture != nil || v.Dependency != nil || v.CVE != nil || v.License != nil || v.Secrets != nil {
			var vb strings.Builder
			fmt.Fprintln(&vb, pii.Mask(verification.RenderCompact(v)).Text)
			// Calibration (Feature Batch C): aggregate confidence line on the
			// fail path too (best-effort; omitted when there is no data).
			if line := app.VerifyConfidenceLine(p.Root()); line != "" {
				fmt.Fprintf(&vb, "%s\n", line)
			}
			fmt.Fprintf(&vb, "\n[task: %s — state: %s]\n", t.ID, t.State)
			return vb.String(), nil
		}
		return "", err
	}
	var vb strings.Builder
	fmt.Fprintf(&vb, "verdict: %s\n", v.Verdict)
	// Mask PII/secrets in the verification output text before returning it.
	fmt.Fprintf(&vb, "summary: %s\n", pii.Mask(v.Summary).Text)
	// Calibration (Feature Batch C): append the aggregate confidence line at
	// the end of the rendered report (best-effort; omitted when no data).
	if line := app.VerifyConfidenceLine(p.Root()); line != "" {
		fmt.Fprintf(&vb, "%s\n", line)
	}
	fmt.Fprintf(&vb, "\n[task: %s — state: %s]\n", t.ID, t.State)
	return vb.String(), nil
}

// Incident implements kern_incident: ingest an alert (or list/add heal
// playbooks), attach a runtime snapshot when provided, and run the incident
// lifecycle through TaskService.
func Incident(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	// Heal-playbook store (Feature Batch D): list or add without an alert.
	if mcpargs.ArgBool(args, "list_playbooks") {
		return app.ListPlaybooksText(root)
	}
	if rb := mcpargs.ArgString(args, "runbook"); rb != "" {
		return app.AddPlaybookJSON(root, rb)
	}
	var al domain.Alert
	if err := json.Unmarshal([]byte(mcpargs.ArgString(args, "alert")), &al); err != nil {
		return "", fmt.Errorf("invalid alert JSON: %w", err)
	}
	// kern_incident now routes through TaskService.InvestigateIncident
	// so the full incident lifecycle (IngestAlert→Correlate→RootCause) creates
	// an authoritative Task with incident + root-cause artifacts.
	p, err := platform(ctx, h, root)
	if err != nil {
		return "", err
	}
	// Attach a runtime snapshot when provided so correlation has data.
	if snap := mcpargs.ArgString(args, "snapshot"); snap != "" {
		store, err := runtime.ParseSnapshot([]byte(snap))
		if err != nil {
			return "", fmt.Errorf("invalid snapshot JSON: %w", err)
		}
		p.WithRuntimeSource(store)
	}
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
	// correlate=true: run the incident→twin→code correlation engine and
	// render the correlation report (Feature Batch D).
	if mcpargs.ArgBool(args, "correlate") {
		t, _, text, err := ts.CorrelateCode(al)
		if err != nil {
			return "", err
		}
		return text + fmt.Sprintf("\n[task: %s — state: %s — incident: %s]\n", t.ID, t.State, al.ID), nil
	}
	t, inc, text, err := ts.InvestigateIncident(al)
	if err != nil {
		return "", err
	}
	return text + fmt.Sprintf("\n[task: %s — state: %s — incident: %s]\n", t.ID, t.State, inc.ID), nil
}

// WhatIf implements kern_what_if: simulates a change through TaskService,
// surfacing a visible not-found warning when the target symbol is absent
// from the index.
func WhatIf(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	change := mcpargs.ArgString(args, "change")
	if change == "" {
		return "", fmt.Errorf("change is required")
	}
	kind := mcpargs.ArgString(args, "kind")
	if kind == "" {
		kind = string(whatif.RemoveSymbol)
	}
	newTarget := mcpargs.ArgString(args, "new_target")
	p, err := platform(ctx, h, root)
	if err != nil {
		return "", err
	}
	// kern_what_if routes through TaskService so the impact and
	// risk are recorded as artifacts on an authoritative Task. The Task
	// record is persisted (queryable via kern task <id>).
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider()).WithTaskPersistence(true)
	t, text, err := ts.WhatIf(whatif.ChangeKind(kind), change, newTarget)
	if err != nil {
		return "", err
	}
	// QA: an unresolvable change (bare symbol not in the project index)
	// must surface a visible not-found warning instead of a clean
	// "Safe to proceed" bill — the simulation found zero affected symbols
	// because the target does not exist, not because it is isolated.
	// whatif.Simulate flags this on the Impact report; the platform text
	// renderer predates the flag, so the handler appends the warning so
	// the MCP surface never hides it.
	if t.ImpactReport != nil && t.ImpactReport.NotResolved {
		text += "\nwarning: " + t.ImpactReport.NotResolvedWarning + "\n"
	}
	return text + fmt.Sprintf("\n[task: %s — state: %s]\n", t.ID, t.State), nil
}

// Impact implements kern_impact (and the risk=true contract formerly served
// by kern_risk): graph-driven ImpactReport or governance risk assessment via
// TaskService.
func Impact(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	change := mcpargs.ArgString(args, "change")
	if change == "" {
		return "", fmt.Errorf("change is required")
	}
	p, err := platform(ctx, h, root)
	if err != nil {
		return "", err
	}
	// risk=true serves the former kern_risk contract: the governance
	// risk assessment for a proposed change — TaskService.Risk behind
	// the CLI `kern risk` command and REST POST /v1/risk, so all three
	// surfaces agree. The renderer is unchanged (RISK for: <change>).
	if mcpargs.ArgString(args, "risk") == "true" {
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
		_, text, err := ts.Risk(change)
		if err != nil {
			return "", err
		}
		return "RISK for: " + change + "\n" + text, nil
	}
	// kern_impact now produces the 11-question deterministic
	// ImpactReport via TaskService.Impact (graph-driven, no LLM). The
	// authoritative Task record is persisted (queryable via kern task <id>).
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider()).WithTaskPersistence(true)
	t, _, text, err := ts.Impact(change)
	if err != nil {
		return "", err
	}
	// renderImpactText already emits the "IMPACT for: <target>" header (the
	// impact renderer is shared with the CLI and REST surfaces), so prepending
	// it here produced a duplicated header (same bug as the CLI side).
	return text + fmt.Sprintf("\n[task: %s — state: %s]\n", t.ID, t.State), nil
}

// Agents implements kern_agents: lists the specialist roles and the task
// registry through the app layer.
func Agents(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := rootpkg.ResolveRoot(mcpargs.ArgString(args, "root"))
	// Route through the app layer: build the shared Platform + TaskService so
	// the specialist role list and the task registry are the authoritative
	// ones (Architecture Invariant 1: interfaces don't orchestrate engines
	// directly).
	p, err := platform(ctx, h, root)
	if err != nil {
		return "", err
	}
	ts := app.NewTaskService(p, nil)
	var ab strings.Builder
	fmt.Fprintln(&ab, "specialists:")
	for _, r := range ts.Agents() {
		fmt.Fprintf(&ab, "  %s (role %s)\n", r.Name, r.Role)
	}
	tasks := ts.List()
	fmt.Fprintf(&ab, "tasks: %d\n", len(tasks))
	for _, t := range tasks {
		fmt.Fprintf(&ab, "  %s [%s] %s: %s\n", t.ID, t.State, t.Type, t.Input)
	}
	return ab.String(), nil
}

// Loop implements kern_loop: runs the intent through TaskService.RunLoop
// (observe) or RunDoContext (autonomous), with a fast-fail LLM provider
// pre-flight in autonomous mode.
func Loop(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := rootpkg.ResolveRoot(mcpargs.ArgString(args, "root"))
	intent := mcpargs.ArgString(args, "intent")
	if intent == "" {
		return "", fmt.Errorf("intent is required")
	}
	// mode=observe (default) is the current kern_loop behavior (no-op
	// stage handlers, default level L0); mode=autonomous is the former
	// kern_do behavior (LLM coder + planner wired as stage handlers,
	// default level L2). The level argument works in both modes.
	mode := mcpargs.ArgString(args, "mode")
	if mode == "" {
		mode = "observe"
	}
	autonomous := mode == "autonomous"
	defaultLevel := loop.L0
	if autonomous {
		defaultLevel = loop.L2
	}
	level := defaultLevel
	if lvl := mcpargs.ArgString(args, "level"); lvl != "" {
		parsed, err := loop.ParseLevel(lvl)
		if err != nil {
			return "", err
		}
		level = parsed
	}
	// autonomous mode hard-depends on a reachable LLM provider. Without
	// the CLI's pre-flight, a dead provider chain falls through to the
	// local agent CLIs (each up to its CLI timeout) — the observed ~180s
	// silent hang. Mirror the CLI's probeLLMProvider (cmd/kern/helpers.go):
	// probe the provider-neutral chain with a short bound and fail fast
	// with a clear one-line error before the workflow starts.
	if autonomous {
		if err := ProbeLLMProviderReachable(); err != nil {
			return "", fmt.Errorf("no reachable LLM provider: %w — start ollama (or set KERN_LLM_PROVIDER to a reachable provider) before using kern_loop mode=autonomous", err)
		}
	}
	// kern_loop routes through TaskService.RunLoop (observe) / RunDo
	// (autonomous) so the run is tracked on an authoritative Task and
	// recorded as an artifact. The handler no longer orchestrates the
	// loop engine inline. The request-derived ctx is threaded into the
	// run so a cancelled/call deadline stops the loop between stages
	// (oracle-gate ctx threading).
	p, err := platform(ctx, h, root)
	if err != nil {
		return "", err
	}
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
	var t *agent.Task
	var res *loop.Result
	if autonomous {
		t, res, err = ts.RunDoContext(ctx, intent, level)
	} else {
		t, res, err = ts.RunLoopContext(ctx, intent, level)
	}
	if err != nil {
		return "", err
	}
	var lb strings.Builder
	fmt.Fprintf(&lb, "intent: %s\n", res.Intent)
	fmt.Fprintf(&lb, "level: %s\n", res.Level)
	for _, st := range res.Stages {
		fmt.Fprintf(&lb, "%s: %s", st.Stage, st.Status)
		if st.Output != "" {
			fmt.Fprintf(&lb, " (%s)", st.Output)
		}
		fmt.Fprintln(&lb)
	}
	fmt.Fprintf(&lb, "deployed: %v\n", res.Deployed)
	fmt.Fprintf(&lb, "observed-healthy: %v\n", res.ObservedHealthy)
	if res.Learned != nil {
		fmt.Fprintf(&lb, "learned: %s\n", res.Learned.ID)
	}
	fmt.Fprintf(&lb, "\n[task: %s — state: %s]\n", t.ID, t.State)
	return lb.String(), nil
}

// ProbeLLMProviderReachable verifies a reachable LLM provider before
// kern_loop mode=autonomous starts its closed loop. It mirrors the CLI's
// probeLLMProvider (cmd/kern/helpers.go): build the provider-neutral chain
// (the same one the coder/planner agents use) and ask it a trivial question
// under a short bound. The auto chain falls back across providers, so this
// only fails when no provider in the chain answers — exactly the silent ~180s
// hang condition the former kern_do used to exhibit.
func ProbeLLMProviderReachable() error {
	prov, err := llm.NewProvider()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := prov.Generate(ctx, "", "Reply with exactly: OK", llm.Options{})
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("provider returned an empty response")
	}
	return nil
}

// Run implements kern_run: runs an intent through TaskService.Run and
// renders the run summary.
func Run(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := rootpkg.ResolveRoot(mcpargs.ArgString(args, "root"))
	intent := mcpargs.ArgString(args, "intent")
	if intent == "" {
		return "", fmt.Errorf("intent is required")
	}
	p, err := platform(ctx, h, root)
	if err != nil {
		return "", err
	}
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
	res, err := ts.Run(intent)
	if err != nil {
		return "", err
	}
	var lb strings.Builder
	fmt.Fprintf(&lb, "task:      %s\n", res.TaskID)
	fmt.Fprintf(&lb, "workflow:  %s\n", res.Workflow)
	fmt.Fprintf(&lb, "intent:    %s (%s)\n", res.Intent.Type, res.Intent.Target)
	fmt.Fprintf(&lb, "risk:      %s (approval: %s)\n", res.Risk.Level, res.ApprovalState)
	fmt.Fprintf(&lb, "caps:      %s\n", strings.Join(res.Capabilities, ", "))
	fmt.Fprintf(&lb, "tools:     %s\n", strings.Join(res.Tools, ", "))
	fmt.Fprintf(&lb, "agents:    %s\n", strings.Join(res.Agents, ", "))
	fmt.Fprintf(&lb, "next:      %s\n", res.NextAction)
	if res.Precheck != nil {
		decision := "denied"
		if res.Precheck.Allowed {
			decision = "allowed"
		}
		fmt.Fprintf(&lb, "precheck:  %s\n", decision)
	}
	return lb.String(), nil
}

// Workflow runs an intent through the agent team ( exit gate):
// Kern selects and coordinates the specialists without the external caller
// manually sequencing it. A fresh run parks at the human approval gate before
// the first execution step — the error carries the approval ID (approval=...)
// — and the caller resumes with the same task_id after approving it.
func Workflow(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := rootpkg.ResolveRoot(mcpargs.ArgString(args, "root"))
	p, err := platform(ctx, h, root)
	if err != nil {
		return "", err
	}
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())

	var task *agent.Task
	if taskID := mcpargs.ArgString(args, "task_id"); taskID != "" {
		task, err = ts.RunWorkflowResume(taskID)
	} else {
		intent := mcpargs.ArgString(args, "intent")
		if intent == "" {
			return "", fmt.Errorf("intent is required")
		}
		task, err = ts.RunWorkflowDefaultContext(ctx, intent)
	}
	if err != nil && task == nil {
		return "", err
	}

	var lb strings.Builder
	fmt.Fprintf(&lb, "task:     %s\n", task.ID)
	fmt.Fprintf(&lb, "state:    %s\n", task.State)
	if task.WorkflowID != "" {
		fmt.Fprintf(&lb, "workflow: %s\n", task.WorkflowID)
	}
	for _, st := range task.Steps {
		status := st.Status
		if status == "" {
			status = "done"
		}
		fmt.Fprintf(&lb, "  - %s [%s] %s\n", st.Action, st.AgentID, status)
	}
	if agent.ApprovalID(err) != "" {
		fmt.Fprintf(&lb, "\napproval required: %s\n", agent.ApprovalID(err))
		fmt.Fprintf(&lb, "resolve with: kern_approve %s then kern_workflow with task_id=%s\n", agent.ApprovalID(err), task.ID)
	} else if err != nil {
		return lb.String(), err
	}
	return lb.String(), nil
}

// Correlate implements kern_correlate: runtime (and optionally code)
// correlation of an alert through TaskService.
func Correlate(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	var al domain.Alert
	if err := json.Unmarshal([]byte(mcpargs.ArgString(args, "alert")), &al); err != nil {
		return "", fmt.Errorf("invalid alert JSON: %w", err)
	}
	p, err := platform(ctx, h, root)
	if err != nil {
		return "", err
	}
	if snap := mcpargs.ArgString(args, "snapshot"); snap != "" {
		store, err := runtime.ParseSnapshot([]byte(snap))
		if err != nil {
			return "", fmt.Errorf("invalid snapshot JSON: %w", err)
		}
		p.WithRuntimeSource(store)
	}
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
	// code=true: extend the runtime correlation with the incident→twin→
	// code correlation report (Feature Batch D).
	if mcpargs.ArgBool(args, "code") {
		t, _, text, err := ts.CorrelateCode(al)
		if err != nil {
			return "", err
		}
		return text + fmt.Sprintf("\n[task: %s — state: %s]\n", t.ID, t.State), nil
	}
	t, _, text, err := ts.Correlate(al)
	if err != nil {
		return "", err
	}
	return text + fmt.Sprintf("\n[task: %s — state: %s]\n", t.ID, t.State), nil
}

// Learn implements kern_learn: learns lessons from the task history above a
// configurable threshold via TaskService.
func Learn(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	threshold := 3
	if t := mcpargs.ArgString(args, "threshold"); t != "" {
		v, err := mcpargs.AtoiArg(t, 3)
		if err != nil {
			return "", err
		}
		threshold = v
	}
	p, err := platform(ctx, h, root)
	if err != nil {
		return "", err
	}
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
	t, _, text, err := ts.Learn(threshold)
	if err != nil {
		return "", err
	}
	return text + fmt.Sprintf("\n[task: %s — state: %s]\n", t.ID, t.State), nil
}

// Modernize implements kern_modernize: generates a modernization plan via
// TaskService.
func Modernize(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	p, err := platform(ctx, h, root)
	if err != nil {
		return "", err
	}
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
	t, _, text, err := ts.Modernize()
	if err != nil {
		return "", err
	}
	return text + fmt.Sprintf("\n[task: %s — state: %s]\n", t.ID, t.State), nil
}

// Audit implements kern_audit: surfaces every firewall decision/approval from
// the tamper-evident audit log. Render mirrors the `kern audit` CLI.
func Audit(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := rootpkg.ResolveRoot(mcpargs.ArgString(args, "root"))
	// Backs the AUDIT intent: surface every firewall decision/approval from
	// the tamper-evident audit log. Render mirrors the `kern audit` CLI.
	entries, err := audit(ctx, h, root)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "no audit entries", nil
	}
	var ab strings.Builder
	fmt.Fprintf(&ab, "%-22s %-14s %-12s %-20s %-8s %s\n", "TIME", "AGENT", "ACTION", "RESOURCE", "APPROVED", "RESULT")
	for _, e := range entries {
		approved := "no"
		if e.Approved {
			approved = "yes"
		}
		result := e.Result
		if len(result) > 40 {
			result = result[:37] + "..."
		}
		fmt.Fprintf(&ab, "%-22s %-14s %-12s %-20s %-8s %s\n",
			e.Timestamp.Format("2006-01-02 15:04:05"),
			e.AgentID,
			e.Action,
			e.Resource,
			approved,
			result,
		)
	}
	return ab.String(), nil
}

// Approve implements kern_approve: lists pending approvals or resolves one
// (approve/reject) through TaskService.ResolveApprovalForTask, mirroring
// `kern approve`.
func Approve(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := rootpkg.ResolveRoot(mcpargs.ArgString(args, "root"))

	id := mcpargs.ArgString(args, "id")
	approver := mcpargs.ArgString(args, "approver")
	if approver == "" {
		approver = "mcp-user"
	}

	if id == "" {
		// List pending approvals — mirrors `kern approve` with no args.
		pending, err := pendingApprovals(ctx, h, root)
		if err != nil {
			return "", err
		}
		if len(pending) == 0 {
			return "no pending approvals", nil
		}
		var ab strings.Builder
		fmt.Fprintf(&ab, "%-20s %-12s %-20s %s\n", "ID", "TASK", "REQUESTER", "REASON")
		for _, a := range pending {
			reason := a.Reason
			if len(reason) > 40 {
				reason = reason[:37] + "..."
			}
			fmt.Fprintf(&ab, "%-20s %-12s %-20s %s\n", a.ID, a.TaskID, a.Requester, reason)
		}
		return ab.String(), nil
	}

	reject := mcpargs.ArgString(args, "reject") == "true"
	reason := mcpargs.ArgString(args, "reason")

	// Route the decision through the app layer (TaskService), mirroring
	// `kern approve`: the decision is persisted to the shared approval store
	// AND, when it gates a task parked at WAITING_FOR_APPROVAL, the task is
	// advanced to its approval-resolved state with the gate-crossing
	// transition recorded in the audit chain. An approval with no
	// gated task attached takes the plain decide path unchanged
	// (ResolveApprovalForTask leaves non-gated approvals untouched beyond
	// persisting the decision).
	p, err := platform(ctx, h, root)
	if err != nil {
		return "", err
	}
	ts := app.NewTaskService(p, nil).WithAgentID(approver)
	a, err := ts.ResolveApprovalForTask(id, approver, !reject, reason)
	if err != nil {
		return "", err
	}

	if reject {
		var rb strings.Builder
		fmt.Fprintf(&rb, "rejected: %s (by %s)", a.ID, approver)
		if a.TaskID != "" {
			fmt.Fprintf(&rb, "\n  task: %s marked REJECTED", a.TaskID)
		}
		return rb.String(), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "approved: %s\n", a.ID)
	fmt.Fprintf(&sb, "  task: %s\n", a.TaskID)
	fmt.Fprintf(&sb, "  approver: %s\n", a.Approver)
	if a.DecidedAt != nil {
		fmt.Fprintf(&sb, "  decided: %s\n", a.DecidedAt.Format(time.RFC3339))
	}
	if a.TaskID != "" {
		fmt.Fprintf(&sb, "  resume: kern workflow --task %s\n", a.TaskID)
	}
	return sb.String(), nil
}
