package hook

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

func largeOutput() string {
	// Repeated identical lines (classic log spam) that the compressor dedupes.
	var b strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "WARN [worker-%d] retrying upstream request after timeout, attempt %d of 5\n", i%4, i%5+1)
	}
	return b.String()
}

func claudePostPayload(tool string, result any) []byte {
	b, _ := json.Marshal(map[string]any{
		"hook_event_name": "PostToolUse",
		"cwd":             "/tmp/proj",
		"tool_name":       tool,
		"tool_input":      map[string]any{"command": "go test ./..."},
		"tool_response":   result,
	})
	return b
}

func TestClaudePostCompressesLargeBashOutput(t *testing.T) {
	dir := t.TempDir()
	out, err := ClaudePost(dir, claudePostPayload("Bash", map[string]any{
		"stdout": largeOutput(), "stderr": "", "interrupted": false,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	hso, ok := m["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("expected hookSpecificOutput, got %#v", m)
	}
	if hso["hookEventName"] != "PostToolUse" {
		t.Errorf("hookEventName = %v", hso["hookEventName"])
	}
	repl, _ := hso["updatedToolOutput"].(string)
	if !strings.Contains(repl, "[kern] compressed") {
		t.Errorf("updatedToolOutput should carry compressed output, got %q", repl)
	}
}

func TestClaudePostSkipsSmallOutput(t *testing.T) {
	dir := t.TempDir()
	out, err := ClaudePost(dir, claudePostPayload("Bash", map[string]any{"stdout": "ok", "stderr": ""}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "{}" {
		t.Errorf("expected {} for small output, got %q", out)
	}
}

func TestClaudePostRecordsEditAndFailure(t *testing.T) {
	dir := t.TempDir()

	// Edit tool: remembered as "Edited <path>".
	edit := claudePostPayload("Edit", map[string]any{"filePath": "src/a.go", "success": true})
	edit = []byte(strings.Replace(string(edit), `"command":"go test ./..."`, `"file_path":"src/a.go"`, 1))
	if out, err := ClaudePost(dir, edit); err != nil || out != "{}" {
		t.Fatalf("edit should not compress, got %q err %v", out, err)
	}
	lessons := memory.List(dir)
	if len(lessons) != 1 || !strings.Contains(lessons[0].Text, "Edited src/a.go") {
		t.Fatalf("edit not remembered: %+v", lessons)
	}

	// Failing command: remembered as "Command failed: ...".
	fail := claudePostPayload("Bash", map[string]any{"stdout": "error: cannot find package", "stderr": ""})
	if _, err := ClaudePost(dir, fail); err != nil {
		t.Fatal(err)
	}
	lessons = memory.List(dir)
	if len(lessons) != 2 || !strings.Contains(lessons[0].Text, "Command failed: error: cannot find package") {
		t.Fatalf("failure not remembered: %+v", lessons)
	}

	// Successful command mentioning "error" must NOT be remembered.
	ok := claudePostPayload("Bash", map[string]any{"stdout": "no error found", "stderr": ""})
	if _, err := ClaudePost(dir, ok); err != nil {
		t.Fatal(err)
	}
	if got := len(memory.List(dir)); got != 2 {
		t.Fatalf("false-positive failure remembered: %d lessons", got)
	}
}

func TestClaudePostIgnoresGarbage(t *testing.T) {
	dir := t.TempDir()
	out, err := ClaudePost(dir, []byte("not json"))
	if err != nil {
		t.Fatal(err)
	}
	if out != "{}" {
		t.Errorf("garbage input should yield {}, got %q", out)
	}
}

func TestGeminiAfterCompressesAndMemory(t *testing.T) {
	dir := t.TempDir()
	large := map[string]any{
		"tool_name": "_shell_",
		"tool_response": map[string]any{
			"llmContent":    largeOutput(),
			"returnDisplay": "",
			"error":         "",
		},
	}
	b, _ := json.Marshal(large)
	repl, err := GeminiAfter(dir, b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(repl, "[kern] compressed") {
		t.Errorf("expected compressed replacement, got %q", repl)
	}

	small, _ := json.Marshal(map[string]any{"tool_name": "_shell_", "tool_response": map[string]any{"llmContent": "ok"}})
	if repl, _ := GeminiAfter(dir, small); repl != "" {
		t.Errorf("small output should not block, got %q", repl)
	}

	// Failed shell command is remembered.
	fail, _ := json.Marshal(map[string]any{
		"tool_name":     "_shell_",
		"tool_response": map[string]any{"llmContent": "fatal: repository not found", "error": "fatal: repository not found"},
	})
	if _, err := GeminiAfter(dir, fail); err != nil {
		t.Fatal(err)
	}
	lessons := memory.List(dir)
	found := false
	for _, l := range lessons {
		if strings.Contains(l.Text, "fatal: repository not found") {
			found = true
		}
	}
	if !found {
		t.Fatalf("gemini failure not remembered: %+v", lessons)
	}
}

func TestGeminiPromptCapturesUserMessage(t *testing.T) {
	dir := t.TempDir()
	b, _ := json.Marshal(map[string]any{
		"llmRequest": map[string]any{
			"messages": []map[string]any{
				{"role": "user", "content": "please refactor the retry loop in client.go"},
			},
		},
	})
	if err := GeminiPrompt(dir, b); err != nil {
		t.Fatal(err)
	}
	lessons := memory.List(dir)
	if len(lessons) != 1 || !strings.Contains(lessons[0].Text, "User:") {
		t.Fatalf("prompt not captured: %+v", lessons)
	}
	// Dedupe: identical prompt must not be stored again.
	GeminiPrompt(dir, b)
	if got := len(memory.List(dir)); got != 1 {
		t.Fatalf("dedupe failed: %d lessons", got)
	}
}

func TestClaudePromptSkipsTrivia(t *testing.T) {
	dir := t.TempDir()
	for _, q := range []string{"what?", "short", "/help", ""} {
		b, _ := json.Marshal(map[string]any{"prompt": q})
		if err := ClaudePrompt(dir, b); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(memory.List(dir)); got != 0 {
		t.Fatalf("trivia should not be remembered: %+v", memory.List(dir))
	}
}
