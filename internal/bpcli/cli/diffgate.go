package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/checks/diffgate"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/sandbox"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
	"github.com/JayveerPrajapati/kern/internal/bppolicy/policy"
)

// runDiffGate implements `kern diff-gate`: a deterministic,
// local, advisory-by-default gate over the working-tree diff. It runs eight
// checks — gofmt, vulnerabilities, schema drift, unsafe execution, changelog,
// MCP catalogue drift (new) plus the reused secret and sandbox build/test
// gates (G3/G8) — and returns structured verdicts.
//
// Exit codes follow the Blueprint aggregate semantics: 0 = PASS/WARN/SKIP
// (advisory; warnings never fail), 1 = BLOCK, 2 = ERROR. --blocking elevates
// WARN to BLOCK for protected CI.
func runDiffGate(args []string) int {
	fl, code := parseDiffGateFlags(args)
	if code != 0 {
		if code == exitFlagHelp {
			return 0 // -h/--help: usage already printed
		}
		return code
	}

	absRoot, code := resolveRepoRoot(fl.root)
	if code != 0 {
		return code
	}

	changes, err := discoverWorkingTreeChanges(absRoot)
	if err != nil {
		if fl.jsonOut {
			emitErrorJSON(2, "cannot discover working-tree changes: "+err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "diff-gate: cannot discover working-tree changes: %v\n", err)
		}
		return 2
	}

	req := domain.ChangeRequest{
		RepositoryRoot: absRoot,
		Source:         domain.SourceHuman,
		Operation:      domain.OpCommit,
		Files:          changes,
	}

	// Load .blueprint/config.yaml ONCE for the whole gate: the sandbox
	// check's timeout/matrix wiring AND the whole-run context budget. The
	// --timeout flag (when explicitly passed) overrides; when it is not,
	// the config's execution.timeout_seconds is the default budget so a
	// configured sandbox timeout (300s here) can never be silently cut off
	// by this gate's old hard-coded 120s default — the same wiring-drift
	// class as the check-vs-ci timeout bug (27e4559), one level up.
	var cfg *policy.LoadedConfig
	if loaded, lerr := policy.Load(absRoot); lerr == nil {
		cfg = loaded
	} else {
		fmt.Fprintf(os.Stderr, "diff-gate: warning: cannot load .blueprint/config.yaml (%v); running with defaults\n", lerr)
	}

	checks := buildDiffGateCheckList(absRoot, cfg, fl.initBaseline, fl.noTests, fl.jsonOut)
	// Per-check progress to stderr (diffGateReporter): the sandboxed
	// tests:build-test check alone can consume the whole gate budget, and the
	// service renders ONE final verdict — without this the gate showed ZERO
	// output for up to the full budget and looked hung. The reporter emits a
	// running line before each check and a verdict line after, so the user
	// always sees incremental activity.
	svc := service.New(checks, service.WithCheckReporter(diffGateReporter{}))

	timeoutSec := fl.timeoutSec
	if timeoutSec <= 0 {
		if cfg != nil && cfg.Service.TimeoutSec > 0 {
			timeoutSec = cfg.Service.TimeoutSec
		} else {
			timeoutSec = 120
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration(timeoutSec))
	defer cancel()
	result := svc.Validate(ctx, req)
	result = applyBlocking(result, fl.blocking)

	if fl.jsonOut {
		emitJSON(result)
	} else {
		emitText(result)
	}
	return result.ExitCode
}

// diffGateFlags carries the parsed `kern diff-gate` command-line flags.
type diffGateFlags struct {
	root         string
	timeoutSec   int
	blocking     bool
	jsonOut      bool
	initBaseline bool
	noTests      bool
}

// diffGateReporter implements service.CheckReporter, printing per-check
// progress to stderr. Before each check runs it prints a running line
// (1-based index); after completion it prints the verdict line with the
// measured duration. Progress always goes to stderr so stdout stays reserved
// for the final verdict (or the --json document).
type diffGateReporter struct{}

// CheckStarted prints the running line before check i (0-based) of n executes.
func (diffGateReporter) CheckStarted(i, n int, name string) {
	fmt.Fprintf(os.Stderr, "diff-gate: running check %d/%d: %s...\n", i+1, n, name)
}

// CheckFinished prints the completed line with the check's final verdict and
// wall-clock duration.
func (diffGateReporter) CheckFinished(i, n int, name string, status domain.Status, duration time.Duration) {
	fmt.Fprintf(os.Stderr, "diff-gate: check %s: %s (%s)\n", name, status, duration.Round(time.Millisecond))
}

// diffGateSandboxFloor is the minimum remaining gate budget (ctx deadline)
// required before the sandboxed tests:build-test check will run. When earlier
// checks have nearly exhausted the gate budget, running the full sandboxed
// build/test would burn the remainder and end in a timeout with no useful
// verdict — the check is skipped with an explicit SKIP verdict and rerun
// guidance instead. Skips never affect the aggregated gate status (SKIP is
// advisory, exit 0).
const diffGateSandboxFloor = 30 * time.Second

// budgetAwareCheck wraps a slow check (the sandboxed tests:build-test) so it
// declines to run when the remaining ctx budget is below the floor, returning
// an explicit SKIP verdict whose message tells the user how to rerun instead
// of letting the check burn the whole budget.
type budgetAwareCheck struct {
	inner service.Check
	floor time.Duration
}

// Name delegates to the wrapped check.
func (c *budgetAwareCheck) Name() string { return c.inner.Name() }

// Run skips the wrapped check when the remaining ctx budget is below the
// floor; otherwise it runs the check unchanged. The skip is expressed exactly
// as the domain models a skipped check: StatusSkip + Skipped=true (counted in
// Summary.Skipped, never affecting the aggregated status), with the rerun
// guidance carried in Error so the text renderer shows it.
func (c *budgetAwareCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < c.floor {
			return domain.CheckResult{
				Name:    c.inner.Name(),
				Status:  domain.StatusSkip,
				Skipped: true,
				Error: fmt.Sprintf(
					"skipped: insufficient remaining budget (%s < %s); rerun with --no-tests or --timeout N",
					remaining.Round(time.Second), c.floor),
			}, nil
		}
	}
	return c.inner.Run(ctx, req)
}

// parseDiffGateFlags parses the `kern diff-gate` flags. It returns the parsed
// flags and an exit code: 0 means ready to run, 2 means a usage error was
// printed, and exitFlagHelp means -h/--help was requested (the flag package
// has already rendered usage, so the caller exits 0 rather than treating help
// as a parse error).
func parseDiffGateFlags(args []string) (diffGateFlags, int) {
	fs := flag.NewFlagSet("diff-gate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", ".", "repository root (default: .)")
	timeoutSec := fs.Int("timeout", 0, "max runtime in seconds for the whole validation (0 = use .blueprint/config.yaml execution.timeout_seconds, else 120)")
	blocking := fs.Bool("blocking", false, "elevate WARN findings to BLOCK (exit 1) for protected CI")
	jsonOut := fs.Bool("json", false, "emit structured JSON verdicts")
	initBaseline := fs.Bool("init-baseline", false, "write the MCP tool-schema baseline and report PASS")
	noTests := fs.Bool("no-tests", false, "skip the expensive tests:build-test check (fast advisory runs)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return diffGateFlags{}, exitFlagHelp
		}
		return diffGateFlags{}, 2
	}
	return diffGateFlags{
		root:         *root,
		timeoutSec:   *timeoutSec,
		blocking:     *blocking,
		jsonOut:      *jsonOut,
		initBaseline: *initBaseline,
		noTests:      *noTests,
	}, 0
}

// buildDiffGateCheckList assembles the diff-gate check set: the six new
// deterministic checks (G30-G35) plus the reused secret (G3, secret:scan)
// and sandbox build/test (G8, tests:build-test) checks from the Blueprint
// engine. The secret check requires the kern client; in degraded mode (binary
// absent) it is omitted rather than panicking. tests:build-test is skipped
// under --no-tests.
//
// The catalog:doc (G36) and contracts:doc doc-freshness gates are scoped to
// repos that already commit the generated docs: they guard DRIFT of an
// existing docs/tool-catalog.md / docs/mcp/tool-contracts.md, and a repo
// that never generated them (any non-kern project) must not be BLOCKed for
// their absence.
func buildDiffGateCheckList(absRoot string, cfg *policy.LoadedConfig, initBaseline, noTests, jsonOut bool) []service.Check {
	// The live MCP catalog is injected at server construction / binary
	// startup (catalog.WithDiffgateTools → diffgate.ToolInfos). cli cannot
	// import mcp (import cycle), so it reads the injected list here and
	// passes it to every catalog-consuming check constructor explicitly —
	// a wiring site that omits it fails to compile (fail-loud).
	tools := diffgate.ToolInfos()
	checks := []service.Check{
		diffgate.NewGofmtCheck(),
		diffgate.NewVulnCheck(),
		diffgate.NewSchemaDriftCheck(absRoot, initBaseline, tools),
		diffgate.NewExecUnsafeCheck(),
		diffgate.NewChangelogCheck(),
		diffgate.NewCatalogDriftCheck(absRoot, tools),
		diffgate.NewDocBudgetCheck(absRoot),
	}
	if _, err := os.Stat(filepath.Join(absRoot, "docs", "tool-catalog.md")); err == nil {
		checks = append(checks, diffgate.NewCatalogDocCheck(absRoot, tools))
	}
	if _, err := os.Stat(filepath.Join(absRoot, "docs", "mcp", "tool-contracts.md")); err == nil {
		checks = append(checks, diffgate.NewContractsDocCheck(absRoot, tools))
	}
	if client, _, code := newKernClientOrDegraded(false, jsonOut, false); code == 0 && client != nil {
		checks = append(checks, kern.NewSecretCheck(client))
	}
	if !noTests {
		// Same config wiring as `kern check`/`kern ci` (sandboxOptsFromConfig)
		// so this gate honors sandbox.timeout_seconds and the configured
		// matrix too. cfg was loaded once in runDiffGate (nil only when the
		// config is unreadable — warning already printed there).
		var testOpts []sandbox.ConfigOption
		if cfg != nil {
			testOpts = append(testOpts, sandboxOptsFromConfig(cfg.File)...)
		}
		// Budget-aware wrapper: when the gate budget is nearly exhausted by
		// the earlier checks, the sandboxed build/test would burn the
		// remainder and time out — it skips with rerun guidance instead.
		checks = append(checks, &budgetAwareCheck{inner: sandbox.NewDefaultCheck(testOpts...), floor: diffGateSandboxFloor})
	}
	return checks
}

// applyBlocking elevates an advisory WARN verdict to BLOCK (exit 1) when
// --blocking is set for protected CI. PASS/BLOCK/ERROR/SKIP verdicts are
// untouched; advisory WARNs never change the exit code otherwise.
func applyBlocking(result domain.ValidationResult, blocking bool) domain.ValidationResult {
	if blocking && result.Status == domain.StatusWarn {
		result.Status = domain.StatusBlock
		result.ExitCode = 1
	}
	return result
}

// discoverWorkingTreeChanges finds the working-tree diff (staged + unstaged
// + untracked) vs HEAD in the same deterministic shape as
// discoverStagedChanges: one --name-status pass plus one --unified=0 pass,
// with per-file diff blocks and real added/removed line numbers attached.
// On an unborn HEAD (fresh repo with no commits) it falls back to the
// staged set.
//
// Untracked files (QA Pick #6, F-DG1): `git diff HEAD` cannot see files not
// yet added to the index, so new-file workflows (the agent-driven common
// case) bypassed every check. They are now included as OpWrite changes with
// a synthesized all-added diff block. `--exclude-standard` respects
// .gitignore, so generated/ignored files stay out.
func discoverWorkingTreeChanges(repoRoot string) ([]domain.FileChange, error) {
	if !isGitRepo(repoRoot) {
		return nil, fmt.Errorf("not a git repository: %s", repoRoot)
	}

	nameStatus, err := gitOutput(repoRoot, "diff", "HEAD", "--name-status")
	if err != nil {
		// Unborn HEAD (no commits yet): fall back to the staged set.
		if changes, derr := discoverStagedChanges(repoRoot); derr == nil {
			return changes, nil
		}
		return nil, fmt.Errorf("git diff HEAD --name-status: %w", err)
	}

	var changes []domain.FileChange
	if strings.TrimSpace(nameStatus) != "" {
		// ONE unified=0 diff over the whole working tree (not one git spawn per
		// file): keeps argv bounded and avoids N subprocess launches.
		unified, err := gitOutput(repoRoot, "-c", "core.quotepath=false", "diff", "HEAD", "--unified=0", "--no-ext-diff")
		if err != nil {
			return nil, fmt.Errorf("git diff HEAD --unified=0: %w", err)
		}
		changes = fileChangesFromStatus(nameStatus, unified, nil)
	}

	// Untracked files: invisible to git diff, visible to ls-files --others.
	untracked, err := gitOutput(repoRoot, "-c", "core.quotepath=false", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("git ls-files --others: %w", err)
	}
	for _, p := range strings.Split(strings.TrimSpace(untracked), "\n") {
		if p == "" {
			continue
		}
		changes = append(changes, untrackedFileChange(repoRoot, p))
	}
	if len(changes) == 0 {
		return nil, nil // clean working tree
	}
	return changes, nil
}

// untrackedFileChange builds the FileChange for a file git diff cannot see:
// status A, whole-file diff block (every line added), and the file content
// attached so content-based checks behave exactly as for tracked changes.
func untrackedFileChange(repoRoot, path string) domain.FileChange {
	fc := domain.FileChange{Path: path, Op: domain.OpWrite}
	b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(path)))
	if err != nil {
		return fc // not on disk (raced away): checks that need it will skip
	}
	fc.Content = string(b)
	lines := strings.Split(strings.TrimRight(fc.Content, "\n"), "\n")
	if fc.Content == "" {
		lines = nil
	}
	var block strings.Builder
	block.WriteString("--- /dev/null\n+++ " + path + "\n")
	if len(lines) > 0 {
		block.WriteString("@@ -0,0 +1," + strconv.Itoa(len(lines)) + " @@\n")
	}
	for i, l := range lines {
		fc.Added = append(fc.Added, strconv.Itoa(i+1))
		block.WriteString("+" + l + "\n")
	}
	fc.Diff = block.String()
	return fc
}
