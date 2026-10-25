package setup

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestKernGuardEverydayPassthrough pins the item-4 policy: ordinary git
// inspection, staging and committing pass; anything that prints file content
// (grep/sed/cat/head, patch-emitting diff/show) or weakens a safety gate
// (add -f, commit --no-verify, push) stays governed. KERN_GUARD_MODE=deny:
// this test pins the allow/governed CLASSIFICATION, not the redirect
// outcome — the default auto-redirects governed commands
// (exit 0 with kern output), which would read as "allowed" here and depend
// on whether kern resolves in the test env. The redirect path itself is
// pinned in TestKernGuardAutoRedirect.
func TestKernGuardEverydayPassthrough(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq required for bash payload extraction")
	}
	script := writeGuardScriptFile(t)
	cases := []struct {
		cmd   string
		allow bool
	}{
		{"git diff --stat", true},
		{"git diff --name-only HEAD~1", true},
		{"git diff --numstat", true},
		{"git show --stat HEAD", true},
		{"git branch --show-current", true},
		{"git branch -a", true},
		{"git branch -vv", true},
		{"git rev-parse HEAD", true},
		{"git rev-parse --show-toplevel", true},
		{"git remote -v", true},
		{"git remote get-url origin", true},
		{"git remote", true},
		{"wc -l README.md", true},
		{"git add internal/setup/guard.go", true},
		{"git add -A", true},
		{"git commit -m fix", true},
		{"git commit -am wip", true},
		{"git commit --amend", true},
		{"git status && git diff --stat", true},

		// git -C <dir> <read-only subcommand> classifies the subcommand
		// exactly like the in-tree form (perfect parity with plain git).
		{"git -C /tmp status", true},
		{"git -C /tmp status --short", true},
		{"git -C /tmp log --oneline -5", true},
		{"git -C /tmp diff --stat", true},
		{"git -C /tmp diff --name-only HEAD~1", true},
		{"git -C /tmp rev-parse HEAD", true},
		{"git -C /tmp branch --show-current", true},
		{"git -C /tmp remote -v", true},
		{"git -C /tmp add file.go", true},
		{"git -C /tmp commit -m fix", true},
		{"git -C /tmp status && git -C /tmp diff --stat", true},
		{"git status && git -C /tmp log --oneline -1", true},
		{"git -C", false},
		{"git -C -p status", false},
		{"git -C /tmp log -p", false},
		{"git -C /tmp diff", false},
		{"git -C /tmp show HEAD", false},
		{"git -C /tmp add -f .env", false},
		{"git -C /tmp commit --no-verify -m x", false},
		{"git -C /tmp push", false},

		// ANY kern CLI invocation runs raw — the CLI carries its own
		// governance (approvals, tty-gated allow-once, sandboxed exec).
		// Pipes and wrapper-hidden kern stay governed.
		{"kern guard -h", true},
		{"kern guard --help", true},
		{"kern guard --help && git status", true},
		{"kern guard allow abc123", true},
		{"kern mcp tools", true},
		{"kern mcp tools list", true},
		{"kern mcp tools --json", true},
		{"kern mcp tools graph", true},
		{"kern mcp tools server --addr :0", true},
		{"kern mcp", true},
		{"kern mcp server", true},
		{"kern mcp --help", true},
		{"kern update", true},
		{"kern setup", true},
		{"kern version --json", true},
		{"kern exec --readonly --lang sh x", true},
		{"kern --version && kern mcp --help", true},
		{"kern ast Foo | head -5", false},
		{"sudo kern update", false},

		{"git diff", false},
		{"git diff -p", false},
		{"git diff --stat -p", false},
		{"git diff --patch-with-stat", false},
		{"git diff -U5 --stat", false},
		{"git diff --word-diff --stat", false},
		{"git diff --stat --output=/tmp/x", false},
		{"git show HEAD", false},
		{"git show --stat HEAD:main.go", false},
		{"git branch newname", false},
		{"git branch -D old", false},
		{"git remote add x url", false},
		{"git remote set-url origin x", false},
		{"git add -f .env", false},
		{"git add --force .env", false},
		{"git add -Af .env", false},
		{"git commit --no-verify -m x", false},
		{"git commit -n -m x", false},
		{"git commit -nm x", false},
		{"git push", false},
		{"git blame main.go", false},
		{"grep -rn foo .", false},
		{"sed -n 1,5p file", false},
		{"cat main.go", false},
		{"head -5 main.go", false},
		{"tail main.go", false},
		{"git status && grep foo x", false},
		{"git diff --stat | cat", false},
		{"git commit -m \"$(cat secrets)\"", false},
	}
	for _, c := range cases {
		b, _ := json.Marshal(map[string]any{"tool_name": "bash", "tool_input": map[string]string{"command": c.cmd}})
		code, stderr := runGuard(t, script, string(b), []string{"KERN_GUARD_MODE=deny"})
		want := 2
		if c.allow {
			want = 0
		}
		if code != want {
			t.Errorf("%q: exit %d, want %d (stderr %q)", c.cmd, code, want, stderr)
		}
	}
}

// TestPluginSimpleCommandMirrorsGuard pins that the opencode plugin carries
// the same everyday-passthrough rules as the shell guard.
func TestPluginSimpleCommandMirrorsGuard(t *testing.T) {
	b, err := PluginAsset()
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"isStatOnlyGit", "GIT_BRANCH_LIST_FLAGS", `"rev-parse"`, `"--no-verify"`, `"wc"`,
		// git -C <dir> and the any-kern-invocation rule (first-token) travel
		// in the plugin mirror too (parity with the shell guard).
		`"-C"`, `parts[0] === "kern"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("plugin missing %s", want)
		}
	}
	if strings.Contains(src, `"grep", "egrep"`) == false {
		t.Error("plugin must still treat grep as a content-intent command")
	}
}
