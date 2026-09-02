package mcp

import (
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// toolAudit returns the server's shared audit log for MCP tool executions,
// persisted to the project's .kern/audit with the same store, cross-process
// lock, and hash chain the platform and CLI use, so `kern audit` shows tool
// calls alongside firewall decisions and approvals. KERN_MCP_AUDIT_DIR
// overrides the directory (tests use it to stay out of the real store);
// failures degrade to an in-memory log. Initialized lazily on first use.
func (s *Server) toolAudit() *governance.AuditLog {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	if s.audit == nil {
		dir := os.Getenv("KERN_MCP_AUDIT_DIR")
		if dir == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return governance.NewAuditLog() // in-memory fallback
			}
			dir = filepath.Join(cwd, ".kern", "audit")
		}
		s.audit = governance.NewAuditLog().
			WithStore(storage.NewLog(dir)).
			WithLockPath(filepath.Join(dir, ".lock"))
	}
	return s.audit
}

// auditToolCall appends one tamper-evident chain entry per MCP tool call —
// executed tools (read-only included) and pre-dispatch rejections alike
// (G-2: the chain previously recorded only firewall decisions, approvals,
// and gated exec). Best-effort by design: an audit failure is swallowed and
// never fails the tool call it describes.
func (s *Server) auditToolCall(name string, args map[string]any, runErr error, executed bool) {
	defer func() { _ = recover() }() // the audit trail must never take a tool call down
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
	s.toolAudit().Record(governance.AuditEntry{
		AgentID:  agent,
		Action:   "tool_call",
		Resource: name,
		Approved: executed,
		Result:   result,
		TaskID:   argString(args, "task"),
	})
}
