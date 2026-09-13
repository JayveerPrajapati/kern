package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/checks/diffgate"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/sandbox"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
)

// runDiffGate implements `kern diff-gate` (KERN-P2-003): a deterministic,
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

	checks := buildDiffGateCheckList(absRoot, fl.initBaseline, fl.noTests, fl.jsonOut)
	svc := service.New(checks)

	ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration(fl.timeoutSec))
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

// parseDiffGateFlags parses the `kern diff-gate` flags. It returns the parsed
// flags and an exit code: 0 means ready to run, 2 means a usage error was
// printed, and exitFlagHelp means -h/--help was requested (the flag package
// has already rendered usage, so the caller exits 0 rather than treating help
// as a parse error).
func parseDiffGateFlags(args []string) (diffGateFlags, int) {
	fs := flag.NewFlagSet("diff-gate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", ".", "repository root (default: .)")
	timeoutSec := fs.Int("timeout", 120, "max runtime in seconds for the whole validation")
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
func buildDiffGateCheckList(absRoot string, initBaseline, noTests, jsonOut bool) []service.Check {
	checks := []service.Check{
		diffgate.NewGofmtCheck(),
		diffgate.NewVulnCheck(),
		diffgate.NewSchemaDriftCheck(absRoot, initBaseline),
		diffgate.NewExecUnsafeCheck(),
		diffgate.NewChangelogCheck(),
		diffgate.NewCatalogDriftCheck(absRoot),
		diffgate.NewCatalogDocCheck(absRoot),
		diffgate.NewNoteFormatCheck(absRoot),
		diffgate.NewNoteMissingCheck(),
		diffgate.NewDocBudgetCheck(absRoot),
	}
	if client, _, code := newKernClientOrDegraded(false, jsonOut); code == 0 && client != nil {
		checks = append(checks, kern.NewSecretCheck(client))
	}
	if !noTests {
		checks = append(checks, sandbox.NewDefaultCheck())
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

// discoverWorkingTreeChanges finds the working-tree diff (staged + unstaged)
// vs HEAD in the same deterministic shape as discoverStagedChanges: one
// --name-status pass plus one --unified=0 pass, with per-file diff blocks and
// real added/removed line numbers attached. On an unborn HEAD (fresh repo
// with no commits) it falls back to the staged set.
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

	if strings.TrimSpace(nameStatus) == "" {
		return nil, nil // clean working tree
	}

	// ONE unified=0 diff over the whole working tree (not one git spawn per
	// file): keeps argv bounded and avoids N subprocess launches.
	unified, err := gitOutput(repoRoot, "-c", "core.quotepath=false", "diff", "HEAD", "--unified=0", "--no-ext-diff")
	if err != nil {
		return nil, fmt.Errorf("git diff HEAD --unified=0: %w", err)
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

		// Map git status code to Operation (mirrors discoverStagedChanges).
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
