// Package main is the Blueprint CLI entry point.
package main

import (
	"fmt"
	"os"

	blueprintversion "github.com/JayveerPrajapati/kern/internal/blueprint/version"
)

var version = blueprintversion.Version

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	cmd, args := os.Args[1], os.Args[2:]

	switch cmd {
	case "version", "--version", "-v", "-version":
		fmt.Printf("blueprint %s\n", version)
	case "doctor":
		os.Exit(runDoctor(args))
	case "help", "--help", "-h":
		usage(os.Stdout)
	case "check":
		os.Exit(runCheck(args))
	case "fix":
		os.Exit(runFix(args))
	case "graph":
		os.Exit(runGraph(args))
	case "install":
		os.Exit(runInstall(args))
	case "watch":
		os.Exit(runWatch(args))
	case "ci":
		os.Exit(runCI(args))
	case "verify-receipt":
		os.Exit(runVerifyReceipt(args))
	case "metrics":
		os.Exit(runMetrics(args))
	case "request-approval":
		os.Exit(runRequestApproval(args))
	case "approve":
		os.Exit(runApprovalDecision("approve", args))
	case "reject":
		os.Exit(runApprovalDecision("reject", args))
	default:
		fmt.Fprintf(os.Stderr, "blueprint: unknown command %q\n\n", cmd)
		usage(os.Stderr)
		os.Exit(4)
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, `blueprint — local change governance engine

Usage:
  blueprint <command> [flags]

Commands:
  version       Print the blueprint version
  doctor        Preflight environment/configuration diagnostic
  check         Validate staged changes against policy
  fix           Validate agent-proposed fixes in an isolated worktree
  graph         Visualize architectural boundaries (Mermaid, DOT, JSON)
  install hook  Install a pre-commit or pre-push git hook
  watch         Continuous advisory file-change watcher
  ci            CI/protected-branch enforcement (base vs head, JSON artifact)
  verify-receipt
                Verify a tamper-evident CI receipt (receipt + audit chain)
  metrics       Show local validation metrics (no cloud telemetry)
  request-approval
                Request human approval for a high-risk change (two-person rule)
  approve       Approve a pending approval request: approve <id> [--reason ...]
  reject        Reject a pending approval request: reject <id> [--reason ...]
  help          Show this help

Exit codes (spec Section 6):
  0   PASS
  1   policy violation / BLOCK (fix: ANY remaining finding — WARN or BLOCK — exits 1; the repair loop must iterate)
  2   tool/runtime/configuration/usage ERROR
  3   invalid Blueprint configuration
  4   unsupported operation or environment`)
}
