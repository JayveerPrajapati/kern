// lock.go implements the `kern lock` / `kern unlock` / `kern status` CLI
// entry points: flag parsing, the wait-bounded acquisition loop and the
// lock lifecycle events live here; the flock store is internal/lock.
package contextcli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/relay"
)

// lockFlags are the flags `kern lock` consumes — exactly the dispatch
// commandFlags["lock"] row (hold/root/timeout/wait).
type lockFlags struct {
	root       string
	hold       bool
	timeout    int
	timeoutSet bool
	// timeoutDur keeps the --timeout duration-string form (30m, 500ms) that
	// the shared parser accepts; runLock reads only the integer-seconds form
	// (.timeout), exactly like the original.
	timeoutDur string
	wait       int
}

// parseLockFlags replicates the unified cmd/kern parser's semantics for the
// lock flags, via the shared cliflags.Parser. --timeout is the dual-form
// flag: an integer is seconds (read by runLock), a Go duration string is
// kept verbatim in timeoutDur (unread, as before); either form sets
// timeoutSet so the default blocking mode is distinguishable from an
// explicit --timeout.
func parseLockFlags(args []string) (lockFlags, []string, error) {
	var f lockFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--hold":
			p.SetBool(&f.hold, "--hold", inline, hasInline)
		case "--timeout":
			p.SetTimeout(&f.timeout, &f.timeoutDur, &f.timeoutSet, "--timeout", inline, hasInline)
		case "--wait":
			p.SetInt(&f.wait, "--wait", inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

// unlockFlags are the flags `kern unlock` consumes — exactly the dispatch
// commandFlags["unlock"] row (root).
type unlockFlags struct {
	root string
}

func parseUnlockFlags(args []string) (unlockFlags, []string, error) {
	var f unlockFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

// statusFlags are the flags `kern status` consumes — exactly the dispatch
// commandFlags["status"] row (json).
type statusFlags struct {
	json bool
}

func parseStatusFlags(args []string) (statusFlags, []string, error) {
	var f statusFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--json":
			p.SetBool(&f.json, "--json", inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

// RunLock executes `kern lock <scope> [root]` and returns the process exit
// code (0 ok, 1 runtime failure, 2 usage error).
func RunLock(rest []string) int {
	return run(func() int { runLockCLI(rest); return 0 })
}

// RunUnlock executes `kern unlock <scope> [root]` and returns the process
// exit code (0 ok, 1 runtime failure, 2 usage error).
func RunUnlock(rest []string) int {
	return run(func() int { runUnlockCLI(rest); return 0 })
}

// RunStatus executes `kern status [root]` and returns the process exit code
// (0 ok, 1 runtime failure, 2 usage error).
func RunStatus(rest []string) int {
	return run(func() int { runStatusCLI(rest); return 0 })
}

func runLockCLI(rest []string) {
	f, args, err := parseLockFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern lock <scope> [root]")
	}
	root := rootOf(f.root)
	if len(args) > 1 {
		root = args[1]
	}
	scope := args[0]
	lk, err := acquireLockWithWait(root, scope, f.wait)
	if err != nil {
		_, pid, _ := lock.Held(root, scope)
		emitLockEvent(root, string(eventbus.LockContended), scope, map[string]any{"holder_pid": pid})
		fatal("lock %q is held (pid %d): %v", scope, pid, err)
	}
	defer func() {
		if rerr := lk.Release(); rerr != nil {
			fmt.Fprintf(os.Stderr, "kern: warning: could not release lock %q: %v\n", scope, rerr)
		}
	}()
	emitLockEvent(root, string(eventbus.LockAcquired), scope, map[string]any{"pid": os.Getpid()})
	if f.hold {
		// One-shot mode for tool/plugin callers: acquire, report, and
		// return immediately. The OS releases the flock on exit, so the
		// lock is only held for the duration of this process — use
		// `kern status` to verify and `kern unlock` to clear the marker.
		fmt.Printf("lock acquired: %s (pid %d). one-shot (--hold): the flock releases when this process exits; the lock marker persists until 'kern unlock %s'.\n", scope, os.Getpid(), scope)
		return
	}
	fmt.Printf("lock acquired: %s (pid %d). holding — release with Ctrl-C, 'kern unlock %s', or --timeout N.\n", scope, os.Getpid(), scope)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if !f.timeoutSet || f.timeout <= 0 {
		// Default blocking mode: hold until interrupt.
		<-ctx.Done()
		fmt.Printf("lock released: %s\n", scope)
		return
	}
	// --timeout N: release after N seconds (or on interrupt, whichever
	// comes first). The deferred Release runs on return either way.
	select {
	case <-ctx.Done():
		fmt.Printf("lock released: %s\n", scope)
	case <-time.After(time.Duration(f.timeout) * time.Second):
		fmt.Printf("lock released: %s (timeout after %ds)\n", scope, f.timeout)
	}

}

func runUnlockCLI(rest []string) {
	f, args, err := parseUnlockFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern unlock <scope> [root]")
	}
	root := rootOf(f.root)
	if len(args) > 1 {
		root = args[1]
	}
	if err := lock.Remove(root, args[0]); err != nil {
		fatal("Unlock: %v", err)
	}
	emitLockEvent(root, string(eventbus.LockReleased), args[0], nil)
	fmt.Printf("lock removed: %s\n", args[0])

}

func runStatusCLI(rest []string) {
	f, args, err := parseStatusFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	sts, err := lock.List(root)
	if err != nil {
		fatal("Status: %v", err)
	}
	if f.json {
		printJSON(map[string]any{"locks": sts})
		return
	}
	if len(sts) == 0 {
		fmt.Println("no locks in workspace")
		return
	}
	for _, s := range sts {
		state := "free"
		if s.Held {
			state = "HELD"
		}
		holder := ""
		if s.PID > 0 {
			holder = fmt.Sprintf(" (pid %d)", s.PID)
		}
		fmt.Printf("  %-24s %-6s%s\n", s.Scope, state, holder)
	}

}

// acquireLockWithWait acquires the lock, retrying a held lock every 250ms
// for up to wait seconds (QA F4: `--wait N` bounds the WAIT to acquire;
// `--timeout N` stays the bound on the HOLD). wait <= 0 keeps the
// fail-fast behavior.
func acquireLockWithWait(root, scope string, wait int) (*lock.Lock, error) {
	if wait <= 0 {
		return lock.Acquire(root, scope)
	}
	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	notified := false
	for {
		lk, err := lock.Acquire(root, scope)
		if err == nil {
			return lk, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		if !notified {
			_, pid, _ := lock.Held(root, scope)
			fmt.Fprintf(os.Stderr, "kern: lock %q is held (pid %d) — waiting up to %ds (--wait)…\n", scope, pid, wait)
			notified = true
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// emitLockEvent publishes a lock lifecycle event through the relay when
// one is running. Best-effort: without a relay owner the event is
// dropped silently — lock semantics never depend on the relay.
func emitLockEvent(root, kind, scope string, payload map[string]any) {
	c, err := relay.Dial(root)
	if err != nil {
		return
	}
	defer func() { _ = c.Close() }()
	_ = c.Emit(eventbus.Event{
		Kind:    eventbus.Kind(kind),
		Source:  "cli",
		Subject: scope,
		Payload: payload,
	})
}
