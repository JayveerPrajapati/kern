package lock

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestAcquireRestampsAfterHolderDeath pins NIT-19a's verify cell: a dead
// holder's flock is dropped by the kernel, Held stops reporting the dead
// PID, and the next acquire re-stamps the LIVE PID. Probed live during the
// 2026-10-25 campaign-2 re-eval closure: current code already satisfies the
// cell (Acquire truncates and rewrites the stamp; List/Held zero or replace
// stale PIDs), so this test exists to keep it that way. The Windows port
// (lock_windows.go) implements the same reclaim via pidAlive checks.
func TestAcquireRestampsAfterHolderDeath(t *testing.T) {
	root := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run", "^TestAcquireRestampsHolderChild$")
	child.Env = append(os.Environ(), "LOCK_ROOT="+root)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if held, pid, _ := Held(root, "probe-scope"); held && pid == child.Process.Pid {
			break
		}
		if time.Now().After(deadline) {
			_ = child.Process.Kill()
			t.Fatal("child never acquired the lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	// flock dropped by the kernel: the stale stamp must not read as held.
	if held, pid, _ := Held(root, "probe-scope"); held || pid != 0 {
		t.Fatalf("after holder death: held=%v pid=%d, want held=false pid=0", held, pid)
	}
	l, err := Acquire(root, "probe-scope")
	if err != nil {
		t.Fatalf("re-acquire after holder death: %v", err)
	}
	defer l.Release()
	held, pid, _ := Held(root, "probe-scope")
	if !held || pid != os.Getpid() {
		t.Fatalf("after re-acquire: held=%v pid=%d, want held=true pid=%d", held, pid, os.Getpid())
	}
}

// TestAcquireRestampsHolderChild is the child side of
// TestAcquireRestampsAfterHolderDeath: hold LOCK_ROOT/probe-scope and park.
func TestAcquireRestampsHolderChild(t *testing.T) {
	root := os.Getenv("LOCK_ROOT")
	if root == "" {
		t.Skip("helper: no LOCK_ROOT")
	}
	l, err := Acquire(root, "probe-scope")
	if err != nil {
		t.Fatalf("helper acquire: %v", err)
	}
	defer l.Release()
	for {
		time.Sleep(time.Hour)
	}
}
