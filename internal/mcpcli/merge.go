// merge.go implements the `kern semantic-diff` and `kern semantic-merge`
// CLI entry points: flag parsing and rendering live here; the bodies are
// merge.Tool (action=diff|merge) in internal/mcp/merge.
package mcpcli

import (
	"context"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/merge"
)

// semanticFlags carries the flags shared by the semantic-diff and
// semantic-merge commands.
type semanticFlags struct {
	root             string
	from, to, range_ string
	json             bool
	file             string
	base, local      string
	remote           string
	apply            bool
}

// parseSemanticFlags replicates the unified cmd/kern parser's semantics for
// the semantic commands' flags, via the shared cliflags.Parser (single-dash
// long forms normalize, --flag=value and --flag value both work, a trailing
// value-less flag is accept-and-default, unknown flags are a sticky parse
// error).
func parseSemanticFlags(args []string) (semanticFlags, []string, error) {
	var f semanticFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--from":
			p.SetStr(&f.from, inline, hasInline)
		case "--to":
			p.SetStr(&f.to, inline, hasInline)
		case "--range":
			p.SetStr(&f.range_, inline, hasInline)
		case "--json":
			p.SetBool(&f.json, "--json", inline, hasInline)
		case "--file":
			p.SetStr(&f.file, inline, hasInline)
		case "--base":
			p.SetStr(&f.base, inline, hasInline)
		case "--local":
			p.SetStr(&f.local, inline, hasInline)
		case "--remote":
			p.SetStr(&f.remote, inline, hasInline)
		case "--apply":
			p.SetBool(&f.apply, "--apply", inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

// RunSemanticDiff executes `kern semantic-diff [--from REV] [--to REV]
// [--range A..B] [--root ROOT]`. It returns the process exit code (0 ok,
// 1 runtime failure, 2 usage error).
func RunSemanticDiff(rest []string) int {
	return run(func() int { runSemanticDiffCLI(rest); return 0 })
}

// RunSemanticMerge executes `kern semantic-merge [--file FILE] [--base CODE]
// [--local CODE] [--remote CODE] [--apply] [--json] [--root ROOT]`. It
// returns the process exit code (0 ok, 1 runtime failure, 2 usage error).
func RunSemanticMerge(rest []string) int {
	return run(func() int { runSemanticMergeCLI(rest); return 0 })
}

func runSemanticDiffCLI(rest []string) {
	f, pos, err := parseSemanticFlags(rest)
	if err != nil {
		fatalUsage("flags: %v\nusage: kern semantic-diff [--from REV] [--to REV] [--range A..B] [--root ROOT]", err)
	}

	args := map[string]any{"root": f.root, "action": "diff"}
	if f.from != "" {
		args["from"] = f.from
	}
	if f.to != "" {
		args["to"] = f.to
	}
	if f.range_ != "" {
		args["range"] = f.range_
	} else if len(pos) > 0 {
		args["range"] = pos[0]
	}
	out, err := merge.Tool(context.Background(), merge.Hooks{LoadIndex: index.LoadOrBuildContext}, args)
	if err != nil {
		fatal("kern_semantic: %v — see kern doctor for diagnostics", err)
	}
	fmt.Println(out)
}

func runSemanticMergeCLI(rest []string) {
	f, _, err := parseSemanticFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}

	format := "text"
	if f.json {
		format = "json"
	}

	args := map[string]any{
		"action": "merge",
		"file":   f.file,
		"base":   f.base,
		"local":  f.local,
		"remote": f.remote,
		"apply":  f.apply,
		"format": format,
		"root":   f.root,
	}
	out, err := merge.Tool(context.Background(), merge.Hooks{LoadIndex: index.LoadOrBuildContext}, args)
	if err != nil {
		fatal("kern_semantic: %v — see kern doctor for diagnostics", err)
	}
	fmt.Println(out)
}
