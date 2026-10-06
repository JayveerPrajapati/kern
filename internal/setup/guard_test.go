package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// writeGuardScriptFile writes the embedded kern-guard.sh to a temp file with
// the same mode the installer uses (0755) and returns its path.
func writeGuardScriptFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "kern-guard.sh")
	if err := os.WriteFile(p, []byte(kernGuardScript), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// runGuard executes the guard script under sh with the given stdin and extra
// env, returning the process exit code and captured stderr. The child runs
// DETACHED from the controlling terminal (Setsid) so /dev/tty fails to open —
// the hook's bypass confirmation is a /dev/tty presence probe, and in a
// detached (agent/CI-like) context the probe must refuse. The confirmation
// timeout is forced low so any residual prompt path finishes fast.
func runGuard(t *testing.T, script, stdin string, env []string) (int, string) {
	t.Helper()
	cmd := exec.Command("sh", script)
	cmd.Stdin = strings.NewReader(stdin)
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	cmd.Env = append(os.Environ(), append([]string{"KERN_GUARD_CONFIRM_TIMEOUT=1"}, env...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run guard script: %v", err)
		}
		code = ee.ExitCode()
	}
	return code, stderr.String()
}

// TestKernGuardScript exercises the embedded PreToolUse guard: guarded tools
// (read/grep/glob/bash and their per-agent aliases) must block with exit 2 and
// a kern suggestion on stderr; unguarded tools, the KERN_ENFORCE=0 bypass and
// unparseable payloads must pass through with exit 0.
func TestKernGuardScript(t *testing.T) {
	script := writeGuardScriptFile(t)
	// Isolated HOME for the bypass cases so their audit records land in a
	// temp dir instead of the real ~/.kern/audit/bypass.jsonl.
	auditHome := t.TempDir()
	// Fixture files for the simple-read exemption matrix: non-code (raw),
	// small code (governed — size is irrelevant), code >=2KB (governed), and
	// .hh (governed since the guard's code-extension list matches the
	// plugin's, which includes hh/kts/cljs).
	dir := t.TempDir()
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	smallGo := filepath.Join(dir, "small.go")
	if err := os.WriteFile(smallGo, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bigGo := filepath.Join(dir, "big.go")
	if err := os.WriteFile(bigGo, []byte(strings.Repeat("package main\n", 400)), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(bigGo); st.Size() < 2048 {
		t.Fatalf("fixture big.go is %d bytes, need >=2048", st.Size())
	}
	bigHh := filepath.Join(dir, "big.hh")
	if err := os.WriteFile(bigHh, []byte(strings.Repeat("// header\n", 600)), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(bigHh); st.Size() < 2048 {
		t.Fatalf("fixture big.hh is %d bytes, need >=2048", st.Size())
	}
	cases := []struct {
		name       string
		stdin      string
		env        []string
		wantExit   int
		wantStderr []string // each substring must appear in stderr; empty = stderr must be empty
	}{
		{
			name:     "read blocked",
			stdin:    `{"tool_name":"Read"}`,
			wantExit: 2,
			wantStderr: []string{
				"kern_compact_file",
			},
		},
		{
			name:     "bash blocked",
			stdin:    `{"tool_name":"Bash"}`,
			wantExit: 2,
			wantStderr: []string{
				"kern_verify",
				"kern_exec",
			},
		},
		{
			name:     "grep blocked",
			stdin:    `{"tool_name":"Grep"}`,
			wantExit: 2,
			wantStderr: []string{
				"kern_search",
			},
		},
		{
			name:     "glob blocked",
			stdin:    `{"tool_name":"Glob"}`,
			wantExit: 2,
			wantStderr: []string{
				"kern_project_map",
			},
		},
		{
			// Regression: a KERN_BYPASS=1 string in the payload (e.g. a file
			// path) must NOT bypass the guard — only the env var may.
			name:     "KERN_BYPASS=1 in read path does not bypass",
			stdin:    `{"tool_name":"Read","path":"/repo/docs/KERN_BYPASS=1-notes.md"}`,
			wantExit: 2,
			wantStderr: []string{
				"kern_compact_file",
			},
		},
		{
			// Regression: a KERN_ENFORCE=0 string inside command/file content
			// must NOT bypass the guard for a blocked tool.
			name:     "KERN_ENFORCE=0 in bash content does not bypass",
			stdin:    `{"tool_name":"Bash","content":"export KERN_ENFORCE=0 && go test ./..."}`,
			wantExit: 2,
			wantStderr: []string{
				"kern_verify",
				"kern_exec",
			},
		},
		{
			name:       "edit passes through",
			stdin:      `{"tool_name":"Edit"}`,
			wantExit:   0,
			wantStderr: nil,
		},
		{
			name:       "write passes through",
			stdin:      `{"tool_name":"Write"}`,
			wantExit:   0,
			wantStderr: nil,
		},
		{
			name:     "gemini read_file blocked",
			stdin:    `{"tool_name":"read_file"}`,
			wantExit: 2,
			wantStderr: []string{
				"kern_compact_file",
			},
		},
		{
			name:     "gemini run_shell_command blocked",
			stdin:    `{"tool_name":"run_shell_command"}`,
			wantExit: 2,
			wantStderr: []string{
				"kern_verify",
				"kern_exec",
			},
		},
		{
			name:     "KERN_ENFORCE=0 refused without terminal",
			stdin:    `{"tool_name":"Read"}`,
			env:      []string{"KERN_ENFORCE=0", "HOME=" + auditHome},
			wantExit: 2,
			wantStderr: []string{
				"kern_compact_file",
			},
		},
		{
			name:     "KERN_BYPASS=1 refused without terminal",
			stdin:    `{"tool_name":"Read"}`,
			env:      []string{"KERN_BYPASS=1", "HOME=" + auditHome},
			wantExit: 2,
			wantStderr: []string{
				"kern_compact_file",
			},
		},
		{
			name:     "antigravity view_file blocked",
			stdin:    `{"toolCall":{"name":"view_file","args":{"AbsolutePath":"/some/file.go"}}}`,
			wantExit: 2,
			wantStderr: []string{
				"kern_compact_file",
			},
		},
		{
			name:     "antigravity run_command blocked",
			stdin:    `{"toolCall":{"name":"run_command","args":{"CommandLine":"go test ./..."}}}`,
			wantExit: 2,
			wantStderr: []string{
				"kern_verify",
				"kern_exec",
			},
		},
		{
			name:     "antigravity KERN_BYPASS=1 refused without terminal",
			stdin:    `{"toolCall":{"name":"run_command","args":{"CommandLine":"git commit -m test"}}}`,
			env:      []string{"KERN_BYPASS=1", "HOME=" + auditHome},
			wantExit: 2,
			wantStderr: []string{
				"kern_verify",
			},
		},
		{
			name:     "antigravity grep_search blocked",
			stdin:    `{"tool_name":"grep_search"}`,
			wantExit: 2,
			wantStderr: []string{
				"kern_search",
			},
		},
		{
			name:     "antigravity find_by_name blocked",
			stdin:    `{"tool_name":"find_by_name"}`,
			wantExit: 2,
			wantStderr: []string{
				"kern_project_map",
			},
		},
		{
			name:       "empty stdin passes through",
			stdin:      "",
			wantExit:   0,
			wantStderr: nil,
		},
		{
			name:       "malformed stdin passes through",
			stdin:      `{not json`,
			wantExit:   0,
			wantStderr: nil,
		},
		// --- simple-command exemption matrix (dcd4138 refinement) ---
		// Trivial exact-token commands run raw.
		{name: "trivial pwd allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"pwd"}}`, wantExit: 0, wantStderr: nil},
		{name: "trivial true allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"true"}}`, wantExit: 0, wantStderr: nil},
		{name: "trivial whoami allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"whoami"}}`, wantExit: 0, wantStderr: nil},
		{name: "trivial date allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"date -u"}}`, wantExit: 0, wantStderr: nil},
		{name: "trivial echo allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"echo hello"}}`, wantExit: 0, wantStderr: nil},
		{name: "trivial ls allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"ls -la"}}`, wantExit: 0, wantStderr: nil},
		{name: "trivial which allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"which gcc"}}`, wantExit: 0, wantStderr: nil},
		// git status (any args) and git log WITHOUT patch flags run raw.
		{name: "git status allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"git status"}}`, wantExit: 0, wantStderr: nil},
		{name: "git status --short allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"git status --short"}}`, wantExit: 0, wantStderr: nil},
		{name: "git log allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"git log"}}`, wantExit: 0, wantStderr: nil},
		{name: "git log --oneline allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"git log --oneline"}}`, wantExit: 0, wantStderr: nil},
		{name: "git log --pretty allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"git log --pretty=oneline"}}`, wantExit: 0, wantStderr: nil},
		// git log with patch-emitting flags emits file content — governed.
		{name: "git log -p blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git log -p"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "git log --patch blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git log --patch"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "git log -u blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git log -u"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "git log --raw blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git log --raw"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "git log --oneline -p blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git log --oneline -p"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		// kern read-only diagnostics (version/--version/doctor/health) run raw
		// — EXACT invocation only; any other kern subcommand (setup/mutate/
		// update) or extra flag stays governed.
		{name: "kern version allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern version"}}`, wantExit: 0, wantStderr: nil},
		{name: "kern --version allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern --version"}}`, wantExit: 0, wantStderr: nil},
		{name: "kern doctor allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern doctor"}}`, wantExit: 0, wantStderr: nil},
		{name: "kern health allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern health"}}`, wantExit: 0, wantStderr: nil},
		{name: "kern version --json blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"kern version --json"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "bare kern blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"kern"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "kern setup blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"kern setup"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "kern update blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"kern update"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "kern mutate blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"kern mutate"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		// git diff/show/blame emit file content — governed, no longer exempt.
		{name: "git status && git log compound allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"git status && git log --oneline"}}`, wantExit: 0},
		{name: "git status; git status --porcelain compound allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"git status; git status --porcelain"}}`, wantExit: 0},
		{name: "git status && git log -p compound blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git status && git log -p"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "git status && cat main.go compound blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git status && cat main.go"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "git status & git diff lone ampersand blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git status & git diff"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "pwd && echo hi compound allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"pwd && echo hi"}}`, wantExit: 0},
		{name: "pwd || whoami compound blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"pwd || whoami"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "go vet redirect suggests kern_verify", stdin: `{"tool_name":"bash","tool_input":{"command":"go vet ./..."}}`, wantExit: 2, wantStderr: []string{"kern_verify", "go vet", "kern_exec"}},
		{name: "git diff blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git diff"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "git show blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git show HEAD:main.go"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "git blame blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git blame file.go"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		// Prefix lookalikes are NOT exempt (exact first-token matching).
		{name: "pwd123 blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"pwd123"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "git statusX blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git statusX"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		// $ anywhere in the command → non-simple.
		{name: "echo $HOME blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"echo $HOME"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		// Newline-joined compounds bypassed the old predicate — must be governed.
		{name: "pwd newline make blocked", stdin: "{\"tool_name\":\"bash\",\"tool_input\":{\"command\":\"pwd\\nmake\"}}", wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "git status newline rm blocked", stdin: "{\"tool_name\":\"bash\",\"tool_input\":{\"command\":\"git status\\nrm -rf /tmp/x\"}}", wantExit: 2, wantStderr: []string{"kern_verify"}},
		// Pipe / substitution / redirection / compound operators — governed.
		{name: "pipe blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git log --oneline | head -5"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "substitution blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"echo $(whoami)"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "semicolon compound blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"ls; rm -rf /tmp/x"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "redirect blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"grep foo file.go > out.txt"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		// Code reads via bash stay governed.
		{name: "sed code read blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"sed -n '1,5p' file.go"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "cat code read blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"cat source.go"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		// Builds/tests/installs stay governed.
		{name: "make blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"make"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "go test blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"go test ./..."}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		// --- simple-read exemption matrix ---
		{name: "read non-code allowed", stdin: fmt.Sprintf(`{"tool_name":"Read","tool_input":{"file_path":%q}}`, readme), wantExit: 0, wantStderr: nil},
		// A small (<2KB) code file is part of the symbol context — always governed.
		{name: "read small code file governed", stdin: fmt.Sprintf(`{"tool_name":"Read","tool_input":{"file_path":%q}}`, smallGo), wantExit: 2, wantStderr: []string{"kern_compact_file"}},
		{name: "read big code file blocked", stdin: fmt.Sprintf(`{"tool_name":"Read","tool_input":{"file_path":%q}}`, bigGo), wantExit: 2, wantStderr: []string{"kern_compact_file"}},
		{name: "read big hh blocked", stdin: fmt.Sprintf(`{"tool_name":"Read","tool_input":{"file_path":%q}}`, bigHh), wantExit: 2, wantStderr: []string{"kern_compact_file"}},
		{name: "read missing code file blocked", stdin: fmt.Sprintf(`{"tool_name":"Read","tool_input":{"file_path":%q}}`, filepath.Join(dir, "nope.go")), wantExit: 2, wantStderr: []string{"kern_compact_file"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stderr := runGuard(t, script, tc.stdin, tc.env)
			if code != tc.wantExit {
				t.Fatalf("exit code = %d, want %d (stderr: %q)", code, tc.wantExit, stderr)
			}
			if len(tc.wantStderr) == 0 {
				if stderr != "" {
					t.Fatalf("expected no stderr, got %q", stderr)
				}
				return
			}
			for _, want := range tc.wantStderr {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr missing %q: %q", want, stderr)
				}
			}
		})
	}
}

// auditLines reads the bypass.jsonl under home and returns its records.
func auditLines(t *testing.T, home string) []map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".kern", "audit", "bypass.jsonl"))
	if err != nil {
		t.Fatalf("bypass audit trail missing: %v", err)
	}
	var recs []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]string
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("audit record not valid JSON: %v\n%s", err, line)
		}
		recs = append(recs, rec)
	}
	return recs
}

// TestKernGuardBypassAudited pins the deep-dive C2 contract (2026-10-03):
// honoring KERN_ENFORCE=0 / KERN_BYPASS=1 must leave a machine-wide audit
// record in ~/.kern/audit/bypass.jsonl naming the bypassed tool, while (a)
// unguarded tools are NOT audited (the bypass never governed them), (b) a
// failed append never breaks the bypass, and (c) stderr stays clean.
func TestKernGuardBypassAudited(t *testing.T) {
	script := writeGuardScriptFile(t)
	home := t.TempDir()

	// All bypass attempts here run with PIPED stdin (runGuard) — no human at a
	// terminal, so the bypass is REFUSED: the guarded tool still blocks (exit
	// 2) and the refusal is audited with mode=refused:no-tty (audit-table-2 B).

	t.Run("KERN_BYPASS=1 refusal records the tool and mode", func(t *testing.T) {
		code, _ := runGuard(t, script, `{"tool_name":"Read","tool_input":{"file_path":"/x/y.go"}}`, []string{"KERN_BYPASS=1", "HOME=" + home})
		if code != 2 {
			t.Fatalf("exit=%d, want 2 (bypass refused without a terminal)", code)
		}
		recs := auditLines(t, home)
		if len(recs) != 1 {
			t.Fatalf("got %d audit records, want 1: %v", len(recs), recs)
		}
		rec := recs[0]
		if rec["event"] != "kern-guard-bypass" || rec["tool"] != "read" {
			t.Fatalf("audit record = %v, want event=kern-guard-bypass tool=read", rec)
		}
		if rec["mode"] != "refused:no-tty" {
			t.Fatalf("audit mode = %q, want refused:no-tty (piped stdin, no human)", rec["mode"])
		}
		if rec["reason"] != "unset" || rec["ts"] == "" || rec["pwd"] == "" {
			t.Fatalf("audit record missing fields: %v", rec)
		}
	})

	t.Run("KERN_ENFORCE=0 refusal records tool and custom reason", func(t *testing.T) {
		code, _ := runGuard(t, script, `{"tool_name":"Bash","tool_input":{"command":"make"}}`, []string{"KERN_ENFORCE=0", "HOME=" + home, "KERN_BYPASS_REASON=operator break-glass"})
		if code != 2 {
			t.Fatalf("exit=%d, want 2 (bypass refused without a terminal)", code)
		}
		recs := auditLines(t, home)
		if len(recs) != 2 {
			t.Fatalf("got %d audit records, want 2: %v", len(recs), recs)
		}
		rec := recs[1]
		if rec["event"] != "kern-guard-bypass" || rec["tool"] != "bash" {
			t.Fatalf("audit record = %v, want event=kern-guard-bypass tool=bash", rec)
		}
		if rec["reason"] != "operator break-glass" {
			t.Fatalf("reason = %q, want the custom KERN_BYPASS_REASON", rec["reason"])
		}
	})

	t.Run("unguarded tool bypass refusal is not audited", func(t *testing.T) {
		before := len(auditLines(t, home))
		code, stderr := runGuard(t, script, `{"tool_name":"Edit","tool_input":{"file_path":"/x/y.go"}}`, []string{"KERN_BYPASS=1", "HOME=" + home})
		if code != 0 || stderr != "" {
			t.Fatalf("exit=%d stderr=%q, want 0/empty (Edit passes through)", code, stderr)
		}
		if after := len(auditLines(t, home)); after != before {
			t.Fatalf("audit grew %d -> %d for an unguarded tool", before, after)
		}
	})

	t.Run("unwritable HOME still enforces the refusal", func(t *testing.T) {
		notHome := filepath.Join(t.TempDir(), "blocked-as-file")
		if err := os.WriteFile(notHome, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		// HOME points at a regular FILE: mkdir -p fails, the append is
		// skipped, and the refusal still blocks with the governed suggestion
		// (audit failure must not weaken the refusal).
		code, stderr := runGuard(t, script, `{"tool_name":"Read"}`, []string{"KERN_BYPASS=1", "HOME=" + notHome})
		if code != 2 {
			t.Fatalf("exit=%d, want 2 (audit failure must not weaken the refusal)", code)
		}
		if !strings.Contains(stderr, "kern_compact_file") {
			t.Fatalf("stderr should carry the governed block suggestion, got %q", stderr)
		}
	})
}
