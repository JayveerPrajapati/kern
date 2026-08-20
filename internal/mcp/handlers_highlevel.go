package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/flight"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/loop"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/verification"
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
		text, err := analyzeChange(root, change)
		if err != nil {
			return "", err
		}
		return "ANALYSIS for: " + change + "\n" + text, nil

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
		text, err := analyzeChange(root, change)
		if err != nil {
			return "", err
		}
		return "PLAN for: " + change + "\n" + text, nil

	}
}

func (s *Server) handleExecute(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		// kern_execute builds inside a sandbox worktree running arbitrary
		// host commands; it must pass the governance firewall, fail closed.
		if err := governance.CheckExec(); err != nil {
			return "", err
		}
		patch := argString(args, "patch")
		if patch == "" {
			return "", fmt.Errorf("patch is required")
		}
		wt, err := execution.NewWorktree(root)
		if err != nil {
			return "", err
		}
		defer wt.Cleanup()
		if err := wt.Apply(patch); err != nil {
			return "", err
		}
		diff, err := wt.Diff()
		if err != nil {
			return "", err
		}
		v := verification.NewEngine(wt.Dir()).Verify([]string{"build"})
		var eb strings.Builder
		fmt.Fprintf(&eb, "verdict: %s\n", v.Verdict)
		fmt.Fprintf(&eb, "summary: %s\n", v.Summary)
		fmt.Fprintf(&eb, "diff:\n%s\n", diff)
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
		v := verification.NewEngine(root).Verify(types)
		var vb strings.Builder
		fmt.Fprintf(&vb, "verdict: %s\n", v.Verdict)
		fmt.Fprintf(&vb, "summary: %s\n", v.Summary)
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
		var src runtime.Source
		src = runtime.NewStore()
		if snap := argString(args, "snapshot"); snap != "" {
			store, err := runtime.ParseSnapshot([]byte(snap))
			if err != nil {
				return "", fmt.Errorf("invalid snapshot JSON: %v", err)
			}
			src = store
		}
		eng, err := incident.NewEngine(root, src, memory.NewMemoryStore(root), governance.NewFirewall())
		if err != nil {
			return "", err
		}
		inc := eng.IngestAlert(al)
		eng.Correlate(inc)
		eng.RootCause(inc)
		var ib strings.Builder
		fmt.Fprintf(&ib, "incident: %s\n", inc.ID)
		fmt.Fprintf(&ib, "service: %s\n", inc.AffectedService)
		fmt.Fprintf(&ib, "status: %s\n", inc.Status)
		if inc.RootCause != nil {
			fmt.Fprintf(&ib, "root cause: %s\n", inc.RootCause.Summary)
		}
		fmt.Fprintf(&ib, "hypotheses: %d\n", len(inc.Hypotheses))
		return ib.String(), nil

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
		text, err := simulateChange(root, whatif.ChangeKind(kind), change, newTarget)
		if err != nil {
			return "", err
		}
		return text, nil

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
		kind := argString(args, "kind")
		if kind == "" {
			kind = string(whatif.RemoveSymbol)
		}
		newTarget := argString(args, "new_target")
		text, err := simulateChange(root, whatif.ChangeKind(kind), change, newTarget)
		if err != nil {
			return "", err
		}
		return "IMPACT for: " + change + "\n" + text, nil

	}
}

func (s *Server) handleAgents(ctx context.Context, args map[string]any) (string, error) {
	{
		root := resolveRoot(argString(args, "root"))
		_, reg, err := agents.StandardTeam()
		if err != nil {
			return "", err
		}
		reg.SetTaskStore(agent.NewTaskStore(root))
		var ab strings.Builder
		fmt.Fprintln(&ab, "specialists:")
		for _, ag := range reg.All() {
			fmt.Fprintf(&ab, "  %s (role %s)\n", ag.ID, ag.Type)
			if len(ag.Capabilities) > 0 {
				fmt.Fprintf(&ab, "    capabilities: %s\n", strings.Join(ag.Capabilities, ", "))
			}
		}
		tasks := reg.ListTasks()
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
		cfg := loop.LoopConfig{Root: root, Level: level, Mem: memory.NewMemoryStore(root), Recorder: flight.New(root)}
		l, err := loop.NewLoop(cfg)
		if err != nil {
			return "", err
		}
		res, err := l.Run(intent, nil)
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
		return lb.String(), nil

	}
}
