package execution

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cliCaptureStdout runs fn with os.Stdout redirected to a pipe and returns
// the captured output (moved from cmd/kern/json_flags_test.go).
func cliCaptureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	_ = w.Close()
	os.Stdout = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// cliCaptureStderr runs fn with os.Stderr redirected to a pipe and returns
// the captured output (moved from cmd/kern/cmd_testhelpers_test.go).
func cliCaptureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	_ = w.Close()
	os.Stderr = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// cliCaptureStderrExit runs fn (an exported Run* entry point returning the
// process exit code) with os.Stderr redirected to a pipe and returns the
// captured stderr together with the exit code.
func cliCaptureStderrExit(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	code := fn()
	_ = w.Close()
	os.Stderr = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), code
}

// cliJSONFixture writes a tiny Go module so index builds, validation and
// impact all have something small to work on (moved from
// cmd/kern/json_flags_test.go).
func cliJSONFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module jsonclifixture\n\ngo 1.20\n",
		"main.go": `package main

func helper() string { return "h" }

func main() { _ = helper() }
`,
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir
}

func cliAssertValidJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("output is not a JSON object: %q", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	return m
}

// TestRunExecSeparatorRunsCommand pins 6.2: a bare `--` after the subcommand
// is the shell-idiom separator — everything after it is the script verbatim.
// `kern exec -- sh -c 'echo separator-ok'` must run `sh -c echo
// separator-ok`, not treat "--" as script text (which previously exited 127).
func TestRunExecSeparatorRunsCommand(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // no netns on darwin; test env opt-in
	out := cliCaptureStdout(t, func() {
		if code := RunExec([]string{"--", "sh", "-c", "echo separator-ok"}); code != 0 {
			t.Fatalf("RunExec(-- sh -c 'echo separator-ok') exit = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "separator-ok") {
		t.Fatalf("separator command output missing marker, got:\n%s", out)
	}
}

// TestRunExecMasksSecretsInStdout locks the CLI trust-boundary parity: `kern
// exec` must mask secrets in script output the same way the MCP kern_exec
// tool does (internal/mcp/handlers_exec.go). Found by the deep-dive
// validation run (kern_validation_report.md, W-2): CLI automation piping
// `kern exec` output into prompts previously lost the masking guarantee.
func TestRunExecMasksSecretsInStdout(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // no netns on darwin; test env opt-in
	out := cliCaptureStdout(t, func() {
		RunExec([]string{
			`echo token=sk-ant-api03-AAAAAAAABBBBBBBBCCCCCCCC and email=jay@x.com`,
			"--lang", "bash",
		})
	})
	if strings.Contains(out, "sk-ant-api03") || strings.Contains(out, "jay@x.com") {
		t.Fatalf("unmasked secret in kern exec output:\n%s", out)
	}
	if !strings.Contains(out, "[MASKED_TOKEN_1]") {
		t.Fatalf("expected masked token placeholder, got:\n%s", out)
	}
	if !strings.Contains(out, "[MASKED_EMAIL_1]") {
		t.Fatalf("expected masked email placeholder, got:\n%s", out)
	}
}

// TestRunExecJSONPathMasksSecrets locks the --json path: the JSON result
// must carry the masked stdout, not the raw script output.
func TestRunExecJSONPathMasksSecrets(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_UNISOLATED", "1")
	out := cliCaptureStdout(t, func() {
		RunExec([]string{
			`echo pat=ghp_16C7e42F292c6912E7710c838347Ae178B4a`,
			"--lang", "bash", "--json",
		})
	})
	if strings.Contains(out, "ghp_16C7e42F292c6912") {
		t.Fatalf("unmasked GitHub token in JSON output:\n%s", out)
	}
	if !strings.Contains(out, "[MASKED_GITHUB_1]") {
		t.Fatalf("expected masked GitHub placeholder, got:\n%s", out)
	}
}

// TestRunExecUndetectableLanguageExits2 pins the C-LOW exit-code contract:
// content with no language signal (no --lang, nothing DetectLang can
// classify) is a MISSING REQUIRED ARGUMENT — a usage error that must exit 2,
// not the runtime-error 1. Live repro: `kern exec "hello world foo bar"`.
func TestRunExecUndetectableLanguageExits2(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_UNISOLATED", "1") // no netns on darwin; test env opt-in
	errOut := cliCaptureStderr(t, func() {
		if code := RunExec([]string{"hello world foo bar"}); code != 2 {
			t.Fatalf("RunExec(undetectable) exit = %d, want 2 (usage error)", code)
		}
	})
	if !strings.Contains(errOut, "cannot detect language") {
		t.Fatalf("expected undetectable-language usage error on stderr, got:\n%s", errOut)
	}
}

// TestRunExecUndetectableLanguageJSONExits2 pins the same C-LOW contract on
// the --json path: the usage error must exit 2 there too (the JSON output
// shape changes, the exit-code contract does not).
func TestRunExecUndetectableLanguageJSONExits2(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	t.Setenv("KERN_ALLOW_UNISOLATED", "1")
	errOut := cliCaptureStderr(t, func() {
		if code := RunExec([]string{"hello world foo bar", "--json"}); code != 2 {
			t.Fatalf("RunExec(undetectable, --json) exit = %d, want 2 (usage error)", code)
		}
	})
	if !strings.Contains(errOut, "cannot detect language") {
		t.Fatalf("expected undetectable-language usage error on stderr, got:\n%s", errOut)
	}
}

// TestRunBuildRejectsCommandSubstitution locks the mode-4 fix: shell
// command-substitution constructs (backticks / $(...)) must be rejected
// BEFORE governance and BEFORE any execution — RunBuild's sh -c would
// evaluate the substitution blind (verified live: `echo \`touch /tmp/x\“
// created the file). The denial must name the construct and the workaround
// (file + -F), and exit as a policy denial (3). A substitution-free command
// must NOT be rejected by this guard.
func TestRunBuildRejectsCommandSubstitution(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_ALLOW_EXEC", "1") // benign control command may run

	cases := []string{
		"echo `date`",
		"echo $(whoami)",
		"echo pre`touch /tmp/never-created`post",
		"git commit -m \"msg`kern approve appr-x`\"",
	}
	for _, cmd := range cases {
		stderr, code := cliCaptureStderrExit(t, func() int {
			return RunBuild([]string{cmd})
		})
		if code != 3 {
			t.Fatalf("RunBuild(%q) exit = %d, want 3 (policy denial)", cmd, code)
		}
		for _, want := range []string{"command-substitution", "backtick", "-F"} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("RunBuild(%q) denial missing %q, got: %.300s", cmd, want, stderr)
			}
		}
	}

	// Positive control: a substitution-free command is not rejected by this
	// guard — with KERN_ALLOW_EXEC=1 it is allowlisted and runs.
	_, code := cliCaptureStderrExit(t, func() int {
		return RunBuild([]string{"echo hello"})
	})
	if code != 0 {
		t.Fatalf("substitution-free command must not be rejected by the substitution guard (exit %d)", code)
	}
}

// TestRunBuildAllowsLiteralSubstitutionText pins the false-positive fix: sh
// does not expand $(...) or backticks inside single quotes, inside a
// quoted-delimiter heredoc body, or after a backslash. Those are literal text
// (markdown tables, commit messages, docs) and must stay usable; an EXPANDING
// construct must still be denied.
func TestRunBuildAllowsLiteralSubstitutionText(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_ALLOW_EXEC", "1") // benign control commands may run

	allowed := []string{
		"echo 'literal $(whoami) and `date`'",
		"cat <<'EOF'\n| `x` | $(y) |\nEOF",
	}
	for _, cmd := range allowed {
		stderr, code := cliCaptureStderrExit(t, func() int {
			return RunBuild([]string{cmd})
		})
		if code != 0 {
			t.Fatalf("RunBuild(%q) exit = %d, want 0 (literal text is not a substitution); stderr: %.300s", cmd, code, stderr)
		}
	}

	denied := []string{
		"cat <<EOF\n$(whoami)\nEOF",
		"cat <<EOF\n`date`\nEOF",
	}
	for _, cmd := range denied {
		_, code := cliCaptureStderrExit(t, func() int {
			return RunBuild([]string{cmd})
		})
		if code != 3 {
			t.Fatalf("RunBuild(%q) exit = %d, want 3 (expanding heredoc body must be denied)", cmd, code)
		}
	}
}

// TestRunBuildPropagatesChildExitCode locks the wrapper-fidelity fix: a
// non-zero CHILD exit must propagate as the wrapper's exit code (the make /
// sh -c convention), not flatten to 1. Observed live: `kern build kern twin`
// reported "[kern] command failed (exit code 1)" while the child exited 2 —
// every non-timeout failure also appended the "raise with --timeout" advice.
// `exit 7` exits 7 through RunBuild's sh -c, so the wrapper must exit 7.
func TestRunBuildPropagatesChildExitCode(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_ALLOW_EXEC", "1") // benign control command may run

	stderr, code := cliCaptureStderrExit(t, func() int {
		return RunBuild([]string{"exit", "7"})
	})
	if code != 7 {
		t.Fatalf("RunBuild(exit 7) exit = %d, want 7 (child exit code must propagate)", code)
	}
	if !strings.Contains(stderr, "command failed with exit code 7") {
		t.Fatalf("RunBuild(exit 7) stderr must name the child exit code, got: %.300s", stderr)
	}
	if strings.Contains(stderr, "--timeout") {
		t.Fatalf("RunBuild(exit 7) is not a timeout — the --timeout advice must not appear, got: %.300s", stderr)
	}
}

// TestRunValidateJSON pins the --json shape of `kern validate`: a
// successful validation reports ok=true and a non-negative duration_ms
// (moved from cmd/kern/json_flags_test.go).
func TestRunValidateJSON(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	root := cliJSONFixture(t)
	// Success path only: a failing validation exits the process, which cannot
	// be asserted in-process. The JSON shape is shared with the failure path.
	out := cliCaptureStdout(t, func() { RunValidate([]string{root, "--json"}) })
	m := cliAssertValidJSON(t, out)
	if m["ok"] != true {
		t.Fatalf("expected ok=true: %v", m)
	}
	if m["duration_ms"].(float64) < 0 {
		t.Fatalf("expected non-negative duration_ms: %v", m)
	}
}
