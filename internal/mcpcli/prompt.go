// prompt.go implements the `kern prompt-fill` CLI entry point: flag parsing
// and rendering live here; the template rendering body is prompt.Fill in
// internal/mcp/prompt.
package mcpcli

import (
	"context"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/mcp/prompt"
)

// RunPromptFill executes `kern prompt-fill --template <name> [--task <desc>]
// [--file <file>]`. It returns the process exit code (0 ok, 1 runtime
// failure, 2 usage error).
func RunPromptFill(rest []string) int {
	return run(func() int { runPromptFillCLI(rest); return 0 })
}

type promptFillFlags struct {
	template     string
	task         string
	injectMemory bool
	root         string
	file         string
}

// parsePromptFillFlags replicates the unified cmd/kern parser's semantics
// for the prompt-fill flags, via the shared cliflags.Parser (single-dash
// long forms normalize, --flag=value and --flag value both work, a trailing
// value-less flag is accept-and-default, unknown flags are a sticky parse
// error).
func parsePromptFillFlags(args []string) (promptFillFlags, []string, error) {
	var f promptFillFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--template":
			p.SetStr(&f.template, inline, hasInline)
		case "--task":
			p.SetStr(&f.task, inline, hasInline)
		case "--inject-memory":
			p.SetBool(&f.injectMemory, "--inject-memory", inline, hasInline)
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--file":
			p.SetStr(&f.file, inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

func runPromptFillCLI(rest []string) {
	f, pos, err := parsePromptFillFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}

	t := f.template
	if t == "" && len(pos) > 0 {
		t = pos[0]
	}
	desc := f.task
	if desc == "" && len(pos) > 1 {
		desc = strings.Join(pos[1:], " ")
	}
	if t == "" {
		fatalUsage("usage: kern prompt-fill --template <name> [--task <desc>] [--file <file>]")
	}
	args := map[string]any{
		"template":      t,
		"task":          desc,
		"inject_memory": f.injectMemory,
		"root":          f.root,
	}
	if f.file != "" {
		args["file"] = f.file
	}
	out, err := prompt.Fill(context.Background(), args)
	if err != nil {
		fatal("kern_prompt_fill: %v — see kern doctor for diagnostics", err)
	}
	fmt.Println(out)
}
