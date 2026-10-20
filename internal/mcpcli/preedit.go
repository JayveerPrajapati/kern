// preedit.go implements the `kern pre-edit` CLI entry point: flag parsing,
// the file-existence guard and the JSON wrapper live here; the report body
// is preedit.Analyze in internal/mcp/preedit.
package mcpcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/preedit"
)

// RunPreEdit executes `kern pre-edit [--file <file>] [--lines N] [--symbol
// NAME] [--root ROOT] [--json]`. It returns the process exit code (0 ok,
// 1 runtime failure, 2 usage error).
func RunPreEdit(rest []string) int {
	return run(func() int { runPreEditCLI(rest); return 0 })
}

type preeditFlags struct {
	file   string
	lines  int
	symbol string
	root   string
	json   bool
}

// parsePreeditFlags replicates the unified cmd/kern parser's semantics for
// the pre-edit flags, via the shared cliflags.Parser (single-dash long forms
// normalize, --flag=value and --flag value both work, a trailing value-less
// flag is accept-and-default, unknown flags are a sticky parse error).
func parsePreeditFlags(args []string) (preeditFlags, []string, error) {
	var f preeditFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--file":
			p.SetStr(&f.file, inline, hasInline)
		case "--lines":
			p.SetInt(&f.lines, "--lines", inline, hasInline)
		case "--symbol":
			p.SetStr(&f.symbol, inline, hasInline)
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--json":
			p.SetBool(&f.json, "--json", inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

func runPreEditCLI(rest []string) {
	f, pos, err := parsePreeditFlags(rest)
	if err != nil {
		fatalUsage("usage: kern pre-edit [--file <file>] [--lines N] [--symbol NAME] [--root ROOT] [--json]")
	}

	file := f.file
	if file == "" && len(pos) > 0 {
		file = pos[0]
	}
	// L1: a bare `kern pre-edit` (no file, no symbol, no positionals) is a
	// usage error (exit 2), not a runtime failure (exit 1): there is nothing
	// to assess. The documented usage requires at least one of --file or
	// --symbol.
	if file == "" && f.symbol == "" && len(pos) == 0 {
		fatalUsage("usage: kern pre-edit [--file <file>] [--lines N] [--symbol NAME] [--root ROOT] [--json]\n  at least one of --file or --symbol is required")
	}
	// Never bless a path that does not exist: a nonexistent -file must fail
	// loudly (rc=1) instead of producing a LOW-risk "Safe to proceed" report.
	if file != "" {
		if _, err := os.Stat(file); err != nil {
			fatal("pre-edit: file not found: %s", file)
		}
	}
	linesStr := ""
	if f.lines > 0 {
		linesStr = strconv.Itoa(f.lines)
	}
	ix, err := index.LoadOrBuild(f.root)
	if err != nil {
		fatal("pre-edit: %v — see kern doctor for diagnostics", err)
	}
	out, err := preedit.Analyze(context.Background(), ix, f.root, file, f.symbol, linesStr)
	if err != nil {
		fatal("pre-edit: %v — see kern doctor for diagnostics", err)
	}
	if f.json {
		risk := "UNKNOWN"
		if i := strings.Index(out, "[Risk: "); i >= 0 {
			restStr := out[i+len("[Risk: "):]
			if j := strings.Index(restStr, "]"); j >= 0 {
				risk = restStr[:j]
			}
		}
		data, merr := json.MarshalIndent(map[string]any{
			"risk":   risk,
			"report": out,
		}, "", "  ")
		if merr == nil {
			fmt.Println(string(data))
			return
		}
	}
	fmt.Println(out)
}
