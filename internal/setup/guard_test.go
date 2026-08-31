package setup

import (
	"bytes"
	"errors"
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
				"kern_run_build",
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
				"kern_run_build",
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
