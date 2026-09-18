package transport

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// StdioServer is the transport-side contract a server must satisfy to be
// served over the process's stdin/stdout by ServeStdio. *mcp.Server
// implements it structurally; the interface exists so the drain choreography
// (signal handling, in-flight cancellation, the 5-second drain deadline) is
// owned by the transport package and testable against any implementation.
type StdioServer interface {
	// Serve reads requests from stdin and writes responses to stdout,
	// blocking until the input stream ends or the server is closed.
	Serve() error
	// CancelAll aborts in-flight tool calls and releases held locks.
	CancelAll()
	// Close tears down the server (background watch, sessions, sampling).
	Close()
	// Inflight reports the number of in-flight tool calls.
	Inflight() int
	// StartBackgroundWatch spawns the implicit background index watcher
	// (no-op for intervals <= 0; first call wins).
	StartBackgroundWatch(ctx context.Context, interval time.Duration)
}

// ErrDrainTimeout is returned by ServeStdio when a SIGINT/SIGTERM shutdown
// is requested but in-flight tool calls do not drain within the 5-second
// deadline. Callers should exit with a non-zero status.
var ErrDrainTimeout = errors.New("mcp: in-flight tool calls did not drain within 5s")

// ServeStdio serves the server over the process's stdin/stdout and blocks
// until it exits. On SIGINT/SIGTERM it cancels in-flight tool calls and
// releases locks so slow tools cannot hang the process: closing os.Stdin
// stops the scanner, then it waits up to 5s for in-flight calls to drain.
//
// ServeStdio never calls os.Exit: a clean drain returns nil (callers exit 0)
// and a drain timeout returns ErrDrainTimeout (callers exit 1), so deferred
// cleanup in main runs on every exit path. The background index watch is
// started here with the caller-supplied interval (the composition layer
// derives it from KERN_MCP_WATCH / KERN_MCP_WATCH_INTERVAL).
func ServeStdio(srv StdioServer, watchInterval time.Duration) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// Implicit background index watch: rebuild stale workspace-root indexes
	// between tool calls so the first call after an edit finds a warm index.
	// The watcher stops on ctx cancellation (signal) or srv.Close() below.
	srv.StartBackgroundWatch(ctx, watchInterval)
	// Closing os.Stdin from another goroutine does not reliably unblock the
	// scanner's read, so Serve() alone may never return after a signal.
	// The drain goroutine therefore owns the exit decision after a signal:
	// it reports the drain outcome through outcomeCh and ServeStdio returns
	// that outcome (nil on clean drain, ErrDrainTimeout on timeout) instead
	// of calling os.Exit from a goroutine.
	type drainOutcome struct{ clean bool }
	outcomeCh := make(chan drainOutcome, 1)
	go func() {
		<-ctx.Done()
		srv.CancelAll()
		srv.Close()
		_ = os.Stdin.Close()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if srv.Inflight() == 0 {
				time.Sleep(100 * time.Millisecond)
				outcomeCh <- drainOutcome{clean: true}
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		outcomeCh <- drainOutcome{clean: false}
	}()

	// Serve() may be stuck in a scanner read that never unblocks after
	// os.Stdin.Close(), so it runs in its own goroutine and the drain
	// outcome can terminate ServeStdio even if Serve never returns.
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve() }()

	select {
	case err := <-serveErr:
		srv.CancelAll()
		srv.Close()
		// A signal may have raced with Serve() returning; if so, the drain
		// goroutine is the authoritative exit path.
		if ctx.Err() != nil {
			if o := <-outcomeCh; o.clean {
				return nil
			}
			return ErrDrainTimeout
		}
		if err != nil {
			return err
		}
		return nil
	case o := <-outcomeCh:
		if o.clean {
			return nil
		}
		return ErrDrainTimeout
	}
}
