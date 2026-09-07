package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/synthtest"
)

// handleSynthesizeTest automatically scaffolds comprehensive table-driven unit tests
// and edge-case invariants for untested functions and methods.
func (s *Server) handleSynthesizeTest(ctx context.Context, args map[string]any) (string, error) {
	root := resolveRoot(argString(args, "root"))
	target := argString(args, "target")
	file := argString(args, "file")
	code := argString(args, "code")
	autoGap := argBool(args, "auto_gap")
	apply := argBool(args, "apply")
	format := argString(args, "format")

	var ix *index.Index
	if autoGap {
		ix, _ = s.loadIndex(ctx, root)
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
