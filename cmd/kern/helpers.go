package main

import (
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/loop"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/schema"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
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
func unusedHelpersSentinel() {}

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
		return "", err
	}
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
	_, res, err := ts.RunLoop(intent, level)
	if err != nil {
		return "", err
	}
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
	if res.Learned != nil {
		fmt.Fprintf(&b, "learned: %s\n", res.Learned.ID)
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
		return "", err
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
		fmt.Fprintf(&b, "error: %v\n", err)
	}
	return b.String(), nil
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
		case "build", "test", "security", "architecture", "dependency":
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
// implementation (G-11). Kept as a thin wrapper for its many CLI callers.
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
	q := strings.ToLower(query)
	seen := map[string]bool{}
	var out []string
	for _, s := range ix.Symbols {
		name := s.Name
		if seen[name] {
			continue
		}
		lname := strings.ToLower(name)
		if strings.Contains(lname, q) || (len(q) >= 3 && strings.HasPrefix(lname, q[:3])) {
			seen[name] = true
			out = append(out, name)
			if len(out) >= 5 {
				break
			}
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
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(string(b))
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

func pct(before, after int) float64 {
	if before <= 0 {
		return 0
	}
	return float64(before-after) / float64(before) * 100
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

func splitLines(s string) []string {
	// Normalize Windows CRLF to LF so every line is stripped of its trailing
	// \r, then split on \n. TrimRight on the whole string alone would leave
	// the \r on all but the final line.
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
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
	u, err := url.Parse(rawURL)
	if err != nil {
		return "doc"
	}
	name := u.Hostname() + u.Path
	name = strings.TrimSuffix(name, "/")
	return sanitizeDocName(name)
}

// sanitizeDocName constrains a doc name to a safe cache filename:
// lowercase alphanumerics and dashes only, so path separators, "../" or
// absolute paths can never escape the cache root. Falls back to "doc".
func sanitizeDocName(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
		} else if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + ('a' - 'A'))
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "doc"
	}
	return out
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
