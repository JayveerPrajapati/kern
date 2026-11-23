package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/daemon"
	"github.com/JayveerPrajapati/kern/internal/testfixture"
)

func runKernCLIBoundedDir(t *testing.T, timeout time.Duration, maxBytes int, dir string, args ...string) (string, string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, kernBinary(t), args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stdout.Len() > maxBytes {
		t.Fatalf("stdout exceeded %d bytes: got %d", maxBytes, stdout.Len())
	}
	if stderr.Len() > maxBytes {
		t.Fatalf("stderr exceeded %d bytes: got %d", maxBytes, stderr.Len())
	}
	return stdout.String(), stderr.String(), err
}

func daemonSupported() bool {
	return runtime.GOOS != "windows"
}

func startDaemon(t *testing.T, bin, root string) {
	t.Helper()
	serve := exec.Command(bin, "daemon", "serve", "--root", root)
	serve.Env = append(os.Environ(),
		"KERN_DAEMON_CHILD=1",
		"KERN_INDEX_MEMCACHE=1",
		"KERN_DAEMON_IDLE=10m",
	)
	if err := serve.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		_, _, _ = runKernCLIBoundedDir(t, 10*time.Second, 16<<10, root, "daemon", "stop", "--root", root)
	})
	waitDaemonReady(t, bin, root)
}

func waitDaemonReady(t *testing.T, bin, root string) daemonStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		stdout, _, err := runKernCLIBoundedDir(t, 5*time.Second, 16<<10, root,
			"daemon", "status", "--root", root, "--json")
		if err == nil {
			var status daemonStatus
			if json.Unmarshal([]byte(stdout), &status) == nil && status.Running {
				return status
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("daemon never became ready")
	return daemonStatus{}
}

func daemonSearch(t *testing.T, root, servedBy string) string {
	t.Helper()
	t.Setenv("KERN_BENCH_SERVED_BY", servedBy)
	stdout, stderr, err := runKernCLIBoundedDir(t, 60*time.Second, 128<<10, root,
		"search", "NewServer", "--root", root, "--json", "--limit", "5")
	if err != nil {
		t.Fatalf("kern search via daemon: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "NewServer") {
		t.Fatalf("daemon search missing NewServer:\n%s", stdout)
	}
	data, err := os.ReadFile(servedBy)
	if err != nil {
		t.Fatalf("read served-by marker: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[0] != "daemon" {
		t.Fatalf("search was not served by daemon; served_by=%q detail=%q", lines[0], strings.Join(lines[1:], " "))
	}
	return stdout
}

func TestE2E_daemon_serves_search_and_restarts_cleanly(t *testing.T) {
	if !daemonSupported() {
		t.Skip("daemon unsupported on this platform")
	}
	withTempHOME(t)
	root := testfixture.Repo(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)

	bin := kernBinary(t)
	startDaemon(t, bin, root)
	before := waitDaemonReady(t, bin, root)

	servedBy := filepath.Join(t.TempDir(), "served-by.txt")
	daemonSearch(t, root, servedBy)
	warm := waitDaemonReady(t, bin, root)
	if warm.Pid != before.Pid {
		t.Fatalf("warm daemon was not reused: initial=%d warm=%d", before.Pid, warm.Pid)
	}

	// A stopped daemon must report stopped, remove its socket, and leave no
	// orphan behind. The next eligible read-only command must transparently
	// start a fresh daemon (rather than silently falling back in-process).
	if _, stderr, err := runKernCLIBoundedDir(t, 10*time.Second, 16<<10, root,
		"daemon", "stop", "--root", root); err != nil {
		t.Fatalf("stop daemon: %v\n%s", err, stderr)
	}
	if _, err := os.Stat(daemon.SocketPath(root)); !os.IsNotExist(err) {
		t.Fatalf("daemon socket leaked after stop: %v", err)
	}
	// `status` intentionally exits 1 when no daemon is running; the JSON body
	// is still the contract under test.
	statusJSON, statusStderr, statusErr := runKernCLIBoundedDir(t, 10*time.Second, 16<<10, root,
		"daemon", "status", "--root", root, "--json")
	if statusErr == nil {
		t.Fatalf("stopped daemon status unexpectedly exited 0; stderr=%s", statusStderr)
	}
	var stopped daemonStatus
	if err := json.Unmarshal([]byte(statusJSON), &stopped); err != nil {
		t.Fatalf("decode stopped status: %v\n%s", err, statusJSON)
	}
	if stopped.Running {
		t.Fatalf("stopped daemon still reports running: %+v", stopped)
	}

	daemonSearch(t, root, servedBy)
	after := waitDaemonReady(t, bin, root)
	if after.Pid == before.Pid {
		t.Fatalf("daemon did not restart after stop: before=%d after=%d", before.Pid, after.Pid)
	}
	if _, stderr, err := runKernCLIBoundedDir(t, 10*time.Second, 16<<10, root,
		"daemon", "stop", "--root", root); err != nil {
		t.Fatalf("cleanup daemon: %v\n%s", err, stderr)
	}
}

func TestE2E_daemon_disabled_search_is_in_process(t *testing.T) {
	if !daemonSupported() {
		t.Skip("daemon unsupported on this platform")
	}
	withTempHOME(t)
	root := testfixture.Repo(t)
	t.Setenv("KERN_ROOTS", root)
	t.Setenv("KERN_MCP_ROOTS", root)
	t.Setenv("KERN_DAEMON", "0")

	servedBy := filepath.Join(t.TempDir(), "served-by.txt")
	t.Setenv("KERN_BENCH_SERVED_BY", servedBy)

	stdout, stderr, err := runKernCLIBoundedDir(t, 60*time.Second, 128<<10, root,
		"search", "NewServer", "--root", root, "--json", "--limit", "5")
	if err != nil {
		t.Fatalf("kern search with daemon disabled: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "NewServer") {
		t.Fatalf("in-process search missing NewServer:\n%s", stdout)
	}

	data, err := os.ReadFile(servedBy)
	if err != nil {
		t.Fatalf("read served-by marker: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[0] != "in_process" {
		t.Fatalf("daemon-disabled search must report in_process, got %q detail=%q", lines[0], strings.Join(lines[1:], " "))
	}
}
