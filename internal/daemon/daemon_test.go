package daemon

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain lets this test binary double as the `kern daemon serve` child
// that Ensure's production spawn path detaches: Ensure execs os.Executable()
// with "daemon serve --root ROOT", and under `go test` that executable is
// this binary. KERN_TEST_DAEMON_VERSION carries the identity version the
// child must serve with (the real child takes it from its build stamp);
// KERN_DAEMON_IDLE, set by the spawning test, bounds the child's life if
// the test dies before stopping it.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "daemon" && os.Getenv("KERN_TEST_DAEMON_VERSION") != "" {
		root := ""
		for i := 1; i < len(os.Args)-1; i++ {
			if os.Args[i] == "--root" {
				root = os.Args[i+1]
			}
		}
		if root == "" {
			os.Exit(2)
		}
		if err := Serve(root, os.Getenv("KERN_TEST_DAEMON_VERSION"), func([]string) Response { return Response{Exit: 0} }); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// captureStderr swaps os.Stderr for the duration of fn and returns what was
// written to it. The implicit-autostart notice is stderr-only, so its
// regression test has to observe the real stream the production code uses.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() {
		os.Stderr = old
		_ = r.Close()
	}()
	fn()
	_ = w.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// waitSocket blocks until Serve answers a ping (or the test times out). A
// bare os.Stat is not enough: a stale socket FILE from a crash must not count
// as a live daemon, which is exactly what the recovery test exercises.
func waitSocket(t *testing.T, root string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if _, err := Call(root, Request{Op: OpPing}, 200*time.Millisecond); err == nil {
			return
		} else {
			lastErr = err
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s never accepted a connection (last error: %v)", SocketPath(root), lastErr)
}

// TestServeCallRoundtrip pins the protocol: a run request reaches the handler,
// its exact stdout/exit come back, an identity mismatch is refused, and stop
// retires the daemon (socket removed).
func TestServeCallRoundtrip(t *testing.T) {
	if !Supported() {
		t.Skip("daemon unsupported on this platform")
	}
	root := t.TempDir()
	identity := BinaryIdentity("v1")
	var gotArgv []string
	handler := func(argv []string) Response {
		gotArgv = argv
		return Response{Exit: 3, Stdout: "out:" + strings.Join(argv, " "), Stderr: "err\n"}
	}
	done := make(chan error, 1)
	go func() { done <- Serve(root, "v1", handler) }()
	waitSocket(t, root)

	resp, err := Call(root, Request{Op: OpRun, Identity: identity, Argv: []string{"search", "x"}}, 2*time.Second)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Exit != 3 || resp.Stdout != "out:search x" || resp.Stderr != "err\n" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if strings.Join(gotArgv, " ") != "search x" {
		t.Fatalf("handler saw %v", gotArgv)
	}

	// A daemon from another build must refuse rather than answer with stale
	// behavior.
	resp, err = Call(root, Request{Op: OpRun, Identity: "other-build", Argv: []string{"search", "x"}}, 2*time.Second)
	if err != nil {
		t.Fatalf("mismatch call: %v", err)
	}
	if !strings.HasPrefix(resp.Fatal, "identity-mismatch") {
		t.Fatalf("expected identity-mismatch, got %+v", resp)
	}

	// Ping reports the daemon's identity and pid.
	resp, err = Call(root, Request{Op: OpPing}, 2*time.Second)
	if err != nil || resp.Identity != identity || resp.Pid != os.Getpid() || resp.Version != "v1" {
		t.Fatalf("ping: %+v err=%v", resp, err)
	}

	if err := Stop(root); err != nil {
		t.Fatalf("stop: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after stop")
	}
	if _, err := os.Stat(SocketPath(root)); !os.IsNotExist(err) {
		t.Fatalf("socket left behind: %v", err)
	}
}

// TestServeIdleExit pins the idle timeout: an unused daemon exits and removes
// its socket, so a stray process never lives forever.
func TestServeIdleExit(t *testing.T) {
	if !Supported() {
		t.Skip("daemon unsupported on this platform")
	}
	t.Setenv("KERN_DAEMON_IDLE", "250ms")
	root := t.TempDir()
	done := make(chan error, 1)
	go func() { done <- Serve(root, "v1", func([]string) Response { return Response{} }) }()
	waitSocket(t, root)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("idle exit returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not exit when idle")
	}
	if _, err := os.Stat(SocketPath(root)); !os.IsNotExist(err) {
		t.Fatalf("socket left behind after idle exit: %v", err)
	}
}

// TestServeRefusesSecondDaemon pins that a live socket is not silently
// replaced (two daemons racing on one root would double-hydrate and confuse
// stop).
func TestServeRefusesSecondDaemon(t *testing.T) {
	if !Supported() {
		t.Skip("daemon unsupported on this platform")
	}
	root := t.TempDir()
	errs := make(chan error, 1)
	go func() { errs <- Serve(root, "v1", func([]string) Response { return Response{} }) }()
	waitSocket(t, root)
	defer Stop(root)
	if err := Serve(root, "v1", func([]string) Response { return Response{} }); err == nil {
		t.Fatal("second Serve on a live socket must fail")
	}
	select {
	case err := <-errs:
		if err != nil {
			t.Fatalf("first Serve exited early: %v", err)
		}
	default:
	}
}

// TestStaleSocketIsReplaced pins crash recovery: a socket file with no
// listener is removed and re-bound instead of wedging every future call.
func TestStaleSocketIsReplaced(t *testing.T) {
	if !Supported() {
		t.Skip("daemon unsupported on this platform")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SocketPath(root), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- Serve(root, "v1", func([]string) Response { return Response{} }) }()
	waitSocket(t, root)
	if _, err := Call(root, Request{Op: OpPing}, 2*time.Second); err != nil {
		t.Fatalf("ping after stale-socket recovery: %v", err)
	}
	_ = Stop(root)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
}

// TestEnsureImplicitStartNotifiesOnce pins F-C2: an implicitly auto-started
// daemon announces itself with exactly one stderr line naming the pid and
// the stop command, and a warm daemon (second invocation) emits nothing.
func TestEnsureImplicitStartNotifiesOnce(t *testing.T) {
	if !Supported() {
		t.Skip("daemon unsupported on this platform")
	}
	t.Setenv("KERN_DAEMON_IDLE", "30s") // leak bound: the child self-exits even if this test dies
	t.Setenv("KERN_TEST_DAEMON_VERSION", "v-ensure")
	root := t.TempDir()
	out := captureStderr(t, func() {
		if err := Ensure(root, "v-ensure"); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
	})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "[kern] background daemon started (pid ") {
		t.Fatalf("implicit autostart must emit exactly one notice line, got %d: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], "`kern daemon stop` to stop") {
		t.Fatalf("notice must name the stop command: %q", lines[0])
	}
	resp, err := Status(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(lines[0], "pid "+FormatPid(resp.Pid)) {
		t.Fatalf("notice pid does not match the running daemon: %q vs pid %d", lines[0], resp.Pid)
	}
	// Second Ensure finds the warm daemon: no second notice.
	quiet := captureStderr(t, func() {
		if err := Ensure(root, "v-ensure"); err != nil {
			t.Fatalf("second Ensure: %v", err)
		}
	})
	if quiet != "" {
		t.Fatalf("warm daemon must not re-notify, got %q", quiet)
	}
	if err := Stop(root); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

// TestServeExplicitStartIsSilent pins the other half of F-C2: an explicit
// `kern daemon serve` (what a user types) never prints the autostart notice.
func TestServeExplicitStartIsSilent(t *testing.T) {
	if !Supported() {
		t.Skip("daemon unsupported on this platform")
	}
	root := t.TempDir()
	out := captureStderr(t, func() {
		go Serve(root, "v-explicit", func([]string) Response { return Response{Exit: 0} })
		waitSocket(t, root)
	})
	if strings.Contains(out, "background daemon started") {
		t.Fatalf("explicit Serve must stay silent, got %q", out)
	}
	if err := Stop(root); err != nil {
		t.Fatalf("stop: %v", err)
	}
}
