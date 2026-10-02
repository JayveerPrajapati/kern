package setup

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
// env, returning the process exit code and captured stderr.
func runGuard(t *testing.T, script, stdin string, env []string) (int, string) {
	t.Helper()
	cmd := exec.Command("sh", script)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), env...)
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
				"kern_validate",
				"kern_exec",
			},
		},
		{
			name:     "grep blocked",
			stdin:    `{"tool_name":"Grep"}`,
			wantExit: 2,
			wantStderr: []string{
				"kern_ast_search",
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
				"kern_validate",
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
				"kern_validate",
				"kern_exec",
			},
		},
		{
			name:       "KERN_ENFORCE=0 bypasses",
			stdin:      `{"tool_name":"Read"}`,
			env:        []string{"KERN_ENFORCE=0"},
			wantExit:   0,
			wantStderr: nil,
		},
		{
			name:       "KERN_BYPASS=1 bypasses",
			stdin:      `{"tool_name":"Read"}`,
			env:        []string{"KERN_BYPASS=1"},
			wantExit:   0,
			wantStderr: nil,
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
				"kern_validate",
				"kern_exec",
			},
		},
		{
			name:       "antigravity KERN_BYPASS=1 bypasses",
			stdin:      `{"toolCall":{"name":"run_command","args":{"CommandLine":"git commit -m test"}}}`,
			env:        []string{"KERN_BYPASS=1"},
			wantExit:   0,
			wantStderr: nil,
		},
		{
			name:     "antigravity grep_search blocked",
			stdin:    `{"tool_name":"grep_search"}`,
			wantExit: 2,
			wantStderr: []string{
				"kern_ast_search",
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
		{name: "git log -p blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git log -p"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		{name: "git log --patch blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git log --patch"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		{name: "git log -u blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git log -u"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		{name: "git log --raw blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git log --raw"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		{name: "git log --oneline -p blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git log --oneline -p"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		// git diff/show/blame emit file content — governed, no longer exempt.
		{name: "git diff blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git diff"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		{name: "git show blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git show HEAD:main.go"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		{name: "git blame blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git blame file.go"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		// Prefix lookalikes are NOT exempt (exact first-token matching).
		{name: "pwd123 blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"pwd123"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		{name: "git statusX blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git statusX"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		// $ anywhere in the command → non-simple.
		{name: "echo $HOME blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"echo $HOME"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		// Newline-joined compounds bypassed the old predicate — must be governed.
		{name: "pwd newline make blocked", stdin: "{\"tool_name\":\"bash\",\"tool_input\":{\"command\":\"pwd\\nmake\"}}", wantExit: 2, wantStderr: []string{"kern_validate"}},
		{name: "git status newline rm blocked", stdin: "{\"tool_name\":\"bash\",\"tool_input\":{\"command\":\"git status\\nrm -rf /tmp/x\"}}", wantExit: 2, wantStderr: []string{"kern_validate"}},
		// Pipe / substitution / redirection / compound operators — governed.
		{name: "pipe blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git log --oneline | head -5"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		{name: "substitution blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"echo $(whoami)"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		{name: "semicolon compound blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"ls; rm -rf /tmp/x"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		{name: "redirect blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"grep foo file.go > out.txt"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		// Code reads via bash stay governed.
		{name: "sed code read blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"sed -n '1,5p' file.go"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		{name: "cat code read blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"cat source.go"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		// Builds/tests/installs stay governed.
		{name: "make blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"make"}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
		{name: "go test blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"go test ./..."}}`, wantExit: 2, wantStderr: []string{"kern_validate"}},
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
