package script

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// allowDegradedNetwork opts a test into env-only isolation (KERN_ALLOW_NET=1).
// Scripts now fail closed when network isolation is unavailable (F9), so tests
// that exercise script mechanics (stdout, timeout, truncation, lang) rather
// than network isolation must explicitly allow the degraded path on hosts
// without an unprivileged netns.
func allowDegradedNetwork(t *testing.T) {
	t.Helper()
	t.Setenv("KERN_ALLOW_NET", "1")
}

func TestPythonStdoutOnly(t *testing.T) {
	if !runtimeInstalled("python3") {
		t.Skip("python3 not installed")
	}
	allowDegradedNetwork(t)
	res := RunScript(Run{Lang: "python3", Code: "print(6 * 7)\nimport sys\nprint('err-to-stderr', file=sys.stderr)\n"})
	if !res.OK || res.ExitCode != 0 {
		t.Fatalf("expected success, got %+v", res)
	}
	if strings.TrimSpace(res.Stdout) != "42" {
		t.Errorf("stdout = %q, want 42", res.Stdout)
	}
	if res.Stderr != "" {
		t.Errorf("stderr must be empty on success, got %q", res.Stderr)
	}
}

func TestNodeStdinAndDuration(t *testing.T) {
	if !runtimeInstalled("node") {
		t.Skip("node not installed")
	}
	allowDegradedNetwork(t)
	res := RunScript(Run{Lang: "node", Code: "let s=''; process.stdin.on('data',d=>s+=d); process.stdin.on('end',()=>console.log(s.toUpperCase().trim()));", Stdin: "hello\nworld\n"})
	if !res.OK {
		t.Fatalf("expected success, got %+v", res)
	}
	if strings.TrimSpace(res.Stdout) != "HELLO\nWORLD" {
		t.Errorf("stdout = %q", res.Stdout)
	}
	if res.Duration <= 0 {
		t.Errorf("duration should be positive")
	}
}

func TestBashShebangDetection(t *testing.T) {
	if !runtimeInstalled("bash") {
		t.Skip("bash not installed")
	}
	allowDegradedNetwork(t)
	res := RunScript(Run{Code: "#!/usr/bin/env bash\nfor i in 1 2 3; do echo $i; done\n"})
	if !res.OK {
		t.Fatalf("expected success, got %+v", res)
	}
	if strings.TrimSpace(res.Stdout) != "1\n2\n3" {
		t.Errorf("stdout = %q", res.Stdout)
	}
	if res.Lang != "bash" {
		t.Errorf("detected lang = %q, want bash", res.Lang)
	}
	if res.Runtime != "bash" {
		t.Errorf("runtime = %q", res.Runtime)
	}
}

func TestFailureSurfacesStderr(t *testing.T) {
	if !runtimeInstalled("python3") {
		t.Skip("python3 not installed")
	}
	allowDegradedNetwork(t)
	res := RunScript(Run{Lang: "python3", Code: "import sys\nprint('clean out')\nprint('boom', file=sys.stderr)\nraise SystemExit(3)\n"})
	if res.OK || res.ExitCode != 3 {
		t.Fatalf("expected exit 3, got %+v", res)
	}
	if !strings.Contains(res.Stderr, "boom") {
		t.Errorf("stderr should carry the error output, got %q", res.Stderr)
	}
}

func TestTimeout(t *testing.T) {
	if !runtimeInstalled("python3") {
		t.Skip("python3 not installed")
	}
	allowDegradedNetwork(t)
	res := RunScript(Run{Lang: "python3", Code: "import time; time.sleep(30)", Timeout: 300 * time.Millisecond})
	if !res.TimedOut {
		t.Fatalf("expected timeout, got %+v", res)
	}
	if !strings.Contains(res.Err.Error(), "timed out") {
		t.Errorf("error = %v", res.Err)
	}
}

func TestTruncation(t *testing.T) {
	if !runtimeInstalled("python3") {
		t.Skip("python3 not installed")
	}
	allowDegradedNetwork(t)
	res := RunScript(Run{Lang: "python3", Code: "for i in range(1000): print('x'*20)", MaxOut: 128})
	if !res.OK {
		t.Fatalf("expected success, got %+v", res)
	}
	if !res.Truncated {
		t.Errorf("expected truncated flag")
	}
	if len(res.Stdout) > 256 {
		t.Errorf("stdout not truncated, len=%d", len(res.Stdout))
	}
}

func TestUnknownLangAndEmptyCode(t *testing.T) {
	if res := RunScript(Run{Lang: "cobol", Code: "x"}); res.Err == nil {
		t.Errorf("unknown lang should error")
	}
	if res := RunScript(Run{Lang: "python3", Code: "   \n"}); res.Err == nil {
		t.Errorf("empty code should error")
	}
	// Ambiguous code with no language-specific signal should still error
	// with the "detect" message. (print(1) is now detected as Python by the
	// content heuristic — use genuinely ambiguous content instead.)
	if res := RunScript(Run{Code: "x = 42"}); res.Err == nil || !strings.Contains(res.Err.Error(), "detect") {
		t.Errorf("ambiguous code with no shebang should error about detection, got %v", res.Err)
	}
}

func TestGoRunSingleFile(t *testing.T) {
	if !runtimeInstalled("go") {
		t.Skip("go not installed")
	}
	allowDegradedNetwork(t)
	res := RunScript(Run{Lang: "go", Code: "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"hi from go\") }\n", Timeout: 60 * time.Second})
	if !res.OK {
		t.Fatalf("expected success, got %+v (stderr %q)", res, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "hi from go" {
		t.Errorf("stdout = %q", res.Stdout)
	}
}

func TestLangFromExt(t *testing.T) {
	for path, want := range map[string]string{
		"a.py": "python3", "b.js": "node", "c.sh": "bash", "d.rs": "rust",
		"e.go": "go", "f.r": "R", "g.rb": "ruby",
	} {
		if got := langFromExt(path); got != want {
			t.Errorf("langFromExt(%s) = %q, want %q", path, got, want)
		}
	}
}

// TestDetectLangFromContent verifies content-based language detection: when
// there is no shebang, the code's structural signals (package, def, import,
// console, echo, etc.) identify the language so `kern exec "print(1)"` works
// without --lang. Ambiguous content returns "" so the caller errors clearly.
func TestDetectLangFromContent(t *testing.T) {
	cases := []struct{ code, want string }{
		{"print(1)", "python3"},
		{"def foo():\n    return 1\nprint(foo())", "python3"},
		{"import os\nprint(os.getcwd())", "python3"},
		{"package main\nfunc main() {}", "go"},
		{"console.log('hi')", "node"},
		{"const x = () => 1", "node"},
		{"echo hello", "bash"},
		{"ls -la /tmp", "bash"},
		// curl still auto-detects as bash when the egress gate is satisfied
		// (a network-only first command without an egress declaration is
		// refused — see TestAutoBashRefusesNetworkCommandWithoutEgress).
		{"curl -s http://example.com\n# egress: example.com:80", "bash"},
		{"git status --short", "bash"},
		{"python3 -m http.server 8000", "bash"},
		{"puts 'hello'", "ruby"},
		{"use strict;\nprint 1;", "perl"},
		{"local x = 1\nfunction f() end", "lua"},
		{"fn main() { println!(\"hi\"); }", "rust"},
		{"x = 42", ""},    // ambiguous → no detection
		{"   \n  \n", ""}, // empty → no detection
	}
	for _, c := range cases {
		got := DetectLang(c.code)
		if got != c.want {
			t.Errorf("DetectLang(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func runtimeInstalled(name string) bool {
	r, ok := runtimes[name]
	if !ok {
		return false
	}
	_, err := exec.LookPath(r.bin)
	return err == nil
}

// TestEgressRegexNetworkBuiltins verifies the egress heuristic catches the
// network-shaped operations that previously slipped through the unisolated
// gate: bash's /dev/tcp and /dev/udp redirection builtins, telnet, socat,
// and openssl's s_client subcommand. Local operations — openssl dgst (local
// crypto) and echo — must stay clear.
func TestEgressRegexNetworkBuiltins(t *testing.T) {
	network := []string{
		"exec 3<>/dev/tcp/evil.example/443",
		"exec 3<>/dev/udp/evil.example/53",
		"telnet evil.example",
		"socat TCP:evil.example:443",
		"openssl s_client -connect evil.example:443",
	}
	for _, code := range network {
		if !containsNetworkCall(code) {
			t.Errorf("containsNetworkCall(%q) = false, want true (network builtin)", code)
		}
	}
	local := []string{
		"openssl dgst -sha256 file",
		"echo hi",
	}
	for _, code := range local {
		if containsNetworkCall(code) {
			t.Errorf("containsNetworkCall(%q) = true, want false (local operation)", code)
		}
	}
}

// TestAutoBashRefusesNetworkCommandWithoutEgress verifies the auto-bash
// fallback refuses to auto-classify a script whose first command is a pure
// network operation (curl, ssh, ...) unless the code declares egress targets.
// Without this gate, `kern exec "curl ..."` would auto-run as bash and
// silently reach the network on hosts where the operator set
// KERN_ALLOW_UNISOLATED=1. Non-network shell commands and egress-declared
// network commands still auto-detect as bash.
func TestAutoBashRefusesNetworkCommandWithoutEgress(t *testing.T) {
	// The RunScript-level check is bash-independent: without bash the content
	// detector returns "" anyway, and with bash the network gate refuses the
	// first command. Either way detection fails closed with the --lang error.
	res := RunScript(Run{Lang: "", Code: "curl -s https://example.com"})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "cannot detect language: pass --lang") {
		t.Errorf("RunScript(curl without egress) error = %v, want language-detection error", res.Err)
	}
	if !runtimeInstalled("bash") {
		t.Skip("bash not installed; remaining assertions exercise the bash fallback")
	}
	if got := detectLangFromContent("curl -s https://example.com"); got != "" {
		t.Errorf("detectLangFromContent(curl without egress) = %q, want \"\"", got)
	}
	if got := detectLangFromContent("curl -s https://example.com\n# egress: example.com:443"); got != "bash" {
		t.Errorf("detectLangFromContent(curl with egress) = %q, want bash", got)
	}
	if got := detectLangFromContent("echo hi"); got != "bash" {
		t.Errorf("detectLangFromContent(echo hi) = %q, want bash", got)
	}
	if got := detectLangFromContent("ssh user@host ls"); got != "" {
		t.Errorf("detectLangFromContent(ssh without egress) = %q, want \"\"", got)
	}
}
