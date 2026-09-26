package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/cache"
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
	"io"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"strings"
	"syscall"
	"time"
)

// maxStdinBytes caps piped input so an uncooperative pipe cannot exhaust
// memory. 64 MiB is far beyond any legitimate use (prompts, logs, source).
const maxStdinBytes = 64 << 20

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

// readStdin returns the full piped stdin, rejecting input larger than
// maxStdinBytes instead of buffering it all. If stdin is a character device
// (interactive terminal) rather than a pipe/redirect, it returns nil immediately
// to prevent indefinite blocking.
func readStdin() ([]byte, error) {
	if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) != 0 {
		return nil, nil
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdinBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxStdinBytes {
		return nil, fmt.Errorf("stdin exceeds %d bytes", maxStdinBytes)
	}
	return b, nil
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
	tasks := reg.ListTasks()
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
func runLoopScheduled(cmd string, f flags, root, intent string) {
	if _, err := loop.ParseSchedule(f.schedule); err != nil {
		fatalUsage("loop: --schedule %q: %v", f.schedule, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

		if f.mode == "autonomous" {
			text, err := runDo(root, f.level, intent)
			if err != nil {
				fmt.Print(text)
				fatal("Loop: %v", err)
			}
			fmt.Print(text)
			continue
		}
		text, err := runLoopCLI(root, f.level, intent)
		if err != nil {
			fmt.Print(text)
			fatal("Loop: %v", err)
		}
		fmt.Print(text)
	}
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
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
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
	if err := probeLLMProvider(); err != nil {
		return "", fmt.Errorf("no reachable LLM provider: %w — start ollama (or set KERN_LLM_PROVIDER to a reachable provider) before using kern do", err)
	}
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
	_, res, err := ts.RunDo(intent, level)
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
// hard-depends on one. It mirrors the provider the coder/planner agents use
// (llm.NewProvider, env-driven with an auto chain) and asks it a trivial
// question under a short timeout. The auto chain falls back across
// providers, so this only fails when no provider in the chain answers —
// exactly the silent-hang condition `kern do` used to exhibit.
func probeLLMProvider() error {
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
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
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
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
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
// verification check types. Used to disambiguate the high-level ADR-0006
// `kern verify <types>` form from the classic claims-verification form.
func isVerifyTypes(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	for _, t := range strings.Split(trimmed, ",") {
		switch strings.TrimSpace(t) {
		case "build", "test", "security", "architecture", "dependency", "cve", "license", "secrets":
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

// suggestSymbols returns up to 5 symbol names from ix that are similar to
// query (case-insensitive substring or prefix match). Used to provide "did
// you mean" hints when a symbol lookup fails.
func suggestSymbols(ix *index.Index, query string) []string {
	if ix == nil || query == "" {
		return nil
	}
	// Ranked-search hits first: a case-variant camelCase query
	// ("sanitizeDocName" -> "SanitizeDocName") that the anchored Search
	// below misses scores strongly here (>= 150 = matches every query
	// token). The bottom dedupe keeps the list at 5 unique full names.
	var suggestions []index.Symbol
	for _, h := range intel.RankedSearchScored(ix, query, 5) {
		if h.Score >= 150 {
			suggestions = append(suggestions, h.Symbol)
		}
	}
	// V10: share the MCP server's candidate logic (handlers_graph.go) so the
	// CLI's "did you mean" set matches kern_explore/kern_graph: anchored
	// Search first, wildcard-substring fallback, deduped by full name.
	if len(suggestions) == 0 {
		suggestions = ix.Search(query, 10)
	}
	if len(suggestions) == 0 {
		suggestions = ix.Search("*"+query+"*", 10)
	}
	if len(suggestions) == 0 {
		// Last resort: the legacy 3-char-prefix heuristic (typo tolerance)
		// when even the substring search finds nothing.
		q := strings.ToLower(query)
		seen := map[string]bool{}
		for _, s := range ix.Symbols {
			full := s.FullName()
			if full == "" || seen[full] {
				continue
			}
			if len(q) >= 3 && strings.HasPrefix(strings.ToLower(full), q[:3]) {
				seen[full] = true
				suggestions = append(suggestions, s)
				if len(suggestions) >= 5 {
					break
				}
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range suggestions {
		full := s.FullName()
		if full == "" || seen[full] {
			continue
		}
		seen[full] = true
		out = append(out, full)
		if len(out) >= 5 {
			break
		}
	}
	return out
}

// fatalNoSymbol prints a "no symbol found" error with optional suggestions.
func fatalNoSymbol(symbol string, ix *index.Index) {
	msg := fmt.Sprintf("no symbol found: %s", symbol)
	if suggestions := suggestSymbols(ix, symbol); len(suggestions) > 0 {
		msg += "\n\ndid you mean one of: " + strings.Join(suggestions, ", ")
	}
	fatal("%s", msg)
}

// fatalNoSearchMatch prints a "no symbols matched" error with optional
// suggestions (the same did-you-mean UX as fatalNoSymbol) and exits 1 — the
// `kern search` no-match contract (F1): search used to exit 0 on an empty
// result, breaking the explore/graph convention that "not found" is an error
// for scripts and CI. The --json path is untouched: an empty result array is
// data, not an error, and stays exit 0.
func fatalNoSearchMatch(query string, ix *index.Index) {
	msg := fmt.Sprintf("no symbols matched: %s", query)
	if suggestions := suggestSymbols(ix, query); len(suggestions) > 0 {
		msg += "\n\ndid you mean one of: " + strings.Join(suggestions, ", ")
	}
	fatal("%s", msg)
}

// splitRange parses a git range "a..b" into (from, to). A single ref is kept
// as "from" with "to" empty, which git treats as "compare to working tree".
func splitRange(r string) (string, string) {
	if r == "" {
		return "", ""
	}
	if p := strings.SplitN(r, "..", 2); len(p) == 2 {
		return p[0], p[1]
	}
	return r, ""
}

func printJSON(v any) {
	b, err := json.MarshalIndent(clipJSONStrings(v), "", "  ")
	if err != nil {
		fatal("printJSON: %v", err)
	}
	fmt.Println(string(b))
}

// maxJSONFieldLen bounds any single string field emitted by printJSON. It is
// a safety net for --json payloads whose embedded command output can grow
// unboundedly (a `go test -v` log in a verification result measured 606 KB;
// dogfooding F4): oversized fields are truncated with a marker so the payload
// stays valid JSON. The engine-side capture clipping already bounds the common
// paths; this guard exists so no --json surface can dump multi-MB strings to
// the terminal.
const maxJSONFieldLen = 512 * 1024

// jsonTruncMarker is appended to a string field truncated by the printJSON
// safety guard, so truncated output is visibly marked rather than silently
// cut.
const jsonTruncMarker = "\n… [kern: field truncated at 512 KiB safety cap — full value kept in .kern/audit logs]"

// clipJSONStrings returns a deep copy of v in which every string longer than
// maxJSONFieldLen is truncated with jsonTruncMarker. Structure, field order,
// and non-string values (including exact int64/float values) are preserved, so
// the JSON shape is identical to an untruncated marshal except for oversized
// strings. Deterministic, no LLM.
func clipJSONStrings(v any) any {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil
	}
	return clipJSONValue(rv).Interface()
}

func clipJSONValue(rv reflect.Value) reflect.Value {
	if !rv.IsValid() {
		return rv
	}
	switch rv.Kind() {
	case reflect.String:
		if rv.Len() > maxJSONFieldLen {
			return reflect.ValueOf(rv.String()[:maxJSONFieldLen] + jsonTruncMarker)
		}
		return rv
	case reflect.Pointer:
		if rv.IsNil() {
			return rv
		}
		ev := clipJSONValue(rv.Elem())
		nv := reflect.New(ev.Type())
		nv.Elem().Set(ev)
		return nv
	case reflect.Interface:
		if rv.IsNil() {
			return rv
		}
		return clipJSONValue(rv.Elem())
	case reflect.Struct:
		nv := reflect.New(rv.Type()).Elem()
		for i := 0; i < rv.NumField(); i++ {
			if rv.Field(i).CanInterface() {
				nv.Field(i).Set(clipJSONValue(rv.Field(i)))
			}
		}
		return nv
	case reflect.Slice:
		if rv.IsNil() {
			return rv
		}
		nv := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		for i := 0; i < rv.Len(); i++ {
			nv.Index(i).Set(clipJSONValue(rv.Index(i)))
		}
		return nv
	case reflect.Map:
		if rv.IsNil() {
			return rv
		}
		nv := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			nv.SetMapIndex(iter.Key(), clipJSONValue(iter.Value()))
		}
		return nv
	default:
		return rv
	}
}

// printSavingsFooter writes the canonical savings banner as the last
// human-readable line of an optimization command: the percentage saved, the
// number of tokens removed, and the estimated USD saved at the given
// cost-per-token rate (callers pass context.CostPerToken(), which honors the
// KERN_COST_PER_TOKEN override and defaults to 1e-5 $/token). It prints
// nothing when no tokens were actually saved.
func printSavingsFooter(w io.Writer, beforeTokens, afterTokens int, costPerToken float64) {
	saved := beforeTokens - afterTokens
	if saved <= 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "[%.0f%% ──> %d tokens ──> $%.4f]\n",
		strutil.Pct(beforeTokens, afterTokens), saved, float64(saved)*costPerToken)
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
