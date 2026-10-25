// guard_grant.go implements `kern guard grant [--check] [--root R]
// <command>` — the Ask Protocol's agent-side bridge for GATE-class commands
// (guard-consent campaign P1). The GATE deny teaches the agent this command;
// running it does exactly one of three things:
//
//   - an active grant covers the command (single-use approved record, or a
//     live prefix grant) → consume it exactly like an execution would and
//     print "granted" (exit 0) — the agent retries the byte-identical
//     command immediately;
//   - --check with no grant → "not granted" (exit 3), nothing minted — the
//     guard script/plugin call this form on every GATE retry so the deny
//     path never pollutes the store with pending records;
//   - no --check and no grant → MINT a pending, command-hash-bound approval
//     (appr-<id>) and print the full ask-wait-retry instructions (exit 0).
//
// Minting grants nothing: only a human at an interactive terminal can
// decide the record (`kern approval ask <id>` — [1] once / [2] prefix /
// [3] reject), and the waiting agent blocks on `kern approval wait <id>`.
package contextcli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

type grantFlags struct {
	check bool
	root  string
}

func parseGrantFlags(args []string) (grantFlags, []string, error) {
	var f grantFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--check":
			p.SetBool(&f.check, "--check", inline, hasInline)
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

// runGuardGrant implements the grant subcommand and returns the process
// exit code: 0 granted or minted, 3 --check not granted, 1 failure (via
// fatal's exitError sentinel path the run() wrapper converts).
func runGuardGrant(rest []string) int {
	f, args, err := parseGrantFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	cmd := ""
	if len(args) > 0 {
		// The command is ONE argument (the deny text teaches passing it
		// quoted); joining extra positionals is a convenience for typed
		// single-space commands only.
		cmd = strings.Join(args, " ")
	} else {
		// No positional: read the exact command from stdin's first line —
		// the byte-faithful form for commands with awkward quoting.
		sc := bufio.NewScanner(os.Stdin)
		if sc.Scan() {
			cmd = sc.Text()
		}
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		fatalUsage("usage: kern guard grant [--check] [--root R] <command> (or pipe the command on stdin)")
	}
	root := f.root
	if root == "" {
		if wd, werr := os.Getwd(); werr == nil {
			root = wd
		} else {
			root = "."
		}
	}
	if err := governance.CheckGateGrant(cmd, root); err == nil {
		fmt.Println("granted: an active approval covers this command — retry the byte-identical command now")
		return 0
	}
	if f.check {
		fmt.Println("not granted: no active approval covers this command (a human must answer)")
		return 3
	}
	id, merr := governance.MintGateApproval(cmd, root)
	if merr != nil {
		fatal("guard grant: %v", merr)
		return 1
	}
	fmt.Printf("pending: %s\n", id)
	fmt.Printf("operator answers: kern approval ask %s --root %s   ([1] accept once / [2] accept prefix this session / [3] reject)\n", id, root)
	fmt.Printf("await the decision: kern approval wait %s --timeout 30m\n", id)
	fmt.Printf("then retry the byte-identical command: %s\n", cmd)
	return 0
}
