package script

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestAvailable(t *testing.T) {
	got := Available()
	if len(got) == 0 {
		t.Skip("no script runtimes installed on this host")
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("Available() = %v, want sorted", got)
	}
	seen := map[string]bool{}
	for _, name := range got {
		rt, ok := runtimes[name]
		if !ok {
			t.Errorf("Available() returned unknown runtime %q", name)
			continue
		}
		if seen[name] {
			t.Errorf("Available() returned duplicate %q", name)
		}
		seen[name] = true
		if _, err := exec.LookPath(rt.bin); err != nil {
			t.Errorf("Available() reported %q but %q is not on PATH", name, rt.bin)
		}
	}
}

func TestLanguages(t *testing.T) {
	got := Languages()
	var want []string
	for name := range runtimes {
		want = append(want, name)
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("Languages() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Languages() = %v, want %v", got, want)
			break
		}
	}
}

func TestRunScript_Path(t *testing.T) {
	if !runtimeInstalled("python3") {
		t.Skip("python3 not installed")
	}
	allowDegradedNetwork(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "greet.py")
	if err := os.WriteFile(p, []byte("print('hello from file')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := RunScript(Run{Path: p})
	if !res.OK {
		t.Fatalf("expected success, got %+v (stderr %q)", res, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "hello from file" {
		t.Errorf("stdout = %q, want \"hello from file\"", res.Stdout)
	}
	if res.Lang != "python3" {
		t.Errorf("Lang = %q, want python3 (detected from extension)", res.Lang)
	}
	if res.Runtime != "python3" {
		t.Errorf("Runtime = %q, want python3", res.Runtime)
	}
}

func TestRunScript_RustCompile(t *testing.T) {
	if !runtimeInstalled("rust") {
		t.Skip("rustc not installed")
	}
	allowDegradedNetwork(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "main.rs")
	if err := os.WriteFile(p, []byte("fn main() { println!(\"hi from rust\"); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := RunScript(Run{Lang: "rust", Path: p, Timeout: 60 * time.Second})
	if !res.OK {
		t.Fatalf("expected success, got %+v (stderr %q)", res, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "hi from rust" {
		t.Errorf("stdout = %q, want \"hi from rust\"", res.Stdout)
	}
	if res.Runtime != "rustc" {
		t.Errorf("Runtime = %q, want rustc", res.Runtime)
	}
}

func TestRunScript_RuntimeNotInstalled(t *testing.T) {
	var missing string
	for name, rt := range runtimes {
		if _, err := exec.LookPath(rt.bin); err != nil {
			missing = name
			break
		}
	}
	if missing == "" {
		t.Skip("every script runtime is installed on this host")
	}
	res := RunScript(Run{Lang: missing, Code: "x"})
	if res.Err == nil {
		t.Fatalf("expected error for missing runtime %q", missing)
	}
	if !strings.Contains(res.Err.Error(), "not installed") {
		t.Errorf("error = %v, want missing-runtime error", res.Err)
	}
}

func TestRunScript_UnknownLanguage(t *testing.T) {
	res := RunScript(Run{Lang: "cobol", Code: "x"})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "unknown language") {
		t.Errorf("got %v, want unknown-language error", res.Err)
	}
}

func TestRunScript_PathUnknownExtension(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "script.xyz")
	if err := os.WriteFile(p, []byte("ambiguous content with no language signal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := RunScript(Run{Path: p})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "detect") {
		t.Errorf("got %v, want language-detection error", res.Err)
	}
}

func TestRunScript_ReadFileFailure(t *testing.T) {
	res := RunScript(Run{Path: filepath.Join(t.TempDir(), "nope.py")})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "read script") {
		t.Errorf("got %v, want read-script error", res.Err)
	}
}

func TestSandboxEnv_Whitelist(t *testing.T) {
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("LC_ALL", "C")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TZ", "UTC")
	t.Setenv("KERN_EMBED_MODEL", "nomic-embed-text")
	t.Setenv("SUPERSECRET", "hunter2")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "s3cr3t")

	dir := t.TempDir()
	env := sandboxEnv(dir)
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	for _, k := range []string{"LANG", "LC_ALL", "TERM", "TZ", "KERN_EMBED_MODEL"} {
		want := os.Getenv(k)
		if m[k] != want {
			t.Errorf("sandboxEnv dropped whitelisted %s=%q (env has %q)", k, want, m[k])
		}
	}
	for _, k := range []string{"SUPERSECRET", "AWS_SECRET_ACCESS_KEY"} {
		if _, ok := m[k]; ok {
			t.Errorf("sandboxEnv leaked non-whitelisted %s", k)
		}
	}
	if got := m["HOME"]; !strings.HasPrefix(got, dir) {
		t.Errorf("HOME = %q, want sandboxed under %s", got, dir)
	}
	if m["PATH"] == "" {
		t.Error("PATH must be preserved")
	}
}
