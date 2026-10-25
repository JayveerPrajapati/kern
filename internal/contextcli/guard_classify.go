// guard_classify.go implements `kern guard classify <cmd> [--json]`: the CLI
// shell over internal/guardrule — the classification engine extracted from
// this file when the classifier outgrew the contextcli family's
// ARCHITECTURE.md LOC cap. The engine is the single Go source of truth for
// the bash-arm verdicts the plugin (TS) and the shell guard shell out to:
// pure-read pipelines are SERVED (tier 1/2/3), write/destructive/network/
// installer segments GATE (deny whole command), simple content-free commands
// ALLOW raw, and everything else is GOVERNED (kern build). Deny/hint
// messages become byte-parity by construction (one source).
package contextcli

import (
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/guardpaths"
	"github.com/JayveerPrajapati/kern/internal/guardrule"
)

type classifyFlags struct {
	json bool
}

func parseClassifyFlags(args []string) (classifyFlags, []string, error) {
	var f classifyFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--json":
			p.SetBool(&f.json, "--json", inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

// RunGuardClassify executes `kern guard classify <cmd> [--json]` and returns
// the process exit code (0 ok, 2 usage error).
func RunGuardClassify(rest []string) int {
	return run(func() int { runGuardClassify(rest); return 0 })
}

func runGuardClassify(rest []string) {
	f, args, err := parseClassifyFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) == 0 {
		fatalUsage("usage: kern guard classify <command> [--json]")
	}
	cmd := strings.Join(args, " ")
	// Path-aware classification: the CLI consumers (the guard script and
	// the opencode plugin) invoke `kern guard classify` from the project
	// root, so cwd is the root the write arm resolves against.
	// KERN_MCP_ROOTS/KERN_ROOTS (inherited from the operator's shell) add
	// further accepted roots for absolute targets.
	root, _ := os.Getwd()
	res := guardrule.ClassifyCommandInRoots(cmd, root, guardpaths.ExtraRootsFromEnv(os.Getenv))
	if f.json {
		printJSON(res)
		return
	}
	fmt.Printf("verdict: %s\n", res.Verdict)
	if res.Tier > 0 {
		fmt.Printf("tier:    %d\n", res.Tier)
	}
	fmt.Printf("hint:    %s\n", res.Hint)
	fmt.Printf("hash:    %s\n", res.Hash)
}
