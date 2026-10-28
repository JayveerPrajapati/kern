package mcp

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/JayveerPrajapati/kern/internal/govaudit"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/mcp/root"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// toolAudit returns the server's shared audit log for MCP tool executions,
// persisted to the project's .kern/audit with the same store, cross-process
// lock, and hash chain the platform and CLI use, so `kern audit` shows tool
// calls alongside firewall decisions and approvals. KERN_MCP_AUDIT_DIR
// overrides the directory (tests use it to stay out of the real store);
// failures degrade to an in-memory log. Initialized lazily on first use.
//
// NEW-2: the default directory (the server cwd) is only used when the cwd is
// a plausible project (a real repo or an already-populated .kern) — a
// rejected no-root call in a scratch cwd must not mint a .kern/audit tree
// there. When the cwd is not a project, the log stays in-memory (entries are
// still recorded for the process lifetime, just not persisted); an operator
// who wants persistence from a non-project cwd sets KERN_MCP_AUDIT_DIR.
func (s *Server) toolAudit() *govaudit.AuditLog {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	if s.audit == nil {
		dir := os.Getenv("KERN_MCP_AUDIT_DIR")
		if dir == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return govaudit.NewAuditLog() // in-memory fallback
			}
			if !root.PlausibleProject(cwd) {
				return govaudit.NewAuditLog() // in-memory only — no scratch-cwd bootstrap
			}
			dir = filepath.Join(cwd, ".kern", "audit")
		}
		s.audit = govaudit.NewAuditLog().
			WithStore(storage.NewLog(dir)).
			WithLockPath(filepath.Join(dir, ".lock")).
			WithMirror(govaudit.MirrorFor(dir))
	}
	return s.audit
}

// auditToolCall appends one tamper-evident chain entry per MCP tool call —
// executed tools (read-only included) and pre-dispatch rejections alike
// (the chain previously recorded only firewall decisions, approvals,
// and gated exec). Best-effort by design: an audit failure is swallowed and
// never fails the tool call it describes — but the loss is not silent: a
// panic is recovered here and marked in the global dropped-audit sidecar,
// and store-write losses are counted inside AuditLog.persist (both surface
// in `kern doctor` as the audit-dropped check). cached marks a D1 cache
// hit: the entry is tagged Policy:"tool-cache" / Reason:"served from
// cache" so the evidence chain reflects what was actually served (F5).
// Result stays "allowed" — no new Result value is invented.
func (s *Server) auditToolCall(name string, args map[string]any, runErr error, executed bool, cached bool, took time.Duration) {
	if readonlyMode() {
		return // KERN_READONLY=1: the per-tool-call audit chain is read-path
		// telemetry; governance-core audit (approvals, firewall, exec
		// denials) records through its own paths and stays intact.
	}
	defer func() {
		if r := recover(); r != nil {
			// The audit trail must never take a tool call down — but the
			// loss must not be silent either: mark the global sidecar so
			// `kern doctor` surfaces the dropped entry.
			govaudit.AppendDroppedAuditMarker("", "tool_call", name, fmt.Sprintf("panic: %v", r))
			log.Printf("kern mcp: auditToolCall panic for tool %q recovered (drop marked): %v", name, r)
		}
	}()
	agent := argString(args, "agent_id")
	if agent == "" {
		agent = governance.DefaultAgentID
	}
	result := "allowed"
	switch {
	case !executed:
		result = "blocked"
	case runErr != nil:
		result = "error"
	}
	entry := govaudit.AuditEntry{
		AgentID:  agent,
		Action:   "tool_call",
		Resource: name,
		Approved: executed,
		Result:   result,
		TaskID:   argString(args, "task"),
	}
	if took > 0 {
		entry.DurationMs = took.Milliseconds()
	}
	if cached {
		entry.Policy = "tool-cache"
		entry.Reason = "served from cache"
	}
	s.toolAudit().Record(entry)
}
