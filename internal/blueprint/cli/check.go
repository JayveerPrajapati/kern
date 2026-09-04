package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/gitleaks"
	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/jscpd"
	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/approval"
	"github.com/JayveerPrajapati/kern/internal/blueprint/audit"
	approvalcheck "github.com/JayveerPrajapati/kern/internal/blueprint/checks/approval"
	resiliencecheck "github.com/JayveerPrajapati/kern/internal/blueprint/checks/resilience"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/metrics"
	"github.com/JayveerPrajapati/kern/internal/blueprint/policy"
	"github.com/JayveerPrajapati/kern/internal/blueprint/risk"
	"github.com/JayveerPrajapati/kern/internal/blueprint/sandbox"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
)

// runCheck executes the `blueprint check` command.
//
// It discovers staged changes from git, loads Blueprint config, runs the
// validation pipeline, and emits structured output.
//
// Flags:
//
//	--staged        Explicitly check staged (git diff --cached) changes.
//	                (This is the default behavior; the flag is for hook clarity.)
//	--format=mode   Output format: "json" or "terminal" (default: terminal).
//	--json          Shorthand for --format=json.
//	--repo=PATH     Repository root (default: current directory).
//	--source=SRC    Change source for the ChangeRequest.
//	--resilience    Also run resilience (fault-injection) scenarios.
//	                Opt-in because fault injection is slow; the pre-commit
//	                hook runs `blueprint check --staged` and must stay fast.
//	                WARN-only, never blocks.
//	--tests         Also run sandbox build/test in an isolated git worktree.
//	                Opt-in because it is slow; blocks on failure per the
//	                `tests` policy (default: block).
//	--isolate-network
//	                Isolate the sandbox from the host network (Linux: new
//	                network namespace; other platforms: fail unless
//	                --allow-unisolated is also given).
//	--allow-unisolated
//	                Explicitly permit an unisolated run when
//	                --isolate-network is requested but the platform cannot
//	                provide it (visible warning; never silent).
//	--require-kern  Hard-fail (exit 2) when the kern binary is missing.
//	                By default a missing kern degrades gracefully: the
//	                architecture check reports a WARN finding and the audit
//	                chain stays local-only, so gitleaks/jscpd/sandbox still
//	                run and the pipeline is never taken down by one absent
//	                subprocess.
//	--approval-id=ID
//	                Present an approved approval request for a high-risk
//	                change (P1.3 two-person rule). Obtain the id via
//	                `blueprint request-approval`, then `blueprint approve ID`.
//	--intent=TEXT   Human-readable intent for the change; recorded by the
//	                approval gate and suggested when requesting approval.
//	--agent-id=ID   Agent identity for the change (P0.4 authz gate). For
//	                --source agent this defaults to $BLUEPRINT_AGENT_ID,
//	                then "agent".
//	--task=TEXT     Task scope for the change (P0.4); sent to kern's authz
//	                gate as the task description. Defaults to --intent when
//	                absent.
//
// exitFlagHelp is the sentinel returned by the flag-parsing helpers when
// -h/--help was requested (the flag package has already printed usage); the
// caller translates it to a clean exit 0 without running the command. It is
// never a real process exit code.
const exitFlagHelp = -1

func runCheck(args []string) int {
	fl, code := parseCheckFlags(args)
	if code != 0 {
		if code == exitFlagHelp {
			return 0
		}
		return code
	}

	// Resolve output format: --json is shorthand for --format=json.
	jsonMode, code := checkOutputFormat(fl.jsonOut, fl.format)
	if code != 0 {
		return code
	}
	_ = fl.staged // --staged is the default behavior; flag exists for hook clarity

	absRoot, code := resolveRepoRoot(fl.repoRoot)
	if code != 0 {
		return code
	}

	cfg, err := policy.Load(absRoot)
	if err != nil {
		if jsonMode {
			emitErrorJSON(3, "invalid configuration: "+err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "blueprint: invalid configuration: %v\n", err)
		}
		return 3
	}

	// Non-fatal loader notices (P1-2): warnings never change the exit code.
	for _, w := range cfg.Warnings {
		fmt.Fprintf(os.Stderr, "blueprint: warning: %s\n", w)
	}

	changes, err := discoverStagedChanges(absRoot)
	if err != nil {
		if jsonMode {
			emitErrorJSON(2, "cannot discover staged changes: "+err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "blueprint: cannot discover staged changes: %v\n", err)
		}
		return 2
	}

	req := buildCheckRequest(absRoot, fl.source, changes, fl.agentID, fl.approvalID, fl.intent, fl.task)

	// Build the full check set: approval, architecture, secrets, duplication.
	client, kernVersion, code := newKernClientOrDegraded(fl.requireKern, jsonMode)
	if code != 0 {
		return code
	}

	checks := buildCheckList(cfg, client, fl.runResilience, fl.runTests, fl.isolateNetwork, fl.allowUnisolated, absRoot)

	opts := []service.Option{
		service.WithConfig(cfg.Service),
		service.WithKernVersion(kernVersion),
		service.WithPolicy(policy.NewEngine(cfg.Policy)),
		service.WithAudit(audit.NewWriter(filepath.Join(absRoot, ".blueprint", "audit", "audit.jsonl"))),
	}
	if m, err := metrics.Load(metrics.DefaultPath(absRoot)); err == nil {
		opts = append(opts, service.WithMetrics(m, metrics.DefaultPath(absRoot)))
	}
	svc := service.New(checks, opts...)

	ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration(cfg.Service.TimeoutSec))
	defer cancel()

	result := svc.Validate(ctx, req)

	if jsonMode {
		emitJSON(result)
	} else {
		emitText(result)
	}

	return result.ExitCode
}

// checkFlags carries the parsed `blueprint check` command-line flags.
type checkFlags struct {
	jsonOut         bool
	format          string
	staged          bool
	repoRoot        string
	source          string
	runResilience   bool
	runTests        bool
	isolateNetwork  bool
	allowUnisolated bool
	requireKern     bool
	approvalID      string
	intent          string
	agentID         string
	task            string
}

// parseCheckFlags parses the `blueprint check` flags. It returns the parsed
// flags and an exit code: 0 means ready to run, 2 means a usage error was
// printed, and exitFlagHelp means -h/--help was requested (the flag package
// has already rendered usage, so the caller exits 0 rather than treating help
// as a parse error).
func parseCheckFlags(args []string) (checkFlags, int) {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "shorthand for --format=json")
	format := fs.String("format", "", "output format: json|terminal (default: terminal)")
	staged := fs.Bool("staged", false, "check staged changes (git diff --cached); this is the default")
	repoRoot := fs.String("repo", "", "repository root (default: current directory)")
	source := fs.String("source", "human", "change source: agent|ide|human|refactor|dep-bot|ci")
	runResilience := fs.Bool("resilience", false, "also run resilience (fault-injection) scenarios (opt-in; slow; WARN-only)")
	runTests := fs.Bool("tests", false, "also run sandbox build/test in an isolated worktree (opt-in; slow; blocks on failure per tests policy)")
	isolateNetwork := fs.Bool("isolate-network", false, "isolate the sandbox from the host network (Linux: new network namespace; other platforms: fail unless --allow-unisolated is also given)")
	allowUnisolated := fs.Bool("allow-unisolated", false, "run without network isolation when the platform cannot provide it (explicit override for --isolate-network; prints a visible warning)")
	requireKern := fs.Bool("require-kern", false, "fail (exit 2) when the kern binary is missing instead of running in degraded mode")
	approvalID := fs.String("approval-id", "", "approved approval request id for a high-risk change (see `blueprint request-approval`)")
	intent := fs.String("intent", "", "human-readable intent for the change (recorded by the approval gate)")
	agentID := fs.String("agent-id", "", "agent identity for the change; enables the authz gate (defaults to BLUEPRINT_AGENT_ID, then \"agent\", for --source agent)")
	task := fs.String("task", "", "task scope for the change; sent to kern's authz gate as the task description (defaults to --intent when absent)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return checkFlags{}, exitFlagHelp // -h/--help: usage already printed
		}
		return checkFlags{}, 2
	}
	return checkFlags{
		jsonOut:         *jsonOut,
		format:          *format,
		staged:          *staged,
		repoRoot:        *repoRoot,
		source:          *source,
		runResilience:   *runResilience,
		runTests:        *runTests,
		isolateNetwork:  *isolateNetwork,
		allowUnisolated: *allowUnisolated,
		requireKern:     *requireKern,
		approvalID:      *approvalID,
		intent:          *intent,
		agentID:         *agentID,
		task:            *task,
	}, 0
}

// checkOutputFormat resolves the output format: --json is shorthand for
// --format=json. It prints an error and returns exit code 2 for an unknown
// format.
func checkOutputFormat(jsonOut bool, format string) (jsonMode bool, code int) {
	outFormat := "terminal"
	if jsonOut {
		outFormat = "json"
	}
	if format != "" {
		outFormat = format
	}
	if outFormat != "json" && outFormat != "terminal" {
		fmt.Fprintf(os.Stderr, "blueprint: invalid --format %q (must be json|terminal)\n", outFormat)
		return false, 2
	}
	return outFormat == "json", 0
}

// buildCheckRequest assembles the ChangeRequest for the check command. P0.4
// authz: agent-sourced changes always carry an identity so kern's authz gate
// can evaluate them — --agent-id wins, then BLUEPRINT_AGENT_ID, then a stable
// default; non-agent sources keep "". The P1.3 approval-gate inputs (approved
// request id, change intent) and the P0.4 --task authz scope ride along in
// Metadata (the least-invasive carrier; ChangeRequest has no dedicated fields
// for them); the architecture check prefers Metadata["task"] over
// Metadata["intent"] when both are present.
func buildCheckRequest(absRoot, source string, changes []domain.FileChange, agentID, approvalID, intent, task string) domain.ChangeRequest {
	req := domain.ChangeRequest{
		RepositoryRoot: absRoot,
		Source:         domain.Source(source),
		Operation:      domain.OpCommit,
		Files:          changes,
	}
	aid := agentID
	if aid == "" && req.Source == domain.SourceAgent {
		aid = defaultAgentID()
	}
	req.AgentID = aid
	if approvalID != "" || intent != "" || task != "" {
		req.Metadata = map[string]string{}
		if approvalID != "" {
			req.Metadata["approval-id"] = approvalID
		}
		if intent != "" {
			req.Metadata["intent"] = intent
		}
		if task != "" {
			req.Metadata["task"] = task
		}
	}
	return req
}

// newKernClientOrDegraded creates the kern client. When the kern binary is
// not found, Blueprint attempts to install it automatically. With
// --require-kern a missing or unresolvable binary is a hard error (exit 2);
// otherwise the pipeline degrades gracefully. After resolution, the kern
// version is checked against Blueprint's minimum requirement — an outdated
// kern is a hard error regardless of --require-kern (contract mismatch
// would cause silent misparses). P2-4: the kern version is probed once for
// provenance stamping — best-effort, an empty string on probe failure must
// never fail validation, and in degraded mode (nil client) the version
// stays "".
func newKernClientOrDegraded(requireKern, jsonMode bool) (client *kern.KernClient, kernVersion string, code int) {
	var err error
	client, err = kern.NewKernClient()
	if err != nil {
		if requireKern {
			// Explicit opt-in: a missing kern binary is a hard error (exit 2).
			if jsonMode {
				emitErrorJSON(2, "kern binary not found: "+err.Error())
			} else {
				fmt.Fprintf(os.Stderr, "blueprint: kern binary not found: %v\n", err)
			}
			return nil, "", 2
		}
		client = nil
		fmt.Fprintf(os.Stderr, "blueprint: WARN: kern binary not found — running in degraded mode (architecture check will be skipped; audit chain is local-only). Pass --require-kern to enforce.\n")
	}
	if client != nil {
		// Check minimum version: outdated kern causes contract mismatch
		// which would be a silent misparse — fail closed.
		if vErr := kern.EnsureMinVersion(client); vErr != nil {
			if jsonMode {
				emitErrorJSON(2, vErr.Error())
			} else {
				fmt.Fprintf(os.Stderr, "blueprint: %v\n", vErr)
			}
			return nil, "", 2
		}
		kernVersion, _ = client.Version()
	}
	return client, kernVersion, 0
}

// buildCheckList assembles the check set. The P1.3 two-person approval gate
// is wired FIRST so an unapproved high-risk change blocks before the file
// checks waste work (skipped entirely when the policy disables the gate,
// approval.enabled: false), followed by architecture + secrets + duplication.
// The resilience (fault-injection) and sandbox build/test checks stay opt-in
// behind --resilience / --tests: fault injection is WARN-only and never
// blocks; the build/test check blocks on failure per the tests policy and
// opts into network isolation on request (Linux: true isolation; other
// platforms: fail closed unless --allow-unisolated explicitly overrides).
func buildCheckList(cfg *policy.LoadedConfig, client *kern.KernClient, runResilience, runTests, isolateNetwork, allowUnisolated bool, absRoot string) []service.Check {
	checks := []service.Check{}
	if cfg.File.Approval.IsEnabled() {
		checks = append(checks, approvalcheck.NewCheck(approval.NewStore(absRoot), risk.LoadConfig(cfg.File.Approval)))
	}
	checks = append(checks,
		kern.NewArchitectureCheck(client),
		// T2.1: secret + duplication detection is delegated to the incumbent
		// tools (gitleaks, jscpd). Each adapter falls back to the in-house
		// check (kern sec / structural fingerprints) when its binary is
		// absent, flagged with a WARN finding.
		gitleaks.NewCheck(client),
		jscpd.NewCheck(client),
	)
	if runResilience {
		checks = append(checks, resiliencecheck.NewCheck())
	}
	if runTests {
		var testOpts []sandbox.ConfigOption
		if isolateNetwork {
			testOpts = append(testOpts, sandbox.WithNetworkIsolation())
		}
		if allowUnisolated {
			testOpts = append(testOpts, sandbox.WithAllowUnisolated())
		}
		// Wire the polyglot sandbox matrix from .blueprint/config.yaml if present.
		if len(cfg.File.Sandbox.Matrix) > 0 {
			matrix := make([]sandbox.MatrixTarget, 0, len(cfg.File.Sandbox.Matrix))
			for _, m := range cfg.File.Sandbox.Matrix {
				target := sandbox.MatrixTarget{
					Name: m.Name,
					Dir:  m.Dir,
				}
				if m.Build != "" {
					target.Build = sandbox.SplitCommand(m.Build)
				}
				if m.Test != "" {
					target.Test = sandbox.SplitCommand(m.Test)
				}
				if m.Command != "" {
					target.Command = sandbox.SplitCommand(m.Command)
				}
				matrix = append(matrix, target)
			}
			testOpts = append(testOpts, sandbox.WithMatrix(matrix))
		}
		checks = append(checks, sandbox.NewDefaultCheck(testOpts...))
	}
	return checks
}

// resolveRepoRoot resolves the --repo flag (defaulting to the current
// directory) to an absolute path. On failure it prints the error to stderr
// and returns a non-zero exit code.
func resolveRepoRoot(repoRoot string) (string, int) {
	root := repoRoot
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: cannot determine working directory: %v\n", err)
			return "", 2
		}
		root = cwd
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: invalid repository path %q: %v\n", root, err)
		return "", 2
	}
	return absRoot, 0
}

// discoverStagedChanges runs `git diff --cached --name-status` to find staged
// files, then ONE `git diff --cached --unified=0` over the whole staged set
// for line-level detail. The combined diff is split into per-file blocks
// (on `diff --git ` lines) and each block is attached to its FileChange with
// the REAL added/removed line numbers parsed from the hunk headers. Returns
// a FileChange per staged file.
func discoverStagedChanges(repoRoot string) ([]domain.FileChange, error) {
	if !isGitRepo(repoRoot) {
		return nil, fmt.Errorf("not a git repository: %s", repoRoot)
	}

	nameStatus, err := gitOutput(repoRoot, "diff", "--cached", "--name-status")
	if err != nil {
		return nil, fmt.Errorf("git diff --cached --name-status: %w", err)
	}

	if strings.TrimSpace(nameStatus) == "" {
		return nil, nil // empty staged set
	}

	// ONE unified=0 diff over the entire staged set (not one git spawn per
	// file): keeps argv bounded and avoids N subprocess launches.
	unified, err := gitOutput(repoRoot, "-c", "core.quotepath=false", "diff", "--cached", "--unified=0", "--no-ext-diff")
	if err != nil {
		return nil, fmt.Errorf("git diff --cached --unified=0: %w", err)
	}

	var changes []domain.FileChange
	for _, line := range strings.Split(strings.TrimSpace(nameStatus), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		statusCode := parts[0]
		filePath := parts[1]

		fc := domain.FileChange{Path: filePath}

		// Map git status code to Operation.
		switch {
		case strings.HasPrefix(statusCode, "A"):
			fc.Op = domain.OpWrite
		case strings.HasPrefix(statusCode, "M"):
			fc.Op = domain.OpEdit
		case strings.HasPrefix(statusCode, "D"):
			fc.Op = domain.OpDelete
		case strings.HasPrefix(statusCode, "R"):
			fc.Op = domain.OpRename
			if len(parts) >= 3 {
				fc.OldPath = parts[1]
				fc.Path = parts[2]
			}
		default:
			fc.Op = domain.OpEdit
		}

		changes = append(changes, fc)
	}

	// Attach the per-file diff blocks (by new path) to the matching FileChange.
	byPath := make(map[string]*domain.FileChange, len(changes))
	for i := range changes {
		byPath[changes[i].Path] = &changes[i]
	}
	for path, block := range splitDiffBlocks(unified) {
		fc, ok := byPath[path]
		if !ok {
			continue
		}
		fc.Diff = block
		if isBinaryDiffBlock(block) {
			// Binary files have no textual hunks: keep the block as Diff but
			// attach no line numbers.
			continue
		}
		fc.Added, fc.Removed = parseDiffLineNumbers(block)
	}

	return changes, nil
}

// splitDiffBlocks splits a combined `git diff` output into per-file blocks
// keyed by the file's new path. Blocks start on lines beginning with
// `diff --git `. The path is taken from the `+++ b/<path>` line when present
// (falling back to the b/ side of the `diff --git` header, which git uses
// for deletions and content-identical renames that carry no +++ line).
func splitDiffBlocks(diff string) map[string]string {
	blocks := make(map[string]string)
	var currentPath string
	var current []string
	flush := func() {
		if currentPath != "" && len(current) > 0 {
			blocks[currentPath] = strings.Join(current, "\n")
		}
		currentPath = ""
		current = nil
	}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			currentPath = diffHeaderNewPath(line)
		case strings.HasPrefix(line, "+++ "):
			// `+++ b/<path>` is the authoritative new path; `+++ /dev/null`
			// marks a deletion, where the diff --git header already carried it.
			if p := diffSidePath(line, "+++ "); p != "" {
				currentPath = p
			}
		}
		current = append(current, line)
	}
	flush()
	return blocks
}

// diffHeaderNewPath extracts the new-file path from a `diff --git a/.. b/..`
// header line, handling git's C-style quoting for paths containing spaces
// (e.g. `diff --git "a/foo bar.go" "b/foo bar.go"`). The b/ side is the new
// path for modifications, additions, deletions, and renames alike.
func diffHeaderNewPath(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	if strings.HasPrefix(rest, "\"") {
		parts := strings.Split(rest, "\" \"")
		if len(parts) == 2 {
			return diffSidePath(parts[1], "")
		}
		return ""
	}
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return ""
	}
	return diffSidePath(fields[1], "")
}

// diffSidePath normalizes a diff-side token (`a/<path>` or `b/<path>`, or a
// path already split out of a quoted header) to the plain repo-relative path.
// A /dev/null marker returns "".
func diffSidePath(val, prefix string) string {
	if prefix != "" {
		val = strings.TrimSpace(strings.TrimPrefix(val, prefix))
	}
	if val == "/dev/null" {
		return ""
	}
	val = strings.Trim(val, "\"")
	if len(val) >= 2 && val[1] == '/' && (val[0] == 'a' || val[0] == 'b') {
		val = val[2:]
	}
	return val
}

// isBinaryDiffBlock reports whether a diff block describes a binary change
// (git emits "Binary files a/... and b/... differ" with no hunks).
func isBinaryDiffBlock(block string) bool {
	return strings.Contains(block, "Binary files ")
}

// parseDiffLineNumbers parses `@@ -a,b +c,d @@` hunk headers from a unified
// diff and returns the REAL line numbers touched by the diff, as decimal
// strings (domain.FileChange.Added/Removed stay []string):
//
//   - added:   c..c+d-1 — the new-file lines the hunk introduces; empty when
//     d == 0 (a context-less pure-deletion hunk, `+0,0`).
//   - removed: a..a+b-1 — the old-file lines the hunk removes; empty when
//     b == 0 (a pure-addition hunk, `-0,0`).
//
// Multi-hunk diffs accumulate across all hunks. Counts omitted by git
// (`@@ -a +c @@`) mean 1.
func parseDiffLineNumbers(diff string) (added, removed []string) {
	for _, line := range strings.Split(diff, "\n") {
		h, ok := parseHunkHeader(line)
		if !ok {
			continue
		}
		for i := 0; i < h.newCount; i++ {
			added = append(added, strconv.Itoa(h.newStart+i))
		}
		for i := 0; i < h.oldCount; i++ {
			removed = append(removed, strconv.Itoa(h.oldStart+i))
		}
	}
	return added, removed
}

// hunkHeader is a parsed unified-diff hunk range.
type hunkHeader struct {
	oldStart, oldCount int // @@ -a,b ...
	newStart, newCount int // ... +c,d @@
}

// parseHunkHeader parses one `@@ -a,b +c,d @@` line. It returns false for any
// non-hunk line. Single-value forms (`@@ -a +c @@`) imply a count of 1; zero
// counts (`-0,0`, `+0,0`) are preserved for pure additions/deletions.
func parseHunkHeader(line string) (hunkHeader, bool) {
	if !strings.HasPrefix(line, "@@ -") {
		return hunkHeader{}, false
	}
	rest := line[3:]
	if end := strings.Index(rest, " @@"); end >= 0 {
		rest = rest[:end]
	}
	fields := strings.Fields(rest)
	if len(fields) != 2 {
		return hunkHeader{}, false
	}
	var h hunkHeader
	for i, f := range fields {
		start, count, ok := parseHunkRange(f)
		if !ok {
			return hunkHeader{}, false
		}
		switch i {
		case 0:
			h.oldStart, h.oldCount = start, count
		case 1:
			h.newStart, h.newCount = start, count
		}
	}
	return h, true
}

// parseHunkRange parses one hunk side: `-a,b` or `+c,d` (or `-a`/`+c` when
// the count is omitted). The sign is validated so malformed lines are
// rejected rather than misparsed.
func parseHunkRange(f string) (start, count int, ok bool) {
	if len(f) < 2 {
		return 0, 0, false
	}
	if f[0] != '-' && f[0] != '+' {
		return 0, 0, false
	}
	val := f[1:]
	startStr, countStr, hasCount := strings.Cut(val, ",")
	s, err := strconv.Atoi(startStr)
	if err != nil {
		return 0, 0, false
	}
	count = 1
	if hasCount {
		c, err := strconv.Atoi(countStr)
		if err != nil {
			return 0, 0, false
		}
		count = c
	}
	return s, count, true
}

func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	if err == nil {
		return true
	}
	// Check if it's a git worktree (gitdir file).
	_, err = gitOutput(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func timeoutDuration(sec int) time.Duration {
	if sec <= 0 {
		return 120 * time.Second
	}
	return time.Duration(sec) * time.Second
}

func emitJSON(result domain.ValidationResult) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)
}

func emitText(result domain.ValidationResult) {
	fmt.Printf("blueprint: %s (exit %d)\n", result.Status, result.ExitCode)
	fmt.Printf("correlation: %s\n", result.CorrelationID)
	fmt.Printf("duration: %dms\n", result.DurationMs)
	fmt.Printf("findings: %d (errors=%d warnings=%d blocks=%d)\n",
		result.Summary.Total, result.Summary.Errors, result.Summary.Warnings, result.Summary.Blocks)
	for _, cr := range result.Checks {
		fmt.Printf("  [%s] %s (%dms, %d findings)\n", cr.Status, cr.Name, cr.Duration, len(cr.Findings))
	}
	// P2-2: opt-in checks that did not run (e.g. resilience without
	// --resilience) are visible, never silently skipped.
	for _, name := range result.ChecksSkipped {
		fmt.Printf("  note: %s: not run (use --%s to enable)\n", name, name)
	}
	if len(result.Findings) > 0 {
		fmt.Println("\nFindings:")
		for _, f := range result.Findings {
			loc := f.File
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			fmt.Printf("  [%s] %s: %s (%s)\n", f.Severity, loc, f.Message, f.RuleID)
		}
	}
}

func emitErrorJSON(exitCode int, message string) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"status":    string(domain.StatusError),
		"exit_code": exitCode,
		"error":     message,
	})
}

// defaultAgentID returns the agent identity for agent-sourced changes
// (P0.4 authz): $BLUEPRINT_AGENT_ID when set, else the stable default
// "agent". Agent-sourced changes must always carry an identity so kern's
// authz gate can evaluate them.
func defaultAgentID() string {
	if id := os.Getenv("BLUEPRINT_AGENT_ID"); id != "" {
		return id
	}
	return "agent"
}
