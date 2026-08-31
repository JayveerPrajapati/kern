package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/loop"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/runtime"
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
		t, text, err := ts.Analyze(change)
		if err != nil {
			return "", err
		}
		return "ANALYSIS for: " + change + "\n" + text + fmt.Sprintf("\n[task: %s — state: %s]\n", t.ID, t.State), nil

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
			typesArg = "build,test"
		}
		var types []string
		for _, t := range strings.Split(typesArg, ",") {
			if t = strings.TrimSpace(t); t != "" {
				types = append(types, t)
			}
		}
		// Verification runs build/test commands (arbitrary host code); it must
		// pass the governance firewall, fail closed.
		if err := governance.CheckExec(); err != nil {
			return "", err
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
			return "", fmt.Errorf("invalid alert JSON: %v", err)
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
				return "", fmt.Errorf("invalid snapshot JSON: %v", err)
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
		return "IMPACT for: " + change + "\n" + text + fmt.Sprintf("\n[task: %s — state: %s]\n", t.ID, t.State), nil
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
		// handler no longer orchestrates the loop engine inline.
		p, err := s.platformFor(ctx, root)
		if err != nil {
			return "", err
		}
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
		t, res, err := ts.RunLoop(intent, level)
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
		task, err = ts.RunWorkflowDefault(intent)
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
			return "", fmt.Errorf("invalid alert JSON: %v", err)
		}
		p, err := s.platformFor(ctx, root)
		if err != nil {
			return "", err
		}
		if snap := argString(args, "snapshot"); snap != "" {
			store, err := runtime.ParseSnapshot([]byte(snap))
			if err != nil {
				return "", fmt.Errorf("invalid snapshot JSON: %v", err)
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
		p, err := s.platformFor(ctx, root)
		if err != nil {
			return "", err
		}
		ts := app.NewTaskService(p, nil)
		// Backs the AUDIT intent: surface every firewall decision/approval from
		// the tamper-evident audit log. Render mirrors the `kern audit` CLI.
		entries, err := ts.AuditEntries()
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
	p, err := s.platformFor(ctx, root)
	if err != nil {
		return "", err
	}
	ts := app.NewTaskService(p, nil)

	id := argString(args, "id")
	approver := argString(args, "approver")
	if approver == "" {
		approver = "mcp-user"
	}

	if id == "" {
		// List pending approvals — mirrors `kern approve` with no args.
		pending, err := ts.PendingApprovals()
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

	a, err := ts.ResolveApproval(id, approver, !reject, reason)
	if err != nil {
		return "", err
	}

	if reject {
		return fmt.Sprintf("rejected: %s (by %s)", a.ID, approver), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "approved: %s\n", a.ID)
	fmt.Fprintf(&sb, "  task: %s\n", a.TaskID)
	fmt.Fprintf(&sb, "  approver: %s\n", a.Approver)
	if a.DecidedAt != nil {
		fmt.Fprintf(&sb, "  decided: %s\n", a.DecidedAt.Format(time.RFC3339))
	}
	return sb.String(), nil
}

// HandleMeta is the exported wrapper around handleMeta, used by the `kern meta`
// CLI subcommand so CLI and MCP use the same classifier and dispatch path.
func (s *Server) HandleMeta(ctx context.Context, args map[string]any) (string, error) {
	return s.handleMeta(ctx, args)
}

// classifyMetaRequest maps a natural-language request to the kern_* tool name
// that best answers it, using deterministic keyword matching. It returns the
// chosen tool name plus the derived arguments to pass to that tool's handler.
// extractSymbol pulls a candidate symbol name from a natural-language request:
// quoted text ("dispatch" or `dispatch`), or a CamelCase / dotted identifier
// token. It mirrors the legacy closure that lived inside classifyMetaRequest.
func extractSymbol(request, low string) string {
	// Quoted: "dispatch" or `dispatch`
	if i := strings.IndexAny(low, "\"`"); i >= 0 {
		q := low[i]
		j := strings.IndexByte(low[i+1:], q)
		if j > 0 {
			return request[i+1 : i+1+j]
		}
	}
	// CamelCase token: dispatch, Server.dispatch, NewServer
	for _, word := range strings.Fields(request) {
		w := strings.Trim(word, ".,;:!?()[]{}\"`'")
		if w == "" {
			continue
		}
		// Contains a dot (qualified) or has mixed case (CamelCase) and looks like an ident
		if strings.Contains(w, ".") {
			return w
		}
		hasUpper, hasLower := false, false
		for _, r := range w {
			if r >= 'A' && r <= 'Z' {
				hasUpper = true
			}
			if r >= 'a' && r <= 'z' {
				hasLower = true
			}
		}
		if hasUpper && hasLower && len(w) > 2 {
			return w
		}
	}
	return ""
}

// extractAfterColon returns the text after the first colon in the request, or
// the whole request when there is none. Used by log/mask/compress-type tools
// that receive their payload inline after a colon.
func extractAfterColon(request, low string) string {
	if i := strings.Index(low, ":"); i >= 0 && i+1 < len(request) {
		return strings.TrimSpace(request[i+1:])
	}
	return request
}

// withSymbol attaches the symbol extracted from the request to args, or
// reroutes to the fallback tool with the full request as its query when no
// symbol is present. A non-empty fallback is required for the reroute; a tool
// that tolerates a missing symbol passes fallback="" and keeps its governance.
func withSymbol(request, low, tool, fallback string, args map[string]any) (string, map[string]any) {
	if sym := extractSymbol(request, low); sym != "" {
		args["symbol"] = sym
		return tool, args
	}
	if fallback != "" {
		args["query"] = request
		return fallback, args
	}
	return tool, args
}

// classifyOptimizeTools routes the safety/PII and prompt/log compress cases.
func classifyOptimizeTools(low, request string) (string, map[string]any, bool) {
	switch {
	case strings.Contains(low, "mask") && (strings.Contains(low, "secret") || strings.Contains(low, "pii")):
		return "kern_mask_pii", map[string]any{"text": extractAfterColon(request, low)}, true
	case strings.Contains(low, "compress") && strings.Contains(low, "log"):
		return "kern_optimize_log", map[string]any{"log": extractAfterColon(request, low)}, true
	case strings.Contains(low, "compress") && (strings.Contains(low, "output") || strings.Contains(low, "response") || strings.Contains(low, "reply")):
		return "kern_optimize_output", map[string]any{"text": extractAfterColon(request, low)}, true
	case strings.Contains(low, "compress") && strings.Contains(low, "prompt"):
		return "kern_optimize_prompt", map[string]any{"prompt": extractAfterColon(request, low)}, true
	case strings.Contains(low, "security") || strings.Contains(low, "scan") && strings.Contains(low, "vulnerab"):
		return "kern_security", map[string]any{}, true
	case strings.Contains(low, "schema") || strings.Contains(low, "validate json"):
		return "kern_schema_validate", map[string]any{}, true
	}
	return "", nil, false
}

// classifyWorkflowTools routes the high-level orchestration cases.
func classifyWorkflowTools(low, request string) (string, map[string]any, bool) {
	switch {
	case strings.Contains(low, "what if") || strings.Contains(low, "simulate") || strings.Contains(low, "remove symbol"):
		return "kern_what_if", map[string]any{"change": request}, true
	case strings.Contains(low, "what breaks") || strings.Contains(low, "impact") || (strings.Contains(low, "change") && !strings.Contains(low, "analyze")):
		return "kern_impact", map[string]any{"change": request}, true
	case strings.Contains(low, "analyze") || strings.Contains(low, "propose"):
		return "kern_analyze", map[string]any{"change": request}, true
	case strings.Contains(low, "plan") && !strings.Contains(low, "implementation plan"):
		return "kern_plan", map[string]any{"change": request}, true
	case strings.Contains(low, "incident"):
		return "kern_incident", map[string]any{}, true
	case strings.Contains(low, "correlate"):
		return "kern_correlate", map[string]any{}, true
	case strings.Contains(low, "modernize"):
		return "kern_modernize", map[string]any{}, true
	case strings.Contains(low, "verify") && strings.Contains(low, "claim"):
		return "kern_verify_output", map[string]any{"text": extractAfterColon(request, low)}, true
	case strings.Contains(low, "verify"):
		return "kern_verify", map[string]any{}, true
	}
	return "", nil, false
}

// classifyArchTools routes the architecture/subsystem inspection cases.
func classifyArchTools(low, request string) (string, map[string]any, bool) {
	switch {
	case strings.Contains(low, "architecture") || strings.Contains(low, "overview") || strings.Contains(low, "subsystem"):
		return "kern_arch", map[string]any{}, true
	case strings.Contains(low, "communit") || strings.Contains(low, "cluster"):
		return "kern_communities", map[string]any{}, true
	case strings.Contains(low, "hub") || strings.Contains(low, "hotspot") || strings.Contains(low, "most depended"):
		return "kern_hubs", map[string]any{}, true
	case strings.Contains(low, "bridge") || strings.Contains(low, "coupling"):
		return "kern_bridges", map[string]any{}, true
	case strings.Contains(low, "dead code") || strings.Contains(low, "unused"):
		return "kern_dead", map[string]any{}, true
	case strings.Contains(low, "largest") || strings.Contains(low, "god function") || strings.Contains(low, "biggest"):
		return "kern_larges", map[string]any{}, true
	case strings.Contains(low, "test gap") || strings.Contains(low, "coverage"):
		return "kern_test_gaps", map[string]any{}, true
	case strings.Contains(low, "entry point") || strings.Contains(low, "handler") || strings.Contains(low, "route"):
		return "kern_entry_points", map[string]any{}, true
	case strings.Contains(low, "framework") || strings.Contains(low, "library") || strings.Contains(low, "detect stack"):
		return "kern_frameworks", map[string]any{}, true
	case strings.Contains(low, "churn") || strings.Contains(low, "changed most"):
		return "kern_churn", map[string]any{}, true
	case strings.Contains(low, "cochange") || strings.Contains(low, "co-change") || strings.Contains(low, "lockstep"):
		return "kern_cochange", map[string]any{}, true
	case strings.Contains(low, "diff") && (strings.Contains(low, "file") || strings.Contains(low, "compare")):
		return "kern_diff_files", map[string]any{}, true
	case strings.Contains(low, "review") || strings.Contains(low, "pr "):
		return "kern_review", map[string]any{}, true
	}
	return "", nil, false
}

// classifyGovernanceTools routes the authorized-context case; it must be
// consulted after the architecture cases and before the symbol-level graph
// cases to preserve the original switch's precedence.
func classifyGovernanceTools(low, request string) (string, map[string]any, bool) {
	switch {
	case strings.Contains(low, "authorize") || strings.Contains(low, "authorized") ||
		strings.Contains(low, "allowed to see") || strings.Contains(low, "permitted") ||
		strings.Contains(low, "what can i"):
		return "kern_authorize_context", map[string]any{"task": request}, true
	}
	return "", nil, false
}

// classifyGraphTools routes the symbol-level graph/explore cases, extracting a
// symbol from the request when one is present and falling back to kern_search.
func classifyGraphTools(low, request string) (string, map[string]any, bool) {
	switch {
	case strings.Contains(low, "how does") || strings.Contains(low, "understand") || strings.Contains(low, "explain"):
		tool, args := withSymbol(request, low, "kern_explore", "kern_search", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "why does") || strings.Contains(low, "why is") || strings.Contains(low, "rationale"):
		tool, args := withSymbol(request, low, "kern_why", "kern_search", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "callers") || strings.Contains(low, "who calls") || strings.Contains(low, "call graph"):
		tool, args := withSymbol(request, low, "kern_code_graph", "kern_search", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "inherit") || strings.Contains(low, "hierarchy") || strings.Contains(low, "extends") || strings.Contains(low, "implements"):
		tool, args := withSymbol(request, low, "kern_inherits", "kern_search", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "path from") || strings.Contains(low, "call path") || strings.Contains(low, "shortest path"):
		return "kern_path", map[string]any{}, true
	case strings.Contains(low, "near") || strings.Contains(low, "depends on") || strings.Contains(low, "neighborhood"):
		tool, args := withSymbol(request, low, "kern_near", "kern_search", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "context for") || strings.Contains(low, "source slice") || strings.Contains(low, "source for"):
		tool, args := withSymbol(request, low, "kern_context", "kern_search", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "trace") && (strings.Contains(low, "stack") || strings.Contains(low, "pprof")):
		return "kern_trace", map[string]any{}, true
	case strings.Contains(low, "probe") || strings.Contains(low, "what does this touch"):
		return "kern_probe", map[string]any{"task": request}, true
	}
	return "", nil, false
}

// classifyProjectTools routes the project-level utility cases.
func classifyProjectTools(low, request string) (string, map[string]any, bool) {
	switch {
	case strings.Contains(low, "project map") || strings.Contains(low, "layout") || strings.Contains(low, "structure") && strings.Contains(low, "project"):
		return "kern_project_map", map[string]any{}, true
	case strings.Contains(low, "pack") || strings.Contains(low, "bundle"):
		return "kern_pack", map[string]any{}, true
	case strings.Contains(low, "compact") && strings.Contains(low, "file"):
		return "kern_compact_file", map[string]any{}, true
	case strings.Contains(low, "buddy") || strings.Contains(low, "onboard") || strings.Contains(low, "getting started"):
		return "kern_buddy", map[string]any{}, true
	case strings.Contains(low, "stats") || strings.Contains(low, "savings") || strings.Contains(low, "token count"):
		return "kern_stats", map[string]any{}, true
	case strings.Contains(low, "commit message") || strings.Contains(low, "commitmsg"):
		return "kern_commitmsg", map[string]any{}, true
	case strings.Contains(low, "memory") || strings.Contains(low, "remember") || strings.Contains(low, "lesson"):
		return "kern_memory_recall", map[string]any{"prompt": request}, true
	case strings.Contains(low, "docs") || strings.Contains(low, "documentation"):
		return "kern_doc_search", map[string]any{"query": request}, true
	case strings.Contains(low, "build") || strings.Contains(low, "test") || strings.Contains(low, "lint"):
		return "kern_run_build", map[string]any{}, true
	case strings.Contains(low, "exec") || strings.Contains(low, "run script") || strings.Contains(low, "run code"):
		return "kern_exec", map[string]any{}, true
	case strings.Contains(low, "safe delete") || strings.Contains(low, "delete symbol") || strings.Contains(low, "can i delete"):
		tool, args := withSymbol(request, low, "kern_safe_delete", "", map[string]any{})
		return tool, args, true
	case strings.Contains(low, "rename") || strings.Contains(low, "refactor name"):
		return "kern_rename", map[string]any{}, true
	}
	return "", nil, false
}

// classifyMetaRequest maps a natural-language request to the kern_* tool name
// that best answers it, using deterministic keyword matching. It returns the
// chosen tool name plus the derived arguments to pass to that tool's handler.
func classifyMetaRequest(request string) (string, map[string]any) {
	low := strings.ToLower(request)
	// The sub-routers are consulted in the same order as the original
	// monolithic switch (safety/optimize -> workflows -> architecture ->
	// governance -> symbol graph -> project), so classification outcomes are
	// unchanged; anything unmatched still falls back to kern_search.
	if t, a, ok := classifyOptimizeTools(low, request); ok {
		return t, a
	}
	if t, a, ok := classifyWorkflowTools(low, request); ok {
		return t, a
	}
	if t, a, ok := classifyArchTools(low, request); ok {
		return t, a
	}
	if t, a, ok := classifyGovernanceTools(low, request); ok {
		return t, a
	}
	if t, a, ok := classifyGraphTools(low, request); ok {
		return t, a
	}
	if t, a, ok := classifyProjectTools(low, request); ok {
		return t, a
	}
	return "kern_search", map[string]any{"query": request}
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
	default:
		// Fallback: search
		subArgs["query"] = request
		result, err = s.handleSearch(ctx, subArgs)
		tool = "kern_search"
	}
	if err != nil {
		return "", err
	}
	out := "[kern] classified as: " + tool + "\n" + result
	if phase != "" {
		out += fmt.Sprintf("\n[phase hint: %s — set KERN_MCP_PHASE=%s to filter the advertised tool list]", phase, phase)
	}
	return out, nil
}
