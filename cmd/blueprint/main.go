// Package main is the legacy Blueprint CLI entry point.
//
// Blueprint has been merged into kern: every subcommand below forwards to the
// same internal/blueprint/cli implementation that `kern check`, `kern fix`,
// `kern ci`, etc. use, so behavior is identical to the merged binary by
// construction. This thin adapter is kept only as a compatibility alias and as
// the build target for the change-firewall gate tests; it is not built or
// installed by CI, the Makefile, or `kern setup`. Use `kern` directly.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/cli"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/policy"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
	blueprintversion "github.com/JayveerPrajapati/kern/internal/blueprint/version"
	"github.com/JayveerPrajapati/kern/internal/blueprint/watcher"
)

var version = blueprintversion.Version

// errKernChainHashNotFound is the exported sentinel from internal/blueprint/cli.
var errKernChainHashNotFound = cli.ErrKernChainHashNotFound
var errKernChainCheckSkipped = cli.ErrKernChainCheckSkipped

// CIArtifact is the shared CI receipt type (defined in internal/blueprint/cli);
// the alias keeps the package's gate tests source-compatible now that the
// duplicated CI code has been removed.
type CIArtifact = cli.CIArtifact

// CIFinding is the CI finding type from internal/blueprint/cli.
type CIFinding = cli.CIFinding

// kernDependentGates mirrors internal/blueprint/cli at the shim boundary so
// the package's doctor test can assert skip behavior without drift.
var kernDependentGates = cli.KernDependentGates()

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	cmd, args := os.Args[1], os.Args[2:]

	switch cmd {
	case "version", "--version", "-v", "-version":
		fmt.Printf("blueprint %s\n", version)
		fmt.Fprintf(os.Stderr, "note: blueprint is a legacy shim; prefer `kern <command>`\n")
	case "help", "--help", "-h":
		usage(os.Stdout)
	case "check":
		os.Exit(cli.RunCheck(args))
	case "fix":
		os.Exit(cli.RunFix(args))
	case "ci":
		os.Exit(cli.RunCI(args))
	case "verify-receipt":
		os.Exit(cli.RunVerifyReceipt(args))
	case "doctor":
		os.Exit(cli.RunDoctor(args))
	case "graph":
		os.Exit(cli.RunGraph(args))
	case "install":
		os.Exit(cli.RunInstall(args))
	case "watch":
		os.Exit(cli.RunWatch(args))
	case "metrics":
		os.Exit(cli.RunMetrics(args))
	case "request-approval":
		os.Exit(cli.RunRequestApproval(args))
	case "approve":
		os.Exit(cli.RunApprovalDecision("approve", args))
	case "reject":
		os.Exit(cli.RunApprovalDecision("reject", args))
	default:
		fmt.Fprintf(os.Stderr, "blueprint: unknown command %q\n\n", cmd)
		usage(os.Stderr)
		os.Exit(4)
	}
}

// runGraph and runInstallHook are kept for the package's gate tests; they
// forward to the same shared implementation that the kern subcommands use.
func runGraph(args []string) int       { return cli.RunGraph(args) }
func runInstallHook(args []string) int { return cli.RunInstallHook(args) }

// parseDiffLineNumbers forwards to the shared hunk-header parser.
func parseDiffLineNumbers(diff string) (added, removed []string) {
	return cli.ParseDiffLineNumbers(diff)
}

// splitDiffBlocks forwards to the shared per-file diff block splitter.
func splitDiffBlocks(diff string) map[string]string {
	return cli.SplitDiffBlocks(diff)
}

// isBinaryDiffBlock forwards to the shared binary-block detector.
func isBinaryDiffBlock(block string) bool {
	return cli.IsBinaryDiffBlock(block)
}

// verifyKernChainHash forwards to the shared kern audit-chain verifier.
func verifyKernChainHash(repo, expectedHash string) error {
	return cli.VerifyKernChainHash(repo, expectedHash)
}

// validateBatch forwards to the shared watch validation pipeline. It is kept
// for the package's gate tests now that the duplicated watch implementation
// has been removed.
func validateBatch(ctx context.Context, root string, checks []service.Check, engine *policy.Engine, events []watcher.Event) (bool, []domain.Finding) {
	return cli.ValidateBatch(ctx, root, checks, engine, events)
}

// buildWatchChecks forwards to the shared --policy → checks mapping.
func buildWatchChecks(policy string, client *kern.KernClient) ([]service.Check, error) {
	return cli.BuildWatchChecks(policy, client)
}

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `usage: blueprint <command> [args]

Blueprint has been merged into kern. This adapter forwards every command to
the shared internal implementation, so behavior matches `+"`kern check`"+`,
`+"`kern ci`"+`, etc. exactly. Prefer `+"`kern <command>`"+`.

Commands:
  check            validate staged changes against policy
  fix              validate agent-proposed fixes in an isolated worktree
  ci               CI change-governance validation (base vs head)
  verify-receipt   verify a tamper-evident CI receipt
  doctor           diagnose Blueprint setup health
  graph            render the change-firewall decision graph
  install          install change-governance git hooks (pre-commit/pre-push)
  watch            watch a repo and auto-check staged changes
  metrics          show local change-governance validation metrics
  request-approval request human approval for a high-risk change
  approve          approve a pending approval request
  reject           reject a pending approval request
  version          print version
`)
}
