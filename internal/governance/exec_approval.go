// Exec-approval integrity (R1): the exec approval records in
// <root>/.kern/approvals.json live inside the client-governed workspace, so a
// governed client can write to the file directly. Every record the exec path
// persists carries an HMAC over its full decision-relevant record (ID, task,
// requester, approver, status, reason, timestamps, risk level, policies,
// evidence — everything a human sees when deciding), keyed
// by a random 32-byte secret stored OUTSIDE the workspace at
// <UserConfigDir>/kern/exec-approval.key (dir 0700, file 0600). Grant
// verification (execApprovedFor / execApprovalMACValid) denies any record
// whose HMAC is missing, malformed, or stale — forged and legacy records can
// never self-approve. The shared FileStore format is untouched: the stamp
// lives in the record's ArtifactID under an exec-only marker prefix, and the
// store's Decide only re-stamps records that already carry the marker.

package governance

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// execApprovalStampPrefix marks an approval record created by the exec
// approval path as integrity-stamped: ArtifactID = "exec-approval:<hex
// HMAC>". Other features sharing the approval file carry no stamp and are
// untouched by the exec verification layer.
const execApprovalStampPrefix = "exec-approval:"

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
const execMACFormatVersion = 2

func execApprovalMAC(a domain.Approval) ([]byte, error) {
	key, err := execApprovalSecret()
	if err != nil {
		return nil, err
	}
	h := hmac.New(sha256.New, key)
	// The format version is the first MAC input so framing changes and
	// tampering are distinguishable by construction.
	_, _ = h.Write([]byte{execMACFormatVersion})
	// prefixed writes one length-prefixed value: the 4-byte big-endian byte
	// length followed by the raw bytes. The length prefix (not the separator)
	// is what makes field boundaries unambiguous.
	prefixed := func(s string) {
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(s)))
		_, _ = h.Write(lenBuf[:])
		_, _ = h.Write([]byte(s))
	}
	field := func(s string) {
		prefixed(s)
		_, _ = h.Write([]byte{0})
	}
	list := func(xs []string) {
		for _, x := range xs {
			prefixed(x)
			_, _ = h.Write([]byte{1})
		}
		_, _ = h.Write([]byte{0})
	}
	field(a.ID)
	field(a.TaskID)
	field(a.Requester)
	field(a.Approver)
	field(a.Status)
	field(a.Reason)
	field(a.RequestedAt.UTC().Format(time.RFC3339Nano))
	if a.DecidedAt != nil {
		field(a.DecidedAt.UTC().Format(time.RFC3339Nano))
	} else {
		field("")
	}
	field(string(a.RiskLevel))
	list(a.PolicyIDs)
	list(a.EvidenceRefs)
	return h.Sum(nil), nil
}

// execApprovalStamp builds the integrity stamp for a record: the marker
// prefix plus the hex HMAC over its exact (TaskID, Status, ID).
func execApprovalStamp(a domain.Approval) (string, error) {
	mac, err := execApprovalMAC(a)
	if err != nil {
		return "", err
	}
	return execApprovalStampPrefix + hex.EncodeToString(mac), nil
}

// execApprovalStampFrom returns the stored hex HMAC of an exec-stamped
// record, or "" when the record carries no exec stamp.
func execApprovalStampFrom(a domain.Approval) string {
	if strings.HasPrefix(a.ArtifactID, execApprovalStampPrefix) {
		return strings.TrimPrefix(a.ArtifactID, execApprovalStampPrefix)
	}
	return ""
}

// execApprovalMACValid verifies an exec-stamped record's HMAC over its exact
// (TaskID, Status, ID). A missing stamp, malformed stamp, wrong-length MAC,
// unavailable secret, or mismatch all deny (fail closed) — forged and legacy
// records can never grant execution.
func execApprovalMACValid(a domain.Approval) bool {
	storedHex := execApprovalStampFrom(a)
	if storedHex == "" {
		return false
	}
	stored, err := hex.DecodeString(storedHex)
	if err != nil || len(stored) != sha256.Size {
		return false
	}
	want, err := execApprovalMAC(a)
	if err != nil {
		return false
	}
	return hmac.Equal(stored, want)
}

// stampExecApproval binds a freshly created approval to the exec secret: it
// sets the HMAC stamp over (TaskID, Status, ID) and upserts the stamped
// record into <root>/.kern/approvals.json, so the persisted record is
// unforgeable by a client who only controls the workspace (R1). An empty
// root means the approval is in-memory only (nothing persisted to forge) and
// the stamp is skipped. Any secret/persistence failure fails closed.
func stampExecApproval(root string, a *domain.Approval) error {
	stamp, err := execApprovalStamp(*a)
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

// execApprovedFor returns the MAC-verified approved approval bound to the
// given command key, or nil. Only records carrying a valid HMAC over their
// full decision-relevant content can grant execution; forged/legacy records
// are denied (R1), and records whose ID is in the consumed ledger are denied
// too (R2d replay resistance: a client that snapshots an approved record and
// re-inserts it cannot reauthorize it). An empty root has no persisted store
// to grant from.
func execApprovedFor(root, key string) *domain.Approval {
	if root == "" {
		return nil
	}
	approvals, err := NewFileStore(root).Load()
	if err != nil {
		return nil
	}
	consumed, cerr := execConsumedIDs()
	if cerr != nil {
		// Fail closed: an unverifiable consumed ledger must not silently
		// re-enable consumed approvals.
		return nil
	}
	for i := range approvals {
		a := approvals[i]
		if a.TaskID == key && a.Status == "approved" && !consumed[a.ID] && execApprovalMACValid(a) {
			return &a
		}
	}
	return nil
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
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(id))
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s %s\n", id, hex.EncodeToString(mac.Sum(nil)))
	return err
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
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	key, err := execApprovalSecret()
	if err != nil {
		return nil, err
	}
	consumed := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("exec consumed ledger %s: corrupt line %q", p, line)
		}
		stored, derr := hex.DecodeString(parts[1])
		if derr != nil || len(stored) != sha256.Size {
			return nil, fmt.Errorf("exec consumed ledger %s: corrupt entry", p)
		}
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(parts[0]))
		if !hmac.Equal(stored, mac.Sum(nil)) {
			return nil, fmt.Errorf("exec consumed ledger %s: entry fails integrity check", p)
		}
		consumed[parts[0]] = true
	}
	return consumed, nil
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
// verifiable. Records without an exec stamp (other features sharing the
// approvals file) are OK by definition — they do not participate in the exec
// integrity layer. Exec-stamped records must carry a valid HMAC over their
// full decision-relevant content. `kern approve` listings flag records
// failing this check, and FileStore.Decide refuses to decide them, so a
// governed client cannot tamper a pending record into misleading a human
// approver (gate-1 attempt-2).
func ApprovalIntegrityOK(a domain.Approval) bool {
	if execApprovalStampFrom(a) == "" {
		return true
	}
	return execApprovalMACValid(a)
}
