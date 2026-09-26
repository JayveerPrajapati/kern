// Package policydsl owns policy-as-code evaluation MCP tool bodies (kern_policy_dsl)
// as plain functions.
package policydsl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/root"
)

// Evaluate evaluates policy-as-code YAML/JSON specs against changed files, diffs, and imports.
func Evaluate(ctx context.Context, args map[string]any) (string, error) {
	root := root.ResolveRoot(mcpargs.ArgString(args, "root"))

	policyText := strings.TrimSpace(mcpargs.ArgString(args, "policy"))
	if policyText == "" {
		candidate := filepath.Join(root, ".kern", "policy.yaml")
		if data, err := os.ReadFile(candidate); err == nil {
			policyText = string(data)
		} else {
			policyText = `{
				"name": "default_guardrails",
				"rules": [
					{
						"id": "protect-workflows",
						"category": "security",
						"severity": "block",
						"description": "Modifications to CI/CD workflows require explicit authorization",
						"protected_paths": [".github/workflows/*", ".gitlab-ci.yml"]
					},
					{
						"id": "ban-unsafe",
						"category": "security",
						"severity": "block",
						"description": "Unsafe memory operations disallowed",
						"banned_imports": ["unsafe"]
					},
					{
						"id": "limit-diff-scope",
						"category": "architecture",
						"severity": "warn",
						"description": "Large multi-file diff warning",
						"max_files_changed": 15
					}
				]
			}`
		}
	}

	spec, err := intel.ParsePolicySpec(policyText)
	if err != nil {
		return "", fmt.Errorf("kern_policy_dsl: invalid policy spec: %w", err)
	}

	var files []string
	if fList, ok := args["files"].([]any); ok {
		for _, item := range fList {
			if s, ok := item.(string); ok {
				files = append(files, s)
			}
		}
	} else if fList, ok := args["files"].([]string); ok {
		files = fList
	} else if fStr := mcpargs.ArgString(args, "files"); fStr != "" {
		for _, p := range strings.Split(fStr, ",") {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				files = append(files, trimmed)
			}
		}
	}

	diff := mcpargs.ArgString(args, "diff")

	var imports []string
	if impList, ok := args["imports"].([]any); ok {
		for _, item := range impList {
			if s, ok := item.(string); ok {
				imports = append(imports, s)
			}
		}
	} else if impList, ok := args["imports"].([]string); ok {
		imports = impList
	}

	eval := intel.EvaluatePolicy(spec, files, diff, imports)

	format := strings.ToLower(mcpargs.ArgString(args, "format"))
	if format == "json" {
		data, _ := json.MarshalIndent(eval, "", "  ")
		return string(data), nil
	}

	var sb strings.Builder
	sb.WriteString("## Policy-as-Code Evaluation Report\n\n")
	statusBadge := "✅ ALLOWED"
	if !eval.Allowed {
		statusBadge = "❌ BLOCKED"
	}
	sb.WriteString(fmt.Sprintf("**Verdict:** %s | **Rules Checked:** %d | **Passed:** %d\n\n",
		statusBadge, eval.TotalRules, eval.RulesPassed))

	if len(eval.Violations) > 0 {
		sb.WriteString("### 🛑 Blocking Violations\n")
		for _, v := range eval.Violations {
			sb.WriteString(fmt.Sprintf("- **[%s] %s**: %s\n  - Target: `%s`\n  - *Remediation*: %s\n",
				v.Category, v.RuleID, v.Message, v.Target, v.Remediation))
		}
		sb.WriteString("\n")
	}

	if len(eval.Warnings) > 0 {
		sb.WriteString("### ⚠️ Warnings\n")
		for _, w := range eval.Warnings {
			sb.WriteString(fmt.Sprintf("- **[%s] %s**: %s (Target: `%s`)\n",
				w.Category, w.RuleID, w.Message, w.Target))
		}
		sb.WriteString("\n")
	}

	if eval.Allowed && len(eval.Warnings) == 0 {
		sb.WriteString("All evaluated policies passed without warnings or violations.\n")
	}

	return sb.String(), nil
}
