package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const standardHosts = "claude/cursor/copilot/qwen/qoder/codex/continue"

func standardPayload(tool, key, value string) string {
	b, _ := json.Marshal(map[string]any{"tool_name": tool, "tool_input": map[string]string{key: value}})
	return string(b)
}

func antigravityPayload(tool, key, value string) string {
	b, _ := json.Marshal(map[string]any{"toolCall": map[string]any{"name": tool, "args": map[string]string{key: value}}})
	return string(b)
}

type hostPayloadContract struct {
	name        string
	payload     string
	env         []string
	wantExit    int
	wantStderr  string
	wantEvent   string
	wantSubject string
}

func hostPayloadContracts(readme, source string) []hostPayloadContract {
	deny := []string{"KERN_GUARD_MODE=deny"}
	bypass := []string{"KERN_BYPASS=1", "KERN_GUARD_MODE=deny"}
	return []hostPayloadContract{
		{name: standardHosts + " code read", payload: standardPayload("Read", "file_path", source), env: deny, wantExit: 2, wantStderr: "kern_explore", wantEvent: "kern-guard-deny", wantSubject: source},
		{name: "gemini code read", payload: standardPayload("read_file", "file_path", source), env: deny, wantExit: 2, wantStderr: "kern_explore", wantEvent: "kern-guard-deny", wantSubject: source},
		{name: "antigravity code read", payload: antigravityPayload("view_file", "AbsolutePath", source), env: deny, wantExit: 2, wantStderr: "kern_explore", wantEvent: "kern-guard-deny", wantSubject: source},
		{name: "kiro code read", payload: standardPayload("read", "path", source), env: deny, wantExit: 2, wantStderr: "kern_explore", wantEvent: "kern-guard-deny", wantSubject: source},

		{name: standardHosts + " non-code read", payload: standardPayload("Read", "file_path", readme), wantExit: 0},
		{name: "gemini non-code read", payload: standardPayload("read_file", "file_path", readme), wantExit: 0},
		{name: "antigravity non-code read", payload: antigravityPayload("view_file", "AbsolutePath", readme), wantExit: 0},
		{name: "kiro non-code read", payload: standardPayload("read", "path", readme), wantExit: 0},

		{name: standardHosts + " grep", payload: standardPayload("Grep", "pattern", "Alpha"), env: deny, wantExit: 2, wantStderr: "kern_search", wantEvent: "kern-guard-deny", wantSubject: "Alpha"},
		{name: "gemini grep", payload: standardPayload("grep", "pattern", "Alpha"), env: deny, wantExit: 2, wantStderr: "kern_search", wantEvent: "kern-guard-deny", wantSubject: "Alpha"},
		{name: "antigravity grep", payload: antigravityPayload("grep_search", "Pattern", "Alpha"), env: deny, wantExit: 2, wantStderr: "kern_search", wantEvent: "kern-guard-deny", wantSubject: "Alpha"},

		{name: standardHosts + " glob", payload: standardPayload("Glob", "pattern", "**/*.go"), env: deny, wantExit: 2, wantStderr: "kern_buddy", wantEvent: "kern-guard-deny"},
		{name: "gemini list_files", payload: standardPayload("list_files", "pattern", "**/*.go"), env: deny, wantExit: 2, wantStderr: "kern_buddy", wantEvent: "kern-guard-deny"},
		{name: "antigravity find_by_name", payload: antigravityPayload("find_by_name", "Pattern", "**/*.go"), env: deny, wantExit: 2, wantStderr: "kern_buddy", wantEvent: "kern-guard-deny"},

		{name: standardHosts + " safe bash", payload: standardPayload("Bash", "command", "pwd"), wantExit: 0},
		{name: "gemini safe bash", payload: standardPayload("run_shell_command", "command", "git status"), wantExit: 0},
		{name: "antigravity safe bash", payload: antigravityPayload("run_command", "CommandLine", "echo ok"), wantExit: 0},
		{name: "kiro safe bash", payload: standardPayload("shell", "command", "pwd"), wantExit: 0},

		{name: standardHosts + " content bash", payload: standardPayload("Bash", "command", "cat "+source), env: deny, wantExit: 2, wantStderr: "kern_explore", wantEvent: "kern-guard-deny", wantSubject: "cat " + source},
		{name: "gemini content bash", payload: standardPayload("run_shell_command", "command", "cat "+source), env: deny, wantExit: 2, wantStderr: "kern_explore", wantEvent: "kern-guard-deny", wantSubject: "cat " + source},
		{name: "antigravity content bash", payload: antigravityPayload("run_command", "CommandLine", "cat "+source), env: deny, wantExit: 2, wantStderr: "kern_explore", wantEvent: "kern-guard-deny", wantSubject: "cat " + source},
		{name: "kiro content bash", payload: standardPayload("shell", "command", "cat "+source), env: deny, wantExit: 2, wantStderr: "kern_explore", wantEvent: "kern-guard-deny", wantSubject: "cat " + source},

		{name: standardHosts + " compound bash", payload: standardPayload("Bash", "command", "git status && cat "+source), env: deny, wantExit: 2, wantStderr: "kern_meta", wantEvent: "kern-guard-deny", wantSubject: "git status && cat " + source},
		{name: "gemini compound bash", payload: standardPayload("run_shell_command", "command", "git status && cat "+source), env: deny, wantExit: 2, wantStderr: "kern_meta", wantEvent: "kern-guard-deny", wantSubject: "git status && cat " + source},
		{name: "antigravity compound bash", payload: antigravityPayload("run_command", "CommandLine", "git status && cat "+source), env: deny, wantExit: 2, wantStderr: "kern_meta", wantEvent: "kern-guard-deny", wantSubject: "git status && cat " + source},
		{name: "kiro compound bash", payload: standardPayload("shell", "command", "git status && cat "+source), env: deny, wantExit: 2, wantStderr: "kern_meta", wantEvent: "kern-guard-deny", wantSubject: "git status && cat " + source},

		{name: standardHosts + " bypass refusal", payload: standardPayload("Read", "file_path", source), env: bypass, wantExit: 2, wantStderr: "kern_explore", wantEvent: "kern-guard-bypass"},
		{name: "gemini bypass refusal", payload: standardPayload("run_shell_command", "command", "cat "+source), env: bypass, wantExit: 2, wantStderr: "kern_explore", wantEvent: "kern-guard-bypass"},
		{name: "antigravity bypass refusal", payload: antigravityPayload("run_command", "CommandLine", "cat "+source), env: bypass, wantExit: 2, wantStderr: "kern_explore", wantEvent: "kern-guard-bypass"},
		{name: "kiro bypass refusal", payload: standardPayload("read", "path", source), env: bypass, wantExit: 2, wantStderr: "kern_explore", wantEvent: "kern-guard-bypass"},
	}
}

func TestGuardHostPayloadParity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	readme := filepath.Join(dir, "README.md")
	source := filepath.Join(dir, "source.go")
	if err := os.WriteFile(readme, []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := writeGuardScriptFile(t)
	for _, tc := range hostPayloadContracts(readme, source) {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			code, stderr := runGuard(t, script, tc.payload, append([]string{"HOME=" + home}, tc.env...))
			if code != tc.wantExit {
				t.Fatalf("exit=%d, want %d (stderr: %q)", code, tc.wantExit, stderr)
			}
			if tc.wantStderr != "" && !strings.Contains(stderr, tc.wantStderr) {
				t.Fatalf("stderr missing %q: %q", tc.wantStderr, stderr)
			}
			if tc.wantExit == 0 && stderr != "" {
				t.Fatalf("passthrough emitted stderr: %q", stderr)
			}
			if tc.wantEvent == "" {
				return
			}
			recs := auditLines(t, home)
			if len(recs) != 1 || recs[0]["event"] != tc.wantEvent {
				t.Fatalf("audit=%v, want one %s record", recs, tc.wantEvent)
			}
			if tc.wantSubject != "" && recs[0]["subject"] != tc.wantSubject {
				t.Fatalf("audit subject=%q, want %q", recs[0]["subject"], tc.wantSubject)
			}
		})
	}

	// Operator allow-once and default auto-redirect are exercised once per
	// payload schema (standard, Gemini, Antigravity, Kiro).
	allowCases := []struct {
		name    string
		payload string
	}{
		{"standard", standardPayload("Bash", "command", "printf hi")},
		{"gemini", standardPayload("run_shell_command", "command", "printf hi")},
		{"antigravity", antigravityPayload("run_command", "CommandLine", "printf hi")},
		{"kiro", standardPayload("shell", "command", "printf hi")},
	}
	for _, tc := range allowCases {
		t.Run("allow-once/"+tc.name, func(t *testing.T) {
			home := t.TempDir()
			sum := sha256.Sum256([]byte("printf hi"))
			reg := filepath.Join(home, "allow-once.jsonl")
			line := fmt.Sprintf(`{"hash":%q,"subject":"probe","created":"2026-10-20T00:00:00Z","used":""}`, hex.EncodeToString(sum[:]))
			if err := os.WriteFile(reg, []byte(line+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			code, stderr := runGuard(t, script, tc.payload, []string{"HOME=" + home, "KERN_GUARD_ALLOW_ONCE=" + reg})
			if code != 0 || stderr != "" {
				t.Fatalf("exit=%d stderr=%q, want one-time allow", code, stderr)
			}
		})
	}

	redirectCases := []struct {
		name    string
		payload string
	}{
		{"standard", standardPayload("Bash", "command", "cat "+source)},
		{"gemini", standardPayload("run_shell_command", "command", "cat "+source)},
		{"antigravity", antigravityPayload("run_command", "CommandLine", "cat "+source)},
	}
	for _, tc := range redirectCases {
		t.Run("auto-redirect/"+tc.name, func(t *testing.T) {
			home := t.TempDir()
			outLog := filepath.Join(home, "kern-args.log")
			kernBin := filepath.Join(home, "kern")
			fake := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + fmt.Sprintf("%q", outLog) + "\nprintf 'kern result: %s\\n' \"$*\"\n"
			if err := os.WriteFile(kernBin, []byte(fake), 0o755); err != nil {
				t.Fatal(err)
			}
			code, stderr := runGuard(t, script, tc.payload, []string{"HOME=" + home, "KERN_BIN=" + kernBin})
			if code != 0 || stderr != "" {
				t.Fatalf("exit=%d stderr=%q, want redirect", code, stderr)
			}
			got, err := os.ReadFile(outLog)
			if err != nil {
				t.Fatalf("fake kern not invoked: %v", err)
			}
			if invoked := string(got); !strings.Contains(invoked, "compact") || !strings.Contains(invoked, source) {
				t.Fatalf("kern invoked with %q, want compact <source>", invoked)
			}
		})
	}
}

func TestGuardHostPayloadAliasFamilies(t *testing.T) {
	for _, arm := range []string{
		"read|read_file|view_file)",
		"grep|grep_file|grep_search)",
		"glob|find_by_name|list_files)",
		"bash|run_shell_command|shell|execute_bash|run_command",
	} {
		if !strings.Contains(kernGuardScript, arm) {
			t.Errorf("guard arm missing from kern-guard.sh: %q", arm)
		}
	}
}
