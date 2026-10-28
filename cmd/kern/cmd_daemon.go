// cmd_daemon.go is the CLI shell for `kern daemon {serve,status,stop}`.
//
// The daemon keeps one hydrated index per project so the read-only commands
// (see daemonProxyCommands) answer without paying index hydration per call.
// This file owns the process lifecycle; the socket protocol lives in
// internal/daemon and the proxying decision lives in daemon_proxy.go.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/astrefs"
	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/daemon"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// daemonFlags holds the small flag surface shared by every daemon subcommand.
type daemonFlags struct {
	root string
}

// parseDaemonFlags accepts only --root; anything else is a usage error (the
// same strictness every other command has).
func parseDaemonFlags(args []string) (daemonFlags, []string, error) {
	var f daemonFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--root", "-r":
			p.SetStr(&f.root, inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

// runDaemon implements `kern daemon [serve|status|stop] [--root ROOT]`.
func runDaemon(rest []string) int {
	sub := "status"
	if len(rest) > 0 && !cliflags.IsFlagToken(rest[0]) {
		sub = rest[0]
		rest = rest[1:]
	}
	f, extra, err := parseDaemonFlags(rest)
	if err != nil {
		fatalUsage("daemon: %v", err)
	}
	if len(extra) > 0 {
		fatalUsage("daemon: unexpected argument %q", extra[0])
	}
	root := f.root
	if root == "" {
		// Resolve the same way the proxy does, so `kern daemon status` from a
		// subdirectory finds the project daemon instead of reporting "not
		// running" for the subdirectory itself.
		resolved, ok := daemonProjectRoot()
		if !ok {
			fatal("daemon: resolve root: cannot determine cwd")
		}
		root = resolved
	}
	abs, absErr := filepath.Abs(root)
	if absErr != nil {
		fatal("daemon: resolve root: %v", absErr)
	}
	switch sub {
	case "serve":
		return daemonServe(abs)
	case "status":
		resp, err := daemon.Status(abs)
		if err != nil {
			fmt.Printf("daemon: not running for %s (%v)\n", abs, err)
			return 1
		}
		fmt.Printf("daemon: running pid=%d version=%s socket=%s\n", resp.Pid, resp.Version, daemon.SocketPath(abs))
		return 0
	case "stop":
		if err := daemon.Stop(abs); err != nil {
			fmt.Fprintf(os.Stderr, "daemon: not running for %s (%v)\n", abs, err)
			return 1
		}
		fmt.Printf("daemon: stopped (%s)\n", abs)
		return 0
	default:
		fatalUsage("daemon: unknown subcommand %q (want serve, status, or stop)", sub)
	}
	return 2
}

// daemonServe runs the accept loop in the foreground (the parent invoked by
// Ensure detaches it). The index memcache is enabled here and ONLY here: this
// process answers read-only requests by design, so a shared *Index is safe.
func daemonServe(root string) int {
	if !daemon.Supported() {
		fmt.Fprintln(os.Stderr, "daemon: not supported on this platform")
		return 2
	}
	if err := os.Chdir(root); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: chdir %s: %v\n", root, err)
		return 1
	}
	os.Setenv("KERN_DAEMON_CHILD", "1")
	// The daemon is the one process allowed to share a hydrated index: it
	// only answers the read-only whitelist (daemon_proxy.go). The derived
	// graph follows the index that produced it (internal/app/platform_memo.go).
	index.MemoizeLoad = true
	app.MemoizePlatform = true
	astrefs.MemoizeScans = true
	// Commands resolve relative paths against cwd; the daemon is per-root, so
	// cwd is the project root for every request by construction.
	if err := daemon.Serve(root, version, capturedHandler()); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: %v\n", err)
		return 1
	}
	return 0
}

// daemonExec is wired to dispatchCommand by main(). It is a variable (not a
// direct call) because commandTable's initializer reaches this file, and a
// direct reference would form an initialization cycle
// (commandTable → runDaemon → runCaptured → dispatchCommand → commandTable).
var daemonExec func(cmd string, rest []string) int

// capturedHandler serializes requests (one hydrated index, one stdout swap at
// a time) and captures the command's output byte-for-byte.
func capturedHandler() daemon.Handler {
	var mu sync.Mutex
	return func(argv []string) daemon.Response {
		mu.Lock()
		defer mu.Unlock()
		return runCaptured(argv)
	}
}

// runCaptured executes one command in-process with os.Stdout/os.Stderr
// redirected to temp files, returning the exact bytes plus the exit code.
// Files (not pipes) keep this deadlock-free regardless of output size, and the
// exitError sentinel panic becomes an exit code exactly as main() does.
func runCaptured(argv []string) (resp daemon.Response) {
	if len(argv) == 0 {
		return daemon.Response{Exit: 2, Stderr: "kern daemon: empty argv\n"}
	}
	outFile, err := os.CreateTemp("", "kern-daemon-out-*")
	if err != nil {
		return daemon.Response{Exit: 1, Stderr: "kern daemon: capture stdout: " + err.Error() + "\n"}
	}
	defer os.Remove(outFile.Name())
	errFile, err := os.CreateTemp("", "kern-daemon-err-*")
	if err != nil {
		outFile.Close()
		return daemon.Response{Exit: 1, Stderr: "kern daemon: capture stderr: " + err.Error() + "\n"}
	}
	defer os.Remove(errFile.Name())

	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outFile, errFile
	code := func() (c int) {
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(exitError); ok {
					c = e.code
					return
				}
				fmt.Fprintf(errFile, "kern: panic in %v: %v\n", argv[0], r)
				c = 1
			}
		}()
		if daemonExec == nil {
			fmt.Fprintf(errFile, "kern daemon: dispatcher not wired\n")
			return 2
		}
		return daemonExec(argv[0], argv[1:])
	}()
	os.Stdout, os.Stderr = oldOut, oldErr
	_ = outFile.Close()
	_ = errFile.Close()

	outBytes, _ := os.ReadFile(outFile.Name())
	errBytes, _ := os.ReadFile(errFile.Name())
	return daemon.Response{Exit: code, Stdout: string(outBytes), Stderr: string(errBytes)}
}
