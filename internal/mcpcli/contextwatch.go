// contextwatch.go implements the `kern context-watch` CLI entry point: flag
// parsing, stdin collection and rendering live here; the audit body is
// contextwatch.Analyze in internal/mcp/contextwatch.
package mcpcli

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/mcp/contextwatch"
)

// RunContextWatch executes `kern context-watch [--budget NUM] [--format
// text|json] <text>`. It returns the process exit code (0 ok, 1 runtime
// failure, 2 usage error).
func RunContextWatch(rest []string) int {
	return run(func() int { runContextWatchCLI(rest); return 0 })
}

type contextWatchFlags struct {
	budget    int
	budgetSet bool
	format    string
	text      string
}

// parseContextWatchFlags replicates the unified cmd/kern parser's semantics
// for the context-watch flags, via the shared cliflags.Parser (single-dash
// long forms normalize, --flag=value and --flag value both work, a trailing
// value-less flag is accept-and-default, unknown flags are a sticky parse
// error). --budget sets budgetSet even when its value is missing (the
// historical quirk: a trailing value-less --budget selects budget=0, not the
// 32000 default).
func parseContextWatchFlags(args []string) (contextWatchFlags, []string, error) {
	var f contextWatchFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--budget":
			p.SetInt(&f.budget, "--budget", inline, hasInline)
			f.budgetSet = true
		case "--format":
			p.SetStr(&f.format, inline, hasInline)
		case "--text":
			p.SetStr(&f.text, inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

func runContextWatchCLI(rest []string) {
	f, pos, err := parseContextWatchFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}

	budget := 32000
	if f.budgetSet {
		budget = f.budget
	}
	format := f.format
	raw := f.text
	if raw == "" && len(pos) > 0 {
		raw = strings.Join(pos, " ")
	}
	if raw == "" {
		b, rerr := cliflags.ReadStdin()
		if rerr == nil {
			raw = string(b)
		}
	}
	if raw == "" {
		fatalUsage("usage: kern context-watch [--budget NUM] [--format text|json] <text>")
	}
	out, err := contextwatch.Analyze(raw, budget, strings.ToLower(format))
	if err != nil {
		fatal("kern_context_watch: %v — see kern doctor for diagnostics", err)
	}
	fmt.Println(out)
}
