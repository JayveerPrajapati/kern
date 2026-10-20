// evidence.go implements the `kern evidence-anchor` CLI entry point: flag
// parsing and rendering live here; the certificate body is evidence.Handle
// (action=anchor) in internal/mcp/evidence.
package mcpcli

import (
	"context"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/evidence"
)

// RunEvidenceAnchor executes `kern evidence-anchor [--claim TEXT] [--file F]
// [--line N] [--symbol NAME] [--root ROOT]`. It returns the process exit
// code (0 ok, 1 runtime failure, 2 usage error).
func RunEvidenceAnchor(rest []string) int {
	return run(func() int { runEvidenceAnchorCLI(rest); return 0 })
}

type evidenceAnchorFlags struct {
	root    string
	claim   string
	file    string
	lineRaw string
	symbol  string
}

// parseEvidenceAnchorFlags replicates the unified cmd/kern parser's
// semantics for the evidence-anchor flags, via the shared cliflags.Parser
// (single-dash long forms normalize, --flag=value and --flag value both
// work, a trailing value-less flag is accept-and-default, unknown flags are
// a sticky parse error).
func parseEvidenceAnchorFlags(args []string) (evidenceAnchorFlags, []string, error) {
	var f evidenceAnchorFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--claim":
			p.SetStr(&f.claim, inline, hasInline)
		case "--file":
			p.SetStr(&f.file, inline, hasInline)
		case "--line":
			p.SetStr(&f.lineRaw, inline, hasInline)
		case "--symbol":
			p.SetStr(&f.symbol, inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

func runEvidenceAnchorCLI(rest []string) {
	f, pos, err := parseEvidenceAnchorFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}

	c := f.claim
	if c == "" && len(pos) > 0 {
		c = strings.Join(pos, " ")
	}
	args := map[string]any{"root": f.root, "claim": c, "action": "anchor"}
	if f.file != "" {
		args["file"] = f.file
	}
	if f.lineRaw != "" {
		args["line"] = f.lineRaw
	}
	if f.symbol != "" {
		args["symbol"] = f.symbol
	}
	out, err := evidence.Handle(context.Background(), evidence.Hooks{LoadIndex: index.LoadOrBuildContext}, args)
	if err != nil {
		fatal("kern_evidence: %v — see kern doctor for diagnostics", err)
	}
	fmt.Println(out)
}
