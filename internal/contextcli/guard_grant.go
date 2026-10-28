// guard_grant.go implements `kern guard grant [--check] [--root R]
// <command>` — the Ask Protocol's agent-side bridge for GATE-class commands
// (guard-consent campaign P1). The GATE deny teaches the agent the no-check
// form; running it does exactly one of three things:
//
//   - an active grant covers the command (single-use approved record, or a
//     live prefix grant) → PEEK, consuming nothing (exit 0): the claim
//     happens once, at execution time — the guard hook/script/plugin's
//     --check form or the exec firewall claims when the command actually
//     runs. A consuming CLI here would double-claim on every hook host and
//     re-mint on the taught retry — an operator re-approval loop;
//   - --check with no grant → "not granted" (exit 3), nothing minted — the
//     guard script/plugin call the CONSUMING --check form on every GATE
//     retry (exit 0 = claimed, then the command runs raw), so the deny path
//     never pollutes the store with pending records;
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

// closeApprovalHint returns a one-line teaching hint when the store holds an
// exec-command approval whose bound command is a token-boundary prefix of
// command (or command a prefix of it) — the decorated-retry trap (campaign
// 2026-10-28): an agent appends output-shaping ("| tail", "&& echo
// ok") to a retry, the bytes no longer match the grant, and a fresh
// approval is minted instead of consuming the one a human already
// answered. Empty when nothing is close.
func closeApprovalHint(root, command string) string {
	if strings.TrimSpace(command) == "" || root == "" {
		return ""
	}
	records, err := governance.NewFileStore(root).Load()
	if err != nil {
		return ""
	}
	for _, a := range records {
		if a.Kind != governance.ApprovalKindExecCommand || a.Command == "" || a.Command == command {
			continue
		}
		if !governance.PrefixMatches(command, a.Command) && !governance.PrefixMatches(a.Command, command) {
			continue
		}
		state := a.Status
		if a.Status == "approved" && a.Scope == "prefix" {
			state = "approved (prefix grant)"
		}
		return fmt.Sprintf("note: approval %s is %s for the close command %q — the grant binds the exact command bytes, so appending output-shaping (pipes, redirects, extra && segments) mints a NEW approval; retry exactly: %s", a.ID, state, a.Command, a.Command)
	}
	return ""
}

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
// exit code: 0 granted (peeked) or minted, 3 --check not granted, 1 failure (via
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
	// The --check form is the PLUGIN/SCRIPT claim: the host guard consults it
	// on a GATE retry and, on exit 0, runs the command RAW — so this form
	// must consume exactly like an execution would (CheckGateGrant). The
	// no-check form is the agent-side MINT bridge: it peeks (consuming
	// nothing) so the taught byte-identical retry — which claims at the host
	// guard — is never double-claimed into an operator re-approval loop.
	if f.check {
		if err := governance.CheckGateGrant(cmd, root); err == nil {
			fmt.Println("granted: an active approval covers this command — this check consumed its use; the command may run now (retry it, do not wait)")
			return 0
		}
		fmt.Println("not granted: no active approval covers this command (a human must answer)")
		return 3
	}
	if err := governance.PeekGateGrant(cmd, root); err == nil {
		fmt.Println("granted: an active approval covers this command — retry the byte-identical command now (nothing consumed here; the guard consumes it when the command runs)")
		return 0
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
	// Decorated-retry teaching (2026-10-28 campaign): when this mint is a
	// decorated variant of a command a human already answered, say so —
	// the agent was one exact retry away from done.
	if hint := closeApprovalHint(root, cmd); hint != "" {
		fmt.Println(hint)
	}
	return 0
}
