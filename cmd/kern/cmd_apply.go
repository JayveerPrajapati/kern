package main

// cmd_apply.go implements `kern apply` — the one-command safe edit path
// (apply campaign P2). The CLI is a thin shell per the layering contract
// (cmd/ = flags + delegation): it parses flags, reads the edits JSON
// (file or stdin), delegates to internal/apply.Run, and renders the
// result. Exit codes follow the plugin/CLI exit contract: 0 = applied
// (or dry-run preview) and verified, 1 = apply or verify failed (findings),
// 2 = usage, 3 = denied.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/JayveerPrajapati/kern/internal/apply"
	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/refactor"
)

type applyFlags struct {
	root   string
	test   bool
	dryRun bool
	json   bool
}

func parseApplyFlags(args []string) (applyFlags, []string, error) {
	var f applyFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--test":
			p.SetBool(&f.test, "--test", inline, hasInline)
		case "--dry-run":
			p.SetBool(&f.dryRun, "--dry-run", inline, hasInline)
		case "--json":
			p.SetBool(&f.json, "--json", inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

// runApply implements `kern apply <edits-file|-> [--root ROOT] [--test]
// [--dry-run] [--json]`. The edits file is a JSON array of {path, content}
// objects (the same shape as kern_refactor_transaction's edits parameter —
// each content is the NEW COMPLETE file content).
func runApply(rest []string) int {
	f, args, err := parseApplyFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) != 1 {
		fatalUsage("usage: kern apply <edits-file|-> [--root ROOT] [--test] [--dry-run] [--json]")
	}

	var data []byte
	if args[0] == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(args[0])
	}
	if err != nil {
		fatal("read edits: %v", err)
	}
	var edits []refactor.FileEdit
	if err := json.Unmarshal(data, &edits); err != nil {
		fatal("parse edits JSON (expected an array of {path, content}): %v", err)
	}

	res, err := apply.Run(context.Background(), apply.Request{
		Root:   f.root,
		Edits:  edits,
		Test:   f.test,
		DryRun: f.dryRun,
	})
	if err != nil {
		fatal("%v", err)
	}

	if f.json {
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
	} else {
		printApplyResult(res)
	}
	if !res.Success {
		return 1
	}
	return 0
}

func printApplyResult(res *apply.Result) {
	verdict := "APPLIED"
	switch {
	case res.RolledBack:
		verdict = "ROLLED BACK"
	case !res.Applied && res.Success:
		verdict = "PREVIEWED" // dry-run: diff computed, tree untouched
	case !res.Success:
		verdict = "FAILED"
	}
	fmt.Printf("kern apply: %s\n", verdict)
	for _, f := range res.ModifiedFiles {
		fmt.Printf("  %s\n", f)
	}
	if res.UnifiedDiff != "" {
		fmt.Print(res.UnifiedDiff)
	}
	if res.TestOutput != "" {
		fmt.Print(res.TestOutput)
	}
	fmt.Printf("receipt: %s\n", res.Receipt)
	if res.Error != "" {
		fmt.Fprintf(os.Stderr, "kern: %s\n", res.Error)
	}
}
