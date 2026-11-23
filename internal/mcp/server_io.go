package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"time"
)

// errLineTooLong reports a stdin line that exceeds the frame cap; the
// caller logs it and keeps serving (envelope audit F-3, 2026-10-25).
var errLineTooLong = errors.New("input line exceeds the frame cap")

// readLineLimit reads one newline-terminated line from r, enforcing a hard
// length cap. A line longer than max is drained to its newline and reported
// as errLineTooLong — the next read starts at the FOLLOWING frame, so an
// oversized line's remainder is never re-read as a garbage frame (the bug
// the old bufio.Scanner approach had: its ErrTooLong path left the tail of
// the line in the raw reader). The returned line excludes the trailing
// newline (and \r before it); a final line without a trailing newline is
// returned when EOF follows content.
func readLineLimit(r *bufio.Reader, max int) ([]byte, error) {
	var (
		buf  []byte
		over bool
	)
	for {
		chunk, err := r.ReadSlice('\n')
		if !over {
			if len(buf)+len(chunk) > max {
				// Over the cap: stop accumulating and drain the rest of the
				// line so the next call starts at the next frame.
				over = true
				buf = nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		switch {
		case err == nil:
			if over {
				return nil, errLineTooLong
			}
			return trimNewline(buf), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue // line longer than the 64 KiB read buffer: keep reading
		case errors.Is(err, io.EOF):
			if over {
				return nil, errLineTooLong
			}
			if len(buf) == 0 {
				return nil, io.EOF
			}
			return buf, nil // final frame without a trailing newline
		default:
			return nil, err
		}
	}
}

// trimNewline strips one trailing \n and an optional \r before it, matching
// bufio.ScanLines' frame semantics.
func trimNewline(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
		if n = len(b); n > 0 && b[n-1] == '\r' {
			b = b[:n-1]
		}
	}
	return b
}

// Serve runs the MCP server loop until EOF or a write error, draining
// in-flight tool calls before returning.
func (s *Server) Serve() error {
	if s.sem == nil {
		s.sem = make(chan struct{}, defaultConcurrency())
	}
	var wg sync.WaitGroup
	// EOF or a write error ends the server: drain in-flight tool calls
	// first (a tool call may be the one queuing a background index save),
	// then Close() so the sessions' background index saves (saveWG) and
	// file watchers are drained before Serve returns. Without this, a
	// caller that tears down its workspace right after Serve returns —
	// e.g. a test removing its t.TempDir — races the late persister
	// writes ("unlinkat .kern: directory not empty"). Close is
	// idempotent, so the explicit Close() in the CLI shutdown paths is
	// unaffected.
	defer func() {
		wg.Wait()
		s.Close()
	}()
	// Line framing with a hard 16 MiB per-line cap — the same cap the HTTP
	// transport enforces on request bodies (413), so one transport cannot
	// buffer 4x the other's worst case. The previous bufio.Scanner approach
	// never actually discarded an oversized line's remainder: by the time
	// ErrTooLong fired, the scanner had already consumed the line's first
	// ~cap bytes into its own buffer, so the leftover fragment was re-read
	// as a new (short) garbage line and answered with a -32700 parse
	// error. readLineLimit tracks the true line length and drains the
	// remainder of an oversized line before the next frame is read
	// (envelope audit F-3, 2026-10-25).
	rd := bufio.NewReaderSize(s.in, 64<<10)
	for {
		line, err := readLineLimit(rd, 16<<20)
		if err == errLineTooLong {
			// Answer the client instead of only skipping silently: a naive
			// client would otherwise wait forever for a response that never
			// comes (W13, 2026-10-26 black-box audit; the HTTP transport
			// already answers 413). The frame is too large to have an
			// extractable id, so the error response carries id null.
			fmt.Fprintf(os.Stderr, "kern-mcp: skipping oversized input line (> %d bytes); continuing\n", 16<<20)
			if werr := s.write(errorResponse(nil, -32600, fmt.Sprintf("message too large: input line exceeds %d bytes", 16<<20))); werr != nil {
				return werr
			}
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			var typeErr *json.UnmarshalTypeError
			if errors.As(err, &typeErr) {
				// Valid JSON with a wrong-typed envelope (an array, a bare
				// scalar) or a wrong-typed field is an invalid REQUEST, not
				// a parse error: answer -32600 with a sanitized message —
				// never the raw Go error, which leaks internal type names
				// like mcp.rpcRequest (W1+W7, 2026-10-26 black-box audit).
				msg := "invalid request: a JSON-RPC request must be a JSON object"
				if typeErr.Field != "" {
					msg = fmt.Sprintf("invalid request: wrong JSON type for field %q", typeErr.Field)
				}
				if err := s.write(errorResponse(nil, -32600, msg)); err != nil {
					return err
				}
				continue
			}
			// A malformed line is a protocol error, not a silent drop.
			if err := s.write(errorResponse(nil, -32700, "parse error: "+err.Error())); err != nil {
				return err
			}
			continue
		}
		// JSON-RPC 2.0 envelope: a frame that parses but does not carry
		// "jsonrpc":"2.0" exactly is not a valid request and is answered
		// -32600 instead of being served (envelope audit F-1, 2026-10-25).
		// Client replies to server-initiated requests carry the same
		// envelope, so they still reach the branch below untouched.
		if req.JSONRPC != "2.0" {
			var envErrID json.RawMessage
			if !req.isNotification() {
				envErrID = req.ID
			}
			if err := s.write(errorResponse(envErrID, -32600, `invalid request: jsonrpc must be "2.0"`)); err != nil {
				return err
			}
			continue
		}
		if req.Method == "" && rawPresent(req.Result) || req.Method == "" && rawPresent(req.Error) {
			// A response to a server-initiated request (host sampling):
			// deliver it to the waiting sampler, never dispatch it as a
			// request.
			if s.deliverSamplingReply(req) {
				continue
			}
		}
		if req.Method == "tools/call" {
			// Run tools concurrently so a slow tool (build, heal, LLM
			// optimize) can never freeze the stdio server: $/cancelRequest,
			// progress and other tool calls keep being served while it runs.
			// Response writes are serialized by s.write's mutex, and the
			// request is captured by value so the loop can move on.
			// Concurrency is bounded by s.sem so a burst of tool calls cannot
			// spawn unbounded goroutines that each build a full project index.
			wg.Add(1)
			s.sem <- struct{}{}
			go func(req rpcRequest) {
				defer wg.Done()
				defer func() { <-s.sem }()
				if resp := s.safeDispatch(req); resp != nil {
					if err := s.write(resp); err != nil {
						fmt.Fprintf(os.Stderr, "kern-mcp: write response: %v\n", err)
					}
				}
			}(req)
			continue
		}
		if resp := s.safeDispatch(req); resp != nil {
			if err := s.write(resp); err != nil {
				return err
			}
		}
	}
}

// safeDispatch computes a request's response, converting any panic into an
// internal-error response instead of crashing the server.
func (s *Server) safeDispatch(req rpcRequest) (r any) {
	defer func() {
		if rec := recover(); rec != nil {
			r = errorResponse(req.ID, -32603, fmt.Sprintf("internal error: %v", rec))
			fmt.Fprintf(os.Stderr, "kern-mcp: panic serving %s: %v\n%s\n", req.Method, rec, debug.Stack())
		}
	}()
	return s.dispatch(req)
}

// Close stops all background file watchers associated with this server's
// sessions and halts the implicit background index watch, draining an
// in-flight rebuild for up to 5s. It is safe to call multiple times, and
// tolerates sessions already closed by LRU eviction (project.Session.Close
// is itself documented safe to call multiple times).
func (s *Server) Close() {
	// Refuse new preloads and drain running ones BEFORE closing sessions: a
	// preload still inside Index() would otherwise start a background save
	// after the session's saveWG was already waited on, or re-create a
	// session (and its watcher) after cleanup. index.Build takes no context,
	// so the drain is bounded; a build that outlives it is abandoned and
	// ends with the process.
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	preloadsDone := make(chan struct{})
	go func() {
		s.preloadWG.Wait()
		close(preloadsDone)
	}()
	select {
	case <-preloadsDone:
	case <-time.After(watchShutdownTimeout):
		fmt.Fprintln(os.Stderr, "kern-mcp: shutdown: a background index preload is still running; abandoning it")
	}
	// Close every cached session and drain the LRU: the sessions' background
	// index saves (saveWG) and file watchers are stopped here.
	s.sessCache.CloseAll()
	// Unregister every host sampler slot: no LLM call may delegate to a host
	// that is shutting down (registrations are effects — dispose them).
	s.samplingMu.Lock()
	for _, d := range s.samplingSlots {
		d()
	}
	s.samplingSlots = map[string]func(){}
	s.samplingHost = false
	s.samplingMu.Unlock()
	// Stop the background index watch: no rebuild may start after this point,
	// and an in-flight rebuild is drained briefly (see stopWatch).
	s.stopWatch()
	// Stop the ADR-0011 reactive file watcher: cancel the poll loops and wait
	// briefly for an in-flight rebuild to drain (same budget as the
	// background watch). A server that never started the watcher (env
	// disabled) has a nil cancel and skips this.
	s.mu.Lock()
	watcherCancel, watcherDone := s.watcherCancel, s.watcherDone
	s.mu.Unlock()
	if watcherCancel != nil {
		watcherCancel()
		select {
		case <-watcherDone:
		case <-time.After(watchShutdownTimeout):
		}
	}
}
