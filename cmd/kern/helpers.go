package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/cliout"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/loop"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/schema"
	"github.com/JayveerPrajapati/kern/internal/strutil"
	"github.com/JayveerPrajapati/kern/internal/tasklife"
)

// cliflags.MaxStdinBytes caps piped input so an uncooperative pipe cannot exhaust
// memory. 64 MiB is far beyond any legitimate use (prompts, logs, source).

// toolTimeout returns the effective timeout for subprocess-running commands
// (build, validate, heal, sandbox). An unset --timeout gets a 120s default so
// a hung subprocess can never wedge a tool call forever; an explicit
// "--timeout 0" means no limit.
func toolTimeout(f flags) time.Duration {
	if f.timeoutSet && f.timeout == 0 {
		return 0
	}
	if f.timeout <= 0 {
		return 120 * time.Second
	}
	return time.Duration(f.timeout) * time.Second
}

// mcpHTTPAddr resolves the address for `kern mcp`: a positional argument wins
// over the --http flag. An empty result means stdio mode.
func mcpHTTPAddr(args []string, f flags) string {
	if len(args) > 0 {
		return args[0]
	}
	return f.http
}

// contextEngine, cliRuntimeSource, cliBoundaryProvider, analyzeChangeCLI,
// riskChangeCLI and simulateChangeCLI have been migrated to internal/app.Platform.
// The CLI now calls app.New(root) + p.Analyze/Risk/WhatIf/Verify so the
// orchestration is shared with MCP and REST instead of duplicated here.

// renderTeamText builds the standard specialist team via agents.StandardTeam
// and renders the roster plus current task states. Read-only and deterministic.
func renderTeamText(root string) (string, error) {
	_, reg, err := agents.StandardTeam()
	if err != nil {
		return "", fmt.Errorf("team: %w", err)
	}
	reg.SetTaskStore(agent.NewTaskStore(root))
	var b strings.Builder
	fmt.Fprintln(&b, "specialists:")
	for _, a := range reg.All() {
		fmt.Fprintf(&b, "  %s (role %s)\n", a.ID, a.Type)
		if len(a.Capabilities) > 0 {
			fmt.Fprintf(&b, "    capabilities: %s\n", strings.Join(a.Capabilities, ", "))
		}
	}
	// Merge the in-memory registry with the persisted store, exactly like
	// runTaskList: the registry holds this-process submissions while the store
	// holds every cross-session record — a fresh StandardTeam() always has an
	// empty in-memory map, so reading only the registry rendered a permanent
	// "tasks: 0" no matter how many tasks were persisted.
	seen := map[string]*agent.Task{}
	var tasks []*agent.Task
	for _, t := range reg.ListTasks() {
		seen[t.ID] = t
		tasks = append(tasks, t)
	}
	if st := reg.TaskStore(); st != nil {
		if persisted, lerr := st.List(); lerr == nil {
			for i := range persisted {
				t := &persisted[i]
				if _, dup := seen[t.ID]; !dup {
					seen[t.ID] = t
					tasks = append(tasks, t)
				}
			}
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	fmt.Fprintf(&b, "tasks: %d\n", len(tasks))
	for _, t := range tasks {
		fmt.Fprintf(&b, "  %s [%s] %s: %s\n", t.ID, t.State, t.Type, t.Input)
	}
	return b.String(), nil
}

// nextScheduledRun is the pure next-run computation behind kern loop
// --schedule: it parses the cron expression and returns the next fire time
// strictly after now. runLoopScheduled uses it between iterations; the unit
// tests pin parse + Next + flag wiring through it without sleeping.
func nextScheduledRun(expr string, now time.Time) (time.Time, error) {
	sched, err := loop.ParseSchedule(expr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(now), nil
}

// runLoopScheduled is the --schedule loop backend (Feature Batch I): it runs
// the closed loop repeatedly on a cron cadence until interrupted or a run
// fails hard. Each iteration is a fresh loop run at the CLI's configured
// level/mode (the existing one-shot path — runLoopCLI or runDo — so output
// and semantics per iteration are unchanged). Before each run it prints the
// next fire time. The sleep between runs is cancelled by SIGINT/SIGTERM
// (signal.NotifyContext, matching the repo's signal conventions), so Ctrl-C
// stops the loop gracefully with exit 0; a failed run propagates the error
// (exit 1). An invalid cron expression is a usage error (exit 2).
//
// CHANGE DETECTION (M6): a scheduled fire re-runs the SAME intent on a cadence
// with no regard for whether anything changed — at L2+ every fire pays a full
// LLM code-generation pass, a fresh worktree, and another memory-lesson write,
// even when the repo is byte-identical since the last fire. The last
// repo-change fingerprint is kept in memory; a fire whose repo state matches
// the previous fire's post-run state AND whose previous run produced no diff
// reports "no change — skipping" and moves to the next fire instead of
// re-running the expensive intent. When the fingerprint differs (new commits,
// edited files) the intent re-runs against the new state. The fingerprint is
// the git HEAD OID plus the working-tree status hash — cheap (two git
// subprocesses), deterministic, and stable across identical trees.
func runLoopScheduled(cmd string, f flags, root, intent string) {
	if _, err := loop.ParseSchedule(f.schedule); err != nil {
		fatalUsage("loop: --schedule %q: %v", f.schedule, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lastState := ""
	lastRunHadDiff := false
	first := true

	for {
		next, err := nextScheduledRun(f.schedule, time.Now())
		if err != nil {
			fatal("Loop: %v", err)
		}
		if next.IsZero() {
			fatal("Loop: --schedule %q: no next run within the scan horizon", f.schedule)
		}
		fmt.Printf("[schedule] next run at %s (%s)\n", next.Format(time.RFC3339), f.schedule)

		if d := time.Until(next); d > 0 {
			timer := time.NewTimer(d)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return // SIGINT/SIGTERM → graceful stop, exit 0
			case <-timer.C:
			}
		}

		// Change detection: skip a fire when the repo is identical to the
		// previous fire's post-run state and that run changed nothing. The
		// FIRST fire always runs (there is no prior state to compare).
		state := repoChangeFingerprint(root)
		if !first && state != "" && state == lastState && !lastRunHadDiff {
			fmt.Printf("[schedule] no repo change since the last run — skipping %q (fingerprint %s)\n", intent, state)
			continue
		}
		first = false

		if f.mode == "autonomous" {
			text, err := runDo(root, f.level, intent)
			if err != nil {
				fmt.Print(text)
				fatal("Loop: %v", err)
			}
			lastRunHadDiff = strings.Contains(text, "diff (")
			fmt.Print(text)
			lastState = repoChangeFingerprint(root)
			continue
		}
		text, err := runLoopCLI(root, f.level, intent)
		if err != nil {
			fmt.Print(text)
			fatal("Loop: %v", err)
		}
		lastRunHadDiff = strings.Contains(text, "diff (")
		fmt.Print(text)
		lastState = repoChangeFingerprint(root)
	}
}

// repoChangeFingerprint returns a deterministic signature of the repository's
// current state: the git HEAD OID plus the working-tree status hash. Identical
// trees produce identical fingerprints; any committed or working-tree change
// flips it. A non-git or gitless root returns "" (change detection disabled —
// the scheduled loop runs every fire, preserving pre-M6 behavior).
func repoChangeFingerprint(root string) string {
	head, err := gitDiffC(root, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	status, err := gitDiffC(root, "status", "--porcelain")
	if err != nil {
		return ""
	}
	h := sha256.New()
	h.Write(head)
	h.Write([]byte{0})
	h.Write(status)
	return hex.EncodeToString(h.Sum(nil))
}

// runLoopCLI drives the closed loop (Workflow E, autonomy-gated) against an
// intent string and returns the stage timeline plus the deployed /
// observed-healthy / learned outcome. It uses the loop's default no-op StepFunc
// (a nil step) so it runs offline and deterministically; the AI stages are
// pluggable via the existing loop.StepFunc mechanism. The autonomy level
// (L0-L5, default L0 read-only) is honored by the loop's autonomy gate.
func runLoopCLI(root, levelStr, intent string) (string, error) {
	level := loop.L0
	if levelStr != "" {
		var err error
		level, err = loop.ParseLevel(levelStr)
		if err != nil {
			return "", err
		}
	}
	p, err := app.New(root)
	if err != nil {
		return "", fmt.Errorf("could not load project: %w — run kern index first", err)
	}
	ts := tasklife.NewTaskService(p, nil).WithPRProvider(tasklife.AutoPRProvider())
	_, res, err := ts.RunLoop(intent, level)
	var b strings.Builder
	fmt.Fprintf(&b, "intent: %s\n", res.Intent)
	fmt.Fprintf(&b, "level: %s\n", res.Level)
	for _, st := range res.Stages {
		fmt.Fprintf(&b, "%s: %s", st.Stage, st.Status)
		if st.Output != "" {
			fmt.Fprintf(&b, " (%s)", st.Output)
		}
		fmt.Fprintln(&b)
	}
	fmt.Fprintf(&b, "deployed: %v\n", res.Deployed)
	fmt.Fprintf(&b, "observed-healthy: %v\n", res.ObservedHealthy)
	if res.VerifyAdvisory != "" {
		fmt.Fprintf(&b, "verify-advisory: %s\n", res.VerifyAdvisory)
	}
	if res.Learned != nil {
		fmt.Fprintf(&b, "learned: %s\n", res.Learned.ID)
	}
	if err != nil {
		return b.String(), loopFailureMessage(res, err)
	}
	return b.String(), nil
}

// runDo is the single-entry "Implement X" command (findings F-12/F-36/F-50).
// It routes through TaskService.RunDo, which runs the closed loop at L2
// (sandbox modifications) with the autonomous coder wired in as the default
// code-stage handler, so `kern do "add a cache layer"` drives the full
// understand→remember→plan→code→verify→protect→observe→learn loop without a
// caller-supplied StepFunc. The coder uses the provider-neutral LLM factory
// (KERN_LLM_PROVIDER, default local Ollama); when no provider is reachable the
// coder returns ErrNoProvider and the loop's code stage surfaces a clear error
// instead of silently no-op'ing.
// The level (default L2) controls which stages run: L0 read-only, L2 sandbox
// code, L3 PR creation, L4 deploy with approval. A caller-supplied plan is
// optional; when empty the loop's plan stage is a no-op and the coder receives
// an empty plan string (it still generates from the intent alone).
func runDo(root, levelStr, intent string) (string, error) {
	level := loop.L2
	if levelStr != "" {
		var err error
		level, err = loop.ParseLevel(levelStr)
		if err != nil {
			return "", err
		}
	}
	p, err := app.New(root)
	if err != nil {
		return "", fmt.Errorf("could not load project: %w — run kern index first", err)
	}
	// `kern do` drives the closed loop with the autonomous coder, which
	// needs a reachable LLM provider. Without one the loop silently blocks
	// inside the first Generate call (long connect/retry windows) with no
	// output at all — an apparent hang. Probe the provider up front with a
	// short timeout so the failure is a clear one-line error instead.
	providerName, err := probeLLMProvider()
	if err != nil {
		return "", fmt.Errorf("no reachable LLM provider: %w — start ollama (or set KERN_LLM_PROVIDER to a reachable provider) before using kern do", err)
	}
	ts := tasklife.NewTaskService(p, nil).WithPRProvider(tasklife.AutoPRProvider())
	_, res, err := ts.RunDo(intent, level)
	var b strings.Builder
	fmt.Fprintf(&b, "intent: %s\n", res.Intent)
	fmt.Fprintf(&b, "level: %s\n", res.Level)
	// Name the provider that actually answers — with Ollama down the chain
	// falls back to a local agent CLI (claude/opencode/codex), and the run
	// output should say which one served it.
	fmt.Fprintf(&b, "provider: %s\n", providerName)
	for _, st := range res.Stages {
		fmt.Fprintf(&b, "%s: %s", st.Stage, st.Status)
		if st.Output != "" {
			fmt.Fprintf(&b, " (%s)", st.Output)
		}
		fmt.Fprintln(&b)
	}
	if res.Diff != "" {
		fmt.Fprintf(&b, "\ndiff (%d bytes):\n%s\n", len(res.Diff), res.Diff)
	}
	fmt.Fprintf(&b, "deployed: %v\n", res.Deployed)
	fmt.Fprintf(&b, "observed-healthy: %v\n", res.ObservedHealthy)
	if res.Learned != nil {
		fmt.Fprintf(&b, "learned: %s\n", res.Learned.ID)
	}
	if err != nil {
		return b.String(), loopFailureMessage(res, err)
	}
	return b.String(), nil
}

// probeLLMProvider verifies a reachable LLM provider before a command that
// hard-depends on one. It delegates to the shared capability-aware
// llm.ProbeReachableName (host session first — the MCP ack, same session does
// the task — then agent CLIs with a cold-start-covering budget, then Ollama),
// so the CLI and the MCP server's kern_loop probe cannot drift. The auto
// chain falls back across providers, so this only fails when no provider in
// the chain answers — exactly the silent-hang condition `kern do` used to
// exhibit. It returns the name of the provider that
// answered so the caller can report which fallback actually served the run.
func probeLLMProvider() (string, error) {
	return llm.ProbeReachableName()
}

// loopFailureMessage converts a failed loop run into a one-line cause that
// names the failing stage. The loop records stage errors in the timeline (and
// returns the error alongside the result); naming the stage tells the user
// which autonomy gate to lower instead of dumping a bare error.
func loopFailureMessage(res *loop.Result, err error) error {
	if res == nil {
		return err
	}
	stage := ""
	for _, st := range res.Stages {
		if strings.HasPrefix(st.Status, "error") {
			stage = st.Stage
		}
	}
	if stage == "" {
		return err
	}
	return fmt.Errorf("loop stage %q failed: %v — rerun with --level L0 to diagnose", stage, err)
}

// runWorkflowCLI runs an intent through the agent team ( exit gate) and
// renders the step trace. A fresh run parks at the human approval gate; the
// output surfaces the approval ID and the task ID needed to resume.
func runWorkflowCLI(root, intent string) (string, error) {
	p, err := app.New(root)
	if err != nil {
		return "", err
	}
	ts := tasklife.NewTaskService(p, nil).WithPRProvider(tasklife.AutoPRProvider())
	task, err := ts.RunWorkflowDefault(intent)
	if err != nil && task == nil {
		return "", err
	}
	// A task-level failure (e.g. ErrInvalidTransition) carries the task
	// object; render the state for context but surface the error so the CLI
	// exits non-zero. Only the approval-required pause is a normal state and
	// exits 0 (matching the request-approval/approve rc=3 policy convention).
	if err != nil && agent.ApprovalID(err) == "" {
		return renderWorkflowResult(task, err), err
	}
	return renderWorkflowResult(task, err), nil
}

// runWorkflowResumeCLI resumes an approval-parked agent-team run for a task.
func runWorkflowResumeCLI(root, taskID string) (string, error) {
	p, err := app.New(root)
	if err != nil {
		return "", err
	}
	ts := tasklife.NewTaskService(p, nil).WithPRProvider(tasklife.AutoPRProvider())
	task, err := ts.RunWorkflowResume(taskID)
	if err != nil && task == nil {
		return "", err
	}
	// Same error-surfacing rule as runWorkflowCLI: invalid transitions are
	// failures (non-zero exit), approval-required is a normal pause (exit 0).
	if err != nil && agent.ApprovalID(err) == "" {
		return renderWorkflowResult(task, err), err
	}
	return renderWorkflowResult(task, err), nil
}

// renderWorkflowResult renders the task state, its selected workflow, the step
// trace, and any pending approval gate.
func renderWorkflowResult(task *agent.Task, err error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "task:     %s\n", task.ID)
	fmt.Fprintf(&b, "state:    %s\n", task.State)
	if task.WorkflowID != "" {
		fmt.Fprintf(&b, "workflow: %s\n", task.WorkflowID)
	}
	for _, st := range task.Steps {
		status := st.Status
		if status == "" {
			status = "done"
		}
		fmt.Fprintf(&b, "  - %s [%s] %s\n", st.Action, st.AgentID, status)
	}
	if id := agent.ApprovalID(err); id != "" {
		fmt.Fprintf(&b, "\napproval required: %s\n", id)
		fmt.Fprintf(&b, "resolve: kern approve %s\n", id)
		fmt.Fprintf(&b, "resume:  kern workflow --task %s\n", task.ID)
	} else if err != nil {
		fmt.Fprintf(&b, "error: %v\n", err)
	}
	return b.String()
}

// runStatsPerformance renders the process-wide metrics snapshot (findings
// F-41/F-46/F-47/F-56). The Recorder is the process-level Default() singleton;
// main() loads the prior persisted snapshot from cache.Path("metrics.json") on
// startup so metrics accumulate across CLI invocations, and saves on exit.
// --reset clears the singleton AND the persisted file (useful for a fresh
// measurement window). --json emits the structured Snapshot instead of the
// human-readable Render.
func runStatsPerformance(reset, jsonOut bool) (string, error) {
	r := metrics.Default()
	metricsPath := cache.Path("metrics.json")
	if reset {
		r.Reset()
		_ = os.Remove(metricsPath) // clear persisted state too
		_ = os.MkdirAll(cache.Dir(), 0o755)
		_ = r.Save(metricsPath) // persist the empty state
		return r.Render(), nil
	}
	if jsonOut {
		out, err := json.Marshal(r.Snapshot())
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
	return r.Render(), nil
}

// isVerifyTypes reports whether s is a comma-separated list of known
// verification check types. Used to disambiguate the high-level
// `kern verify <types>` form from the classic claims-verification form.
func isVerifyTypes(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	for _, t := range strings.Split(trimmed, ",") {
		switch strings.TrimSpace(t) {
		case "build", "test", "security", "architecture", "dependency", "reuse", "cve", "license", "secrets":
		default:
			return false
		}
	}
	return true
}

func wireRecorder() {
	_ = optimize.EnsureRecorder()
}

// loadOrBuild delegates to index.LoadOrBuild — the canonical shared
// implementation. Kept as a thin wrapper for its many CLI callers.
func loadOrBuild(root string) (*index.Index, error) {
	return index.LoadOrBuild(root)
}

// printJSON renders v as indented JSON on stdout, clipping oversized string
// fields first (the 512 KiB safety guard). The clip machinery lives in
// internal/cliout — the shared home for every --json surface's output path
// (consolidation tranche 7); a marshal failure stays a fatal error with the
// historical "printJSON: %v" phrasing.
func printJSON(v any) {
	if err := cliout.Print(os.Stdout, v); err != nil {
		fatal("printJSON: %v", err)
	}
}

// printTokenSavings writes the "kern: X -> Y tokens (saved N, P%…)" stderr
// accounting line shared by optimize/log/budget/terse. The line claims
// compression happened, so it prints ONLY on a genuine reduction
// (after < before) — equal or inflated results stay silent (F11). The
// percentage's denominator is the before count X, stated on the same line,
// so the % never appears without its denominator (L2/honest reporting).
func printTokenSavings(w io.Writer, before, after int, extra, suffix string) {
	if after >= before {
		return
	}
	fmt.Fprintf(w, "kern: %d -> %d tokens (saved %d, %.1f%%%s)%s\n",
		before, after, before-after, strutil.Pct(before, after), extra, suffix)
}

func projectLangs() string {
	ix, err := loadOrBuild(".")
	if err != nil {
		return ""
	}
	return strings.Join(ix.Languages(), ", ")
}

func fileContext(path string) string {
	content, err := code.ReadFile(path)
	if err != nil {
		return ""
	}
	return code.Summarize(path, content, 200).Render()
}

// exitError is the sentinel panic type used by fatal and fatalUsage to
// signal a process exit. main() recovers it and converts it back into the
// exit code, so the metrics-save block runs before the real exit (a direct
// os.Exit would skip it — os.Exit does not run deferred functions or any
// code after the call).
type exitError struct{ code int }

// fatal prints an error to stderr and exits with code 1 (the Unix convention
// for runtime errors). Keep fatalUsage() for usage errors. Like fatalUsage,
// it panics with the exitError sentinel instead of calling os.Exit directly
// so main() can persist metrics before the real exit.
//
// MUST be called from the main dispatch goroutine only; panicking from a
// spawned goroutine will not be recovered and will crash the process.
func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 1})
}

// fatalUsage prints an error to stderr and exits with code 2 (the Unix
// convention for usage errors). Use it for bad flags, missing required
// arguments, and unknown commands. Keep fatal() for runtime errors.
// Like fatal, it panics with the exitError sentinel instead of calling
// os.Exit directly so main() can persist metrics before the real exit.
//
// MUST be called from the main dispatch goroutine only; panicking from a
// spawned goroutine will not be recovered and will crash the process.
func fatalUsage(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 2})
}

// fatalPolicy prints an error to stderr and exits with code 3 (the
// decided-state / policy-outcome convention — see `kern exitcode`). Use it
// for approvals that are already decided and similar policy outcomes.
// Like fatal, it panics with the exitError sentinel.
func fatalPolicy(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 3})
}

// fatalFindings prints an error to stderr and exits with code 1 (findings
// tier — see `kern exitcode`). Use it for findings-family outcomes (kern
// review / kern changes / kern security findings): reported results that
// are signals for scripts and CI, not denials.
// Like fatal, it panics with the exitError sentinel.
func fatalFindings(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 1})
}

func splitNames(s string) []string {
	var out []string
	for _, n := range strings.Split(s, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func loadSchema(spec string) (*schema.Schema, error) {
	if strings.HasPrefix(spec, "{") {
		return schema.Parse(spec)
	}
	b, err := os.ReadFile(spec)
	if err != nil {
		return nil, fmt.Errorf("cannot read schema: %w", err)
	}
	return schema.Parse(string(b))
}

// hasSemantic reports whether any indexed doc carries a dense embedding.
func hasSemantic(ix *docsearch.Index) bool {
	for _, d := range ix.Docs {
		if len(d.Semantic) > 0 {
			return true
		}
	}
	return false
}

// gitDiff runs a git diff subcommand in the current directory and returns its
// stdout.
func gitDiff(args string) ([]byte, error) {
	cmd := exec.Command("git", strings.Split(args, " ")...)
	return cmd.Output()
}

// gitDiffC runs a git subcommand in the given repository root (via git -C) and
// returns its stdout. Args are passed as separate argv entries, so repository
// paths containing spaces are handled correctly.
func gitDiffC(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	return cmd.Output()
}

// gitOutput runs a git subcommand and returns its combined output.
func gitOutput(args ...string) ([]byte, error) {
	return exec.Command("git", args...).CombinedOutput()
}

// gitCommit creates a commit with the given message fed over stdin, so no
// message ever appears in a shell argument or the process table.
func gitCommit(message string) ([]byte, error) {
	cmd := exec.Command("git", "commit", "-F", "-")
	cmd.Stdin = strings.NewReader(message)
	return cmd.CombinedOutput()
}

// shortHash returns the current HEAD's short hash, or "" if none exists yet.
func shortHash() string {
	out, err := gitDiff("rev-parse --short HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// slugName derives a filesystem-safe document name from a URL, e.g.
// https://react.dev/reference/usestate -> react.dev-reference-usestate.
func slugName(rawURL string) string {
	return strutil.DocSlug(rawURL)
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// resolveRoot resolves the project root shared by the graph subcommands — the
// --root flag, else the first positional argument, else "." — and opens the
// project index, reproducing the root-resolution + index-open preamble each
// subcommand repeats. The index-open error is returned so each caller formats
// its own failure message.
func resolveRoot(f flags, args []string) (string, *index.Index, error) {
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 {
		root = args[0]
	}
	ix, err := intel.ReadIndex(root)
	if err != nil {
		return root, nil, err
	}
	return root, ix, nil
}

// projectRoot returns the command's --root value, defaulting to the current
// directory — the root-resolution preamble most subcommands repeat (previously
// an inline "default root to ." guard at every call site).
func projectRoot(f flags) string {
	if f.root != "" {
		return f.root
	}
	return "."
}

// parseFlagsOrDie parses subcommand flags, exiting with a usage error (exit
// code 2) on failure — the flag-parsing preamble every subcommand repeats
// (previously an inline err-check + fatalUsage at every call site).
func parseFlagsOrDie(rest []string) (flags, []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	return f, args
}
