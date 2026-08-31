package mcp

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// ErrDrainTimeout is returned by ServeStdio when a SIGINT/SIGTERM shutdown
// is requested but in-flight tool calls do not drain within the 5-second
// deadline. Callers should exit with a non-zero status.
var ErrDrainTimeout = errors.New("mcp: in-flight tool calls did not drain within 5s")

// ServeStdio serves the MCP server over the process's stdin/stdout and
// blocks until the server exits. On SIGINT/SIGTERM it cancels in-flight
// tool calls and releases locks so slow tools cannot hang the process:
// closing os.Stdin stops the scanner, then it waits up to 5s for
// in-flight calls to drain.
//
// ServeStdio never calls os.Exit: a clean drain returns nil (callers exit 0)
// and a drain timeout returns ErrDrainTimeout (callers exit 1), so deferred
// cleanup in main runs on every exit path.
func ServeStdio(srv *Server) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
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
