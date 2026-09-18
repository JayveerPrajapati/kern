package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/loop"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/profiles"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"github.com/JayveerPrajapati/kern/internal/whatif"
	"strings"
	"time"
)

func (s *Server) handleAnalyze(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		change := argString(args, "change")
		if change == "" {
			return "", fmt.Errorf("change is required")
		}
		p, err := s.platformFor(ctx, root)
		if err != nil {
			return "", err
		}
		// MCP is the AI-agent surface: create an authoritative Task record so
		// the analysis is queryable via kern task <id> and the lifecycle
		// (context packet, risks, evidence) is persisted. The task ID is
		// appended to the output so the caller can reference it later.
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
		var t *agent.Task
		var text string
		if lensName := argString(args, "lens"); lensName != "" {
			t, text, err = ts.AnalyzeWithLens(change, lensName)
		} else {
			t, text, err = ts.Analyze(change)
		}
		if err != nil {
			return "", err
		}
		out := "ANALYSIS for: " + change + "\n" + text + fmt.Sprintf("\n[task: %s — state: %s]\n", t.ID, t.State)
		if profileName := argString(args, "profile"); profileName != "" {
			p, ok := profiles.NewRegistryWithUserProfiles(root).Select(profileName)
			if !ok {
				return "", fmt.Errorf("unknown profile %q", profileName)
			}
			out = profiles.ApplyProfile(p, out)
		}
		return out, nil

	}
}

func (s *Server) handlePlan(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		change := argString(args, "change")
		if change == "" {
			return "", fmt.Errorf("change is required")
		}
		p, err := s.platformFor(ctx, root)
		if err != nil {
			return "", err
		}
		// kern_plan now produces a structured domain.Plan via the
		// control-plane Plan workflow (analyze → memory → impact → risk →
		// architecture → plan artifact), distinct from kern_analyze.
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
		t, plan, text, err := ts.Plan(change)
		if err != nil {
			return "", err
		}
		return "PLAN for: " + change + "\n" + text + fmt.Sprintf("\n[task: %s — state: %s — %d steps, risk=%s]\n", t.ID, t.State, len(plan.ImplementationSteps), plan.Risk), nil

	}
}

func (s *Server) handleExecute(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		patch := argString(args, "patch")
		if patch == "" {
			return "", fmt.Errorf("patch is required")
		}
		// kern_execute now routes through TaskService.Execute so an
		// authoritative Task is created, governance is centralized (not
		// per-call-site), and the diff is recorded as an artifact.
		p, err := s.platformFor(ctx, root)
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
}

func (s *Server) handleVerify(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		typesArg := argString(args, "types")
		if typesArg == "" {
			typesArg = "build"
		}
		var types []string
		for _, t := range strings.Split(typesArg, ",") {
			if t = strings.TrimSpace(t); t != "" {
				types = append(types, t)
			}
		}
		// QA: reject garbage types up front — a token the engine cannot run
		// (e.g. types=123, a number coerced to "123") must error BEFORE any
		// check runs instead of degrading into a vacuous "summary: PASS"
		// where every sub-check is silently skipped. Empty stays defaulted
		// to "build" above.
		if err := validateVerifyTypes(types); err != nil {
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
		if verifyTypesExec(types) {
			if err := governance.CheckExecCommand("kern verify "+strings.Join(types, " "), resolveRoot(root)); err != nil {
				return "", fmt.Errorf("%w (set KERN_ALLOW_EXEC=1 or configure KERN_TOOLS allowlist)", err)
			}
		}
		p, err := s.platformFor(ctx, root)
		if err != nil {
			return "", err
		}
		// kern_verify now routes through TaskService so the
		// verification is recorded as an artifact on an authoritative Task.
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
		t, v, err := ts.Verify(types)
		if err != nil {
			// A FAIL verdict is a valid verification outcome, not an MCP
			// error: surface the typed verdict and per-check status so the
			// caller sees what failed instead of a bare error.
			if v.Verdict != "" || v.Build != nil || v.UnitTests != nil || v.Security != nil || v.Architecture != nil || v.Dependency != nil {
				var vb strings.Builder
				fmt.Fprintln(&vb, pii.Mask(verification.RenderCompact(v)).Text)
				fmt.Fprintf(&vb, "\n[task: %s — state: %s]\n", t.ID, t.State)
				return vb.String(), nil
			}
			return "", err
		}
		var vb strings.Builder
		fmt.Fprintf(&vb, "verdict: %s\n", v.Verdict)
		// Mask PII/secrets in the verification output text before returning it.
		fmt.Fprintf(&vb, "summary: %s\n", pii.Mask(v.Summary).Text)
		fmt.Fprintf(&vb, "\n[task: %s — state: %s]\n", t.ID, t.State)
		return vb.String(), nil

	}
}

func (s *Server) handleIncident(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		var al domain.Alert
		if err := json.Unmarshal([]byte(argString(args, "alert")), &al); err != nil {
			return "", fmt.Errorf("invalid alert JSON: %w", err)
		}
		// kern_incident now routes through TaskService.InvestigateIncident
		// so the full incident lifecycle (IngestAlert→Correlate→RootCause) creates
		// an authoritative Task with incident + root-cause artifacts.
		p, err := s.platformFor(ctx, root)
		if err != nil {
			return "", err
		}
		// Attach a runtime snapshot when provided so correlation has data.
		if snap := argString(args, "snapshot"); snap != "" {
			store, err := runtime.ParseSnapshot([]byte(snap))
			if err != nil {
				return "", fmt.Errorf("invalid snapshot JSON: %w", err)
			}
			p.WithRuntimeSource(store)
		}
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
		t, inc, text, err := ts.InvestigateIncident(al)
		if err != nil {
			return "", err
		}
		return text + fmt.Sprintf("\n[task: %s — state: %s — incident: %s]\n", t.ID, t.State, inc.ID), nil
	}
}

func (s *Server) handleWhatIf(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		change := argString(args, "change")
		if change == "" {
			return "", fmt.Errorf("change is required")
		}
		kind := argString(args, "kind")
		if kind == "" {
			kind = string(whatif.RemoveSymbol)
		}
		newTarget := argString(args, "new_target")
		p, err := s.platformFor(ctx, root)
		if err != nil {
			return "", err
		}
		// kern_what_if routes through TaskService so the impact and
		// risk are recorded as artifacts on an authoritative Task.
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
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
}

func (s *Server) handleImpact(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		change := argString(args, "change")
		if change == "" {
			return "", fmt.Errorf("change is required")
		}
		p, err := s.platformFor(ctx, root)
		if err != nil {
			return "", err
		}
		// kern_impact now produces the 11-question deterministic
		// ImpactReport via TaskService.Impact (graph-driven, no LLM).
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
		t, _, text, err := ts.Impact(change)
		if err != nil {
			return "", err
		}
		// renderImpactText already emits the "IMPACT for: <target>" header (the
		// impact renderer is shared with the CLI and REST surfaces), so prepending
		// it here produced a duplicated header (same bug as the CLI side).
		return text + fmt.Sprintf("\n[task: %s — state: %s]\n", t.ID, t.State), nil
	}
}

// handleRisk serves kern_risk: the governance risk assessment for a proposed
// change — the same TaskService.Risk behind the CLI `kern risk` command and
// REST POST /v1/risk, so all three surfaces agree (D3: the tool was the only
// missing sibling of kern_impact / kern_what_if).
func (s *Server) handleRisk(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		change := argString(args, "change")
		if change == "" {
			return "", fmt.Errorf("change is required")
		}
		p, err := s.platformFor(ctx, root)
		if err != nil {
			return "", err
		}
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
		_, text, err := ts.Risk(change)
		if err != nil {
			return "", err
		}
		return "RISK for: " + change + "\n" + text, nil
	}
}

func (s *Server) handleAgents(ctx context.Context, args map[string]any) (string, error) {
	{
		root := resolveRoot(argString(args, "root"))
		// Route through the app layer: build the shared Platform + TaskService so
		// the specialist role list and the task registry are the authoritative
		// ones (Architecture Invariant 1: interfaces don't orchestrate engines
		// directly).
		p, err := s.platformFor(ctx, root)
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
}

func (s *Server) handleLoop(ctx context.Context, args map[string]any) (string, error) {
	{
		root := resolveRoot(argString(args, "root"))
		intent := argString(args, "intent")
		if intent == "" {
			return "", fmt.Errorf("intent is required")
		}
		level := loop.L0
		if lvl := argString(args, "level"); lvl != "" {
			parsed, err := loop.ParseLevel(lvl)
			if err != nil {
				return "", err
			}
			level = parsed
		}
		// kern_loop routes through TaskService.RunLoop so the run is
		// tracked on an authoritative Task and recorded as an artifact. The
		// handler no longer orchestrates the loop engine inline. The
		// request-derived ctx is threaded into the run so a cancelled/call
		// deadline stops the loop between stages (oracle-gate ctx threading).
		p, err := s.platformFor(ctx, root)
		if err != nil {
			return "", err
		}
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
		t, res, err := ts.RunLoopContext(ctx, intent, level)
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
}

// handleDo is the MCP counterpart of `kern do`: the autonomous "Implement X"
// closed loop. Unlike kern_loop (read-only no-op stages), it wires the LLM
// coder + planner (provider-neutral factory, default local Ollama) as the
// default stage handlers, so an agent can drive
// understand→remember→plan→code→verify→protect→observe→learn from a single
// call. Default level L2 (sandbox code); L4 adds deploy-with-approval.
func (s *Server) handleDo(ctx context.Context, args map[string]any) (string, error) {
	{
		root := resolveRoot(argString(args, "root"))
		intent := argString(args, "intent")
		if intent == "" {
			return "", fmt.Errorf("intent is required")
		}
		level := loop.L2
		if lvl := argString(args, "level"); lvl != "" {
			parsed, err := loop.ParseLevel(lvl)
			if err != nil {
				return "", err
			}
			level = parsed
		}
		// QA: `kern do` hard-depends on a reachable LLM provider. Without the
		// CLI's pre-flight, a dead provider chain falls through to the local
		// agent CLIs (each up to its CLI timeout) — the observed ~180s silent
		// hang. Mirror the CLI's probeLLMProvider (cmd/kern/helpers.go): probe
		// the provider-neutral chain with a short bound and fail fast with a
		// clear one-line error before the workflow starts.
		if err := probeLLMProviderReachable(); err != nil {
			return "", fmt.Errorf("no reachable LLM provider: %w — start ollama (or set KERN_LLM_PROVIDER to a reachable provider) before using kern_do", err)
		}
		p, err := s.platformFor(ctx, root)
		if err != nil {
			return "", err
		}
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
		t, res, err := ts.RunDoContext(ctx, intent, level)
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
}

// probeLLMProviderReachable verifies a reachable LLM provider before kern_do
// starts its closed loop. It mirrors the CLI's probeLLMProvider
// (cmd/kern/helpers.go): build the provider-neutral chain (the same one the
// coder/planner agents use) and ask it a trivial question under a short
// bound. The auto chain falls back across providers, so this only fails when
// no provider in the chain answers — exactly the silent ~180s hang condition
// kern_do used to exhibit.
func probeLLMProviderReachable() error {
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

func (s *Server) handleRun(ctx context.Context, args map[string]any) (string, error) {
	{
		root := resolveRoot(argString(args, "root"))
		intent := argString(args, "intent")
		if intent == "" {
			return "", fmt.Errorf("intent is required")
		}
		p, err := s.platformFor(ctx, root)
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
}

// handleWorkflow runs an intent through the agent team ( exit gate):
// Kern selects and coordinates the specialists without the external caller
// manually sequencing it. A fresh run parks at the human approval gate before
// the first execution step — the error carries the approval ID (approval=...)
// — and the caller resumes with the same task_id after approving it.
func (s *Server) handleWorkflow(ctx context.Context, args map[string]any) (string, error) {
	root := resolveRoot(argString(args, "root"))
	p, err := s.platformFor(ctx, root)
	if err != nil {
		return "", err
	}
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())

	var task *agent.Task
	if taskID := argString(args, "task_id"); taskID != "" {
		task, err = ts.RunWorkflowResume(taskID)
	} else {
		intent := argString(args, "intent")
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

func (s *Server) handleCorrelate(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		var al domain.Alert
		if err := json.Unmarshal([]byte(argString(args, "alert")), &al); err != nil {
			return "", fmt.Errorf("invalid alert JSON: %w", err)
		}
		p, err := s.platformFor(ctx, root)
		if err != nil {
			return "", err
		}
		if snap := argString(args, "snapshot"); snap != "" {
			store, err := runtime.ParseSnapshot([]byte(snap))
			if err != nil {
				return "", fmt.Errorf("invalid snapshot JSON: %w", err)
			}
			p.WithRuntimeSource(store)
		}
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
		t, _, text, err := ts.Correlate(al)
		if err != nil {
			return "", err
		}
		return text + fmt.Sprintf("\n[task: %s — state: %s]\n", t.ID, t.State), nil
	}
}

func (s *Server) handleLearn(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		threshold := 3
		if t := argString(args, "threshold"); t != "" {
			v, err := atoiArg(t, 3)
			if err != nil {
				return "", err
			}
			threshold = v
		}
		p, err := s.platformFor(ctx, root)
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
}

func (s *Server) handleModernize(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		p, err := s.platformFor(ctx, root)
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
}

func (s *Server) handleAudit(ctx context.Context, args map[string]any) (string, error) {
	{
		root := resolveRoot(argString(args, "root"))
		// Backs the AUDIT intent: surface every firewall decision/approval from
		// the tamper-evident audit log. Render mirrors the `kern audit` CLI.
		entries, err := s.svc.Governance.Audit(ctx, root, "")
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
}

func (s *Server) handleApprove(ctx context.Context, args map[string]any) (string, error) {
	root := resolveRoot(argString(args, "root"))

	id := argString(args, "id")
	approver := argString(args, "approver")
	if approver == "" {
		approver = "mcp-user"
	}

	if id == "" {
		// List pending approvals — mirrors `kern approve` with no args.
		pending, err := s.svc.Governance.PendingApprovals(ctx, root)
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

	reject := argString(args, "reject") == "true"
	reason := argString(args, "reason")

	// Route the decision through the app layer (TaskService), mirroring
	// `kern approve`: the decision is persisted to the shared approval store
	// AND, when it gates a task parked at WAITING_FOR_APPROVAL, the task is
	// advanced to its approval-resolved state with the gate-crossing
	// transition recorded in the audit chain. An approval with no
	// gated task attached takes the plain decide path unchanged
	// (ResolveApprovalForTask leaves non-gated approvals untouched beyond
	// persisting the decision).
	p, err := s.platformFor(ctx, root)
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

// HandleMeta is the exported wrapper around handleMeta, used by the `kern meta`
// CLI subcommand so CLI and MCP use the same classifier and dispatch path.
func (s *Server) HandleMeta(ctx context.Context, args map[string]any) (string, error) {
	return s.handleMeta(ctx, args)
}

// handleMeta implements the `kern` meta-tool: it takes a natural-language
// request, classifies it via classifyMetaRequest, dispatches to the chosen
// handler, and returns the result prefixed with the classification.
func (s *Server) handleMeta(ctx context.Context, args map[string]any) (string, error) {
	request := argString(args, "request")
	if request == "" {
		return "", fmt.Errorf("request is required")
	}
	// Phase hint (P1.2): the caller declares which agent phase it is in. It
	// does not mutate server state — MCP's tools/list is stateless — so the
	// advertised surface is filtered server-wide via KERN_MCP_PHASE instead.
	// The hint is validated and echoed back so agents learn the env switch.
	phase := strings.ToLower(strings.TrimSpace(argString(args, "phase")))
	if phase != "" && !validPhase(phase) {
		return "", fmt.Errorf("phase must be one of explore|plan|edit|verify, got %q", phase)
	}
	root := argString(args, "root")
	tool, subArgs := classifyMetaRequest(request)
	viaSemantic := false
	if v, _ := subArgs[viaSemanticArg].(bool); v {
		delete(subArgs, viaSemanticArg)
		viaSemantic = true
	}
	if root != "" {
		subArgs["root"] = root
	}
	// Forward the governed-mode agent context (P1.2) so kern_meta's routed
	// retrieval sub-tools can authorize: agent_id/task/scope reach the same
	// handlers an explicit kern_explore/kern_context/kern_graph call would.
	for _, k := range []string{"agent_id", "task", "scope"} {
		if v, ok := args[k]; ok {
			subArgs[k] = v
		}
	}

	// Dispatch to the chosen handler. The handlers all share the signature
	// func(ctx, args) (string, error) and live on *Server.
	var result string
	var err error
	switch tool {
	case "kern_search":
		result, err = s.handleSearch(ctx, subArgs)
	case "kern_prose":
		result, err = s.handleProse(ctx, subArgs)
	case "kern_explore":
		result, err = s.handleExplore(ctx, subArgs)
	case "kern_code_graph":
		result, err = s.handleCodeGraph(ctx, subArgs)
	case "kern_why":
		result, err = s.handleWhy(ctx, subArgs)
	case "kern_inherits":
		result, err = s.handleInherits(ctx, subArgs)
	case "kern_near":
		result, err = s.handleNear(ctx, subArgs)
	case "kern_context":
		result, err = s.handleContext(ctx, subArgs)
	case "kern_path":
		result, err = s.handlePath(ctx, subArgs)
	case "kern_walk":
		// kern_walk is served by the near/walk handler in the tool registry.
		result, err = s.handleNear(ctx, subArgs)
	case "kern_arch":
		result, err = s.handleArch(ctx, subArgs)
	case "kern_communities":
		result, err = s.handleCommunities(ctx, subArgs)
	case "kern_hubs":
		result, err = s.handleHubs(ctx, subArgs)
	case "kern_bridges":
		result, err = s.handleBridges(ctx, subArgs)
	case "kern_dead":
		result, err = s.handleDead(ctx, subArgs)
	case "kern_cycles":
		result, err = s.handleCycles(ctx, subArgs)
	case "kern_surprising":
		result, err = s.handleSurprising(ctx, subArgs)
	case "kern_snapshot":
		result, err = s.handleSnapshot(ctx, subArgs)
	case "kern_larges":
		result, err = s.handleLarges(ctx, subArgs)
	case "kern_test_gaps":
		result, err = s.handleTestGaps(ctx, subArgs)
	case "kern_entry_points":
		result, err = s.handleEntryPoints(ctx, subArgs)
	case "kern_frameworks":
		result, err = s.handleFrameworks(ctx, subArgs)
	case "kern_churn":
		result, err = s.handleChurn(ctx, subArgs)
	case "kern_cochange":
		result, err = s.handleCochange(ctx, subArgs)
	case "kern_review":
		result, err = s.handleReview(ctx, subArgs)
	case "kern_trace":
		result, err = s.handleTrace(ctx, subArgs)
	case "kern_probe":
		result, err = s.handleProbe(ctx, subArgs)
	case "kern_retrieve":
		result, err = s.handleRetrieve(ctx, subArgs)
	case "kern_resolve":
		result, err = s.handleResolve(ctx, subArgs)
	case "kern_plan_context":
		result, err = s.handlePlanContext(ctx, subArgs)
	case "kern_mask_pii":
		result, err = s.handleMaskPII(ctx, subArgs)
	case "kern_security":
		result, err = s.handleSecurity(ctx, subArgs)
	case "kern_safe_delete":
		result, err = s.handleSafeDelete(ctx, subArgs)
	case "kern_schema_validate":
		result, err = s.handleSchemaValidate(ctx, subArgs)
	case "kern_verify_output":
		result, err = s.handleVerifyOutput(ctx, subArgs)
	case "kern_optimize_log":
		result, err = s.handleOptimizeLog(ctx, subArgs)
	case "kern_optimize_output":
		result, err = s.handleOptimizeOutput(ctx, subArgs)
	case "kern_optimize_prompt":
		result, err = s.handleOptimizePrompt(ctx, subArgs)
	case "kern_compact_file":
		result, err = s.handleCompact(ctx, subArgs)
	case "kern_project_map":
		result, err = s.handleProjectMap(ctx, subArgs)
	case "kern_pack":
		result, err = s.handlePack(ctx, subArgs)
	case "kern_buddy":
		result, err = s.handleBuddy(ctx, subArgs)
	case "kern_stats":
		result, err = s.handleStats(ctx, subArgs)
	case "kern_commitmsg":
		result, err = s.handleCommitmsg(ctx, subArgs)
	case "kern_diff_files":
		result, err = s.handleDiffFiles(ctx, subArgs)
	case "kern_doc_search":
		result, err = s.handleDocSearch(ctx, subArgs)
	case "kern_run_build":
		result, err = s.handleRunBuild(ctx, "meta", subArgs)
	case "kern_exec":
		result, err = s.handleExec(ctx, subArgs)
	case "kern_memory_recall":
		result, err = s.handleMemoryRecall(ctx, subArgs)
	case "kern_analyze":
		result, err = s.handleAnalyze(ctx, subArgs)
	case "kern_plan":
		result, err = s.handlePlan(ctx, subArgs)
	case "kern_impact":
		result, err = s.handleImpact(ctx, subArgs)
	case "kern_what_if":
		result, err = s.handleWhatIf(ctx, subArgs)
	case "kern_verify":
		result, err = s.handleVerify(ctx, subArgs)
	case "kern_incident":
		result, err = s.handleIncident(ctx, subArgs)
	case "kern_correlate":
		result, err = s.handleCorrelate(ctx, subArgs)
	case "kern_modernize":
		result, err = s.handleModernize(ctx, subArgs)
	case "kern_authorize_context":
		result, err = s.handleAuthorizeContext(ctx, subArgs)
	case "kern_health":
		result, err = s.handleHealth(ctx, subArgs)
	case "kern_onboard":
		result, err = s.handleOnboard(ctx, subArgs)
	case "kern_llm_providers":
		result, err = s.handleLLMProviders(ctx, subArgs)
	case "kern_compose":
		result, err = s.handleCompose(ctx, subArgs)
	case "kern_pre_edit":
		result, err = s.handlePreEdit(ctx, subArgs)
	case "kern_prompt_fill":
		result, err = s.handlePromptFill(ctx, subArgs)
	case "kern_semantic_diff":
		result, err = s.handleSemanticDiff(ctx, subArgs)
	case "kern_evidence_anchor":
		result, err = s.handleEvidenceAnchor(ctx, subArgs)
	case "kern_context_watch":
		result, err = s.handleContextWatch(ctx, subArgs)
	case "kern_agent_fingerprint":
		result, err = s.handleAgentFingerprint(ctx, subArgs)
	case "kern_explain":
		result, err = s.handleExplain(ctx, subArgs)
	case "kern_cross_repo_impact":
		result, err = s.handleCrossRepoImpact(ctx, subArgs)
	case "kern_memory_ranked":
		result, err = s.handleMemoryRanked(ctx, subArgs)
	case "kern_policy_dsl":
		result, err = s.handlePolicyDSL(ctx, subArgs)
	case "kern_agent_coordination":
		result, err = s.handleAgentCoordination(ctx, subArgs)
	case "kern_agent_role_rbac":
		result, err = s.handleAgentRoleRBAC(ctx, subArgs)
	case "kern_stream":
		result, err = s.handleStream(ctx, subArgs)
	case "kern_skill":
		result, err = s.handleSkill(ctx, subArgs)
	case "kern_note":
		result, err = s.handleNote(ctx, subArgs)
	default:
		// Fallback: search
		subArgs["query"] = request
		result, err = s.handleSearch(ctx, subArgs)
		tool = "kern_search"
	}
	if err != nil {
		return "", err
	}
	out := "[kern] classified as: " + tool
	if viaSemantic {
		out += " (semantic fallback)"
	}
	hint := costHintFor(tool)
	out += fmt.Sprintf(" · est %dms · %d out tokens", hint.EstMs, hint.EstTokens)
	out += "\n" + result
	if phase != "" {
		out += fmt.Sprintf("\n[phase hint: %s — set KERN_MCP_PHASE=%s to filter the advertised tool list]", phase, phase)
	}
	return out, nil
}
