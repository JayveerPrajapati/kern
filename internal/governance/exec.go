// Package exec is the single governance gate that the execution tools
// (kern_exec, kern_sandbox, kern_execute) pass through. It fails closed.
package governance

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Execution surfaces (kern_exec, kern_sandbox, kern_execute) run arbitrary
// host commands, so this file is the single governance gate those tools pass
// through. It fails closed — any error or denial refuses to run. Three
// independent gates apply:
// 1. An empty (unset) KERN_TOOLS allowlist means "all tools allowed", so exec
// is refused unless the operator opts in via KERN_ALLOW_EXEC=1.
// 2. When KERN_TOOLS is set, the specific exec tool being invoked must be
// named in it; a non-empty but unrelated allowlist does not re-enable exec.
// 3. The change firewall authorizes the command via the risk model, so a
// command.execute can become approval-gated (HIGH/CRITICAL) rather than
// hardcoded LOW.
const execAgentID = "mcp-exec"

// execToolNames are the host-command tools that a KERN_TOOLS allowlist must
// name for command execution to be permitted. When CheckExec is called without
// a specific tool name, at least one of these must appear in the allowlist.
var execToolNames = []string{"kern_exec", "kern_sandbox", "kern_execute"}

// CheckExec reports whether arbitrary host command execution is permitted for
// the current server invocation, returning an error when execution must be
// refused. toolName optionally names the specific exec tool so the allowlist
// is validated against exactly what is being allowed; when empty, at least one
// exec tool must be present in the allowlist.
// A HIGH/CRITICAL command.execute fails closed here with an
// approval-required error. Callers that want to drive the human-approval
// workflow should use RequestExecApproval / ResumeExecApproval instead.
func CheckExec(toolName ...string) error {
	tool := ""
	if len(toolName) > 0 {
		tool = strings.TrimSpace(toolName[0])
	}
	if err := execAllowlistGate(tool); err != nil {
		return err
	}
	allowed, risk, approval, err := execFirewallCheck()
	if err != nil {
		return fmt.Errorf("governance: exec firewall denied: %w", err)
	}
	if !allowed {
		if approval != nil || RequiresApproval(risk.Level) {
			return fmt.Errorf("governance: command execution requires human approval (risk level %s, score %.2f); approve it before running", risk.Level, risk.Score)
		}
		return errors.New("governance: exec firewall denied command execution")
	}
	return nil
}

// execAllowlistGate enforces the KERN_TOOLS allowlist (gates 1 and 2 above).
// It fails closed: an empty allowlist without KERN_ALLOW_EXEC is refused, and a
// non-empty allowlist must actually name the exec tool (or at least one exec
// tool when no specific tool is given).
func execAllowlistGate(tool string) error {
	allowlist := parseToolAllowlist(os.Getenv("KERN_TOOLS"))

	// 1. Empty-allowlist gate: an unset KERN_TOOLS means "all tools allowed",
	// which must not implicitly allow host command execution.
	if len(allowlist) == 0 && os.Getenv("KERN_ALLOW_EXEC") == "" {
		return errors.New("command execution blocked: no KERN_TOOLS allowlist is set and KERN_ALLOW_EXEC is not enabled; refusing to run ungoverned host commands (set KERN_ALLOW_EXEC=1 to opt in, or list the tool in KERN_TOOLS)")
	}

	// 2. Allowlist-contents gate. Only enforced when an allowlist is configured.
	if len(allowlist) > 0 {
		if tool != "" {
			if !containsString(allowlist, tool) {
				return fmt.Errorf("command execution blocked: tool %q is not in the KERN_TOOLS allowlist", tool)
			}
		} else if !containsAnyString(allowlist, execToolNames...) {
			return fmt.Errorf("command execution blocked: KERN_TOOLS allowlist does not name any exec tool (%s)", strings.Join(execToolNames, ", "))
		}
	}
	return nil
}

// execFirewallCheck runs the change-firewall gate for command.execute and
// returns whether it is allowed, the assessed risk, and — when the action is
// approval-gated — the pending approval from the firewall's own ephemeral
// workflow. Callers that want a real, externally-reviewable approval should
// use RequestExecApproval instead.
func execFirewallCheck() (allowed bool, risk domain.Risk, approval *domain.Approval, err error) {
	fw := NewFirewall().WithPolicies(execPolicies())
	agent := NewAgent(execAgentID, "mcp-exec", "application", []Permission{{Resource: "command", Action: "execute"}})
	allowed, risk, approval, err = fw.WithAgents(agent).Check(execAgentID, "command", "execute")
	return allowed, risk, approval, err
}

// RequestExecApproval runs the full exec governance gate and, when the command
// is approval-gated (command.execute scoring HIGH/CRITICAL via KERN_EXEC_RISK),
// submits an ApprovalRequest to wf and returns the pending approval so the
// orchestrator/CLI can route it to a human reviewer.
// Returns:
// - (nil, risk, nil): the command may run directly (no approval required).
// - (&approval, risk, nil): an approval is pending; it must be approved
// (wf.Approve) before ResumeExecApproval lets the command run.
// - (nil, risk, err): the command is denied, or approval is required but no
// workflow is configured (fails closed).
func RequestExecApproval(wf *ApprovalWorkflow, toolName ...string) (*domain.Approval, domain.Risk, error) {
	tool := ""
	if len(toolName) > 0 {
		tool = strings.TrimSpace(toolName[0])
	}
	if err := execAllowlistGate(tool); err != nil {
		return nil, domain.Risk{}, err
	}
	allowed, risk, _, err := execFirewallCheck()
	if err != nil {
		return nil, risk, fmt.Errorf("governance: exec firewall denied: %w", err)
	}
	if allowed {
		return nil, risk, nil // no approval needed; proceed directly
	}
	if !RequiresApproval(risk.Level) {
		return nil, risk, errors.New("governance: exec firewall denied command execution")
	}
	if wf == nil {
		// Approval required but no workflow to review it: fail closed. Never run
		// a HIGH/CRITICAL command without a human approval path.
		return nil, risk, fmt.Errorf("governance: command execution requires human approval (risk level %s, score %.2f) but no approval workflow is configured; failing closed", risk.Level, risk.Score)
	}
	ap := wf.Request(TaskKey(execAgentID, "command", "execute"), execAgentID, risk.Mitigation)
	return &ap, risk, nil
}

// ResumeExecApproval reports whether a previously-requested exec approval may
// proceed, i.e. the human has approved it. It returns nil (proceed) only when
// wf has the approval in the "approved" state; otherwise it fails closed
// (still pending / rejected / unknown).
func ResumeExecApproval(wf *ApprovalWorkflow, approvalID string) error {
	if wf == nil {
		return errors.New("governance: no approval workflow configured; cannot resume command execution")
	}
	a, err := wf.Get(approvalID)
	if err != nil {
		return err
	}
	if a.Status != "approved" {
		return fmt.Errorf("governance: exec approval %q is %s, not approved; command not run", approvalID, a.Status)
	}
	return nil
}

// execPolicies returns the risk policies for the exec firewall: the defaults
// plus a command.execute policy whose severity is operator-configurable via
// KERN_EXEC_RISK (default MEDIUM; HIGH or CRITICAL makes command.execute
// require human approval). An unrecognized value defaults to MEDIUM.
func execPolicies() []domain.Policy {
	level := "MEDIUM"
	if v := strings.ToUpper(strings.TrimSpace(os.Getenv("KERN_EXEC_RISK"))); v != "" {
		switch v {
		case "LOW", "MEDIUM", "HIGH", "CRITICAL":
			level = v
		}
	}
	return append(DefaultPolicies(), domain.Policy{
		ID:          "pol-command-execute",
		Name:        "command_execute",
		Description: "Arbitrary host command execution (operator-configurable risk).",
		Rule:        level + " command.execute",
		Scope:       "command",
		Enabled:     true,
	})
}

// parseToolAllowlist parses a comma-separated tool allowlist (KERN_TOOLS),
// trimming whitespace and dropping empty entries.
func parseToolAllowlist(v string) []string {
	var out []string
	for _, n := range strings.Split(v, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// containsString reports whether s appears in list (exact match).
func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// containsAnyString reports whether any of needles appears in list.
func containsAnyString(list []string, needles ...string) bool {
	for _, x := range list {
		for _, n := range needles {
			if x == n {
				return true
			}
		}
	}
	return false
}
