// Package governance is the unified policy / risk / permissions engine of the
// Kern 2.0 control plane. Every agent action passes through the change
// firewall in the order: Authentication → Authorization → Permission → Risk →
// Impact → Policy → Approval → Execution.
//
// It provides: agent identity and a permission model (identity/), deterministic
// risk scoring over domain.Policy rules (risk/), a human-in-the-loop approval
// workflow for HIGH/CRITICAL risk (approval/), an in-memory audit log of every
// decision (audit/), the Firewall facade tying these together (firewall/), and
// the exec governance gate for host-command execution (exec/).
//
// This root package is a backward-compatible facade that re-exports every
// public symbol of the sub-packages via type aliases and wrapper functions.
//
// Design rules: the firewall fails closed — an unknown agent, a missing
// permission, or an always-blocked action is denied by default. Risk scoring
// is deterministic: the same resource+action always yields the same Risk.
package governance
