// Package mcp implements a minimal Model Context Protocol server over stdio.
// It is deliberately dependency-free so the binary stays offline and static.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/metrics"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/project"
	"github.com/JayveerPrajapati/kern/internal/stats"
	"github.com/JayveerPrajapati/kern/internal/strutil"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
	"github.com/JayveerPrajapati/kern/internal/verify"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	protocolVersion = "2025-06-18"
	serverName      = "kern"
)

// serverVersion is stamped at build time via -ldflags "-X main.version=...";
// the binary entry points forward it through SetServerVersion. Defaults to
// "dev" when built without ldflags so initialize still reports something sane.
var serverVersion = "dev"

// SetServerVersion overrides the version reported in the initialize response.
// The CLI entry points call it with their ldflags-stamped main.version.
func SetServerVersion(v string) {
	if v != "" {
		serverVersion = v
	}
}

// Tool is an MCP tool definition.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// Phase tags the tool with the agent phase it belongs to (explore, plan,
	// edit or verify). "meta" (kern_meta itself) and "cross" (phase-agnostic
	// utilities) are always advertised regardless of the active phase; an
	// empty phase means the tool is not phase-filtered.
	Phase string `json:"phase,omitempty"`
}

// Agent phases for phase-aware tool routing (P1.2). Each phase exposes a
// focused shortlist of tools instead of the full catalog; kern_meta routes
// within whatever tools are available. Tools tagged PhaseMeta or PhaseCross
// are always advertised regardless of the active KERN_MCP_PHASE.
const (
	PhaseExplore = "explore"
	PhasePlan    = "plan"
	PhaseEdit    = "edit"
	PhaseVerify  = "verify"
	PhaseMeta    = "meta"
	PhaseCross   = "cross"
)

// NOTE: KERN_MCP_PHASE filters tool ADVERTISEMENT only (tools/list responses),
// not tool EXECUTION (tools/call). A client that knows a tool name can call it
// directly regardless of the phase setting. This is by design so kern_meta can
// route to unadvertised sub-tools. KERN_MCP_PHASE is NOT a security boundary —
// for real per-phase tool restriction, use the KERN_TOOLS allowlist.

// ToolNames returns every registered MCP tool name.
func ToolNames() []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

// parseAllowlist reads the KERN_TOOLS allowlist from the environment. A
// comma-separated list restricts which tools the server exposes and executes;
// unset or empty means everything is allowed. The result is parsed once at
// server construction and cached on the Server, not re-read on every dispatch.
func parseAllowlist() []string {
	v := strings.TrimSpace(os.Getenv("KERN_TOOLS"))
	if v == "" {
		return nil
	}
	var out []string
	for _, n := range strings.Split(v, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// highLevelOnly reports whether to expose only high-level tools (per the MCP
// spec) via KERN_MCP_HIGH_LEVEL_ONLY=1; otherwise all tools are registered.
func highLevelOnly() bool {
	return os.Getenv("KERN_MCP_HIGH_LEVEL_ONLY") == "1"
}

// singleTool reports whether to expose only the kern meta-tool via
// KERN_MCP_SINGLE_TOOL=1. Agents that find the tool catalog overwhelming
// can point their MCP config at this mode and interact with kern through the
// single natural-language `kern` entry point.
func singleTool() bool {
	return os.Getenv("KERN_MCP_SINGLE_TOOL") == "1"
}

// fullCatalog reports whether to advertise the full tool catalog via
// KERN_MCP_FULL=1. By default only the minimal defaultTools surface is
// advertised; this opts back in to the full 86-tool catalog for power users
// and direct sub-tool callers. Phase-aware routing (KERN_MCP_PHASE) still
// filters the advertised list within the full catalog.
func fullCatalog() bool {
	return os.Getenv("KERN_MCP_FULL") == "1"
}

// mcpPhase returns the active agent phase from KERN_MCP_PHASE. An unset or
// invalid value returns "" which means no phase filtering: the whole tier
// surface (default/high-level/full) is advertised as before.
func mcpPhase() string {
	p := strings.ToLower(strings.TrimSpace(os.Getenv("KERN_MCP_PHASE")))
	if !validPhase(p) {
		return ""
	}
	return p
}

// validPhase reports whether p is one of the four agent phases.
func validPhase(p string) bool {
	switch p {
	case PhaseExplore, PhasePlan, PhaseEdit, PhaseVerify:
		return true
	}
	return false
}

// phaseToolAllowed reports whether a tool should be advertised for the active
// phase. Tools tagged "meta" or "cross" are always available; otherwise the
// tool's phase must equal the active phase. An empty phase allows everything.
func phaseToolAllowed(t Tool, phase string) bool {
	if phase == "" {
		return true
	}
	switch t.Phase {
	case PhaseMeta, PhaseCross:
		return true
	}
	return t.Phase == phase
}

// toolAllowed reports whether name passes the KERN_TOOLS allowlist. toolsList
// is the (already filtered or full) registered catalog; a nil/empty allowlist
// allows everything, otherwise name must appear in both the allowlist and
// the catalog. The allowlist slice is passed in (already cached on the
// Server) rather than re-read from the environment on every call.
func toolAllowed(toolsList []Tool, allowed []string, name string) bool {
	if len(allowed) == 0 {
		return true
	}
	inCatalog := false
	for _, t := range toolsList {
		if t.Name == name {
			inCatalog = true
			break
		}
	}
	if !inCatalog {
		return false
	}
	for _, a := range allowed {
		if a == name {
			return true
		}
	}
	return false
}

// highLevelTools is the set of tools kept when KERN_MCP_HIGH_LEVEL_ONLY=1.
// It includes the 5 high-level orchestration tools (kern_analyze, kern_plan,
// kern_execute, kern_verify, kern_incident) plus a minimal set of essential
// primitives that high-level agents still need.
var highLevelTools = map[string]bool{
	"kern_analyze":         true,
	"kern_plan":            true,
	"kern_execute":         true,
	"kern_verify":          true,
	"kern_incident":        true,
	"kern_what_if":         true,
	"kern_impact":          true,
	"kern_search":          true,
	"kern_context":         true,
	"kern_explore":         true,
	"kern_graph":           true,
	"kern_memory_add":      true,
	"kern_memory_list":     true,
	"kern_memory_recall":   true,
	"kern_memory":          true,
	"kern_review":          true,
	"kern_security":        true,
	"kern_validate":        true,
	"kern_run_build":       true,
	"kern_exec":            true,
	"kern_sandbox":         true,
	"kern_commitmsg":       true,
	"kern_pack":            true,
	"kern_project_map":     true,
	"kern_compact_file":    true,
	"kern_buddy":           true,
	"kern_usage_guide":     true,
	"kern_mask_pii":        true,
	"kern_optimize_prompt": true,
	"kern_optimize_log":    true,
	"kern_doc_search":      true,
	"kern_doc_fetch":       true,
	"kern_doc_index":       true,
	"kern_context_budget":  true,
	"kern_swap":            true,
	"kern_verify_output":   true,
	"kern_check_draft":     true,
	"kern_taint":           true,
	"kern_schema_validate": true,
	"kern_stats":           true,
}

// defaultTools is the minimal surface advertised by default. The full
// 86-tool catalog is gated behind KERN_MCP_FULL=1, and phase-aware routing
// (KERN_MCP_PHASE) filters either surface down to the active phase's
// shortlist. kern_meta's NL router
// still reaches every sub-tool handler internally regardless of what is
// advertised, so no capability is lost — only the advertised surface
// shrinks. This implements the MCP spec's "high-level tools, not dozens
// of tiny low-value tools" guidance.
var defaultTools = map[string]bool{
	"kern_meta":              true, // NL router → all sub-tools
	"kern_explore":           true, // symbol source + callers/callees + blast radius
	"kern_impact":            true, // blast radius of a change
	"kern_review":            true, // token-optimised review context
	"kern_search":            true, // ranked symbol search
	"kern_context":           true, // minimal source slice
	"kern_optimize_prompt":   true, // compress prompts
	"kern_plan":              true, // implementation plan
	"kern_verify":            true, // unified verification
	"kern_run":               true, // orchestrate a whole task
	"kern_authorize_context": true, // authorized-context primitive (P0.1)
}

// filteredTools returns the registered tools minus any excluded by the
// KERN_TOOLS allowlist, intersected with the active agent phase from
// KERN_MCP_PHASE. By default only the minimal defaultTools surface is
// advertised; KERN_MCP_FULL=1 opts back in to the full catalog, the legacy
// KERN_MCP_HIGH_LEVEL_ONLY mode keeps the mid-size highLevelTools set for
// backward compat, and KERN_MCP_SINGLE_TOOL=1 collapses to kern_meta alone.
// Phase-aware routing (KERN_MCP_PHASE=explore|plan|edit|verify) keeps only
// the active phase's tools plus the always-on meta/cross tools; an unset or
// invalid phase advertises the whole tier surface. It lazily reads the env
// once per server lifetime and caches the result.
func (s *Server) filteredTools() []Tool {
	s.toolsMu.Lock()
	defer s.toolsMu.Unlock()
	if s.filtered != nil {
		return s.filtered
	}
	if singleTool() {
		for _, t := range tools {
			if t.Name == "kern_meta" {
				s.filtered = []Tool{t}
				return s.filtered
			}
		}
	}
	allowed := s.allowlist
	// KERN_MCP_FULL=1 → advertise the full catalog (with KERN_TOOLS filter).
	if fullCatalog() {
		out := make([]Tool, 0, len(tools))
		for _, t := range tools {
			if len(allowed) > 0 {
				in := false
				for _, a := range allowed {
					if a == t.Name {
						in = true
						break
					}
				}
				if !in {
					continue
				}
			}
			if !phaseToolAllowed(t, mcpPhase()) {
				continue
			}
			out = append(out, t)
		}
		s.filtered = out
		return s.filtered
	}
	// KERN_MCP_HIGH_LEVEL_ONLY=1 → the legacy 38-tool middle set (deprecated;
	// prefer the default minimal set or KERN_MCP_FULL). Otherwise the NEW
	// DEFAULT: the minimal 11-tool defaultTools surface.
	var keep map[string]bool
	if highLevelOnly() {
		keep = highLevelTools
	} else {
		keep = defaultTools
	}
	out := make([]Tool, 0, len(keep))
	for _, t := range tools {
		if !keep[t.Name] {
			continue
		}
		if len(allowed) > 0 {
			in := false
			for _, a := range allowed {
				if a == t.Name {
					in = true
					break
				}
			}
			if !in {
				continue
			}
		}
		if !phaseToolAllowed(t, mcpPhase()) {
			continue
		}
		out = append(out, t)
	}
	s.filtered = out
	return s.filtered
}

func schema(props map[string]any, required []string) map[string]any {
	s := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// Server handles MCP requests over a stdio stream or HTTP.
type Server struct {
	in        io.Reader // raw stdio reader, used to rebuild the scanner after an oversized line
	out       io.Writer
	mu        sync.Mutex
	toolsMu   sync.Mutex
	filtered  []Tool   // cached KERN_TOOLS-filtered tool list (nil = not computed)
	allowlist []string // parsed KERN_TOOLS allowlist, cached once at init (nil = allow all)
	locks     map[string]*lock.Lock
	inflight  map[string]context.CancelFunc
	sessions  map[string]*project.Session
	// platforms caches one application Platform per project root, keyed to
	// the exact index instance it was built from. High-level handlers used to
	// rebuild the whole Platform (call graph + 4 twin extractors, each a full
	// tree walk) on every tool call; the cache reuses it while the session
	// serves the same index instance and rebuilds only after a real index
	// rebuild (new instance pointer).
	platforms map[string]*platformEntry
	transport string // "stdio" (default) or "http"
	// sem bounds how many tool calls may build an index concurrently (each
	// call can construct a full project index). Acquired before a tools/call
	// goroutine is spawned and released when it finishes.
	sem chan struct{}
	// roots confine every tool root/dir argument; KERN_ROOTS when set, else
	// the server's startup directory.
	roots []string
	// gate confines every tool call's path-typed arguments (root, dir, or any
	// key containing "path") to the KERN_MCP_ROOTS roots, resolving symlinks
	// before containment. A nil gate preserves the default behavior exactly.
	gate *Gate
	// commits caches the short HEAD commit per project root so git is spawned
	// at most once per root per server lifetime.
	commits map[string]string
	// indexOnce defers background index preloading until the first MCP
	// request (initialize or tools/call) instead of server startup, so
	// setup is instant and the index cost is paid while the user waits.
	indexOnce sync.Once
	// indexedRoots records which project roots have completed at least one
	// index build this process, so the "first build in progress" notice is
	// printed once per root instead of on every stale rebuild.
	indexedRoots sync.Map
	// audit records every executed (and pre-dispatch-rejected) MCP tool
	// call into the project's tamper-evident audit chain (G-2); auditMu
	// guards toolAudit's lazy initialization.
	audit   *governance.AuditLog
	auditMu sync.Mutex
	// preTool is an optional hook invoked before every tools/call execution.
	// It receives the tool name and its arguments and returns nil to allow the
	// call or an error to deny it (denial surfaces as a tool error response,
	// isError=true, no side effects). A nil hook preserves the default
	// behavior exactly — callers that never set it see zero change.
	preTool func(name string, args map[string]any) error
}

// WithPreToolHook registers a pre-tool-use hook. NewServer wires the
// KERN_MCP_ROOTS confinement gate as the default hook (opt out via
// KERN_MCP_NO_CONFINE=1); calling WithPreToolHook replaces that default with
// the caller's own governance/allowlist/accounting hook. A nil hook leaves
// every tools/call untouched, so callers that explicitly pass nil get the
// fully default behavior.
func (s *Server) WithPreToolHook(fn func(name string, args map[string]any) error) *Server {
	s.preTool = fn
	return s
}

// defaultConcurrency returns the worker concurrency limit for the server.
// Configurable via KERN_MCP_CONCURRENCY (default 32, minimum 8).
func defaultConcurrency() int {
	if v := os.Getenv("KERN_MCP_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 8 {
			return n
		}
	}
	return 32
}

// NewServer returns a *Server wired to the given reader/writer.
func NewServer(in io.Reader, out io.Writer) *Server {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<20), 64<<20)
	s := &Server{in: in, out: out, sem: make(chan struct{}, defaultConcurrency()), locks: map[string]*lock.Lock{}, inflight: map[string]context.CancelFunc{}, sessions: map[string]*project.Session{}, transport: "stdio", roots: defaultWorkspaceRoots(), gate: confinementGate(), commits: map[string]string{}, allowlist: parseAllowlist()}
	// P0.1: register the built-in default agent so calls without an explicit
	// agent_id are governed (cwd-scoped) instead of raw. KERN_MCP_PERMISSIVE=1
	// remains the explicit opt-out that restores raw mode.
	governance.EnsureDefaultAgent()
	// Confinement is default-on: the KERN_MCP_ROOTS gate runs as the
	// pre-tool-use hook, so a tool call whose path-typed arguments resolve
	// outside the allowed roots is denied before any handler side effect runs.
	// KERN_MCP_NO_CONFINE=1 opts out entirely. With no KERN_MCP_ROOTS the gate
	// fails closed to the process cwd; KERN_MCP_PERMISSIVE=1 restores the old
	// allow-all loopback-client-trust behavior.
	if s.gate != nil {
		s.preTool = s.gate.Check
	}
	return s
}

// confinementGate builds the KERN_MCP_ROOTS confinement gate, or nil when
// KERN_MCP_NO_CONFINE=1 opts out of confinement. A nil gate allows every
// call; a non-nil gate is enabled unless KERN_MCP_PERMISSIVE=1 opts out, and
// defaults its roots to the process cwd when KERN_MCP_ROOTS is unset.
func confinementGate() *Gate {
	if os.Getenv("KERN_MCP_NO_CONFINE") == "1" {
		return nil
	}
	return NewGateFromEnv()
}

// defaultWorkspaceRoots returns the roots tools may target: KERN_ROOTS when
// set, else the startup directory. Every tool root/dir is confined to these.
func defaultWorkspaceRoots() []string {
	var roots []string
	if env := os.Getenv("KERN_ROOTS"); env != "" {
		for _, r := range strings.FieldsFunc(env, func(r rune) bool { return r == ':' || r == ',' }) {
			r = strings.TrimSpace(r)
			if r != "" {
				roots = append(roots, resolveAbs(r))
			}
		}
	}
	if len(roots) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			roots = []string{resolveAbs(cwd)}
		}
	}
	return roots
}

// resolveAbs cleans p to an absolute path.
func resolveAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

// checkRootArg rejects any non-empty root/dir tool argument that resolves
// outside the server's workspace roots. Called once per tool call before
// dispatch so confinement cannot be forgotten for a new tool.
func (s *Server) checkRootArg(args map[string]any) error {
	for _, key := range []string{"root", "dir"} {
		v := argString(args, key)
		if v == "" {
			continue
		}
		if err := s.checkWithinWorkspace(v); err != nil {
			return fmt.Errorf("%s %q: %w", key, v, err)
		}
	}
	return nil
}

// checkWithinWorkspace reports whether p (absolute or relative) resolves
// inside one of the workspace roots, following symlinks. The resolved target
// must be the root itself or a descendant; a symlink pointing outside is
// rejected even though its text lives inside.
func (s *Server) checkWithinWorkspace(p string) error {
	real, err := realPath(p)
	if err != nil {
		return err
	}
	for _, r := range s.roots {
		rr, err := realPath(r)
		if err != nil {
			return err
		}
		if real == rr || within(rr, real) {
			return nil
		}
	}
	var roots []string
	for _, r := range s.roots {
		roots = append(roots, resolveAbs(r))
	}
	return fmt.Errorf("outside the allowed workspace (roots: %s)", strings.Join(roots, ", "))
}

// realPath resolves p to an absolute, symlink-resolved path. For paths that do
// not exist yet it resolves the nearest existing ancestor and re-appends the
// remaining components.
func realPath(p string) (string, error) {
	abs := resolveAbs(p)
	var rem []string
	probe := abs
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if len(rem) == 0 {
				return real, nil
			}
			return filepath.Join(append([]string{real}, rem...)...), nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return abs, fmt.Errorf("cannot resolve %q", p)
		}
		rem = append([]string{filepath.Base(probe)}, rem...)
		probe = parent
	}
}

// within reports whether child is parent or a descendant of parent. Unlike
// verify.WithinAbs it also rejects an absolute rel path (e.g. a different
// drive root on Windows).
func within(parent, child string) bool {
	if !verify.WithinAbs(parent, child) {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return !filepath.IsAbs(rel)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// isNotification reports whether the request is a JSON-RPC notification: it
// has no id, so the client expects no response.
func (r rpcRequest) isNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

func (s *Server) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.out.Write(append(data, '\n'))
	return err
}

// progress sends a notifications/progress message for a tool call. Notifications
// are only pushed on transports that can deliver them (stdio); HTTP answers
// each request with a single response body and has no push channel (SSE is
// not supported). ctx guards against emitting progress after the request was
// cancelled or answered.
func (s *Server) progress(ctx context.Context, id string, tool string, pct int, msg string) {
	if s.transport != "stdio" {
		return
	}
	if ctx != nil && ctx.Err() != nil {
		return
	}
	n := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/progress",
		"params": map[string]any{
			"progressToken": id,
			"progress":      pct,
			"total":         100,
			"message":       msg,
		},
	}
	_ = s.write(n)
}

// startProgress emits an initial 0% notification for a slow tool and returns a
// stop func that emits 100% and halts the background ticker. stop blocks until
// the ticker goroutine has exited, so a stale "still running" emission can
// never arrive after "finished"; every emission is gated on ctx.Err() and the
// writer mutex, so a progress message can never arrive after the final
// response.
func (s *Server) startProgress(ctx context.Context, id, tool string) func() {
	done := make(chan struct{})
	var wg sync.WaitGroup
	s.progress(ctx, id, tool, 0, tool+" running")
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
				s.progress(ctx, id, tool, -1, tool+" still running")
			}
		}
	}()
	return func() {
		close(done)
		wg.Wait()
		s.progress(ctx, id, tool, 100, tool+" finished")
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
// preloadIndexes builds and caches each workspace root's index in the
// background so a later tool call reuses it instead of blocking on a cold
// build. It is triggered lazily on the first MCP request (see indexOnce) and
// runs in a goroutine so setup never blocks on it. Skipped for filesystem
// roots and when KERN_PRELOAD=0 (test/CI guard).
func (s *Server) preloadIndexes() {
	if os.Getenv("KERN_PRELOAD") == "0" {
		return
	}
	for _, r := range s.workspaceRoots() {
		if isFilesystemRoot(r) {
			continue
		}
		go func(root string) {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "kern-mcp: preload index %s: panic recovered: %v\n", root, r)
				}
			}()
			_, err := s.sessionFor(root).Index()
			if err != nil {
				fmt.Fprintf(os.Stderr, "kern-mcp: preload index %s: %v\n", root, err)
				return
			}
			// Record the build so a later graph tool call that reuses this
			// warm index does not print the "first build in progress" notice.
			s.indexedRoots.Store(root, true)
		}(r)
	}
}

// isFilesystemRoot reports whether p is a filesystem root ("/" on Unix, or a
// drive root on Windows) that must never be treated as a project to index.
func isFilesystemRoot(p string) bool {
	vol := filepath.VolumeName(p)
	return filepath.Clean(p) == filepath.Clean(vol+string(filepath.Separator))
}

// workspaceRoots returns the roots field, falling back to the default when
// unset (defensive; NewServer always initializes it).
func (s *Server) workspaceRoots() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.roots) == 0 {
		return defaultWorkspaceRoots()
	}
	out := append([]string(nil), s.roots...)
	return out
}

func (s *Server) Serve() error {
	if s.sem == nil {
		s.sem = make(chan struct{}, defaultConcurrency())
	}
	var wg sync.WaitGroup
	defer wg.Wait() // drain in-flight tool calls before returning on EOF
	// newScanner rebuilds the stdio line scanner. A scanner cannot be reused
	// after it hits bufio.ErrTooLong, so it is recreated from the raw reader.
	newScanner := func() *bufio.Scanner {
		sc := bufio.NewScanner(s.in)
		sc.Buffer(make([]byte, 64<<20), 64<<20)
		return sc
	}
	for {
		sc := newScanner()
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			var req rpcRequest
			if err := json.Unmarshal(line, &req); err != nil {
				// A malformed line is a protocol error, not a silent drop.
				if err := s.write(errorResponse(nil, -32700, "parse error: "+err.Error())); err != nil {
					return err
				}
				continue
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
		// A single oversized message (>64MB) must not kill the server: the
		// scanner hit bufio.ErrTooLong, so skip the oversized token (in 64MB
		// chunks until its newline passes), recreate the scanner and keep
		// serving instead of terminating on the error.
		if sc.Err() == bufio.ErrTooLong {
			fmt.Fprintf(os.Stderr, "kern-mcp: skipping oversized input line (> %d bytes); continuing\n", 64<<20)
			continue
		}
		return sc.Err()
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
// sessions. It is safe to call multiple times.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sessions {
		sess.Close()
	}
}

// dispatch computes the JSON-RPC response for a request. A nil return means
// the request needs no response (e.g. a notification). The response is a
// transport-neutral object so both stdio and HTTP can send it.
func (s *Server) dispatch(req rpcRequest) any {
	// KERN_MCP_ROOTS confinement gate (memory-#31): every tools/call whose
	// path-typed arguments (root, dir, or any key containing "path") resolve
	// outside the allowed roots is rejected here, before any handler runs.
	// The rejection uses the same result shape a handler error produces — a
	// tool result with isError=true — so the client sees a clean tool error
	// rather than a panic or a JSON-RPC error. A nil or disabled gate is a
	// no-op, keeping the default (loopback-client trust) behavior identical.
	if s.gate != nil && s.gate.enabled && req.Method == "tools/call" {
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if json.Unmarshal(req.Params, &p) == nil {
			if err := s.gate.Check(p.Name, p.Arguments); err != nil {
				return map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"result": map[string]any{
						"content": []any{map[string]any{"type": "text", "text": "pre-tool-use denied: " + err.Error()}},
						"isError": true,
					},
				}
			}
		}
	}
	// $/cancelRequest is a notification per JSON-RPC (no response expected),
	// but must still be processed: dispatch returns nil for notifications
	// below, so it is handled here before the short-circuit.
	if req.Method == "$/cancelRequest" {
		var p struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(req.Params, &p)
		key := idKey(p.ID)
		s.mu.Lock()
		cancel, ok := s.inflight[key]
		s.mu.Unlock()
		if ok && cancel != nil {
			cancel()
		}
		if req.isNotification() {
			return nil
		}
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}}
	}
	if req.isNotification() {
		return nil
	}
	switch req.Method {
	case "initialize":
		// Kick off background indexing on the first connection so setup stays
		// instant: graph tools block until the build finishes, while tools
		// that don't need the index serve immediately. indexOnce guards the
		// trigger so it fires exactly once per server lifetime.
		s.indexOnce.Do(func() { go s.preloadIndexes() })
		caps := map[string]any{
			"tools":   map[string]any{"listChanged": false},
			"prompts": map[string]any{"listChanged": false},
		}
		if s.transport == "http" {
			caps["streamableHttpCapabilities"] = map[string]any{"sse": false}
		}
		// Echo the client's negotiated protocol version when it is one we
		// support; otherwise report the version we implement.
		version := protocolVersion
		var initParams struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(req.Params, &initParams) == nil && supportedProtocolVersions[initParams.ProtocolVersion] {
			version = initParams.ProtocolVersion
		}
		return map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{
				"protocolVersion": version,
				"capabilities":    caps,
				"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
			},
		}
	case "notifications/initialized":
		return nil
	case "ping":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}}
	case "tools/list":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": s.filteredTools()}}
	case "tools/call":
		return s.toolCallResponse(req.ID, req.Params)
	case "prompts/list":
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"prompts": prompts}}
	case "prompts/get":
		return s.promptGetResponse(req.ID, req.Params)
	default:
		return errorResponse(req.ID, -32601, "method not found: "+req.Method)
	}
}

func errorResponse(id json.RawMessage, code int, msg string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": msg},
	}
}

// idKey canonicalizes a JSON-RPC id into the map key used to track in-flight
// requests, so tools/call and $/cancelRequest agree on the same key whether
// the client used a JSON number (77) or string ("77") id. A raw id that does
// not parse is used verbatim.
func idKey(id json.RawMessage) string {
	var v any
	if err := json.Unmarshal(id, &v); err != nil || v == nil {
		return string(id)
	}
	switch t := v.(type) {
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case string:
		return t
	default:
		return fmt.Sprintf("%v", v)
	}
}
func (s *Server) toolCallResponse(id json.RawMessage, params json.RawMessage) any {
	// First real use: start background indexing (no-op if initialize already
	// triggered it — indexOnce fires exactly once per server lifetime).
	s.indexOnce.Do(func() { go s.preloadIndexes() })
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResponse(id, -32602, "invalid params")
	}
	key := idKey(id)
	// Pre-tool-use hook: deny the call before any side effect runs. The hook
	// is optional (nil = no-op) and returns nil to allow, or an error to
	// reject — rejection is reported as a tool error (isError=true) so the
	// agent sees why the call was blocked.
	if s.preTool != nil {
		if err := s.preTool(p.Name, p.Arguments); err != nil {
			return map[string]any{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]any{
					"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("pre-tool-use denied: %s", err)}},
					"isError": true,
				},
			}
		}
	}
	// A generous 30-minute per-call ceiling so a hung subprocess (an
	// unresponsive Ollama during plan/analyze, or a slow index build) can
	// never wedge the server goroutine forever; long legitimate operations
	// (kern_execute sandbox builds, kern_verify full suites) run within it.
	// This is the effective cap for plugin-driven calls: it must be >= the
	// plugin MAX_CEILING_MS (kern.ts) so the agent's requested timeout
	// governs. The exec-family handlers install their own shorter deadlines
	// on top.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	// The per-call scope carries the index loaded during this tool's execution
	// so provenance is stamped from this call's index, never another's. It is
	// scoped here instead of on the Server struct to avoid cross-talk between
	// concurrent tool calls.
	scope := &indexScope{}
	ctx = context.WithValue(ctx, indexScopeKey{}, scope)
	s.registerInflight(key, cancel)
	defer func() {
		cancel()
		s.unregisterInflight(key)
	}()
	text, err := func() (out string, runErr error) {
		defer func() {
			if rec := recover(); rec != nil {
				runErr = fmt.Errorf("panic in tool %s: %v", p.Name, rec)
				fmt.Fprintf(os.Stderr, "kern-mcp: panic running %s: %v\n%s\n", p.Name, rec, debug.Stack())
			}
		}()
		return s.runTool(ctx, key, p.Name, p.Arguments)
	}()
	// Cap every tool response at the output budget so a large result cannot
	// flood the agent's context. Overridable per call with max_output=N.
	if err == nil {
		var budget int
		budget, err = callOutputBudget(p.Arguments)
		if err == nil {
			text = sandboxOutput(text, budget, p.Name)
		}
	}
	result := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": false,
	}
	// Structured provenance (P1.2): retrieval handlers stamp it on the
	// per-call scope; any other tool that loaded an index gets
	// index-identity-only raw provenance. The one-line summary appended to
	// the content text is derived from the same structured field, so there
	// is a single source of truth for index evidence.
	if scope.prov == nil && scope.ix != nil {
		scope.prov = s.rawProvenance(scope.ix, nil)
	}
	if err != nil {
		if scope.prov != nil {
			// Errors can carry provenance too: governed denials attach the
			// auditable authorizing rule alongside the error text.
			text = err.Error() + "\n" + s.provenanceSummary(scope.ix, scope.prov)
			result["provenance"] = scope.prov
		} else {
			text = err.Error()
		}
		result["content"] = []any{map[string]any{"type": "text", "text": text}}
		result["isError"] = true
	} else if scope.prov != nil {
		result["provenance"] = scope.prov
		result["content"] = []any{map[string]any{"type": "text", "text": text + "\n" + s.provenanceSummary(scope.ix, scope.prov)}}
	}
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

// defaultOutputBudget is the MCP output sandbox cap in bytes, used when the
// agent does not pass max_output= and KERN_MCP_MAX_OUTPUT is unset. ~6K tokens
// of safety net for a single tool result.
const defaultOutputBudget = 24 << 10

// outputBudget resolves the global cap from KERN_MCP_MAX_OUTPUT (bytes).
func outputBudget() int {
	if v := os.Getenv("KERN_MCP_MAX_OUTPUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultOutputBudget
}

// callOutputBudget returns the per-call budget: an explicit max_output=N
// argument (bytes; 0 disables the sandbox) wins over the global cap. A
// malformed max_output is an error, not a silent fallback.
func callOutputBudget(args map[string]any) (int, error) {
	if v := argString(args, "max_output"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("max_output: invalid integer %q", v)
		}
		if n <= 0 {
			return 0, nil // disabled for this call
		}
		return n, nil
	}
	return outputBudget(), nil
}

// sandboxOutput truncates text to budget bytes (when budget > 0) and stamps a
// marker with before/after token counts and a tool-specific recovery hint. The
// marker doubles as the anti-context-flood boundary: an agent that needs more
// can re-call with a larger max_output or a narrower tool.
func sandboxOutput(text string, budget int, tool string) string {
	if budget <= 0 || len(text) <= budget {
		return text
	}
	// Trim to a rune-safe boundary: slicing mid-multi-byte-rune would leave a
	// dangling UTF-8 sequence that corrupts the marker's own token counts and
	// any downstream tokenizer.
	cut := budget
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + fmt.Sprintf("\n\n… [MCP output sandbox: %d → %d chars (%d → %d tokens). %s Pass max_output=N to this tool for more, or narrow the request.]",
		len(text), cut, tokenize.Count(text), tokenize.Count(text[:cut]), recoveryHint(tool))
}

// recoveryHint suggests the narrower tool to recover the truncated detail.
func recoveryHint(tool string) string {
	switch tool {
	case "kern_project_map", "kern_compact_file":
		return "Use kern_context or kern_compact_file for specific symbols instead."
	case "kern_walk":
		return "Use a shallower depth= or a different root symbol."
	case "kern_near":
		return "Lower max= or depth=."
	case "kern_context":
		return "Request fewer lines=."
	case "kern_review", "kern_context_budget":
		return "Lower max_tokens=."
	case "kern_doc_search":
		return "Narrow the query or lower k=."
	case "kern_ast_search":
		return "Tighten the pattern."
	case "kern_exec":
		return "Cap the script's own output with max=."
	case "kern_graph", "kern_arch", "kern_hubs":
		return "This report is inherently large; prefer kern_search/kern_context for specifics."
	default:
		return "Narrow the query."
	}
}

func (s *Server) promptGetResponse(id json.RawMessage, params json.RawMessage) any {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResponse(id, -32602, "invalid params")
	}
	var def *Prompt
	for i := range prompts {
		if prompts[i].Name == p.Name {
			def = &prompts[i]
			break
		}
	}
	if def == nil {
		return errorResponse(id, -32602, "prompt not found: "+p.Name)
	}
	if p.Arguments == nil {
		p.Arguments = map[string]any{}
	}
	return map[string]any{
		"jsonrpc": "2.0", "id": id,
		"result": map[string]any{
			"description": def.Description,
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": map[string]any{
						"type": "text",
						"text": promptText(p.Name, p.Arguments),
					},
				},
			},
		},
	}
}

func argString(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

// argBool reads an optional boolean tool argument. Accepts native bools and
// the strings "true"/"1" (MCP clients often pass everything as strings).
func argBool(args map[string]any, key string) bool {
	v, ok := args[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.TrimSpace(t)
		return s == "true" || s == "1"
	case float64:
		return t != 0
	}
	return false
}

// atoiArg parses an integer tool argument, falling back to def for empty
// input. A malformed value is an error, not a silent default, so a typo'd
// number can't quietly zero out a limit or mis-size a buffer.
func atoiArg(v string, def int) (int, error) {
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q", v)
	}
	return n, nil
}

// rootedPath resolves p for a file-reading tool. When root is given, the path
// must stay inside it (rejecting "..", absolute paths outside, and symlink
// escapes). A rootless call may only reference a path relative to the current
// working directory: an absolute path is rejected outright, since otherwise a
// caller could pass e.g. path=/etc/shadow and read any file on the system
// outside the confined workspace.
func rootedPath(root, p string) (string, error) {
	if root == "" {
		if filepath.IsAbs(p) {
			return "", fmt.Errorf("absolute path requires root argument")
		}
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return withinRoot(cwd, p)
	}
	return withinRoot(root, p)
}

func (s *Server) runTool(ctx context.Context, id string, name string, args map[string]any) (out string, runErr error) {
	// Record every incoming tool call and its duration; the defer covers all
	// return paths, and runErr is non-nil exactly when dispatch returned an error.
	origName := name // precheckTool may remap the name; audit the executed one
	metrics.Default().RecordRequest()
	start := time.Now()
	defer func() {
		metrics.Default().RecordToolCall(time.Since(start))
		if runErr != nil {
			metrics.Default().RecordError()
		}
		// G-2: every MCP tool execution — read-only tools included —
		// appends to the tamper-evident audit chain. Pre-dispatch
		// rejections (allowlist / root validation) are recorded as
		// blocked; executed calls as allowed/error.
		if name != "" {
			s.auditToolCall(name, args, runErr, true)
		} else {
			s.auditToolCall(origName, args, runErr, false)
		}
	}()
	name, err := s.precheckTool(name, args)
	if err != nil {
		return "", err
	}
	return s.dispatchTool(ctx, id, name, args)
}

// precheckTool validates a tool name against the KERN_TOOLS allowlist and the
// root argument before dispatch, resolving blocked tools to their
// policy-approved fallback. It returns the (possibly fallback) tool name to
// dispatch, or an error when the tool is not allowed and no allowed
// alternative exists, or when the root argument fails validation.
func (s *Server) precheckTool(name string, args map[string]any) (string, error) {
	// Validates against the full registered catalog by design — phase
	// filtering (KERN_MCP_PHASE) only affects advertisement, never execution.
	// Resolve against the FULL registered catalog, not the advertised
	// (filtered) set: kern_meta's NL router reaches every sub-tool handler
	// internally even when the sub-tool is not advertised, and an explicit
	// KERN_TOOLS allowlist still gates execution here.
	if !toolAllowed(tools, s.allowlist, name) {
		// Tool fallback: when a tool is blocked by the KERN_TOOLS
		// allowlist, route to its policy-approved alternative if one exists and
		// IS allowed, so a restricted deployment still gets an equivalent
		// result instead of a hard failure. Fail closed when no allowed
		// alternative exists.
		if alt := app.FallbackFor(name); alt != "" {
			if toolAllowed(tools, s.allowlist, alt) {
				name = alt
			}
		}
		// Fail closed only when no allowed alternative exists.
		if !toolAllowed(tools, s.allowlist, name) {
			return "", fmt.Errorf("tool %q is not allowed (KERN_TOOLS allowlist)", name)
		}
	}
	if err := s.checkRootArg(args); err != nil {
		return "", err
	}
	if root := argString(args, "root"); root != "" {
		if err := validateRoot(root); err != nil {
			return "", err
		}
	}
	return name, nil
}

// analyzeChange and simulateChange have been migrated to internal/app.Platform.
// The MCP handlers now call app.New(root) + p.Analyze/WhatIf so the
// orchestration is shared with CLI and REST instead of duplicated here.

// changedContext resolves the changed files for a tool call: an explicit
// comma-separated file list wins; otherwise the git range (empty = working
// tree). Returns line-aware FileChanges so blast radius can be scoped to the
// changed hunks.
func (s *Server) changedContext(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
	root := resolveRoot(argString(args, "root"))
	ix, err := s.loadIndex(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	if files := argString(args, "file"); files != "" {
		var out []intel.FileChange
		for _, p := range strings.Split(files, ",") {
			if p = strings.TrimSpace(p); p != "" {
				resolved, err := withinRoot(root, p)
				if err != nil {
					return nil, nil, err
				}
				rel, err := filepath.Rel(root, resolved)
				if err != nil {
					return nil, nil, err
				}
				out = append(out, intel.FileChange{File: rel})
			}
		}
		return out, ix, nil
	}
	from, to := "", ""
	if r := argString(args, "range"); r != "" {
		if p := strings.SplitN(r, "..", 2); len(p) == 2 {
			from, to = p[0], p[1]
		} else {
			from = r
		}
	}
	changes, err := intel.FilesForRangeL(root, from, to)
	if err != nil {
		return nil, nil, err
	}
	return changes, ix, nil
}

// truncateMCP truncates s to n bytes with a visible continuation marker.
func truncateMCP(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}

// sessionFor returns the project session for root, creating and caching one
// per root so index state and stats identity are shared across tool calls.
func (s *Server) sessionFor(root string) *project.Session {
	root = resolveRoot(root)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string]*project.Session{}
	}
	sess, ok := s.sessions[root]
	if !ok {
		sess = project.New(root, "")
		s.sessions[root] = sess
	}
	return sess
}

// resolveRoot cleans a tool root argument to an absolute path and requires it
// to be an existing directory. An empty root falls back to the current working
// directory. This guards every index-using tool against traversal-style root
// values and turns confusing downstream index errors into clear ones.
func resolveRoot(root string) string {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = filepath.Clean(abs)
	}
	return root
}

// withinRoot resolves file against root (absolute paths are used as-is) and
// requires the result to stay inside root, rejecting `..` escapes, absolute
// paths that point outside the project boundary, and symlink escapes (a
// symlink inside the project that points outside). It returns the resolved
// absolute path.
func withinRoot(root, file string) (string, error) {
	var abs string
	if filepath.IsAbs(file) {
		abs = filepath.Clean(file)
	} else {
		abs = filepath.Join(root, file)
	}
	// Resolve symlinks on both the root and the candidate so a symlink inside
	// the project that points outside cannot read/escape the project boundary.
	// If either resolution fails (e.g. the file does not exist yet), fall back
	// to the lexical Clean+Rel check below.
	if rRoot, rerr := filepath.EvalSymlinks(root); rerr == nil {
		if rAbs, aerr := filepath.EvalSymlinks(abs); aerr == nil {
			rel, err := filepath.Rel(rRoot, rAbs)
			if err != nil {
				return "", fmt.Errorf("resolve %q: %w", file, err)
			}
			if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
				return "", fmt.Errorf("path %s escapes project root %s", abs, root)
			}
			return abs, nil
		}
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", file, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %s escapes project root %s", abs, root)
	}
	return abs, nil
}

// validateRoot rejects a tool root that exists on disk but is not a directory
// (e.g. a file). A nonexistent root is allowed because several tools create the
// target directory before indexing it. The filesystem root ("/") is also
// rejected: it is never a project to index. It returns "" on success so it can
// be called inline as `if msg := validateRoot(root); msg != "" { return "", msg }`.
func validateRoot(root string) error {
	root = resolveRoot(root)
	if isFilesystemRoot(root) {
		return fmt.Errorf("root %q is a filesystem root and cannot be treated as a project", root)
	}
	if st, err := os.Stat(root); err == nil {
		if !st.IsDir() {
			return fmt.Errorf("root %q exists but is not a directory", root)
		}
	}
	return nil
}

// indexScope carries the symbol index loaded during one tool call so provenance
// can be stamped onto that same call's response. It lives on the per-request
// context instead of the Server struct, so concurrent tool calls never share a
// mutable lastIndex and read each other's provenance.
type indexScope struct {
	ix   *index.Index
	prov *Provenance // structured evidence stamped by retrieval handlers
}
type indexScopeKey struct{}

// loadIndex returns the session's symbol index, reused while fresh and rebuilt
// when stale or missing (see project.Session.Index). The returned index is
// recorded on the per-call scope so the tool response can be stamped with
// provenance.
func (s *Server) loadIndex(ctx context.Context, root string) (*index.Index, error) {
	// First time this root is indexed in-process (including while a lazy
	// background preload is still running): tell the user the wait is the
	// initial build, not a hang. Subsequent stale rebuilds stay silent.
	if _, built := s.indexedRoots.Load(root); !built {
		fmt.Fprintf(os.Stderr, "kern-mcp: first index build for %s in progress, tool call waiting...\n", root)
	}
	ix, err := s.sessionFor(root).Index()
	if err != nil {
		return nil, err
	}
	s.indexedRoots.Store(root, true)
	if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok {
		scope.ix = ix
	}
	return ix, nil
}

// platformEntry ties a cached Platform to the exact index instance it was
// built from. The Platform owns the call graph and the twin-merged knowledge
// graph; constructing it runs intelligence.FromIndex plus four twin
// extractors, each a full filesystem walk. Reusing it while the session
// serves the same index instance turns ~5 tree walks per high-level tool
// call into zero.
type platformEntry struct {
	ix *index.Index
	p  *app.Platform
}

// platformFor returns the shared application Platform for root, caching it
// per root keyed by the index instance it was built from. While
// project.Session serves the same *index.Index pointer (fresh index, 1s
// staleness cooldown), the cached Platform is returned; a real index rebuild
// allocates a new instance, so the next call rebuilds the Platform exactly
// once. The Platform and its graph are treated as read-only after
// construction (same contract as web.App), so sharing across tool calls is
// safe. Handlers that previously called loadIndex + app.NewWithIndex per
// call should use this instead.
func (s *Server) platformFor(ctx context.Context, root string) (*app.Platform, error) {
	ix, err := s.loadIndex(ctx, root)
	if err != nil {
		return nil, err
	}
	root = resolveRoot(root)
	s.mu.Lock()
	if s.platforms == nil {
		s.platforms = map[string]*platformEntry{}
	}
	if e, ok := s.platforms[root]; ok && e.ix == ix {
		p := e.p
		s.mu.Unlock()
		return p, nil
	}
	s.mu.Unlock()
	p, err := app.NewWithIndex(root, ix)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	// Another caller may have populated the cache while we built; prefer the
	// existing entry when it matches the same index instance (identical
	// content, saves the duplicate graph).
	if e, ok := s.platforms[root]; ok && e.ix == ix {
		p = e.p
	} else {
		s.platforms[root] = &platformEntry{ix: ix, p: p}
	}
	s.mu.Unlock()
	return p, nil
}

// commit returns the short HEAD commit of root, cached per root. Git is
// optional: a non-repo root or missing git yields "" (omitted from the stamp).
func (s *Server) commit(root string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.commits[root]; ok {
		return c
	}
	c := ""
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--short", "HEAD").Output(); err == nil {
		c = strings.TrimSpace(string(out))
	}
	s.commits[root] = c
	return c
}

// splitShellLine tokenizes a command line into argv, honoring single and
// double quotes so the documented `sh -c 'cmd ...'` (and Windows `cmd /c "..."`)
// form is preserved as a single argument instead of being split by whitespace.
func splitShellLine(line string) []string {
	var (
		out      []string
		cur      strings.Builder
		inSingle bool
		inDouble bool
		started  bool
	)
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			} else {
				cur.WriteByte(c)
			}
		case inDouble:
			if c == '"' {
				inDouble = false
			} else {
				cur.WriteByte(c)
			}
		case c == '\'':
			inSingle = true
			started = true
		case c == '"':
			inDouble = true
			started = true
		case c == ' ' || c == '\t':
			if started {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteByte(c)
			started = true
		}
	}
	if started {
		out = append(out, cur.String())
	}
	return out
}

func renderOptimize(label string, res optimize.Result) string {
	return fmt.Sprintf("%s (tokens: %d -> %d, saved %d (%.1f%%)):\n%s",
		label, res.BeforeTokens, res.AfterTokens, res.SavedTokens, res.SavedPercent, res.Output)
}

// clipForMarker shortens a matched-input preview so a multi-megabyte log match
// never floods the "served from semantic cache" marker.
func clipForMarker(s string) string {
	const max = 80
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// clip trims a string to n bytes for a tool summary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// hasSlugChar reports whether s contains at least one character a slug keeps,
// so sanitizeDocName can reject names that would collapse to nothing.
func hasSlugChar(s string) bool {
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// sanitizeDocName constrains a doc name to a safe cache filename:
// lowercase alphanumerics and dashes only. Path separators, dot-dot and
// other punctuation are replaced (or collapse to nothing), so a name can
// never escape the cache root via ../ or produce a bogus index key.
// Empty input (or a name with no slug-able characters) yields an error.
func sanitizeDocName(name string) (string, error) {
	if !hasSlugChar(name) {
		return "", fmt.Errorf("invalid doc name %q", name)
	}
	return strutil.Slug(name), nil
}

// docSearchSlug derives a filesystem-safe doc name from a URL, e.g.
// https://react.dev/reference/usestate -> react-dev-reference-usestate.
func docSearchSlug(rawURL string) string {
	return strutil.DocSlug(rawURL)
}

func renderStats(daysStr, session string) (string, error) {
	days := 7
	if daysStr != "" {
		if _, err := fmt.Sscanf(daysStr, "%d", &days); err != nil {
			return "", fmt.Errorf("invalid days: %s", daysStr)
		}
	}
	rec, err := stats.NewRecorder()
	if err != nil {
		return "", err
	}
	sum, err := rec.Summarize(days, session)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("operations=%d before=%d after=%d saved=%d (%.1f%%) cost_saved=$%.4f",
		sum.Operations, sum.BeforeTotal, sum.AfterTotal, sum.SavedTotal, sum.SavedPct, sum.CostSaved), nil
}

// ensureRecorder wires the shared stats recorder used by optimize operations.
func ensureRecorder() error {
	return optimize.EnsureRecorder()
}
