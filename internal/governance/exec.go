// Package exec is the single governance gate that the execution tools
// (kern_exec, kern_sandbox, kern_execute) pass through. It fails closed.

package governance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/config"
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
// hardcoded LOW. When it is, the approval is bound to the exact command text
// (SHA-256 in the key) and persisted to <root>/.kern/approvals.json, so
// `kern approve <id>` can resolve it out-of-band and one approval authorizes
// exactly one command (audit A3/A4).
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
// A HIGH/CRITICAL command.execute fails closed here with an approval-required
// error. Callers that hold the concrete command text should use
// CheckExecCommand instead, so the approval is command-specific and
// persisted; CheckExec is the command-less form and delegates to it with an
// empty command (approval stays in-memory — there is no command to bind and
// no root to persist under).
func CheckExec(toolName ...string) error {
	return CheckExecCommand("", "", toolName...)
}

// CheckExecCommand is the command-aware form of CheckExec: it gates one
// concrete command text. When the command is approval-gated (HIGH/CRITICAL
// via KERN_EXEC_RISK, and no KERN_ALLOW_EXEC / KERN_TOOLS allowlist bypass
// applies), it creates a PERSISTED, command-hash-bound approval under root
// (<root>/.kern/approvals.json) — integrity-stamped with an HMAC keyed by a
// secret outside the workspace (R1) — and returns an error carrying the
// approval ID with the resolution hint "resolve with: kern approve <id>".
// After a human approves that ID, the SAME command passes exactly once (the
// grant is consumed, R2); a DIFFERENT command never passes on that approval
// (the approval key embeds the command's SHA-256). An empty root keeps the
// approval in-memory (visible only to this process) and returns honest
// guidance instead of the out-of-band approve hint (R6).
func CheckExecCommand(command, root string, toolName ...string) error {
	tool := ""
	if len(toolName) > 0 {
		tool = strings.TrimSpace(toolName[0])
	}
	if err := execAllowlistGate(tool); err != nil {
		return err
	}
	key := TaskKey(execAgentID, "command", "execute")
	if command != "" {
		key = execCommandKey(command)
	}
	fw := newExecFirewall(root, command)
	granted := execApprovedFor(root, key)
	if granted != nil {
		// A human approved this exact command out-of-band (`kern approve
		// <id>` persisted a MAC-verified approved record): grant the key so
		// the firewall's approval gate lets this ONE execution through.
		fw.grantApproval(key)
	}
	allowed, risk, ap, err := fw.Check(execAgentID, "command", "execute")
	if err != nil {
		return fmt.Errorf("governance: exec firewall denied: %w", err)
	}
	if allowed {
		// R2: the grant authorizes exactly one execution — atomically claim
		// the approved record (single flock'd mutation; only one concurrent
		// execution wins) and ledger the consumed ID outside the workspace so
		// a snapshot-and-reinsert replay cannot reauthorize it (R2d). The
		// claim targets the record from the FIRST lookup: if a concurrent
		// execution already consumed it, Consume reports claimed=false and
		// this call denies BEFORE executing (gate-1 attempt-3).
		if granted != nil {
			claimed, cerr := claimExecApproval(root, granted.ID)
			if cerr != nil {
				return fmt.Errorf("governance: exec approval %s could not be consumed (single-use guarantee broken; failing closed): %w", granted.ID, cerr)
			}
			if !claimed {
				return fmt.Errorf("governance: exec approval %s was already consumed by a concurrent execution (single-use); request a new approval", granted.ID)
			}
		}
		return nil
	}
	if ap != nil {
		if root == "" {
			// R6: in-memory approval — no other process can resolve it, so
			// the `kern approve <id>` hint would be a dead end. Give honest
			// guidance instead.
			return fmt.Errorf("governance: command execution requires human approval (risk level %s, score %.2f); no persistent approval store for this call — allow it via the KERN_TOOLS allowlist or run with a project root to enable `kern approve`", risk.Level, risk.Score)
		}
		if serr := stampExecApproval(root, ap); serr != nil {
			return fmt.Errorf("governance: command execution requires human approval, but the approval could not be integrity-protected: %w", serr)
		}
		// The firewall already requested (and persisted) the command-bound
		// approval. Surface it with the resolution hint so the caller can
		// route it to a human (`kern approve <id>`).
		return fmt.Errorf("governance: command execution requires human approval (risk level %s, score %.2f); approval %s pending — resolve with: kern approve %s", risk.Level, risk.Score, ap.ID, ap.ID)
	}
	if RequiresApproval(risk.Level) {
		// Approval-required but no approval could be surfaced: fail closed.
		return fmt.Errorf("governance: command execution requires human approval (risk level %s, score %.2f) but no approval could be created; failing closed", risk.Level, risk.Score)
	}
	return errors.New("governance: exec firewall denied command execution")
}

// execAllowlistGate enforces the KERN_TOOLS allowlist (gates 1 and 2 above).
// It fails closed: an empty allowlist without KERN_ALLOW_EXEC is refused, and a
// non-empty allowlist must actually name the exec tool (or at least one exec
// tool when no specific tool is given).
func execAllowlistGate(tool string) error {
	allowlist := parseToolAllowlist(os.Getenv("KERN_TOOLS"))

	// 1. Empty-allowlist gate: an unset KERN_TOOLS means "all tools allowed",
	// which must not implicitly allow host command execution. Strict parsing:
	// only KERN_ALLOW_EXEC=1 opts in; any other value is treated as unset.
	if len(allowlist) == 0 && os.Getenv("KERN_ALLOW_EXEC") != "1" {
		return errors.New("command execution blocked: no KERN_TOOLS allowlist is set and KERN_ALLOW_EXEC is not enabled; refusing to run ungoverned host commands (set KERN_ALLOW_EXEC=1 to opt in, or list the tool in KERN_TOOLS)")
	}

	// 2. Allowlist-contents gate. Only enforced when an allowlist is configured.
	if len(allowlist) > 0 {
		if tool != "" {
			if !containsString(allowlist, tool) {
				return fmt.Errorf("command execution blocked: tool %q is not in the KERN_TOOLS allowlist", tool)
			}
		} else if !containsAnyString(allowlist, execToolNames...) {
			return fmt.Errorf("command execution blocked: KERN_TOOLS allowlist does not name any exec tool (%s — CLI aliases exec, sandbox, execute are accepted too)", strings.Join(execToolNames, ", "))
		}
	}
	return nil
}

// execFirewallCheck runs the change-firewall gate for command.execute and
// returns whether it is allowed, the assessed risk, and — when the action is
// approval-gated — the pending approval from the firewall's own ephemeral
// workflow. Callers that want a real, externally-reviewable approval should
// use RequestExecApproval / CheckExecCommand instead.
func execFirewallCheck() (allowed bool, risk domain.Risk, approval *domain.Approval, err error) {
	fw := NewFirewall().WithPolicies(execPolicies())
	agent := NewAgent(execAgentID, "mcp-exec", "application", []Permission{{Resource: "command", Action: "execute"}})
	allowed, risk, approval, err = fw.WithAgents(agent).Check(execAgentID, "command", "execute")
	return allowed, risk, approval, err
}

// newExecFirewall builds the exec firewall: the operator-configurable exec
// policies plus the mcp-exec agent. When root is non-empty the approval
// workflow is persisted to <root>/.kern/approvals.json (so `kern approve
// <id>` can resolve approvals out-of-band); otherwise it is in-memory. When
// command is non-empty the firewall binds its approval gate to the exact
// command text (see WithExecCommand).
func newExecFirewall(root, command string) *Firewall {
	var fw *Firewall
	if root != "" {
		fw = NewFirewallWithApprovalStore(root)
	} else {
		fw = NewFirewall()
	}
	fw = fw.WithPolicies(execPolicies())
	fw = fw.WithAgents(NewAgent(execAgentID, "mcp-exec", "application", []Permission{{Resource: "command", Action: "execute"}}))
	if command != "" {
		fw = fw.WithExecCommand(command)
	}
	return fw
}

// execCommandHash returns the hex SHA-256 of the command text — the component
// that makes an exec approval command-specific (audit A3).
func execCommandHash(command string) string {
	sum := sha256.Sum256([]byte(command))
	return hex.EncodeToString(sum[:])
}

// execCommandKey builds the approval/task key for a concrete command: the
// exec task triple plus the command hash, so an approval authorizes exactly
// one command and cannot be replayed against a different one.
func execCommandKey(command string) string {
	return TaskKey(execAgentID, "command", "execute") + "|" + execCommandHash(command)
}

// RequestExecApproval runs the full exec governance gate and, when the command
// is approval-gated (command.execute scoring HIGH/CRITICAL via KERN_EXEC_RISK),
// submits a command-bound ApprovalRequest to wf and returns the pending
// approval so the orchestrator/CLI can route it to a human reviewer.
// command is the exact command text that will run: the approval is bound to
// its SHA-256 (audit A3), so one approval authorizes exactly that command.
// Returns:
// - (nil, risk, nil): the command may run directly (no approval required).
// - (&approval, risk, nil): an approval is pending; it must be approved
// (wf.Approve) before ResumeExecApproval lets the command run.
// - (nil, risk, err): the command is denied, or approval is required but no
// workflow is configured (fails closed).
func RequestExecApproval(wf *ApprovalWorkflow, command string, toolName ...string) (*domain.Approval, domain.Risk, error) {
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
	key := TaskKey(execAgentID, "command", "execute")
	if command != "" {
		key = execCommandKey(command)
	}
	var evidence []string
	if command != "" {
		evidence = []string{command}
	}
	ap, err := wf.RequestWithBinding(key, execAgentID, risk.Mitigation, risk.Level, nil, evidence, "")
	if err != nil {
		return nil, risk, fmt.Errorf("governance: command execution requires human approval, but the approval could not be persisted: %w", err)
	}
	// R1: a persistence-backed workflow must carry the exec integrity stamp
	// so a forged/legacy record in the shared approval file can never be
	// resumed. In-memory workflows (no store) skip the stamp — there is
	// nothing on disk to forge.
	if wf.store != nil {
		stamp, serr := execApprovalStamp(ap)
		if serr != nil {
			return nil, risk, fmt.Errorf("governance: exec approval %s could not be integrity-stamped: %w", ap.ID, serr)
		}
		ap.ArtifactID = stamp
		if perr := wf.store.AddPending(ap); perr != nil {
			return nil, risk, fmt.Errorf("governance: exec approval %s integrity stamp could not be persisted: %w", ap.ID, perr)
		}
	}
	return &ap, risk, nil
}

// ResumeExecApproval reports whether a previously-requested exec approval may
// proceed, i.e. the human has approved it. It returns nil (proceed) only when
// wf has the approval in the "approved" state; otherwise it fails closed
// (still pending / rejected / unknown). For persistence-backed workflows the
// approved record must additionally carry a valid exec HMAC (R1): the store
// copy is verified — not the in-memory copy, which keeps the pre-decision
// stamp — so a forged/legacy record can never resume execution.
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
	if wf.store != nil {
		stored, serr := wf.store.Get(approvalID)
		if serr != nil {
			return fmt.Errorf("governance: exec approval %q not in the persisted store: %w", approvalID, serr)
		}
		if !execApprovalMACValid(stored) {
			return fmt.Errorf("governance: exec approval %q failed integrity verification; command not run", approvalID)
		}
	}
	return nil
}

// execPolicies returns the risk policies for the exec firewall: the defaults
// plus a command.execute policy whose severity is operator-configurable via
// KERN_EXEC_RISK (or exec.risk in .kern/config.json; default MEDIUM; HIGH or
// CRITICAL makes command.execute require human approval). An unrecognized
// value defaults to MEDIUM.
func execPolicies() []domain.Policy {
	level := "MEDIUM"
	if v := strings.ToUpper(strings.TrimSpace(config.String("", "KERN_EXEC_RISK", "exec.risk", ""))); v != "" {
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
// trimming whitespace and dropping empty entries. CLI subcommand aliases are
// normalized to their canonical MCP tool names ("exec" → "kern_exec") so an
// operator writing CLI-style names gets the same behavior as MCP-style ones.
func parseToolAllowlist(v string) []string {
	var out []string
	for _, n := range strings.Split(v, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, NormalizeToolName(n))
		}
	}
	return out
}

// NormalizeToolName maps a CLI subcommand alias to its canonical MCP tool
// name: a bare name gains the "kern_" prefix ("exec" → "kern_exec"). Names
// already carrying the prefix pass through unchanged. Every MCP tool is
// kern_-prefixed, so the mapping cannot collide with a real tool name.
func NormalizeToolName(name string) string {
	if name != "" && !strings.HasPrefix(name, "kern_") {
		return "kern_" + name
	}
	return name
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
