// transform.go implements the `kern ast-transform` CLI entry point: flag
// parsing, the in-process index load and rendering live here; the
// transformation body is transform.Transform in internal/mcp/transform. The
// transformation runs in-process (not through the loopback server, whose own
// index build races the tool call) so implement_interface resolves real
// interfaces — the index is loaded with the same load-or-build every other
// CLI command uses.
package mcpcli

import (
	"context"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/transform"
)

// RunAstTransform executes `kern ast-transform <action> [flags]`. It returns
// the process exit code (0 ok, 1 runtime failure, 2 usage error).
func RunAstTransform(rest []string) int {
	return run(func() int { runAstTransformCLI(rest); return 0 })
}

type astTransformFlags struct {
	root                             string
	action, file, target, iface      string
	field, fieldType, tag, sig, body string
	apply                            bool
}

// parseAstTransformFlags replicates the unified cmd/kern parser's semantics
// for the ast-transform flags, via the shared cliflags.Parser (single-dash
// long forms normalize, --flag=value and --flag value both work, a trailing
// value-less flag is accept-and-default, unknown flags are a sticky parse
// error).
func parseAstTransformFlags(args []string) (astTransformFlags, []string, error) {
	var f astTransformFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--action":
			p.SetStr(&f.action, inline, hasInline)
		case "--file":
			p.SetStr(&f.file, inline, hasInline)
		case "--target":
			p.SetStr(&f.target, inline, hasInline)
		case "--iface":
			p.SetStr(&f.iface, inline, hasInline)
		case "--field":
			p.SetStr(&f.field, inline, hasInline)
		case "--field-type":
			p.SetStr(&f.fieldType, inline, hasInline)
		case "--tag":
			p.SetStr(&f.tag, inline, hasInline)
		case "--sig":
			p.SetStr(&f.sig, inline, hasInline)
		case "--body":
			p.SetStr(&f.body, inline, hasInline)
		case "--apply":
			p.SetBool(&f.apply, "--apply", inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

func runAstTransformCLI(rest []string) {
	f, pos, err := parseAstTransformFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}

	act := f.action
	if act == "" {
		act = "implement_interface"
	}
	if len(pos) > 0 {
		act = pos[0]
	}

	// Load the index directly (the same load-or-build every other CLI command
	// uses) and fail loud when it cannot be built — never run against a nil
	// index, which would make interface lookups miss ("unknown interface").
	ix, err := index.LoadOrBuild(f.root)
	if err != nil {
		fatal("ast-transform: %v", err)
	}
	out, err := transform.Transform(context.Background(), transform.Hooks{
		LoadIndex: func(context.Context, string) (*index.Index, error) { return ix, nil },
	}, map[string]any{
		"action":           act,
		"file":             f.file,
		"root":             f.root,
		"target_symbol":    f.target,
		"interface_name":   f.iface,
		"field_name":       f.field,
		"field_type":       f.fieldType,
		"field_tag":        f.tag,
		"method_signature": f.sig,
		"method_body":      f.body,
		"apply":            f.apply,
	})
	if err != nil {
		fatal("ast-transform: %v", err)
	}
	fmt.Print(out)
}
