//go:build !windows

// guard_hook_parity_test.go pins the pure-Go guard hook (`kern guard hook`)
// byte-parity against the legacy shell script (assets/hooks/kern-guard.sh)
// for every arm: same exit code, same stderr deny reason, same stdout shape
// (antigravity JSON decisions exact; serve outputs non-empty). The battery
// is the migration gate: hosts switch their PreToolUse command from the
// script to `kern guard hook` only while this stays green.
package setup

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGoHook executes `kern guard hook` with the payload on stdin under the
// same environment the script battery uses (isolated HOME, assumed kern
// availability, KERN_BIN pointing at the freshly built binary).
func runGoHook(t *testing.T, bin, stdin string, env []string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(bin, "guard", "hook")
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), append([]string{
		"KERN_GUARD_CONFIRM_TIMEOUT=1",
		"KERN_GUARD_ASSUME_KERN=1",
		"KERN_DAEMON=0",
		"HOME=" + t.TempDir(),
		"KERN_BIN=" + bin,
	}, env...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("run go hook: %v", err)
		} else {
			code = ee.ExitCode()
		}
	}
	return code, stdout.String(), stderr.String()
}

// runScriptHook runs the legacy script under the identical environment.
func runScriptHook(t *testing.T, script, bin, stdin string, env []string) (int, string, string) {
	t.Helper()
	cmd := exec.Command("sh", script)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), append([]string{
		"KERN_GUARD_CONFIRM_TIMEOUT=1",
		"KERN_GUARD_ASSUME_KERN=1",
		"KERN_DAEMON=0",
		"HOME=" + t.TempDir(),
		"KERN_BIN=" + bin,
	}, env...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("run script hook: %v", err)
		} else {
			code = ee.ExitCode()
		}
	}
	return code, stdout.String(), stderr.String()
}

func TestGuardHookParity(t *testing.T) {
	script := writeGuardScriptFile(t)
	bin := buildCurrentKern(t)

	dir := t.TempDir()
	fixture := filepath.Join(dir, "fixture.go")
	os.WriteFile(fixture, []byte("package p\n\nfunc Hello() {}\n"), 0o644)
	mdFixture := filepath.Join(dir, "notes.md")
	os.WriteFile(mdFixture, []byte("# notes\n"), 0o644)

	cases := []struct {
		name  string
		stdin string
		env   []string
	}{
		{name: "read code file denied", stdin: `{"tool_name":"Read","path":"internal/governance/allowonce.go"}`},
		{name: "read non-code allowed", stdin: `{"tool_name":"Read","path":"` + mdFixture + `"}`},
		{name: "read no path denied", stdin: `{"tool_name":"Read"}`},
		{name: "grep denied", stdin: `{"tool_name":"Grep","pattern":"ClassifyCommand"}`},
		{name: "grep no pattern denied", stdin: `{"tool_name":"Grep"}`},
		{name: "glob denied", stdin: `{"tool_name":"Glob"}`},
		{name: "bash empty denied", stdin: `{"tool_name":"Bash"}`},
		{name: "bash simple allowed", stdin: `{"tool_name":"Bash","tool_input":{"command":"pwd"}}`},
		{name: "bash git commit quoted arrows allowed", stdin: `{"tool_name":"Bash","tool_input":{"command":"git commit --only internal/verification/suppress.go -m 'fix: drop stale suppression; move count pin 84->83'"}}`},
		{name: "bash git commit unquoted arrow gate denied", stdin: `{"tool_name":"Bash","tool_input":{"command":"git commit -m 84->83"}}`},
		{name: "bash git show revpath served", stdin: `{"tool_name":"Bash","tool_input":{"command":"git show HEAD~1:internal/guardrule/guardrule.go"}}`},
		{name: "bash git diff rev served", stdin: `{"tool_name":"Bash","tool_input":{"command":"git diff HEAD~1 -- internal/guardrule/guardrule.go"}}`},
		{name: "bash gate rm denied", stdin: `{"tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/kern-smoke-xyz"}}`},
		{name: "bash git show rev denied", stdin: `{"tool_name":"Bash","tool_input":{"command":"git show HEAD:internal/governance/exec.go"}}`},
		{name: "bash cat go served", stdin: `{"tool_name":"Bash","tool_input":{"command":"cat ` + fixture + `"}}`},
		{name: "bash unknown tool governed", stdin: `{"tool_name":"Bash","tool_input":{"command":"terraform plan"}}`},
		{name: "edit passthrough", stdin: `{"tool_name":"Edit"}`},
		{name: "write passthrough", stdin: `{"tool_name":"Write"}`},
		{name: "gemini read_file alias denied", stdin: `{"tool_name":"read_file","path":"` + fixture + `"}`},
		{name: "gemini run_shell_command alias denied", stdin: `{"tool_name":"run_shell_command","args":{"command":"go test ./..."}}`},
		{name: "antigravity toolcall read denied", stdin: `{"toolCall":{"name":"read_file","args":{"filePath":"` + fixture + `"}}}`},
		{name: "antigravity toolcall bash allowed", stdin: `{"toolCall":{"name":"run_command","args":{"command":"pwd"}}}`},
		{name: "empty payload passes", stdin: ``},
		{name: "non-json payload passes", stdin: `not json at all`},
		{name: "bypass env refused falls through", stdin: `{"tool_name":"Read"}`, env: []string{"KERN_BYPASS=1", "KERN_BYPASS_MODE=refused:no-tty"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc, sout, serr := runScriptHook(t, script, bin, tc.stdin, tc.env)
			gc, gout, gerr := runGoHook(t, bin, tc.stdin, tc.env)

			if sc != gc {
				t.Errorf("exit code: script=%d go=%d\nscript stderr: %s\ngo stderr: %s", sc, gc, serr, gerr)
			}
			if serr != gerr {
				t.Errorf("stderr mismatch:\nscript: %q\ngo:     %q", serr, gerr)
			}
			// Deny cases (exit 2): stdout must match exactly (antigravity
			// JSON embeds the reason). Allow/serve cases (exit 0): serve
			// stdout carries environment-dependent kern output — require
			// non-empty parity only when the script produced output.
			if sc == 2 {
				if sout != gout {
					t.Errorf("stdout mismatch:\nscript: %q\ngo:     %q", sout, gout)
				}
			} else if (len(strings.TrimSpace(sout)) > 0) != (len(strings.TrimSpace(gout)) > 0) {
				t.Errorf("stdout presence mismatch: script=%q go=%q", sout, gout)
			}
		})
	}
}

// TestGuardHookParityAuditRecords pins the audit trail: denies from the Go
// hook land in the same ~/.kern/audit/bypass.jsonl with the same event name
// and tool/subject fields the script writes.
func TestGuardHookParityAuditRecords(t *testing.T) {
	bin := buildCurrentKern(t)
	payload := `{"tool_name":"Read","tool_input":{"path":"/tmp/parity-fixture.go"}}`

	home := t.TempDir()
	code, _, _ := runGoHook(t, bin, payload, []string{"HOME=" + home})
	if code != 2 {
		t.Fatalf("want exit 2, got %d", code)
	}
	data, err := os.ReadFile(filepath.Join(home, ".kern", "audit", "bypass.jsonl"))
	if err != nil {
		t.Fatalf("audit file missing: %v", err)
	}
	line := strings.TrimSpace(string(data))
	for _, want := range []string{`"event":"kern-guard-deny"`, `"tool":"read"`, `"/tmp/parity-fixture.go"`} {
		if !strings.Contains(line, want) {
			t.Errorf("audit record missing %s in %s", want, line)
		}
	}
}
