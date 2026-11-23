package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/mcpcatalog"
	"os"
	"sync"
	"time"
)

func (s *Server) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err = s.out.Write(data); err != nil {
		return err
	}
	_, err = s.out.Write([]byte{'\n'})
	return err
}

// progress sends a notifications/progress message for a tool call. Notifications
// are only pushed on transports that can deliver them (stdio); HTTP answers
// each request with a single response body and has no push channel (SSE is
// not supported). ctx guards against emitting progress after the request was
// cancelled or answered.
//
// pct < 0 is a KEEPALIVE marker (L-batch): the numeric progress fields are
// omitted so the notification carries only the message. The MCP spec's
// progress notification requires progress to be within [0, total]; a keepalive
// has no progress to report, and sending -1 (the previous behavior) emitted a
// spec-invalid percentage that strict clients reject.
func (s *Server) progress(ctx context.Context, token, tool string, pct int, msg string) {
	if s.transport != "stdio" {
		return
	}
	// MCP spec: the server must only emit progress when the client supplied a
	// progressToken in the request's _meta. token == "" means the client did
	// not opt in; skip the write entirely (defense in depth — the runTool
	// gate already prevents calls without a token).
	if token == "" {
		return
	}
	if ctx != nil && ctx.Err() != nil {
		return
	}
	params := map[string]any{
		"progressToken": token,
		"message":       msg,
	}
	if pct >= 0 {
		params["progress"] = pct
		params["total"] = 100
	}
	n := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/progress",
		"params":  params,
	}
	// A failed progress write must be surfaced: the client's "still
	// running" ticker stalls silently otherwise. The call itself is
	// unaffected — progress is best-effort liveness, not the answer.
	if err := s.write(n); err != nil {
		fmt.Fprintf(os.Stderr, "kern-mcp: progress notification write failed: %v\n", err)
	}
}

// startProgress emits an initial 0% notification for a slow tool and returns a
// stop func that emits 100% and halts the background ticker. stop blocks until
// the ticker goroutine has exited, so a stale "still running" emission can
// never arrive after "finished"; every emission is gated on ctx.Err() and the
// writer mutex, so a progress message can never arrive after the final
// response.
// slowTools are the tools whose dispatch can take seconds to minutes; they
// emit MCP progress notifications so agents see liveness instead of
// silence during multi-second tool calls. The "slow" flag lives on the
// per-tool catalog table (catalog.Tool.Slow in internal/mcpcatalog/tools.go)
// — slowness is declared next to each tool definition (index/build/scan,
// sandbox, heal, validate, refactor, repair), not in a drifting side map —
// and this set is derived from it. Fast lookups (search, explore, graph,
// context) emit no progress and stay silent. Tests may replace the whole map
// to exercise the central runTool wrap. LLM-class tools (kern_analyze/
// kern_plan) are deliberately NOT slow-flagged: their responses are captured
// byte-exact by cross-interface tests, and the LLM legs have their own
// latency story.
var slowTools = func() map[string]bool {
	m := make(map[string]bool, 16)
	for _, t := range catalog.All {
		if t.Slow {
			m[t.Name] = true
		}
	}
	return m
}()

// durationFooterThreshold gates the per-call "[kern] took Xs" result footer:
// fast calls stay byte-identical to today (parity tests capture exact
// outputs), only multi-second calls gain the line. A var so tests can lower
// it without sleeping real seconds.
var durationFooterThreshold = 2 * time.Second

// appendDurationFooter appends the model-visible latency line to a tool
// result (duration-visibility L1): the tool-call duration is otherwise shown
// only by host UIs from their own part timestamps, which the model never
// sees — and some hosts (opencode) discard progress-notification content.
// One line, last position, threshold-gated; empty text and short calls are
// returned unchanged.
func appendDurationFooter(text string, took time.Duration) string {
	if text == "" || took <= durationFooterThreshold {
		return text
	}
	return fmt.Sprintf("%s\n[kern] took %.1fs", text, took.Seconds())
}

func (s *Server) startProgress(ctx context.Context, id, token, tool string) func() {
	done := make(chan struct{})
	var wg sync.WaitGroup
	s.progress(ctx, token, tool, 0, tool+" running")
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				s.progress(ctx, token, tool, -1, tool+" still running")
			}
		}
	}()
	return func() {
		close(done)
		wg.Wait()
		s.progress(ctx, token, tool, 100, tool+" finished")
	}
}

// cancelAll cancels every in-flight tool call and releases every lock held by
// this server. It is invoked on graceful shutdown (SIGINT/SIGTERM) so slow
// tools stop promptly and the workspace is left unlocked. The inflight map is
// deliberately NOT cleared here: each running tool goroutine removes its own
// entry (unregisterInflight) once it finishes, so Inflight() keeps reporting
// still-running tools and the shutdown drain can wait for them to exit.
func (s *Server) cancelAll() {
	s.mu.Lock()
	for _, cancel := range s.inflight {
		cancel()
	}
	var held []*lock.Lock
	for _, lk := range s.locks {
		held = append(held, lk)
	}
	s.locks = map[string]*lock.Lock{}
	s.mu.Unlock()
	for _, lk := range held {
		_ = lk.Release()
	}
}

// CancelAll aborts in-flight tools and releases held locks. Safe to call from
// a signal handler or after Serve returns.
func (s *Server) CancelAll() { s.cancelAll() }

// Inflight returns the number of tool calls currently registered as
// in-flight. Graceful shutdown polls it after CancelAll so it can wait for
// cancelled tools to drain their responses before exiting.
func (s *Server) Inflight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inflight)
}

// registerInflight stores the cancel func for a request id so $/cancelRequest
// and graceful shutdown can abort it.
func (s *Server) registerInflight(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	if s.inflight == nil {
		s.inflight = map[string]context.CancelFunc{}
	}
	s.inflight[id] = cancel
	s.mu.Unlock()
}

// unregisterInflight removes the cancel func once the tool call has finished.
func (s *Server) unregisterInflight(id string) {
	s.mu.Lock()
	delete(s.inflight, id)
	s.mu.Unlock()
}

// Serve runs until the stream ends.
// startPreload fires the background index preload exactly once per server
// lifetime (indexOnce). It is called at serve start so a fresh root's build
// is underway before the client connects — the cold-start head start (MCP
// audit 2026-10-25 R6) — and again from dispatch as a fallback for serve
// paths that do not call it; the Once makes the later call a no-op. The
// goroutine keeps setup instant.
