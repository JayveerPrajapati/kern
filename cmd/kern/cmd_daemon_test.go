package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/daemon"
)

func TestDaemonStatusJSONWhenNotRunning(t *testing.T) {
	if !daemon.Supported() {
		t.Skip("daemon unsupported on this platform")
	}
	root := t.TempDir()
	out := captureStdout(t, func() {
		if code := runDaemon([]string{"status", "--root", root, "--json"}); code != 1 {
			t.Errorf("runDaemon(status --json) = %d, want 1 when no daemon is running", code)
		}
	})
	var status daemonStatus
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("decode daemon status JSON: %v\n%s", err, out)
	}
	if status.Running {
		t.Error("status.running = true for an empty temporary root")
	}
	if status.Error == "" {
		t.Error("status.error must be present when no daemon answers")
	}
	wantSocket := daemon.SocketPath(root)
	wantLog := daemon.LogPath(root)
	if status.Root != root || status.Socket != wantSocket || status.Log != wantLog {
		t.Errorf("diagnostic paths mismatch: root=%q socket=%q log=%q; want root=%q socket=%q log=%q",
			status.Root, status.Socket, status.Log, root, wantSocket, wantLog)
	}
}

func TestDaemonFallbackWarningNamesDiagnosticPaths(t *testing.T) {
	root := t.TempDir()
	err := &testDaemonError{msg: "bind: operation not permitted"}
	out := captureStderr(t, func() {
		daemonFallbackWarning(root, "start", err)
	})
	for _, want := range []string{
		"daemon fallback (start)",
		"bind: operation not permitted",
		"socket=" + daemon.SocketPath(root),
		"log=" + daemon.LogPath(root),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fallback warning missing %q, got %q", want, out)
		}
	}
}

type testDaemonError struct{ msg string }

func (e *testDaemonError) Error() string { return e.msg }

func TestParseDaemonFlagsAcceptsJSON(t *testing.T) {
	f, rest, err := parseDaemonFlags([]string{"--root", "/tmp/repo", "--json"})
	if err != nil {
		t.Fatalf("parseDaemonFlags: %v", err)
	}
	if f.root != "/tmp/repo" || !f.json || len(rest) != 0 {
		t.Fatalf("flags = %+v rest = %v, want root=/tmp/repo json=true rest=nil", f, rest)
	}
	if _, _, err := parseDaemonFlags([]string{"--nope"}); err == nil {
		t.Fatal("parseDaemonFlags must reject unknown flags")
	}
}

func TestDaemonHumanStatusExposesPaths(t *testing.T) {
	if !daemon.Supported() {
		t.Skip("daemon unsupported on this platform")
	}
	root := t.TempDir()
	out := captureStdout(t, func() {
		if code := runDaemon([]string{"status", "--root", filepath.Clean(root)}); code != 1 {
			t.Errorf("runDaemon(status) = %d, want 1 when no daemon is running", code)
		}
	})
	for _, want := range []string{"socket=" + daemon.SocketPath(root), "log=" + daemon.LogPath(root)} {
		if !strings.Contains(out, want) {
			t.Errorf("human status missing %q, got %q", want, out)
		}
	}
}

// TestDaemonDispatchStateReportsInProcess pins the serve-state report
// plumbing (2026-11-20 remediation): with KERN_BENCH_SERVED_BY set, a
// dispatch that skips the daemon writes its state to the report file so the
// bench parent gets the child's own account instead of a latency inference.
// It exercises the deterministic in_process branches (env-disabled and
// not-proxied command) — the failed/daemon branches need a live socket,
// which the sandbox denies (that denial is exactly what the report makes
// visible in bench output).
func TestDaemonDispatchStateReportsInProcess(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "served-by.txt")

	t.Setenv("KERN_DAEMON", "0")
	t.Setenv("KERN_BENCH_SERVED_BY", report)

	if resp, ok := daemonDispatch("search", nil); ok {
		t.Fatalf("daemonDispatch(search) with KERN_DAEMON=0 must not serve, got response %+v", resp)
	}
	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("serve-state report not written: %v", err)
	}
	if !strings.HasPrefix(string(raw), serveInProcess+"\n") {
		t.Fatalf("serve-state report = %q, want %q state line", raw, serveInProcess)
	}

	// A non-proxied command (bench itself) also reports in_process — with a
	// daemon available, only read-only proxy commands are eligible.
	os.Remove(report)
	t.Setenv("KERN_DAEMON", "1")
	if _, ok := daemonDispatch("bench", nil); ok {
		t.Fatal("daemonDispatch(bench) must not proxy a non-read-only command")
	}
	raw, err = os.ReadFile(report)
	if err != nil || !strings.HasPrefix(string(raw), serveInProcess+"\n") {
		t.Fatalf("non-proxied command must report %q, got %q (err %v)", serveInProcess, raw, err)
	}
}
