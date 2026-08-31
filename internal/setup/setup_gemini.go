package setup

import "path/filepath"

// wireGeminiHooks registers kern's BeforeTool, AfterTool and BeforeAgent hooks
// in ~/.gemini/settings.json (global user scope). Gemini names BeforeTool, not
// PreToolUse; AfterTool, not PostToolUse; BeforeAgent, not UserPromptSubmit.
// The BeforeTool hook blocks built-in read_file/run_shell_command/grep/glob/
// list_files calls via the kern-guard script (hard block + suggest); the
// AfterTool hook compresses oversized shell/read/grep results (via exit-2
// stderr substitution) and records edits + failures; BeforeAgent captures
// substantive user prompts. Global scope means hooks fire in EVERY project
// without per-repo setup. Existing hooks and the mcpServers key are preserved;
// stale kern hooks are replaced on re-run (overwrite-always).
func wireGeminiHooks(bin string) Status {
	guardPath, err := writeGuardScriptGlobal()
	if err != nil {
		return Status{Agent: "gemini-hooks", Installed: false, Path: filepath.Join(globalHomeDir(), ".gemini", "settings.json"), Note: err.Error()}
	}
	path := filepath.Join(globalHomeDir(), ".gemini", "settings.json")
	groups := map[string]any{
		"BeforeTool": []any{
			map[string]any{
				"matcher": "read_file|run_shell_command|grep|glob|list_files",
				"hooks": []any{
					map[string]any{
						"type":        "command",
						"command":     guardPath,
						"name":        "kern-before-tool",
						"description": "kern: block built-in read/grep/glob/bash, suggest kern equivalents",
					},
				},
			},
		},
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
	return Status{Agent: "gemini-hooks", Installed: true, Path: path, Note: "gemini BeforeTool/AfterTool/BeforeAgent hooks registered"}
}
