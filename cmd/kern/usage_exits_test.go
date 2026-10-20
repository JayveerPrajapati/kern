package main

import "testing"

// QA 2026-10-15 finding 9: bare usage-class invocations must exit 2 per the
// documented exit-code contract (`kern exitcode`). These pins cover the
// stragglers fixed in this pass: kern mcp-client (printed help and returned
// 0), kern agent-role-rbac (forced an "evaluate" default that failed at the
// tool boundary with a vague error, exit 1). kern agent-coordination is
// deliberately NOT here: its bare invocation prints a genuine coordination
// status report (the read-only default the MCP tool itself uses), so exit 0
// is correct, not a usage error. (kern runtime's pin left with the dead
// runtime CLI shell, 2026-10-18 P5.)

func TestMcpClientBareIsUsageExit2(t *testing.T) {
	expectExit(t, 2, func() { runMcpClient(nil) })
}

func TestAgentRoleRBACBareIsUsageExit2(t *testing.T) {
	expectExit(t, 2, func() { runAgentRoleRBAC(nil) })
}
