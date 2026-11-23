package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shortSockPath builds the SocketPath short form for a root with an arbitrary
// binary key: kernels with long roots (t.TempDir on macOS) exceed sun_path's
// ~104-byte limit, which is exactly why SocketPath has this form — and why the
// stale sweep must glob it too.
func shortSockPath(t *testing.T, root, key string) string {
	t.Helper()
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	return filepath.Join(os.TempDir(), "kern-daemon-"+hex.EncodeToString(sum[:8])+"-"+key+".sock")
}

// fakeStaleDaemon listens on sock and answers pings with a foreign identity,
// exiting (and unlinking the socket) on OpStop — the shape a previous build's
// daemon presents to the current one.
func fakeStaleDaemon(t *testing.T, sock string) (stop func()) {
	t.Helper()
	ln, err := listenUnix(sock)
	if err != nil {
		t.Fatalf("listen %s: %v", sock, err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			var req Request
			_ = json.NewDecoder(conn).Decode(&req)
			if req.Op == OpStop {
				_ = json.NewEncoder(conn).Encode(Response{Pid: 4242, Identity: "v0|1|2"})
				_ = conn.Close()
				_ = ln.Close()
				_ = os.Remove(sock)
				return
			}
			_ = json.NewEncoder(conn).Encode(Response{Pid: 4242, Version: "v0", Identity: "v0|1|2"})
			_ = conn.Close()
		}
	}()
	return func() {
		_ = ln.Close()
		<-done
	}
}

// fakeStubbornStaleDaemon keeps its listener after OpStop. If closeOnStop is
// true it drops the response connection; otherwise it acknowledges the request
// but refuses to exit. Both cases must leave the socket untouched.
func fakeStubbornStaleDaemon(t *testing.T, sock string, closeOnStop bool) (stop func()) {
	t.Helper()
	ln, err := listenUnix(sock)
	if err != nil {
		t.Fatalf("listen %s: %v", sock, err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			var req Request
			_ = json.NewDecoder(conn).Decode(&req)
			if req.Op == OpStop {
				if closeOnStop {
					_ = conn.Close()
					continue
				}
				_ = json.NewEncoder(conn).Encode(Response{Pid: 4243, Identity: "v0|3|4"})
				_ = conn.Close()
				continue
			}
			_ = json.NewEncoder(conn).Encode(Response{Pid: 4243, Version: "v0", Identity: "v0|3|4"})
			_ = conn.Close()
		}
	}()
	return func() {
		_ = ln.Close()
		<-done
		_ = os.Remove(sock)
	}
}

func assertSocketExists(t *testing.T, sock string) {
	t.Helper()
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("live socket disappeared after failed stop: %v", err)
	}
}

func TestStopRequestFailurePreservesLiveSocket(t *testing.T) {
	if !Supported() {
		t.Skip("daemon unsupported on this platform")
	}
	root := t.TempDir()
	sock := shortSockPath(t, root, "deadbee1")
	stop := fakeStubbornStaleDaemon(t, sock, true)
	defer stop()

	err := stopSocket(sock, 50*time.Millisecond, 50*time.Millisecond)
	if err == nil {
		t.Fatal("stopSocket unexpectedly succeeded after a failed stop request")
	}
	assertSocketExists(t, sock)
}

func TestStopTimeoutPreservesLiveSocket(t *testing.T) {
	if !Supported() {
		t.Skip("daemon unsupported on this platform")
	}
	root := t.TempDir()
	sock := shortSockPath(t, root, "deadbee2")
	stop := fakeStubbornStaleDaemon(t, sock, false)
	defer stop()

	err := stopSocket(sock, 50*time.Millisecond, 50*time.Millisecond)
	if err == nil {
		t.Fatal("stopSocket unexpectedly succeeded while daemon stayed alive")
	}
	assertSocketExists(t, sock)
}

func TestStopAllDoesNotCountRefusingDaemon(t *testing.T) {
	if !Supported() {
		t.Skip("daemon unsupported on this platform")
	}
	root := t.TempDir()
	sock := shortSockPath(t, root, "deadbee3")
	stop := fakeStubbornStaleDaemon(t, sock, false)
	defer stop()

	stopped, err := StopAll(root, "v1")
	if err == nil {
		t.Fatal("StopAll unexpectedly succeeded while daemon refused to stop")
	}
	if stopped != 0 {
		t.Fatalf("StopAll stopped %d daemons, want 0 when no stop was confirmed", stopped)
	}
	assertSocketExists(t, sock)
}

// TestStaleDaemonsDetectAndStop pins the P6 lifecycle check: a live daemon on
// a foreign binary key is reported as stale, the current build's own socket is
// never reported, StopAll retires the stale daemon and unlinks its socket, and
// dead socket files are swept.
func TestStaleDaemonsDetectAndStop(t *testing.T) {
	if !Supported() {
		t.Skip("daemon unsupported on this platform")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	staleSock := shortSockPath(t, root, "deadbeef")
	stop := fakeStaleDaemon(t, staleSock)
	defer stop()

	stale := StaleDaemons(root, "v1")
	if len(stale) != 1 {
		t.Fatalf("StaleDaemons = %+v, want exactly the fake stale daemon", stale)
	}
	if stale[0].Pid != 4242 || stale[0].Version != "v0" {
		t.Fatalf("stale daemon fields wrong: %+v", stale[0])
	}
	// The current build's own socket is excluded by construction.
	if stale[0].Socket == SocketPath(root) {
		t.Fatalf("current binary's socket reported as stale: %s", stale[0].Socket)
	}

	// A dead socket file (no listener) is debris: not a daemon, but swept.
	deadSock := shortSockPath(t, root, "feedface")
	deadLn, err := listenUnix(deadSock)
	if err != nil {
		t.Fatalf("listen dead: %v", err)
	}
	_ = deadLn.Close()

	stopped, err := StopAll(root, "v1")
	if err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	if stopped != 1 {
		t.Fatalf("StopAll stopped %d, want 1 (the live stale daemon)", stopped)
	}
	if _, err := os.Stat(staleSock); !os.IsNotExist(err) {
		t.Fatalf("stale socket file survived StopAll: %v", err)
	}
	if _, err := os.Stat(deadSock); !os.IsNotExist(err) {
		t.Fatalf("dead socket debris survived StopAll: %v", err)
	}
	if got := StaleDaemons(root, "v1"); len(got) != 0 {
		t.Fatalf("StaleDaemons after StopAll = %+v, want none", got)
	}
}
