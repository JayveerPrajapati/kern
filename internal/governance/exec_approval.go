// Exec-approval integrity. The exec approval records in
// <root>/.kern/approvals.json live inside the client-governed workspace, so a
// governed client can write to the file directly. Two layers protect them:
//
//   - GRANT AUTHORITY (P0-3, 2026-10-28 adversarial audit): a decision is
//     signed with an Ed25519 key whose private seed is encrypted at rest with
//     an operator passphrase entered only at the interactive approval prompt
//     (<UserConfigDir>/kern/exec-approval-signing.json). Exec verification
//     uses the public key, so a same-user process that can read every file on
//     the machine still cannot forge an approval or a rejection.
//   - TAMPER DETECTION / COUNTER INTEGRITY: every exec record also carries an
//     HMAC over its full decision-relevant record, keyed by
//     <UserConfigDir>/kern/exec-approval.key. The HMAC still detects naive
//     edits, binds the pending record a human is about to decide, and covers
//     the mutable prefix-use counter (Uses is excluded from the signed
//     message because it is incremented non-interactively at exec time).
//     The HMAC key is same-user readable and is NOT a grant authority.
//
// The shared FileStore format is untouched: the HMAC stamp lives in the
// record's ArtifactID under an exec-only marker prefix, the signature lives in
// Approval.Signature, and the store's Decide re-stamps/re-signs only records
// that already carry the exec marker.

package governance

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/approvalsign"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/execapproval"
)

// Exec-approval stamps use the "exec-approval:" ArtifactID marker prefix
// (execapproval.StampPrefix). Other features sharing the approval file carry
// no stamp and are untouched by the exec verification layer; the pure
// integrity primitives (MAC framing, stamping, consumed ledger) live in
// internal/execapproval.

// execApprovalSecretPath returns the path of the exec-approval HMAC secret.
// It is a package-level func so tests can redirect it away from the real
// user config dir.
var execApprovalSecretPath = func() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kern", "exec-approval.key"), nil
}

// ExecSigner produces the interactive operator signature over one approval.
// It is a closure so the signing passphrase can be prompted once per command
// and never held in a global.
type ExecSigner func(a domain.Approval, rootID string) (string, error)

// execApprovalSigningKeyPath returns the encrypted Ed25519 signing key
// envelope. Package-level so tests can redirect it.
var execApprovalSigningKeyPath = func() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kern", "exec-approval-signing.json"), nil
}

// ExecApprovalSigningKeyPath exposes the operator signing-key path (doctor,
// docs, tests).
func ExecApprovalSigningKeyPath() (string, error) { return execApprovalSigningKeyPath() }

// NewExecSigner returns an ExecSigner backed by the operator key at the
// default path. pass is invoked only when an exec record is actually signed;
// non-exec approvals never prompt.
func NewExecSigner(pass approvalsign.PassphraseFunc) ExecSigner {
	return func(a domain.Approval, rootID string) (string, error) {
		p, err := execApprovalSigningKeyPath()
		if err != nil {
			return "", err
		}
		return approvalsign.SignApproval(p, a, rootID, pass)
	}
}

// execApprovalPublicKey loads the operator public key. Verification at exec
// time needs no passphrase and no private material.
func execApprovalPublicKey() (ed25519.PublicKey, error) {
	p, err := execApprovalSigningKeyPath()
	if err != nil {
		return nil, err
	}
	return approvalsign.PublicKey(p)
}

// execApprovalSignatureValid is the GRANT check: the record must carry an
// Ed25519 signature made by the operator key over this exact decision-relevant
// content and root identity.
func execApprovalSignatureValid(a domain.Approval, rootID string) bool {
	pub, err := execApprovalPublicKey()
	if err != nil {
		return false
	}
	return approvalsign.SignatureValid(a, pub, rootID)
}

// The exec secret is generated once per process and cached; the file
// persists across processes so approvals decided by a separate `kern approve`
// process verify in the server process.
var (
	execApprovalSecretOnce sync.Once
	execApprovalSecretKey  []byte
	execApprovalSecretErr  error
)

// execApprovalSecret returns the 32-byte HMAC secret, creating it once at
// <UserConfigDir>/kern/exec-approval.key (dir 0700, file 0600) on first use.
// The secret lives OUTSIDE the client-governed workspace, so a governed
// client cannot forge approval records. Any failure (unreadable/undersized
// existing file, unwritable dir, rand failure) is cached and returned so
// callers fail closed rather than minting weak integrity.
func execApprovalSecret() ([]byte, error) {
	execApprovalSecretOnce.Do(func() {
		p, err := execApprovalSecretPath()
		if err != nil {
			execApprovalSecretErr = err
			return
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			execApprovalSecretErr = err
			return
		}
		if data, rerr := os.ReadFile(p); rerr == nil {
			if len(data) != 32 {
				execApprovalSecretErr = fmt.Errorf("exec-approval secret %s is %d bytes, want 32; refusing to use it", p, len(data))
				return
			}
			execApprovalSecretKey = data
			return
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			execApprovalSecretErr = err
			return
		}
		// O_CREATE|O_EXCL so two processes racing the first-use generation
		// cannot cross-stamp: the loser re-reads the winner's key (gate-1
		// attempt-2 nit).
		f, werr := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if werr != nil {
			if errors.Is(werr, fs.ErrExist) && func() bool {
				data, rerr := os.ReadFile(p)
				if rerr == nil && len(data) == 32 {
					execApprovalSecretKey = data
					return true
				}
				return false
			}() {
				return
			}
			execApprovalSecretErr = werr
			return
		}
		if _, err := f.Write(key); err != nil {
			_ = f.Close()
			execApprovalSecretErr = err
			return
		}
		if err := f.Close(); err != nil {
			execApprovalSecretErr = err
			return
		}
		execApprovalSecretKey = key
	})
	return execApprovalSecretKey, execApprovalSecretErr
}

// execApprovalMAC computes the HMAC-SHA256 over the FULL decision-relevant
// record: ID, TaskID, Requester, Approver, Status, Reason, both timestamps,
// RiskLevel, PolicyIDs and EvidenceRefs — everything a human sees when
// deciding (gate-1 attempt-2: a MAC over only (TaskID, Status, ID) let a
// governed client tamper a pending record's Reason/EvidenceRefs into
// misleading the approver while the stamp stayed valid). ArtifactID is
// excluded because it carries the stamp itself.
//
// Framing (oracle-gate): every field and every list element is written as
// <4-byte big-endian byte length><raw bytes>, followed by a 0x00 field
// separator (0x01 for list items). Length-prefixing makes the serialization
// INJECTIVE: two different ways of splitting the same bytes across fields
// can never collide ("ab"|"c" vs "a"|"bc", or a field containing a raw 0x00
// vs two fields split at that byte, previously produced identical streams
// under bare separators, so a governed client could re-frame an approved
// record's fields and keep a valid stamp).
// execMACFormatVersion versions the execApprovalMAC serialization: v2 is
// length-prefixed framing (v1 was NUL-separated). Stamping it into the MAC
// input makes a future framing change distinguishable from tampering by
// construction (gate-7 nit).
// execApprovalMAC computes the raw HMAC over a record (root-bound, current
// framing).
func execApprovalMAC(a domain.Approval, rootID string) ([]byte, error) {
	key, err := execApprovalSecret()
	if err != nil {
		return nil, err
	}
	return execapproval.MAC(a, key, rootID)
}

// execRootIdentity canonicalizes a root for the exec-approval MAC (v4,
// from the 2026-10-28 campaign): symlink-resolved absolute path — same directory,
// one identity; a copied workspace, another. Empty root keeps "".
func execRootIdentity(root string) string {
	if root == "" {
		return ""
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return filepath.Clean(root)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return filepath.Clean(abs)
}

// execApprovalStamp builds the root-bound integrity stamp: marker prefix
// plus the hex HMAC over the full decision-relevant content.
func execApprovalStamp(a domain.Approval, rootID string) (string, error) {
	key, err := execApprovalSecret()
	if err != nil {
		return "", err
	}
	return execapproval.Stamp(a, key, rootID)
}

// execApprovalStampFrom returns the stored hex HMAC of an exec-stamped
// record, or "" when the record carries no exec stamp.
func execApprovalStampFrom(a domain.Approval) string {
	return execapproval.StampFrom(a)
}

// execApprovalMACValid verifies an exec-stamped record's HMAC against the
// canonical root identity: missing/malformed stamps, wrong keys, and
// foreign-root v4 stamps all deny (fail closed); legacy v2/v3 stamps still
// validate so an upgrade never orphans an in-flight approval. This is NOT a
// grant check: the HMAC key is same-user readable. Grants require
// execApprovalSignatureValid (P0-3).
func execApprovalMACValid(a domain.Approval, rootID string) bool {
	key, err := execApprovalSecret()
	if err != nil {
		return false
	}
	return execapproval.MACValid(a, key, rootID)
}

// stampExecApproval binds a freshly created approval to the exec secret: it
// sets the root-bound HMAC stamp and upserts the record into
// <root>/.kern/approvals.json, so the persisted record is unforgeable by a
// client who only controls the workspace (R1) and unportable to another
// workspace. Empty root = in-memory only (nothing to forge); any
// secret/persistence failure fails closed.
func stampExecApproval(root string, a *domain.Approval) error {
	stamp, err := execApprovalStamp(*a, execRootIdentity(root))
	if err != nil {
		return fmt.Errorf("governance: exec approval %s could not be integrity-stamped: %w", a.ID, err)
	}
	a.ArtifactID = stamp
	if root != "" {
		if err := NewFileStore(root).AddPending(*a); err != nil {
			return fmt.Errorf("governance: exec approval %s integrity stamp could not be persisted: %w", a.ID, err)
		}
	}
	return nil
}

// execApprovedFor returns the operator-signed approved approval bound to the
// given command key, or nil. Only records carrying a valid Ed25519 operator
// signature can grant execution; HMAC-only (pre-P0-3) and forged records are
// denied, and records whose ID is in the consumed ledger are denied too (R2d
// replay resistance: a client that snapshots an approved record and re-inserts
// it cannot reauthorize it). An empty root has no persisted store to grant
// from.
func execApprovedFor(root, key string) *domain.Approval {
	if root == "" {
		return nil
	}
	approvals, err := NewFileStore(root).Load()
	if err != nil {
		return nil
	}
	rid := execRootIdentity(root)
	consumed, cerr := execConsumedIDs()
	if cerr != nil {
		// Fail closed: an unverifiable consumed ledger must not silently
		// re-enable consumed approvals.
		return nil
	}
	for i := range approvals {
		a := approvals[i]
		// Ask Protocol (P1): a prefix-scoped grant is NOT a single-use
		// approval — treating it as one let claimExecApproval consume (and
		// DELETE) the grant record on the first covered command, silently
		// revoking the "rest of session" answer a human gave. Prefix grants
		// are claimed only by consumePrefixGrantUse, which counts uses
		// instead of removing the record.
		// The signature is the grant authority (P0-3): a readable HMAC key
		// must never be enough to forge an approval. Prefix grants also need
		// the HMAC because it protects the mutable use counter.
		if a.TaskID == key && a.Status == "approved" && a.Scope != "prefix" && !consumed[a.ID] && execApprovalSignatureValid(a, rid) {
			return &a
		}
	}
	return nil
}

// defaultExecRejectionTTL is how long a human REJECTED exec approval keeps
// blocking byte-identical retries of its command (rejection stickiness).
// Rejections must be sticky — otherwise an agent can spam fresh approvals
// for a command a human already refused — but "permanent until an operator
// edits approvals.json by hand" converts a routine "not now" into a
// file-surgery chore. The TTL bounds both: within it, retries are
// hard-denied; after it, the rejection lapses and the next attempt mints a
// fresh approval a human STILL has to decide (the gate re-arms, it does not
// open). Operators can re-open a still-sticky command immediately with
// `kern approval clear <id>`; KERN_REJECTION_TTL overrides the default.
const defaultExecRejectionTTL = 7 * 24 * time.Hour

// execRejectionTTL returns the rejection stickiness window: the
// KERN_REJECTION_TTL duration override (e.g. "72h", "30m") when it parses
// to a positive duration, else the 7-day default. An unparseable or
// non-positive value falls back to the default rather than disabling
// stickiness — expiry must never be accidentally switched off by a typo.
func execRejectionTTL() time.Duration {
	if v := strings.TrimSpace(os.Getenv("KERN_REJECTION_TTL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultExecRejectionTTL
}

// execRejectionTTLText renders the rejection TTL for deny messages: whole
// days as a compact "7d", anything else as the Go duration string ("72h0m0s"
// stays exact for non-day-granular overrides).
func execRejectionTTLText() string {
	d := execRejectionTTL()
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	return d.String()
}

// execRejectedFor returns the most recent persisted, MAC-verified approval
// record for the given command key that was REJECTED by a human within the
// rejection TTL (rejection stickiness), or nil when no unexpired rejection
// blocks the key. Only records carrying a valid operator signature count, so
// a forged rejection cannot deny execution (P0-3). An unreadable store returns
// nil — the normal mint path below
// still gates the command. A rejection older than the TTL is skipped
// (auto-expiry): the deny side never outlives the human's decision by more
// than the configured window, and the newest rejection wins when several
// records exist for the same key.
func execRejectedFor(root, key string) *domain.Approval {
	if root == "" {
		return nil
	}
	approvals, err := NewFileStore(root).Load()
	if err != nil {
		return nil
	}
	ttl := execRejectionTTL()
	var match *domain.Approval
	rid := execRootIdentity(root)
	for i := range approvals {
		a := approvals[i]
		// A rejection blocks execution for its TTL, so it must be
		// operator-signed too: a forged HMAC-only rejection would be a
		// denial-of-service primitive (P0-3).
		if a.TaskID != key || a.Status != "rejected" || !execApprovalSignatureValid(a, rid) {
			continue
		}
		decided := resolvedAt(a)
		if time.Since(decided) > ttl {
			continue
		}
		if match == nil || decided.After(resolvedAt(*match)) {
			match = &a
		}
	}
	return match
}

// execConsumedLedgerPath returns the path of the MAC'd consumed-approval
// ledger, stored OUTSIDE the client-governed workspace next to the HMAC
// secret. It is a package-level func so tests can redirect it.
var execConsumedLedgerPath = func() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kern", "exec-consumed.log"), nil
}

// appendConsumedID records a consumed approval ID in the ledger, MAC'd with
// the exec secret. The ledger lives outside the workspace, so a governed
// client cannot forge entries, delete entries, or replay old ones to any
// effect (a replayed line is a duplicate denial). Append-only small writes
// under O_APPEND are atomic on POSIX.
func appendConsumedID(id string) error {
	key, err := execApprovalSecret()
	if err != nil {
		return err
	}
	p, err := execConsumedLedgerPath()
	if err != nil {
		return err
	}
	return execapproval.AppendConsumedID(p, key, id)
}

// execConsumedIDs returns the set of approval IDs already consumed, verifying
// each entry's HMAC against the exec secret. A missing ledger is an empty
// set; an unreadable or corrupt ledger is an error so callers fail closed
// rather than treating tampered state as "nothing consumed" (R2d).
//
// Ops note (gate-1 attempt-3): a torn final line (crash mid-append) makes the
// ledger unreadable and ALL exec grants fail closed until the file is
// repaired or removed. Reset: rm <UserConfigDir>/kern/exec-consumed.log —
// this also resets replay protection, so commands mid-flight need re-approval.
func execConsumedIDs() (map[string]bool, error) {
	p, err := execConsumedLedgerPath()
	if err != nil {
		return nil, err
	}
	key, err := execApprovalSecret()
	if err != nil {
		return nil, err
	}
	return execapproval.ConsumedIDs(p, key)
}

// claimExecApproval consumes a granted approval atomically (R2): the record
// is removed from the store in ONE flock'd load-modify-save mutation
// (FileStore.Consume — only one concurrent claim can win), and the ID is
// recorded in the MAC'd consumed ledger outside the workspace so a
// snapshot-and-reinsert replay cannot reauthorize it (R2d). The claim is
// reported as (claimed, error): claimed=false with nil error means another
// concurrent execution already consumed it.
func claimExecApproval(root, id string) (bool, error) {
	claimed, err := NewFileStore(root).Consume(id)
	if err != nil {
		return false, err
	}
	if !claimed {
		return false, nil
	}
	if lerr := appendConsumedID(id); lerr != nil {
		return false, lerr
	}
	return true, nil
}

// ApprovalIntegrityOK reports whether an approval record's integrity is
// verifiable at the given project root. Records without an exec stamp
// (other features sharing the approvals file) are OK by definition — they
// do not participate in the exec integrity layer. Exec-stamped records
// must carry a valid HMAC over their full decision-relevant content,
// bound to the root's canonical identity (v4): a record copied from
// another workspace fails here. `kern approve` listings flag records
// failing this check, and FileStore.Decide refuses to decide them, so a
// governed client cannot tamper a pending record into misleading a human
// approver (gate-1 attempt-2).
func ApprovalIntegrityOK(a domain.Approval, root string) bool {
	rid := execRootIdentity(root)
	if a.Signature != "" || a.Status == "approved" {
		// An approved exec record is a grant only when the operator signature
		// verifies. A v4 HMAC-only approved record (pre-P0-3) is no longer a
		// grant: the HMAC key is same-user-readable and forgeable.
		return execApprovalSignatureValid(a, rid)
	}
	if execApprovalStampFrom(a) == "" {
		return true
	}
	// Pending records still carry the HMAC so a tampered record is flagged
	// before a human decides it (the HMAC is not a grant authority).
	return execApprovalMACValid(a, rid)
}
