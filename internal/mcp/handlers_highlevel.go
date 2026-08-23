package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/loop"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/whatif"
	"strings"
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
		p, err := app.New(root)
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
		p, err := app.New(root)
		if err != nil {
			return "", err
		}
		// Phase 6: kern_plan now produces a structured domain.Plan via the
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
		// Phase 11: kern_execute now routes through TaskService.Execute so an
		// authoritative Task is created, governance is centralized (not
		// per-call-site), and the diff is recorded as an artifact.
		p, err := app.New(root)
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
		fmt.Fprintf(&eb, "%s\n", t.Output)
		fmt.Fprintf(&eb, "diff:\n%s\n", diff)
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
		p, err := app.New(root)
		if err != nil {
			return "", err
		}
		// Phase 2.2: kern_verify now routes through TaskService so the
		// verification is recorded as an artifact on an authoritative Task.
		ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
		t, v, err := ts.Verify(types)
		if err != nil {
			return "", err
		}
		var vb strings.Builder
		fmt.Fprintf(&vb, "verdict: %s\n", v.Verdict)
		fmt.Fprintf(&vb, "summary: %s\n", v.Summary)
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
		// Phase 15: kern_incident now routes through TaskService.InvestigateIncident
		// so the full incident lifecycle (IngestAlert→Correlate→RootCause) creates
		// an authoritative Task with incident + root-cause artifacts.
		p, err := app.New(root)
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
		p, err := app.New(root)
		if err != nil {
			return "", err
		}
		// Phase 2.2: kern_what_if routes through TaskService so the impact and
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
		p, err := app.New(root)
		if err != nil {
			return "", err
		}
		// Phase 7: kern_impact now produces the 11-question deterministic
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
		p, err := app.New(root)
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
		// Phase 2.2: kern_loop routes through TaskService.RunLoop so the run is
		// tracked on an authoritative Task and recorded as an artifact. The
		// handler no longer orchestrates the loop engine inline.
		p, err := app.New(root)
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
		p, err := app.New(root)
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
			if n, err := fmt.Sscanf(t, "%d", &threshold); err != nil || n != 1 {
				threshold = 3
			}
		}
		p, err := app.New(root)
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
		p, err := app.New(root)
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
