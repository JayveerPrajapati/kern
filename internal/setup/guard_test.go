package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
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
	detachCmd(cmd)
	// KERN_GUARD_ASSUME_KERN=1: the unit tests exercise the governed paths, so
	// the "kern is unavailable -> fail open" probe is off unless a test
	// overrides it (see TestKernGuardFailsOpen*). HOME is sandboxed to a temp
	// dir by default (plain denials are audited as kern-guard-deny, and
	// that must never land in the real ~/.kern/audit/bypass.jsonl); tests that
	// assert on the audit trail pass HOME explicitly, which wins (last entry
	// in the env list).
	cmd.Env = append(os.Environ(), append([]string{"KERN_GUARD_CONFIRM_TIMEOUT=1", "KERN_GUARD_ASSUME_KERN=1", "HOME=" + t.TempDir()}, env...)...)
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

// TestKernGuardAllowOnceConsume pins the shell-guard side of the operator
// one-time allow: an operator
// allowance (minted tty-side by `kern guard allow <hash>`) for the exact
// denied subject lets the NEXT identical tool call through ONCE — the
// second identical call denies again, and the deny message names the hash
// so the operator can mint the allowance in the first place.
func TestKernGuardAllowOnceConsume(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq required for bash payload extraction")
	}
	script := writeGuardScriptFile(t)
	payload := `{"tool_name":"bash","tool_input":{"command":"printf hi"}}`
	subject := "printf hi"
	sum := sha256.Sum256([]byte(subject))
	hash := hex.EncodeToString(sum[:])
	reg := filepath.Join(t.TempDir(), "allow-once.jsonl")
	line := fmt.Sprintf(`{"hash":%q,"subject":"probe","created":"2026-10-08T00:00:00Z","used":""}`, hash)
	if err := os.WriteFile(reg, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{"KERN_GUARD_ALLOW_ONCE=" + reg}
	// First call: the unused allowance is consumed — allow.
	if code, _ := runGuard(t, script, payload, env); code != 0 {
		t.Fatalf("first call with an unused allowance must allow, got exit %d", code)
	}
	// Second identical call: the allowance is inert — deny again, naming the
	// hash so the operator can mint a fresh allowance if they choose to.
	if code, stderr := runGuard(t, script, payload, env); code != 2 {
		t.Fatalf("second identical call must deny (single-use), got exit %d, stderr %q", code, stderr)
	} else if !strings.Contains(stderr, hash) {
		t.Fatalf("deny message must name the subject hash for the operator, stderr %q", stderr)
	}
}

// buildCurrentKern builds the repository's current kern binary for hook
// tests. The hook's bash arm now delegates classification to
// `kern guard classify`; using the installed binary here can make a correct
// source change fail under a stale install.
func buildCurrentKern(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "kern")
	buildArgs := []string{"build"}
	if !index.TreesitterEnabled() {
		buildArgs = append(buildArgs, "-tags", "notreesitter")
	}
	buildArgs = append(buildArgs, "-o", bin, "github.com/JayveerPrajapati/kern/cmd/kern")
	cmd := exec.Command("go", buildArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build current kern: %v\n%s", err, out)
	}
	return bin
}

// TestKernGuardScript exercises the embedded PreToolUse guard: guarded tools
// (read/grep/glob/bash and their per-agent aliases) must block with exit 2 and
// a kern suggestion on stderr; unguarded tools, the KERN_ENFORCE=0 bypass and
// unparseable payloads must pass through with exit 0.
func TestKernGuardScript(t *testing.T) {
	t.Parallel()
	script := writeGuardScriptFile(t)
	currentKern := buildCurrentKern(t)
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
				"kern_meta",
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
				"kern_meta",
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
				"kern_meta",
			},
		},
		{
			name:     "antigravity run_command blocked",
			stdin:    `{"toolCall":{"name":"run_command","args":{"CommandLine":"go test ./..."}}}`,
			wantExit: 2,
			wantStderr: []string{
				"kern_verify",
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
			name:     "antigravity KERN_BYPASS=1 refused without terminal",
			stdin:    `{"toolCall":{"name":"run_command","args":{"CommandLine":"cat /some/file.go"}}}`,
			env:      []string{"KERN_BYPASS=1", "KERN_GUARD_MODE=deny", "HOME=" + auditHome},
			wantExit: 2,
			wantStderr: []string{
				"kern_explore",
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
		// --- simple-command exemption matrix ---
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
		// ANY kern CLI invocation runs raw — the CLI carries its own
		// governance (approvals, tty-gated allow-once, sandboxed exec,
		// update provenance guard); the guard's deny messages steer agents
		// TO kern commands. Pipes and wrapper-hidden kern stay governed.
		{name: "kern version allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern version"}}`, wantExit: 0, wantStderr: nil},
		{name: "kern --version allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern --version"}}`, wantExit: 0, wantStderr: nil},
		{name: "kern doctor allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern doctor"}}`, wantExit: 0, wantStderr: nil},
		{name: "kern health allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern health"}}`, wantExit: 0, wantStderr: nil},
		{name: "kern version --json allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern version --json"}}`, wantExit: 0, wantStderr: nil},
		{name: "bare kern allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern"}}`, wantExit: 0, wantStderr: nil},
		{name: "kern setup allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern setup"}}`, wantExit: 0, wantStderr: nil},
		{name: "kern update allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern update"}}`, wantExit: 0, wantStderr: nil},
		{name: "kern mutate allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern mutate"}}`, wantExit: 0, wantStderr: nil},
		{name: "kern mcp --help allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern mcp --help"}}`, wantExit: 0, wantStderr: nil},
		{name: "kern guard allow allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern guard allow abc123"}}`, wantExit: 0, wantStderr: nil},
		{name: "kern --version && kern mcp --help compound allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"kern --version && kern mcp --help"}}`, wantExit: 0, wantStderr: nil},
		{name: "kern ast pipe blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"kern ast Foo | head -5"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "sudo kern update blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"sudo kern update"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		// git diff/show/blame emit file content — governed, no longer exempt.
		{name: "git status && git log compound allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"git status && git log --oneline"}}`, wantExit: 0},
		{name: "git status; git status --porcelain compound allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"git status; git status --porcelain"}}`, wantExit: 0},
		{name: "git status && git log -p compound blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git status && git log -p"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "git status && cat code file compound served", stdin: `{"tool_name":"bash","tool_input":{"command":"git status && cat guard_test.go"}}`, wantExit: 0, wantStderr: nil},
		{name: "git status & git diff lone ampersand blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git status & git diff"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "pwd && echo hi compound allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"pwd && echo hi"}}`, wantExit: 0},
		// Buddy C1: a compound of content-free segments (pwd || whoami) has
		// no write/network surface — it runs raw instead of paying the
		// governed wrapper.
		{name: "pwd || whoami compound allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"pwd || whoami"}}`, wantExit: 0},
		{name: "go vet redirect suggests kern_verify", stdin: `{"tool_name":"bash","tool_input":{"command":"go vet ./..."}}`, env: []string{"KERN_GUARD_MODE=deny"}, wantExit: 2, wantStderr: []string{"kern_verify", "go vet"}},
		// git diff/show emit file content — served from the readonly sandbox
		// (rev-reads of the guard-consent campaign), no longer denied.
		{name: "git diff served", stdin: `{"tool_name":"bash","tool_input":{"command":"git diff"}}`, wantExit: 0},
		{name: "git show served", stdin: `{"tool_name":"bash","tool_input":{"command":"git show HEAD:main.go"}}`, wantExit: 0},
		{name: "git blame blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git blame file.go"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		// Prefix lookalikes are NOT exempt (exact first-token matching).
		{name: "pwd123 blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"pwd123"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "git statusX blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"git statusX"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		// $ anywhere in the command → non-simple.
		{name: "echo $HOME blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"echo $HOME"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		// Newline-joined compounds bypassed the old predicate — must be governed.
		{name: "pwd newline make blocked", stdin: "{\"tool_name\":\"bash\",\"tool_input\":{\"command\":\"pwd\\nmake\"}}", wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "git status newline rm blocked", stdin: "{\"tool_name\":\"bash\",\"tool_input\":{\"command\":\"git status\\nrm /etc/hosts\"}}", wantExit: 2, wantStderr: []string{"GATE command"}},
		// Pipe / substitution / redirection / compound operators — classified
		// by the risk axis: pure-read pipelines are SERVED, write-redirect /
		// gate segments deny.
		{name: "pipe served", stdin: `{"tool_name":"bash","tool_input":{"command":"git log --oneline | head -5"}}`, wantExit: 0, wantStderr: nil},
		{name: "substitution blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"echo $(whoami)"}}`, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "semicolon compound blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"ls; rm /etc/hosts"}}`, wantExit: 2, wantStderr: []string{"GATE command"}},
		// path-aware writes: in-tree targets allow (exit 0, no stderr),
		// tmp targets allow, out-of-root targets gate.
		{name: "in-tree rm allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"rm scratch.go"}}`, wantExit: 0, wantStderr: []string{}},
		{name: "in-tree mv allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"mv internal/a.go internal/b.go"}}`, wantExit: 0, wantStderr: []string{}},
		{name: "tmp rm allowed", stdin: `{"tool_name":"bash","tool_input":{"command":"rm -rf /tmp/x"}}`, wantExit: 0, wantStderr: []string{}},
		{name: "credential write gated", stdin: `{"tool_name":"bash","tool_input":{"command":"touch .env"}}`, wantExit: 2, wantStderr: []string{"GATE"}},
		{name: "git internals gated", stdin: `{"tool_name":"bash","tool_input":{"command":"rm .git/index.lock"}}`, wantExit: 2, wantStderr: []string{"GATE"}},
		{name: "redirect blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"grep foo file.go > out.txt"}}`, wantExit: 2, wantStderr: []string{"GATE command"}},
		// Code reads via bash stay governed (the auto-redirect default
		// answers them via kern; KERN_GUARD_MODE=deny pins the deny path).
		{name: "sed code read blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"sed -n '1,5p' file.go"}}`, env: []string{"KERN_GUARD_MODE=deny"}, wantExit: 2, wantStderr: []string{"kern_verify", "kern_explore"}},
		{name: "cat code read blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"cat source.go"}}`, env: []string{"KERN_GUARD_MODE=deny"}, wantExit: 2, wantStderr: []string{"kern_verify", "kern_explore"}},
		// Builds/tests/installs stay governed (the auto-redirect default
		// routes them to kern verify; KERN_GUARD_MODE=deny pins the deny).
		{name: "make blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"make"}}`, env: []string{"KERN_GUARD_MODE=deny"}, wantExit: 2, wantStderr: []string{"kern_verify"}},
		{name: "go test blocked", stdin: `{"tool_name":"bash","tool_input":{"command":"go test ./..."}}`, env: []string{"KERN_GUARD_MODE=deny"}, wantExit: 2, wantStderr: []string{"kern_verify"}},
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
			env := append([]string{"KERN_BIN=" + currentKern}, tc.env...)
			code, stderr := runGuard(t, script, tc.stdin, env)
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

// TestKernGuardAutoRedirect pins the auto-redirect default: simple
// content-intent commands run
// their kern equivalent via $KERN_BIN and exit 0, leaving a kern-guard-redirect
// audit record. Auto-redirect is the DEFAULT (env unset);
// KERN_GUARD_MODE=deny restores the uniform deny (exit 2, audited
// kern-guard-deny). Ambiguous commands (compounds) deny even in auto-redirect
// mode. The fake kern binary records its argv so the invocation
// (compact <path>) is pinned without needing a real kern install.
func TestKernGuardAutoRedirect(t *testing.T) {
	script := writeGuardScriptFile(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "source.go")
	if err := os.WriteFile(src, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// mkFakeKern installs a fake kern binary that logs its argv to outLog and
	// exits 0, so the redirect path is fully hermetic.
	mkFakeKern := func(t *testing.T) (kernBin, outLog string) {
		t.Helper()
		outLog = filepath.Join(t.TempDir(), "kern-args.log")
		kernBin = filepath.Join(t.TempDir(), "kern")
		fakeScript := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" >> %q
if [ "$1" = guard ] && [ "$2" = grant ] && [ "$3" = --check ]; then
  # Ask-Protocol grant check with no grant in the (absent) store: denied.
  exit 3
fi
if [ "$1" = guard ] && [ "$2" = classify ] && [ "$3" = --json ]; then
  shift 3
  cmd="$*"
  case "$cmd" in
  rm\ *) printf '{"verdict":"gate","tier":0,"hash":"gatehash"}\n'; exit 0 ;;
  cat\ *\|*) printf '{"verdict":"serve","tier":3}\n'; exit 0 ;;
  cat\ *|\ cat\ *) printf '{"verdict":"serve","tier":1,"head":"cat","file":"%%s"}\n' "${cmd#cat }"; exit 0 ;;
  esac
  printf '{"verdict":"governed","tier":0,"hash":"governedhash"}\n'
  exit 0
fi
printf 'kern result: %%s\n' "$*"
`, outLog)
		if err := os.WriteFile(kernBin, []byte(fakeScript), 0o755); err != nil {
			t.Fatal(err)
		}
		return kernBin, outLog
	}

	t.Run("default mode redirects to kern and exits 0", func(t *testing.T) {
		home := t.TempDir()
		kernBin, outLog := mkFakeKern(t)
		payload := fmt.Sprintf(`{"tool_name":"bash","tool_input":{"command":"cat %s"}}`, src)
		code, stderr := runGuard(t, script, payload, []string{"KERN_BIN=" + kernBin, "HOME=" + home})
		if code != 0 {
			t.Fatalf("exit=%d, want 0 (redirected to kern by default), stderr %q", code, stderr)
		}
		if stderr != "" {
			t.Fatalf("redirect must not print the deny message on stderr, got %q", stderr)
		}
		got, err := os.ReadFile(outLog)
		if err != nil {
			t.Fatalf("fake kern was not invoked: %v", err)
		}
		invoked := strings.TrimSpace(string(got))
		if !strings.Contains(invoked, "compact") || !strings.Contains(invoked, src) {
			t.Fatalf("kern invoked with %q, want compact <source path>", invoked)
		}
		recs := auditLines(t, home)
		if len(recs) != 1 || recs[0]["event"] != "kern-guard-redirect" {
			t.Fatalf("audit = %v, want exactly one kern-guard-redirect record", recs)
		}
		if recs[0]["tool"] != "bash" || recs[0]["pwd"] == "" || recs[0]["ts"] == "" {
			t.Fatalf("audit record missing fields: %v", recs[0])
		}
	})

	t.Run("explicit KERN_GUARD_MODE=deny restores the deny", func(t *testing.T) {
		home := t.TempDir()
		payload := fmt.Sprintf(`{"tool_name":"bash","tool_input":{"command":"cat %s"}}`, src)
		code, stderr := runGuard(t, script, payload, []string{"KERN_GUARD_MODE=deny", "HOME=" + home})
		if code != 2 {
			t.Fatalf("exit=%d, want 2 (deny when KERN_GUARD_MODE=deny)", code)
		}
		if !strings.Contains(stderr, "kern_explore") {
			t.Fatalf("deny should suggest kern_explore for cat, got %q", stderr)
		}
		recs := auditLines(t, home)
		if len(recs) != 1 || recs[0]["event"] != "kern-guard-deny" {
			t.Fatalf("audit = %v, want exactly one kern-guard-deny record", recs)
		}
	})

	t.Run("compound read pipeline is served, never denied", func(t *testing.T) {
		// A pure-read pipeline (cat <file> | head -5) is a READ — under the
		// 3-tier serve it is never denied; the untranslatable form (Tier 3)
		// delegates to kern exec --readonly and exits 0 with the output. The
		// fake kern binary records the invocation so the serve path is pinned.
		home := t.TempDir()
		kernBin, outLog := mkFakeKern(t)
		payload := fmt.Sprintf(`{"tool_name":"bash","tool_input":{"command":"cat %s | head -5"}}`, src)
		code, stderr := runGuard(t, script, payload, []string{"KERN_BIN=" + kernBin, "HOME=" + home})
		if code != 0 {
			t.Fatalf("exit=%d, want 0 (pure-read pipeline is served, never denied), stderr %q", code, stderr)
		}
		if _, err := os.Stat(outLog); err != nil {
			t.Fatalf("fake kern was not invoked — the Tier-3 serve must delegate to kern exec --readonly")
		}
		got, err := os.ReadFile(outLog)
		if err != nil {
			t.Fatalf("read fake kern log: %v", err)
		}
		if !strings.Contains(string(got), "exec") || !strings.Contains(string(got), "--readonly") {
			t.Fatalf("Tier-3 serve invoked kern with %q, want exec --readonly", strings.TrimSpace(string(got)))
		}
	})

	t.Run("gate command denies even in default mode", func(t *testing.T) {
		// The risky axis is unchanged: a write/destructive/network/installer
		// command denies (exit 2, GATE message) even in the default serve
		// mode — reads are served, gates are denied. The fake kern must NOT
		// be invoked for a gate.
		home := t.TempDir()
		kernBin, outLog := mkFakeKern(t)
		payload := fmt.Sprintf(`{"tool_name":"bash","tool_input":{"command":"rm -rf %s"}}`, src)
		code, stderr := runGuard(t, script, payload, []string{"KERN_BIN=" + kernBin, "HOME=" + home})
		if code != 2 {
			t.Fatalf("exit=%d, want 2 (GATE command denies), stderr %q", code, stderr)
		}
		if !strings.Contains(stderr, "GATE command") {
			t.Fatalf("GATE deny must name the GATE contract, got %q", stderr)
		}
		got, err := os.ReadFile(outLog)
		if err != nil {
			t.Fatalf("fake kern must be invoked for classification: %v", err)
		}
		invoked := string(got)
		for _, forbidden := range []string{"buddy", "compact", " exec", "--readonly"} {
			if strings.Contains(invoked, forbidden) {
				t.Fatalf("GATE command ran a non-classifier kern action %q: %q", forbidden, invoked)
			}
		}
	})
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

// TestKernGuardBypassAudited pins the bypass-refusal contract:
// honoring KERN_ENFORCE=0 / KERN_BYPASS=1 must leave a machine-wide audit
// record in ~/.kern/audit/bypass.jsonl naming the bypassed tool, while (a)
// unguarded tools are NOT audited (the bypass never governed them), (b) a
// failed append never breaks the bypass, and (c) stderr stays clean.
func TestKernGuardBypassAudited(t *testing.T) {
	script := writeGuardScriptFile(t)
	home := t.TempDir()

	// All bypass attempts here run with PIPED stdin (runGuard) — no human at a
	// terminal, so the bypass is REFUSED: the guarded tool still blocks (exit
	// 2) and the refusal is audited with mode=refused:no-tty.

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
