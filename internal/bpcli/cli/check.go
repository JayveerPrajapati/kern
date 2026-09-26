package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/audit"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/sandbox"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
	"github.com/JayveerPrajapati/kern/internal/bppolicy/policy"
	"github.com/JayveerPrajapati/kern/internal/bppolicy/risk"
	"github.com/JayveerPrajapati/kern/internal/bpreceipt/metrics"
	"github.com/JayveerPrajapati/kern/internal/gates"
	resiliencecheck "github.com/JayveerPrajapati/kern/internal/resilience"
	"github.com/JayveerPrajapati/kern/internal/scanners/gitleaks"
	"github.com/JayveerPrajapati/kern/internal/scanners/jscpd"
)

// runCheck executes the `blueprint check` command.
//
// It discovers staged changes from git, loads Blueprint config, runs the
// validation pipeline, and emits structured output.
//
// Flags:
//
//	--ci            CI mode: emit a machine-readable JSON verdict on stdout
//	                ({passed, checks[], evidence}) and exit 0 when the check
//	                passes, 1 when it fails (usage errors stay exit 2). The
//	                verdict is the same validation pipeline as the default
//	                run — only the output shape and exit mapping differ.
//	--staged        Explicitly check staged (git diff --cached) changes.
//	                (This is the default behavior; the flag is for hook clarity.)
//	--fast          Fast mode: skip the jscpd two-pass duplication scan
//	                (advisory in-house findings only; the full two-pass check
//	                runs in CI). Also enabled by KERN_CHECK_FAST=1. Additive —
//	                without it the check is exactly as before.
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
//	--agent-id=ID Agent identity for the change (authz gate). For
//	                --source agent this defaults to $BLUEPRINT_AGENT_ID,
//	                then "agent".
//	--task=TEXT Task scope for the change; sent to kern's authz
//	                gate as the task description. Defaults to --intent when
//	                absent.
//
// exitFlagHelp is the sentinel returned by the flag-parsing helpers when
// -h/--help was requested (the flag package has already printed usage); the
// caller translates it to a clean exit 0 without running the command. It is
// never a real process exit code.
const exitFlagHelp = -1

func runCheck(args []string) int {
	code, _, _ := RunCheckAndReport(args)
	return code
}

// RunCheckAndReport runs the `kern check` pipeline exactly as RunCheck does —
// the same output and the same exit code — and additionally reports the
// resolved repo root and the first failing check's name (empty when the run
// passed or never reached the validation pipeline). cmd/kern uses it to
// record dogfood gate outcomes (Self-Improvement Tier 4 #10) without
// re-running or re-implementing the pipeline.
func RunCheckAndReport(args []string) (code int, failedCheck string, root string) {
	o := runCheckCore(args)
	if o.result == nil {
		return o.code, "", o.root
	}
	if o.ciMode {
		emitCIVerdict(*o.result)
		if o.result.ExitCode != 0 {
			return 1, firstFailedCheck(*o.result), o.root
		}
		return 0, "", o.root
	}
	if o.jsonMode {
		emitJSON(*o.result)
	} else {
		emitText(*o.result)
	}
	return o.result.ExitCode, firstFailedCheck(*o.result), o.root
}

// runCheckOutcome carries what the check runners need: the final exit code,
// the pipeline result (nil when the run failed before validation), the
// resolved repo root, and the output modes that decide how the result is
// emitted.
type runCheckOutcome struct {
	code     int
	result   *domain.ValidationResult
	root     string
	ciMode   bool
	jsonMode bool
}

// runCheckCore executes the `kern check` pipeline: flag parsing, root
// resolution, config load, staged-change discovery, and validation. Error
// paths emit their own output and return a nil result; a successful pipeline
// returns the raw ValidationResult for the caller to emit.
func runCheckCore(args []string) runCheckOutcome {
	fl, code := parseCheckFlags(args)
	if code != 0 {
		if code == exitFlagHelp {
			return runCheckOutcome{code: 0}
		}
		return runCheckOutcome{code: code}
	}

	// Resolve output format: --json is shorthand for --format=json.
	jsonMode, code := checkOutputFormat(fl.jsonOut, fl.format)
	if code != 0 {
		return runCheckOutcome{code: code}
	}
	_ = fl.staged // --staged is the default behavior; flag exists for hook clarity

	absRoot, code := resolveRepoRoot(fl.repoRoot)
	if code != 0 {
		return runCheckOutcome{code: code}
	}
	// keep `git status` clean after the first run — gitignore the
	// runtime state this command is about to write. Best-effort.
	ensureBlueprintRuntimeGitignored(absRoot)

	cfg, err := policy.Load(absRoot)
	if err != nil {
		if fl.ci {
			emitCIError("invalid configuration: " + err.Error())
			return runCheckOutcome{code: 1}
		}
		if jsonMode {
			emitErrorJSON(3, "invalid configuration: "+err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "blueprint: invalid configuration: %v\n", err)
		}
		return runCheckOutcome{code: 3}
	}

	// Non-fatal loader notices: warnings never change the exit code.
	for _, w := range cfg.Warnings {
		fmt.Fprintf(os.Stderr, "blueprint: warning: %s\n", w)
	}

	changes, err := discoverStagedChanges(absRoot)
	if err != nil {
		if fl.ci {
			emitCIError("cannot discover staged changes: " + err.Error())
			return runCheckOutcome{code: 1}
		}
		if jsonMode {
			emitErrorJSON(2, "cannot discover staged changes: "+err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "blueprint: cannot discover staged changes: %v\n", err)
		}
		return runCheckOutcome{code: 2}
	}

	req := buildCheckRequest(absRoot, fl.source, changes, fl.agentID, fl.approvalID, fl.intent, fl.task)

	// Build the full check set: approval, architecture, secrets, duplication.
	client, kernVersion, code := newKernClientOrDegraded(fl.requireKern, jsonMode, fl.ci)
	if code != 0 {
		return runCheckOutcome{code: code}
	}

	// Fast mode is additive: --fast or KERN_CHECK_FAST=1 (either activates
	// it). It skips the jscpd two-pass scan (advisory in-house findings only)
	// so the pre-commit hook stays fast; without either, behavior is exactly
	// as before.
	fast := fl.fast || os.Getenv("KERN_CHECK_FAST") == "1"
	checks := buildCheckList(cfg, client, fl.runResilience, fl.runTests, fl.isolateNetwork, fl.allowUnisolated, absRoot, fast)

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
	return runCheckOutcome{code: 0, result: &result, root: absRoot, ciMode: fl.ci, jsonMode: jsonMode}
}

// firstFailedCheck returns the first check (pipeline order) whose status is a
// hard failure — BLOCK or ERROR — the deterministic signature of a gate
// failure. WARN/PASS/SKIP checks are not failures. Empty when nothing failed.
func firstFailedCheck(result domain.ValidationResult) string {
	for _, cr := range result.Checks {
		if cr.Status == domain.StatusBlock || cr.Status == domain.StatusError {
			return cr.Name
		}
	}
	return ""
}

// checkFlags carries the parsed `blueprint check` command-line flags.
type checkFlags struct {
	jsonOut         bool
	format          string
	staged          bool
	fast            bool
	ci              bool
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
	fast := fs.Bool("fast", false, "fast mode: skip the jscpd two-pass duplication scan (advisory in-house findings only; full check runs in CI). Also enabled by KERN_CHECK_FAST=1")
	ci := fs.Bool("ci", false, "CI mode: emit a machine-readable JSON verdict ({passed, checks, evidence}) on stdout and exit 0/1 matching the verdict")
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
		fast:            *fast,
		ci:              *ci,
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

// buildCheckRequest assembles the ChangeRequest for the check command.
// Agent-sourced changes always carry an identity so kern's authz gate
// can evaluate them — --agent-id wins, then BLUEPRINT_AGENT_ID, then a stable
// default; non-agent sources keep "". The approval-gate inputs (approved
// request id, change intent) and the --task authz scope ride along in
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
// stays "". ciMode routes hard failures through the machine-readable CI
// verdict shape instead of the human/JSON error objects.
func newKernClientOrDegraded(requireKern, jsonMode, ciMode bool) (client *kern.KernClient, kernVersion string, code int) {
	var err error
	client, err = kern.NewKernClient()
	if err != nil {
		if requireKern {
			// Explicit opt-in: a missing kern binary is a hard error (exit 2).
			if ciMode {
				emitCIError("kern binary not found: " + err.Error())
			} else if jsonMode {
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
			if ciMode {
				emitCIError(vErr.Error())
			} else if jsonMode {
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
func buildCheckList(cfg *policy.LoadedConfig, client *kern.KernClient, runResilience, runTests, isolateNetwork, allowUnisolated bool, absRoot string, fast bool) []service.Check {
	checks := []service.Check{}
	if cfg.File.Approval.IsEnabled() {
		checks = append(checks, gates.NewCheck(gates.NewStore(absRoot), risk.LoadConfig(cfg.File.Approval)))
	}
	checks = append(checks,
		kern.NewArchitectureCheck(client),
		// T2.1: secret + duplication detection is delegated to the incumbent
		// tools (gitleaks, jscpd). Each adapter falls back to the in-house
		// check (kern sec / structural fingerprints) when its binary is
		// absent, flagged with a WARN finding.
		gitleaks.NewCheck(client),
		jscpd.NewCheck(client, jscpd.WithFast(fast)),
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
		// Timeout + polyglot matrix from .blueprint/config.yaml — one shared
		// helper for check/ci/diff-gate (see sandboxOptsFromConfig for why
		// this wiring must not be re-inlined per command).
		testOpts = append(testOpts, sandboxOptsFromConfig(cfg.File)...)
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

	// Never treat kern/blueprint runtime artifacts (the CI artifact, audit
	// trail, receipts, verdict/fingerprint caches) as staged user changes:
	// they are generated local state, and scanning them re-validates the
	// tool's own output.
	return fileChangesFromStatus(nameStatus, unified, isBlueprintRuntimeArtifact), nil
}

// SplitDiffBlocks exposes splitDiffBlocks for the legacy cmd/blueprint
// compatibility shim's tests.
func SplitDiffBlocks(diff string) map[string]string { return splitDiffBlocks(diff) }

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

// blueprintRuntimeArtifacts are the local runtime-state paths blueprint and
// kern write while validating (kern's .kern/ state and blueprint's cache dirs
// are gitignored; the audit/receipt/verdict-cache/fingerprint-cache dirs and
// metrics.json are never meant to be committed). They must never be treated
// as user changes: the CI artifact embeds prior findings and the verdict
// cache embeds scanned content, so scanning them makes the gates self-inflict
// BLOCKs on the tool's own output. User-authored configuration
// (.blueprint/config.yaml, suppressions.yaml, owners.yaml,
// .kern/boundaries.json) is intentionally NOT excluded — it is the declared
// repository configuration and must be validated like any other change.
var blueprintRuntimeArtifacts = []string{
	".blueprint/audit/",
	".blueprint/receipts/",
	".blueprint/verdict-cache/",
	".blueprint/fingerprint-cache/",
	".blueprint/metrics.json",
	".kern/blueprint-result.json",
}

// isBlueprintRuntimeArtifact reports whether a repo-relative path is a kern
// or blueprint runtime artifact (never a user change).
func isBlueprintRuntimeArtifact(path string) bool {
	p := filepath.ToSlash(path)
	for _, a := range blueprintRuntimeArtifacts {
		if strings.HasPrefix(p, a) {
			return true
		}
	}
	return false
}

// blueprintGitignoreMarker marks the block of blueprint runtime-state entries
// this package owns in the repo's .gitignore (mirroring internal/setup's
// kern-generated block pattern). Re-running overwrites the block, so new
// entries added in an upgrade are picked up.
const blueprintGitignoreMarker = "# --- blueprint runtime state (kern generated) ---"

// ensureBlueprintRuntimeGitignored best-effort appends the blueprint runtime
// state paths to the repo's .gitignore, so the first ci/check run
// does not leave `git status` / `git add -A` polluted with generated state.
// User-authored configuration (.blueprint/config.yaml, suppressions.yaml,
// owners.yaml, .kern/boundaries.json) is intentionally NOT ignored — it is
// the declared repository configuration and must stay committable. Any
// failure is silent: the runtime dirs are also excluded from validation by
// isBlueprintRuntimeArtifact, so a missing ignore entry is cosmetic only.
func ensureBlueprintRuntimeGitignored(root string) {
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return
	}
	closeMarker := "# --- end blueprint runtime state ---"
	block := "\n" + blueprintGitignoreMarker + "\n" +
		".blueprint/audit/\n" +
		".blueprint/receipts/\n" +
		".blueprint/verdict-cache/\n" +
		".blueprint/fingerprint-cache/\n" +
		".blueprint/metrics.json\n" +
		".blueprint/sec-cache.json\n" +
		closeMarker + "\n"
	cleaned := removeMarkedBlock(string(data), blueprintGitignoreMarker, closeMarker)
	cleaned = removeLegacyBlueprintEntries(cleaned)
	out := strings.TrimRight(cleaned, "\n")
	if out != "" {
		out += "\n"
	}
	out += block
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return
	}
}

// blueprintRuntimeEntries are the exact entry lines owned by the blueprint
// runtime block. Reused to strip legacy (pre-marker) entries so a second run
// never appends a duplicate block.
var blueprintRuntimeEntries = []string{
	".blueprint/audit/",
	".blueprint/receipts/",
	".blueprint/verdict-cache/",
	".blueprint/fingerprint-cache/",
	".blueprint/metrics.json",
	".blueprint/sec-cache.json",
}

// removeLegacyBlueprintEntries strips bare lines that exactly match a known
// blueprint runtime entry but sit outside a marked block (legacy writes from
// before the markers existed). Exact-match only — user content is untouched.
func removeLegacyBlueprintEntries(data string) string {
	lines := strings.Split(data, "\n")
	keep := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isEntry := false
		for _, e := range blueprintRuntimeEntries {
			if trimmed == e {
				isEntry = true
				break
			}
		}
		if !isEntry {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n")
}

// removeMarkedBlock removes the region between startMarker and endMarker
// (inclusive of both marker lines), leaving surrounding content intact.
func removeMarkedBlock(data, startMarker, endMarker string) string {
	start := strings.Index(data, startMarker)
	if start < 0 {
		return data
	}
	end := strings.Index(data[start:], endMarker)
	if end < 0 {
		return data
	}
	end += start + len(endMarker)
	// Also drop the trailing newline after the end marker so the rebuilt
	// block does not accumulate blank lines.
	if end+1 < len(data) && data[end] == '\n' {
		end++
	} else if end+1 < len(data) && data[end+1] == '\n' {
		end++
	}
	return data[:start] + data[end:]
}

// IsBinaryDiffBlock exposes isBinaryDiffBlock for the legacy cmd/blueprint
// compatibility shim's tests.
func IsBinaryDiffBlock(block string) bool { return isBinaryDiffBlock(block) }

// isBinaryDiffBlock reports whether a diff block describes a binary change
// (git emits "Binary files a/... and b/... differ" with no hunks).
func isBinaryDiffBlock(block string) bool {
	return strings.Contains(block, "Binary files ")
}

// ParseDiffLineNumbers exposes parseDiffLineNumbers for the legacy
// cmd/blueprint compatibility shim's tests.
func ParseDiffLineNumbers(diff string) (added, removed []string) {
	return parseDiffLineNumbers(diff)
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
		if cr.Error != "" {
			// A check that errored carries its failure in Error, not in
			// Findings — "0 findings" would be misleading (e.g. a BLOCKed
			// catalog:doc with a stale-doc error and no findings).
			fmt.Printf("  [%s] %s (%dms): %s\n", cr.Status, cr.Name, cr.Duration, cr.Error)
		} else {
			fmt.Printf("  [%s] %s (%dms, %d findings)\n", cr.Status, cr.Name, cr.Duration, len(cr.Findings))
		}
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

// ciVerdict is the machine-readable `kern check --ci` output contract. It is
// intentionally stable and minimal: `passed` (mirrors the process exit code
// 0/1), one entry per executed check, and an `evidence` object carrying the
// raw per-check results with the bpcli verdict fields — the exact
// ValidationResult shape the check pipeline already produces (the check path
// does not build an internal/evidence bundle today, so the raw verdict IS
// the evidence, per the Batch A spec).
type ciVerdict struct {
	Passed   bool           `json:"passed"`
	Checks   []ciCheckEntry `json:"checks"`
	Evidence ciEvidence     `json:"evidence"`
}

// ciCheckEntry is one executed check inside the CI verdict.
type ciCheckEntry struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// ciEvidence carries the raw validation verdict the pipeline produced, so CI
// consumers can drill into the same fields the JSON output path emits
// (status, exit_code, summary, correlation_id, duration_ms, checks_skipped).
type ciEvidence struct {
	Status        string         `json:"status"`
	ExitCode      int            `json:"exit_code"`
	Summary       domain.Summary `json:"summary"`
	CorrelationID string         `json:"correlation_id"`
	DurationMs    int64          `json:"duration_ms"`
	ChecksSkipped []string       `json:"checks_skipped,omitempty"`
}

// emitCIVerdict writes the machine-readable CI verdict for a completed
// validation run. passed mirrors result.ExitCode == 0; the caller maps it to
// the process exit code (0/1).
func emitCIVerdict(result domain.ValidationResult) {
	checks := make([]ciCheckEntry, 0, len(result.Checks))
	for _, cr := range result.Checks {
		checks = append(checks, ciCheckEntry{
			Name:   cr.Name,
			Status: string(cr.Status),
			Detail: ciCheckDetail(cr),
		})
	}
	v := ciVerdict{
		Passed: result.ExitCode == 0,
		Checks: checks,
		Evidence: ciEvidence{
			Status:        string(result.Status),
			ExitCode:      result.ExitCode,
			Summary:       result.Summary,
			CorrelationID: result.CorrelationID,
			DurationMs:    result.DurationMs,
			ChecksSkipped: result.ChecksSkipped,
		},
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// emitCIError writes a failing CI verdict for a run that errored before the
// pipeline produced a ValidationResult (config load, change discovery, kern
// client). passed is always false; the process should exit 1.
func emitCIError(message string) {
	v := ciVerdict{
		Passed: false,
		Checks: []ciCheckEntry{{Name: "kern-check", Status: string(domain.StatusError), Detail: message}},
		Evidence: ciEvidence{
			Status:   string(domain.StatusError),
			ExitCode: 1,
		},
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// ciCheckDetail renders one check's findings as a compact single-line detail
// for the CI verdict: the check error when the check errored, otherwise a
// finding count with the first few findings (bounded so the verdict stays
// readable in CI logs).
func ciCheckDetail(cr domain.CheckResult) string {
	if cr.Skipped {
		return "skipped"
	}
	if cr.Error != "" {
		return "error: " + cr.Error
	}
	if len(cr.Findings) == 0 {
		return "ok (0 findings)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d finding(s):", len(cr.Findings))
	const maxShown = 10
	for i, f := range cr.Findings {
		if i >= maxShown {
			fmt.Fprintf(&b, " +%d more", len(cr.Findings)-maxShown)
			break
		}
		loc := f.File
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		fmt.Fprintf(&b, " [%s] %s (%s)", f.Severity, f.Message, loc)
	}
	return b.String()
}

// defaultAgentID returns the agent identity for agent-sourced changes
// (authz): $BLUEPRINT_AGENT_ID when set, else the stable default
// "agent". Agent-sourced changes must always carry an identity so kern's
// authz gate can evaluate them.
func defaultAgentID() string {
	if id := os.Getenv("BLUEPRINT_AGENT_ID"); id != "" {
		return id
	}
	return "agent"
}
