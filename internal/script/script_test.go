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
		{"puts 'hello'", "ruby"},
		{"use strict;\nprint 1;", "perl"},
		{"local x = 1\nfunction f() end", "lua"},
		{"fn main() { println!(\"hi\"); }", "rust"},
		{"x = 42", ""},                 // ambiguous → no detection
		{"   \n  \n", ""},              // empty → no detection
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
