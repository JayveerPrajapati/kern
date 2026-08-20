package setup

import "path/filepath"

// wireGeminiHooks registers kern's AfterTool and BeforeAgent hooks in
// .gemini/settings.json (Gemini names AfterTool, not PostToolUse; BeforeAgent,
// not UserPromptSubmit). The AfterTool hook compresses oversized shell/read/
// grep results (via exit-2 stderr substitution) and records edits + failures;
// BeforeAgent captures substantive user prompts. Existing hooks and the
// mcpServers key are preserved.
func wireGeminiHooks(root, bin string) Status {
	path := filepath.Join(root, ".gemini", "settings.json")
	groups := map[string]any{
		"AfterTool": []any{
			map[string]any{
				"matcher": "_shell_|read_file|grep|glob",
				"hooks": []any{
					map[string]any{
						"type":        "command",
						"command":     bin + ` hook gemini-after "$GEMINI_PROJECT_DIR"`,
						"name":        "kern-after-tool",
						"description": "kern: compress oversized tool output, record edits and failures",
					},
				},
			},
		},
		"BeforeAgent": []any{
			map[string]any{
				"hooks": []any{
					map[string]any{
						"type":        "command",
						"command":     bin + ` hook gemini-prompt "$GEMINI_PROJECT_DIR"`,
						"name":        "kern-prompt-capture",
						"description": "kern: capture substantive user prompts into project memory",
					},
				},
			},
		},
	}
	if err := mergeHookGroups(path, groups); err != nil {
		return Status{Agent: "gemini-hooks", Path: path, Note: err.Error()}
	}
	return Status{Agent: "gemini-hooks", Installed: true, Path: path, Note: "gemini AfterTool/BeforeAgent hooks registered"}
}
