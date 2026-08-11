// Package hook implements native hooks that give non-opencode agents the same
// output compression and session-memory capture that the opencode plugin
// provides (see .opencode/plugins/kern.ts). Each agent's hook system has its
// own JSON framing, so the package parses the per-agent payloads and emits the
// agent-specific response:
//
//   - Claude Code: PostToolUse / UserPromptSubmit, configured in
//     .claude/settings.json under "hooks". A PostToolUse handler may replace
//     the tool's result via "updatedToolOutput".
//   - Gemini CLI: AfterTool / BeforeAgent, configured in .gemini/settings.json
//     (note: Gemini uses AfterTool, not PostToolUse). A handler that exits 2
//     with text on stderr hides the real tool result and substitutes it.
//
// Every handler is best-effort: a failure to parse, compress or remember never
// breaks the agent's tool call. The kern CLI exposes these via
// `kern hook <claude-post|claude-prompt|gemini-after|gemini-prompt> [root]`.
package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/optimize"
)

// Threshold is the minimum output size (in characters) that triggers
// compression, matching the opencode plugin's DEFAULT_COMPACT_THRESHOLD.
const Threshold = 4000

// RateLimit is the minimum gap between prompt-capture writes, matching the
// plugin's chat.message dedupe window.
const RateLimit = 60 * time.Second

// stateDir holds hook bookkeeping (dedupe + rate stamps) under .kern, which
// kern's own walks skip and setup gitignores, so it never pollutes the index.
func stateDir(root string) (string, error) {
	dir := filepath.Join(root, ".kern", "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// remember writes a lesson into project memory. Prompt captures are
// rate-limited and deduped through .kern/hooks stamps so a chatty session
// cannot flood project memory (that would evict real lessons).
func remember(root, lesson string, rateLimit bool) error {
	if strings.TrimSpace(lesson) == "" {
		return nil
	}
	if rateLimit {
		dir, err := stateDir(root)
		if err != nil {
			return err
		}
		last := filepath.Join(dir, "prompt.last")
		if b, err := os.ReadFile(last); err == nil && string(b) == lesson {
			return nil
		}
		stamp := filepath.Join(dir, "prompt.stamp")
		if st, err := os.Stat(stamp); err == nil && time.Since(st.ModTime()) < RateLimit {
			return nil
		}
		_ = os.WriteFile(stamp, []byte("1"), 0o644)
		_ = os.WriteFile(last, []byte(lesson), 0o644)
	}
	return memory.Add(root, lesson)
}

// compress runs kern's log compressor on text and returns the compacted
// result, or "" when the text is below Threshold or the compressor fails.
// Compression failures are swallowed: the agent keeps the original output.
func compress(text string) string {
	if len(text) < Threshold {
		return ""
	}
	res, err := optimize.Log(text, optimize.Options{})
	if err != nil || res.Output == "" || res.Output == text {
		return ""
	}
	return "[kern] compressed " + compact(len(text)) + " -> " + compact(len(res.Output)) + " chars\n" + res.Output
}

func compact(n int) string {
	if n >= 1000 {
		return strconv.Itoa(n/1000) + "k"
	}
	return strconv.Itoa(n)
}

// failRe marks command output that indicates a real failure. Anchored like the
// plugin's FAIL_RE so a successful command that merely mentions "error" is not
// misremembered as a failure.
var failRe = regexp.MustCompile(`(?mi)^\s*(?:error|fatal|panic|failed|cannot)[:;]|panic:|command not found|: No such file|no such file or directory`)

// firstFailureLine returns the first line of text that looks like a failure,
// or "".
func firstFailureLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if failRe.MatchString(line) && strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// editTools are the Claude Code / Gemini tool names that write files.
var editTools = map[string]bool{"Edit": true, "Write": true, "edit_file": true, "write_file": true}

// bashTools are the tool names whose output is worth compressing.
var bashTools = map[string]bool{"Bash": true, "Read": true, "Grep": true, "_shell_": true, "read_file": true, "grep": true, "glob": true}

// --- Claude Code -----------------------------------------------------------

// ClaudePostInput mirrors the fields of a Claude Code PostToolUse hook payload
// that kern uses. Unknown fields are ignored, so future additions to the
// schema keep working.
type ClaudePostInput struct {
	Cwd        string          `json:"cwd"`
	ToolName   string          `json:"tool_name"`
	ToolInput  map[string]any  `json:"tool_input"`
	ToolResult json.RawMessage `json:"tool_response"`
}

// ClaudePost handles a Claude Code PostToolUse hook: it compresses oversized
// Bash/Read/Grep results (replacing them via updatedToolOutput) and records
// edits and failed commands into project memory. It returns the JSON to print
// on stdout (always a valid hook response; "{}" when no action was taken).
func ClaudePost(root string, in []byte) (string, error) {
	var p ClaudePostInput
	if err := json.Unmarshal(in, &p); err != nil {
		return "{}", nil // not a PostToolUse payload: ignore
	}
	if p.Cwd != "" && root == "." {
		root = p.Cwd
	}
	captureToolOutcome(root, p.ToolName, p.ToolInput, resultText(p.ToolResult))

	compressed := ""
	if bashTools[p.ToolName] {
		compressed = compress(resultText(p.ToolResult))
	}
	if compressed == "" {
		return "{}", nil
	}
	// updatedToolOutput replaces the tool's result before Claude sees it.
	out, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":    "PostToolUse",
			"updatedToolOutput": compressed,
		},
	})
	if err != nil {
		return "{}", nil
	}
	return string(out), nil
}

// ClaudePrompt handles a Claude Code UserPromptSubmit hook: substantive user
// prompts are recorded into project memory (rate-limited and deduped).
func ClaudePrompt(root string, in []byte) error {
	var p struct {
		Cwd    string `json:"cwd"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(in, &p); err != nil {
		return nil
	}
	if p.Cwd != "" && root == "." {
		root = p.Cwd
	}
	rememberPrompt(root, p.Prompt)
	return nil
}

// --- Gemini CLI ------------------------------------------------------------

// GeminiAfterInput mirrors the fields of a Gemini CLI AfterTool hook payload.
type GeminiAfterInput struct {
	ToolName   string         `json:"tool_name"`
	ToolInput  map[string]any `json:"tool_input"`
	ToolResult struct {
		LLMContent   string `json:"llmContent"`
		ReturnDisplay string `json:"returnDisplay"`
		Error        string `json:"error"`
	} `json:"tool_response"`
}

// GeminiAfter handles a Gemini CLI AfterTool hook. When the tool output is
// large it returns a replacement string; the CLI then exits 2 with it on
// stderr, which Gemini documents as hiding the real result and substituting
// the stderr text. Also records edits and failures into project memory.
func GeminiAfter(root string, in []byte) (string, error) {
	var p GeminiAfterInput
	if err := json.Unmarshal(in, &p); err != nil {
		return "", nil
	}
	raw := geminiResultText(p.ToolResult)
	captureToolOutcome(root, p.ToolName, p.ToolInput, raw)

	if !bashTools[p.ToolName] {
		return "", nil
	}
	return compress(raw), nil
}

// geminiResultText joins the display fields of a Gemini tool_response so
// compression and failure detection see what the model saw.
func geminiResultText(r struct {
	LLMContent   string `json:"llmContent"`
	ReturnDisplay string `json:"returnDisplay"`
	Error        string `json:"error"`
}) string {
	var b strings.Builder
	for _, s := range []string{r.LLMContent, r.ReturnDisplay, r.Error} {
		if s != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(s)
		}
	}
	return b.String()
}

// GeminiPrompt handles a Gemini CLI BeforeAgent hook: the latest user message
// is recorded into project memory (rate-limited and deduped).
func GeminiPrompt(root string, in []byte) error {
	var p struct {
		Prompt    string `json:"prompt"`
		LLMRequest struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		} `json:"llmRequest"`
	}
	if err := json.Unmarshal(in, &p); err != nil {
		return nil
	}
	text := p.Prompt
	if text == "" {
		for i := len(p.LLMRequest.Messages) - 1; i >= 0; i-- {
			if m := p.LLMRequest.Messages[i]; m.Role == "user" && strings.TrimSpace(m.Content) != "" {
				text = m.Content
				break
			}
		}
	}
	rememberPrompt(root, text)
	return nil
}

// --- shared helpers --------------------------------------------------------

// captureToolOutcome records edits and command failures into project memory,
// mirroring the opencode plugin's tool.execute.after session capture. text is
// the tool's extracted output (already unwrapped from the hook JSON framing).
func captureToolOutcome(root, toolName string, toolInput map[string]any, text string) {
	if editTools[toolName] {
		for _, k := range []string{"file_path", "filePath", "path"} {
			if v, ok := toolInput[k]; ok {
				if fp, ok := v.(string); ok && fp != "" {
					_ = remember(root, "Edited "+fp, false)
					return
				}
			}
		}
		return
	}
	if bashTools[toolName] {
		if line := firstFailureLine(text); line != "" {
			_ = remember(root, "Command failed: "+line, false)
		}
	}
}

// rememberPrompt records a user prompt when it looks substantive (not a short
// question, not a command), matching the plugin's chat.message filters.
func rememberPrompt(root, text string) {
	text = strings.TrimSpace(text)
	if len(text) < 16 || len(text) > 600 {
		return
	}
	if strings.HasSuffix(text, "?") {
		return
	}
	if strings.HasPrefix(text, "/") {
		return
	}
	_ = remember(root, "User: "+text, true)
}

// resultText extracts the printable text of a Claude-style tool_response
// (Bash: stdout/stderr; others: a raw string or JSON blob).
func resultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err == nil {
		var b strings.Builder
		for _, k := range []string{"stdout", "stderr", "output", "content"} {
			if v, ok := m[k].(string); ok && v != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(v)
			}
		}
		return b.String()
	}
	return string(raw)
}
