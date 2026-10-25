// grants.go implements the Ask-Protocol grant layer (guard-consent campaign
// P1, 2026-10-24): one grant store, one matching rule, three answers.
//
// A human answers a gated command exactly once per question with ONE of:
//
//	[1] accept once      — the legacy single-use grant (scope "once"): the
//	                      approved record authorizes exactly ONE execution,
//	                      atomically claimed and ledgered (R2/R2d unchanged).
//	[2] accept prefix    — a session-scoped prefix grant (scope "prefix"):
//	                      commands starting with the grant's prefix (token
//	                      boundary) run without re-asking until the grant
//	                      expires (24h default) or hits its use cap; every
//	                      use is counted and audit-logged.
//	[3] reject           — rejection stickiness, unchanged (TTL-bounded).
//
// Uniformity contract: the SAME store and matching rule serve every consumer
// — CheckExecCommand (kern exec), the guard GATE arm (kern guard hook / the
// shell script / the opencode plugin), and the CLI (kern approve --scope,
// kern approval ask). Agents can MINT pending approvals (MintGateApproval)
// and CONSUME granted ones, but only a human at a terminal (kern approve /
// kern approval ask, both tty-gated) can decide one — the decide surface is
// unchanged.
//
// Integrity: scope, prefix, command, expiry and use counters are covered by
// the exec-approval HMAC (execapproval v3 framing), so a governed client
// cannot flip a once-grant into a prefix grant, widen a prefix, extend an
// expiry, or reset a use count — any edit breaks the stamp and the record
// stops granting (fail closed).
//
// Session binding honesty: hosts expose no stable session identifier to
// hooks (Claude Code, Cursor, Codex, opencode each differ), so "this
// session" is enforced as a TTL bound (24h default) on the GRANT, not a
// session id. An operator can end a grant early with `kern approval clear
// <id>`; `kern approval list` shows every live grant with its remaining
// uses. Installer-class commands get a tighter use cap (5) so a prefix
// answer cannot amortize an unbounded number of installs; CRITICAL-risk
// commands are never prefix-grantable at all.
package governance

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/execclassify"
)

// Ask-Protocol grant parameters.
const (
	// prefixGrantTTL bounds a prefix grant: "this session" is approximated
	// by 24h because hosts expose no stable session id to hooks.
	prefixGrantTTL = 24 * time.Hour
	// defaultPrefixMaxUses caps uses of a normal prefix grant.
	defaultPrefixMaxUses = 20
	// installerPrefixMaxUses caps uses of an installer-class prefix grant
	// (dangerous class "installer": package managers, install scripts) —
	// one human answer must not amortize unbounded installs.
	installerPrefixMaxUses = 5
)

// errNoPrefixGrant is the sentinel "no live prefix grant covers this
// command" — consumers treat it as "ask the human", everything else as a
// fail-closed error.
var errNoPrefixGrant = errors.New("no live prefix grant")

// SuggestedPrefix returns the suggested prefix for an accept-prefix answer:
// the first two whitespace tokens of the approved command (editable by the
// human at answer time).
func SuggestedPrefix(command string) string {
	fields := strings.Fields(command)
	if len(fields) >= 2 {
		return fields[0] + " " + fields[1]
	}
	return strings.TrimSpace(command)
}

// PrefixMatches reports whether command starts with prefix at a token
// boundary: the command equals the prefix, or continues with whitespace.
// "make install" matches "make install" and "make install X", never "make
// installx" — a prefix answer never widens into different tools.
func PrefixMatches(command, prefix string) bool {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return false
	}
	if command == prefix {
		return true
	}
	return strings.HasPrefix(command, prefix+" ")
}

// MintGateApproval mints a PENDING exec-command approval for a guard
// GATE-class command, independent of exec-firewall risk scoring (the guard's
// GATE classes and the exec firewall's KERN_EXEC_RISK model are different
// lenses over the same intent). The record is command-hash-bound (TaskID =
// execCommandKey) and HMAC-stamped, exactly like an exec approval. Minting
// grants NOTHING — a human must still decide it via kern approve /
// kern approval ask. An empty root fails closed (no store to persist into).
func MintGateApproval(command, root string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", errors.New("governance: cannot mint a gate approval for an empty command")
	}
	if root == "" {
		return "", errors.New("governance: no project root — cannot persist a gate approval")
	}
	reason := "guard GATE command: " + command
	if len(reason) > 200 {
		reason = reason[:197] + "..."
	}
	a := domain.Approval{
		Kind:         ApprovalKindExecCommand,
		ID:           randomApprovalID(),
		TaskID:       execCommandKey(command),
		Requester:    execAgentID,
		Status:       "pending",
		Reason:       reason,
		RiskLevel:    domain.RiskHigh,
		EvidenceRefs: []string{command},
		Command:      command,
		RequestedAt:  time.Now(),
	}
	if err := stampExecApproval(root, &a); err != nil {
		return "", fmt.Errorf("governance: gate approval could not be integrity-stamped: %w", err)
	}
	return a.ID, nil
}

// CheckGateGrant answers whether a GATE-class command is ALREADY authorized
// by the Ask Protocol, consuming the authorization exactly like an
// execution would:
//
//   - a single-use (scope "once"/"") approved grant: atomically claimed —
//     exactly one execution, ledgered against replay (R2/R2d);
//   - a live prefix grant: one use counted, re-stamped and audit-logged.
//
// nil return means the command may proceed. errNoPrefixGrant (or a
// consumed-record error) means the human must be asked; any other error is
// fail-closed (the caller denies rather than guessing).
func CheckGateGrant(command, root string) error {
	if strings.TrimSpace(command) == "" || root == "" {
		return errNoPrefixGrant
	}
	key := execCommandKey(command)
	// Rejection stickiness outranks grants: a human's explicit "no" to this
	// exact command must not be bypassed by an older prefix grant (the
	// denial message teaches the clear/modify escapes as usual).
	if rejected := execRejectedFor(root, key); rejected != nil {
		return fmt.Errorf("governance: this exact command was previously rejected by a human (approval %s) — rejections stop blocking %s after the decision; clear it with: kern approval clear %s --root %s, or modify the command", rejected.ID, execRejectionTTLText(), rejected.ID, root)
	}
	if granted := execApprovedFor(root, key); granted != nil {
		claimed, err := claimExecApproval(root, granted.ID)
		if err != nil {
			return fmt.Errorf("governance: exec approval %s could not be consumed (single-use guarantee broken; failing closed): %w", granted.ID, err)
		}
		if !claimed {
			return fmt.Errorf("governance: exec approval %s was already consumed by a concurrent execution (single-use); ask again for this command", granted.ID)
		}
		return nil
	}
	return consumePrefixGrantUse(root, command)
}

// consumePrefixGrantUse finds a live prefix grant covering command and
// consumes one use under the store's flock'd mutation: re-verifies the
// record (MAC, status, scope, expiry, cap), increments Uses, re-stamps and
// saves, then audit-logs the use. errNoPrefixGrant when nothing covers the
// command; any store/integrity failure is returned as an error (fail
// closed).
func consumePrefixGrantUse(root, command string) error {
	store := NewFileStore(root)
	records, err := store.Load()
	if err != nil {
		return err
	}
	match := ""
	for _, a := range records {
		if !livePrefixGrant(a) || !PrefixMatches(command, a.Prefix) {
			continue
		}
		match = a.ID
		break
	}
	if match == "" {
		return errNoPrefixGrant
	}
	var used domain.Approval
	var outcome error
	if err := store.mutate(func(list []domain.Approval) []domain.Approval {
		outcome = nil
		for i := range list {
			if list[i].ID != match {
				continue
			}
			a := list[i]
			if !livePrefixGrant(a) || !PrefixMatches(command, a.Prefix) {
				outcome = errNoPrefixGrant // lapsed/expired between scan and claim
				return list
			}
			a.Uses++
			stamp, serr := execApprovalStamp(a)
			if serr != nil {
				outcome = fmt.Errorf("governance: prefix grant %s could not be re-stamped after use (failing closed): %w", a.ID, serr)
				return list
			}
			a.ArtifactID = stamp
			list[i] = a
			used = a
			return list
		}
		outcome = errNoPrefixGrant // vanished between scan and claim
		return list
	}); err != nil {
		return err
	}
	if outcome == nil && used.ID != "" {
		max := used.MaxUses
		if max <= 0 {
			max = defaultPrefixMaxUses
		}
		store.recordAudit(used, "ask-protocol", true, fmt.Sprintf("prefix grant use %d/%d: %s", used.Uses, max, command))
	}
	return outcome
}

// livePrefixGrant reports whether a record is an approved, MAC-valid,
// unexpired prefix grant under its use cap.
func livePrefixGrant(a domain.Approval) bool {
	if a.Kind != ApprovalKindExecCommand || a.Status != "approved" || a.Scope != "prefix" {
		return false
	}
	if !execApprovalMACValid(a) {
		return false
	}
	if !a.ExpiresAt.IsZero() && time.Now().After(a.ExpiresAt) {
		return false
	}
	max := a.MaxUses
	if max <= 0 {
		max = defaultPrefixMaxUses
	}
	return a.Uses < max
}

// SetScopePrefix converts an APPROVED, MAC-valid exec-command approval into
// a session-scoped prefix grant (the "[2] accept prefix" answer). prefix ""
// defaults to the first two tokens of the bound command; a custom prefix
// must be a token-boundary prefix of that command (the human may narrow,
// never widen). CRITICAL-risk records are refused — they are never
// prefix-grantable — and installer-class commands get the tighter cap.
// The mutation is flock'd and re-stamped, so the widened record stays
// tamper-evident.
func SetScopePrefix(root, id, prefix, actor string) (domain.Approval, error) {
	store := NewFileStore(root)
	var out domain.Approval
	var outcome error
	if err := store.mutate(func(list []domain.Approval) []domain.Approval {
		for i := range list {
			if list[i].ID != id {
				continue
			}
			a := list[i]
			if a.Kind != ApprovalKindExecCommand {
				outcome = fmt.Errorf("governance: approval %s is not a command approval", id)
				return list
			}
			if a.Status != "approved" {
				outcome = fmt.Errorf("governance: approval %s is %s, not approved — decide it first, then grant the prefix", id, a.Status)
				return list
			}
			if !execApprovalMACValid(a) {
				outcome = fmt.Errorf("governance: approval %s failed integrity verification — decide it again", id)
				return list
			}
			if a.RiskLevel == domain.RiskCritical {
				outcome = errors.New("governance: CRITICAL commands are never prefix-grantable — accept once instead")
				return list
			}
			if prefix == "" {
				prefix = SuggestedPrefix(a.Command)
			}
			if prefix == "" || !PrefixMatches(a.Command, prefix) {
				outcome = fmt.Errorf("governance: prefix %q must be a token prefix of the approved command %q", prefix, a.Command)
				return list
			}
			max := defaultPrefixMaxUses
			if class, _ := execclassify.ClassifyDangerousCommand(a.Command); class == execclassify.DangerInstaller {
				max = installerPrefixMaxUses
			}
			a.Scope = "prefix"
			a.Prefix = prefix
			a.MaxUses = max
			a.ExpiresAt = time.Now().Add(prefixGrantTTL)
			stamp, serr := execApprovalStamp(a)
			if serr != nil {
				outcome = fmt.Errorf("governance: approval %s could not be re-stamped (failing closed): %w", id, serr)
				return list
			}
			a.ArtifactID = stamp
			list[i] = a
			out = a
			return list
		}
		outcome = fmt.Errorf("governance: approval %s not found", id)
		return list
	}); err != nil {
		return out, err
	}
	if outcome == nil && out.ID != "" {
		store.recordAudit(out, actor, true, fmt.Sprintf("prefix grant: prefix=%q max-uses=%d expires=%s", out.Prefix, out.MaxUses, out.ExpiresAt.Format(time.RFC3339)))
	}
	return out, outcome
}

// LivePrefixGrants returns every live (unexpired, under-cap) prefix grant
// in the root's store — `kern approval list` renders these as the active
// Ask-Protocol grants.
func LivePrefixGrants(root string) ([]domain.Approval, error) {
	records, err := NewFileStore(root).Load()
	if err != nil {
		return nil, err
	}
	var out []domain.Approval
	for _, a := range records {
		if livePrefixGrant(a) {
			out = append(out, a)
		}
	}
	return out, nil
}
