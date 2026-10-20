// compose.go implements the `kern compose` / `kern meta --pipeline` CLI
// entry point: pipeline input collection (--pipeline flag, positional JSON,
// stdin), validation and rendering live here; the pipeline engine is
// compose.Compose in internal/mcp/compose. ComposeRunner is injected by
// cmd/kern — the loopback server cannot be constructed inside this package
// (the mcp package imports the compose leaf), so the CLI's runner is the
// only wire that stays in cmd/kern.
package mcpcli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/mcp/compose"
)

// ComposeRunner executes a single pipeline step by tool name (id is the step
// id, e.g. "compose-step-1"). The CLI injects its loopback runner; the MCP
// server path uses its own runTool. Nil means no runner configured.
var ComposeRunner func(ctx context.Context, id, name string, args map[string]any) (string, error)

// RunCompose executes `kern compose` / `kern meta --pipeline`: parse the
// pipeline input and run it step by step through the pipeline engine. It
// returns the process exit code (0 ok, 1 runtime failure, 2 usage error).
func RunCompose(rest []string) int {
	return run(func() int { runComposeCLI(rest); return 0 })
}

type composeFlags struct {
	root       string
	pipeline   string
	timeout    int
	timeoutSet bool
}

// parseComposeFlags replicates the unified cmd/kern parser's semantics for
// the compose flags, via the shared cliflags.Parser (single-dash long forms
// normalize, --flag=value and --flag value both work, a trailing value-less
// flag is accept-and-default, unknown flags are a sticky parse error).
// --timeout sets timeoutSet even when its value is missing (the historical
// quirk, matching context-watch's --budget).
func parseComposeFlags(args []string) (composeFlags, []string, error) {
	var f composeFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--pipeline":
			p.SetStr(&f.pipeline, inline, hasInline)
		case "--timeout":
			p.SetInt(&f.timeout, "--timeout", inline, hasInline)
			f.timeoutSet = true
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

func runComposeCLI(rest []string) {
	f, pos, err := parseComposeFlags(rest)
	if err != nil {
		fatalUsage("usage: kern compose --pipeline '[{\"tool\": \"kern_search\", \"args\": {\"query\": \"Index.Search\"}}]'")
	}
	root := f.root
	pipelineJSON := f.pipeline
	timeout := "60"
	if f.timeoutSet {
		timeout = strconv.Itoa(f.timeout)
	}

	raw := pipelineJSON
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
		fatalUsage("usage: kern compose --pipeline '[{\"tool\": \"kern_search\", \"args\": {\"query\": \"Index.Search\"}}]'")
	}
	var steps any
	if err := json.Unmarshal([]byte(raw), &steps); err != nil {
		fatal("compose: invalid pipeline JSON: %v", err)
	}
	if ComposeRunner == nil {
		fatal("compose: tool runner not configured")
	}
	out, err := compose.Compose(context.Background(), compose.Hooks{RunTool: ComposeRunner}, map[string]any{
		"root":     root,
		"pipeline": steps,
		"timeout":  timeout,
	})
	if err != nil {
		fatal("kern_compose: %v — see kern doctor for diagnostics", err)
	}
	fmt.Println(out)
}
