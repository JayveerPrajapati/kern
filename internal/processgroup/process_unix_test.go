//go:build !windows

package processgroup

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSetPGID verifies Set places the child in its own process group so the
// whole group can be killed together (the security property that prevents
// orphaned grandchildren escaping exec timeouts).
func TestSetPGID(t *testing.T) {
	cmd := exec.Command("sh", "-c", "echo $$")
	Set(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("Set must set SysProcAttr.Setpgid on the command")
	}
}

// TestKillKillsWholeGroup spawns a shell that itself spawns a grandchild sleep,
// then Kill the process group and assert both the direct child and the
// grandchild die together (no orphan left running).
func TestKillKillsWholeGroup(t *testing.T) {
	// The shell records its own PID to a file, starts a long-running grandchild,
	// and exits. After Kill(-pgid) the grandchild must be dead too.
	pidFile := t.TempDir() + "/child.pid"
	cmd := exec.Command("sh", "-c",
		"echo $$ > "+pidFile+" ; sleep 30 & echo $! > "+pidFile+".grand")
	Set(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait until both PIDs are written so the group is fully formed.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, e1 := os.Stat(pidFile); e1 == nil {
			if _, e2 := os.Stat(pidFile + ".grand"); e2 == nil {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	childPID, err := readPID(pidFile)
	if err != nil {
		t.Fatalf("child pid: %v", err)
	}
	grandPID, err := readPID(pidFile + ".grand")
	if err != nil {
		t.Fatalf("grandchild pid: %v", err)
	}

	Kill(cmd)
	_ = cmd.Wait()

	// Both must be gone (ESRCH — actually reaped, not a lingering zombie)
	// shortly after. Poll because SIGKILL delivery + reaping is asynchronous.
	for _, pid := range []int{childPID, grandPID} {
		deadline := time.Now().Add(5 * time.Second)
		gone := false
		for time.Now().Before(deadline) {
			if !procAlive(pid) {
				gone = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !gone {
			t.Fatalf("pid %d (process group member) is still alive after Kill", pid)
		}
	}
}

func readPID(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

// procAlive reports whether a process exists AND is not a zombie. kill(pid, 0)
// returns nil for zombies too, so it is not sufficient; we additionally probe
// that the process is not in the zombie (defunct) state via `ps` output.
func procAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	// Probe existence.
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if proc.Signal(os.Signal(syscall.Signal(0))) != nil {
		return false // ESRCH → gone
	}
	// Exists — check it is not defunct (zombie).
	out, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false // ps failed to find it → gone
	}
	return !strings.Contains(string(out), "Z")
}
