// Package synthtest owns the test synthesis MCP tool bodies (kern_synthesize_test)
// as plain functions.
package synthtest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/root"
	"github.com/JayveerPrajapati/kern/internal/sec"
	"github.com/JayveerPrajapati/kern/internal/synthtest"
)

// Hooks provides dependencies from the owning MCP server.
type Hooks struct {
	LoadIndex func(ctx context.Context, root string) (*index.Index, error)
}

// Synthesize scaffolds table-driven tests and edge-case invariants for symbols.
func Synthesize(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := root.ResolveRoot(mcpargs.ArgString(args, "root"))
	// sinks=<rule ids> switches to the tainted-sink scaffold mode (the
	// former kern_taint generate=true output, moved here): scan the project,
	// keep the tainted sinks whose rule matches, and emit one deterministic
	// test scaffold per sink (go test for Go sinks, pytest for Python sinks).
	if sinks := mcpargs.ArgString(args, "sinks"); sinks != "" {
		return taintScaffolds(ctx, h, root, sinks)
	}
	target := mcpargs.ArgString(args, "target")
	file := mcpargs.ArgString(args, "file")
	code := mcpargs.ArgString(args, "code")
	autoGap := mcpargs.ArgBool(args, "auto_gap")
	apply := mcpargs.ArgBool(args, "apply")
	format := mcpargs.ArgString(args, "format")

	var ix *index.Index
	if autoGap && h.LoadIndex != nil {
		ix, _ = h.LoadIndex(ctx, root)
	}

	req := synthtest.Request{
		Target:  target,
		File:    file,
		Code:    code,
		Root:    root,
		AutoGap: autoGap,
		Apply:   apply,
		Index:   ix,
	}

	res, err := synthtest.Synthesize(req)
	if err != nil {
		return "", fmt.Errorf("synthesize test: %w", err)
	}

	if format == "json" {
		b, jerr := json.MarshalIndent(res, "", "  ")
		if jerr != nil {
			return "", jerr
		}
		return string(b), nil
	}

	var sb strings.Builder
	sb.WriteString("# Synthesize Test Report\n\n")
	sb.WriteString(fmt.Sprintf("- **Target Symbol**: `%s`\n", res.TargetSymbol))
	if res.TargetFile != "" {
		sb.WriteString(fmt.Sprintf("- **Target Source**: `%s`\n", res.TargetFile))
	}
	sb.WriteString(fmt.Sprintf("- **Test File**: `%s`\n", res.TestFile))
	sb.WriteString(fmt.Sprintf("- **Test Function**: `%s`\n", res.TestFunction))
	sb.WriteString(fmt.Sprintf("- **Applied to Disk**: `%v`\n", res.Applied))
	if res.Message != "" {
		sb.WriteString(fmt.Sprintf("- **Notice**: %s\n", res.Message))
	}
	if len(res.Cases) > 0 {
		sb.WriteString(fmt.Sprintf("- **Synthesized Cases**: %s\n\n", strings.Join(res.Cases, ", ")))
	}

	if res.Diff != "" {
		sb.WriteString("### Proposed Test Diff\n```diff\n")
		sb.WriteString(res.Diff)
		sb.WriteString("```\n\n")
	}

	sb.WriteString("### Synthesized Test Code\n```go\n")
	sb.WriteString(res.TestCode)
	sb.WriteString("\n```\n")

	return sb.String(), nil
}

// taintScaffolds implements the sinks= mode of kern_synthesize_test: one
// deterministic test scaffold per tainted security sink whose rule matches
// the requested comma-separated rule ids (the former kern_taint generate=true
// output, moved here; go test for Go sinks, pytest for Python sinks).
func taintScaffolds(ctx context.Context, h Hooks, root, sinks string) (string, error) {
	want := map[string]bool{}
	for _, s := range strings.Split(sinks, ",") {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			want[s] = true
		}
	}
	findings, serr := sec.Scan(root)
	if serr != nil {
		return "", fmt.Errorf("security scan failed: %w", serr)
	}
	var ix *index.Index
	if h.LoadIndex != nil {
		ix, _ = h.LoadIndex(ctx, root)
	}
	tainted := sec.TaintLite(ix, findings)
	var b strings.Builder
	count := 0
	for _, tf := range tainted {
		if !tf.Tainted {
			continue
		}
		rule := strings.ToLower(tf.Rule)
		if !want[rule] {
			continue
		}
		sc := sec.ScaffoldFor(tf)
		lang := "go"
		if strings.HasSuffix(strings.ToLower(tf.File), ".py") || strings.HasPrefix(rule, "py-") {
			lang = "python"
		}
		fmt.Fprintf(&b, "# write to: %s\n```%s\n%s\n```\n", sc.File, lang, sc.Code)
		count++
	}
	if count == 0 {
		return "no tainted sinks matched sinks=" + sinks, nil
	}
	return b.String(), nil
}
