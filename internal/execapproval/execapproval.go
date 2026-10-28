// Package execapproval contains the pure integrity primitives for exec
// approvals: Ed25519 operator signatures (the grant authority — see sign.go
// and keystore.go), injective HMAC framing for pending-record tamper detection
// and the mutable prefix-use counter, and the MAC-verified consumed-approval
// ledger. Callers own secret, signing-key and ledger-path resolution; this
// package never reads process-global configuration.
package execapproval

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// StampPrefix marks an approval record created by the exec approval path as
// integrity-stamped: ArtifactID = "exec-approval:<hex HMAC>".
const StampPrefix = "exec-approval:"

// macFormatVersion versions the MAC serialization: v4 binds the approval
// to a canonical ROOT IDENTITY (first field after the version byte), so a
// record copied to a different workspace (a copied .kern/approvals.json —
// 2026-10-28 campaign) cannot grant there. v3 added the
// Ask-Protocol grant fields; v2 was the pre-ask-protocol framing. Records
// stamped by an older binary must keep validating — a legacy stamp is not
// a forgery — so MACValid accepts the current AND both legacy framings;
// MAC/Stamp always stamp with the current version only. Root identity
// canonicalization is the caller's (governance) job; an empty rootID is a
// distinct identity from every real root.
const macFormatVersion = 4
const macLegacyVersion = 3
const macLegacyVersion2 = 2

// MAC computes an HMAC-SHA256 over the full decision-relevant approval
// record, bound to the canonical root identity. Length-prefixed field and
// list framing makes the representation injective, so field-boundary
// re-framing cannot preserve a valid stamp.
func MAC(a domain.Approval, secret []byte, rootID string) ([]byte, error) {
	return macCompute(a, secret, macFormatVersion, rootID)
}

// macCompute computes the HMAC under a specific format version. v4 hashes
// the root identity first; v3 (legacy) hashes the ask-protocol grant fields
// after the evidence list; v2 (legacy) stops before them — those are
// exactly the byte streams pre-v4 binaries hashed, so their stamps keep
// verifying.
func macCompute(a domain.Approval, secret []byte, version byte, rootID string) ([]byte, error) {
	if len(secret) != sha256.Size {
		return nil, fmt.Errorf("exec approval secret is %d bytes, want %d", len(secret), sha256.Size)
	}
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write([]byte{version})
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
	if version >= 4 {
		field(rootID)
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
	if version < 3 {
		// v2 framing ends here — grant fields did not exist.
		return h.Sum(nil), nil
	}
	// Ask-Protocol grant fields (v3): scope, prefix, command, expiry and the
	// use counters are decision-relevant — a governed client flipping scope
	// "once"→"prefix", widening Prefix, or resetting Uses/MaxUses must break
	// the stamp. Formatted with the same length-prefixed framing.
	field(a.Scope)
	field(a.Prefix)
	field(a.Command)
	if a.ExpiresAt.IsZero() {
		field("")
	} else {
		field(a.ExpiresAt.UTC().Format(time.RFC3339Nano))
	}
	field(strconv.Itoa(a.MaxUses))
	field(strconv.Itoa(a.Uses))
	return h.Sum(nil), nil
}

// Stamp builds the integrity stamp for an approval record, bound to the
// canonical root identity.
func Stamp(a domain.Approval, secret []byte, rootID string) (string, error) {
	mac, err := MAC(a, secret, rootID)
	if err != nil {
		return "", err
	}
	return StampPrefix + hex.EncodeToString(mac), nil
}

// StampFrom returns the stored hex HMAC of an exec-stamped record, or ""
// when the record carries no exec stamp.
func StampFrom(a domain.Approval) string {
	if strings.HasPrefix(a.ArtifactID, StampPrefix) {
		return strings.TrimPrefix(a.ArtifactID, StampPrefix)
	}
	return ""
}

// MACValid verifies an exec-stamped record's HMAC against the canonical
// root identity the caller resolved. Missing stamps, malformed stamps,
// wrong-length MACs, invalid secrets, and mismatches all return false. A
// stamp made under ANY known format version (current or legacy) validates
// — a legacy-stamped record is not a forgery — but only the CURRENT (v4,
// root-bound) stamp validates at a DIFFERENT root identity than the record
// was stamped for: a v4 record copied to another workspace fails here.
func MACValid(a domain.Approval, secret []byte, rootID string) bool {
	storedHex := StampFrom(a)
	if storedHex == "" {
		return false
	}
	stored, err := hex.DecodeString(storedHex)
	if err != nil || len(stored) != sha256.Size {
		return false
	}
	for _, version := range []byte{macFormatVersion, macLegacyVersion, macLegacyVersion2} {
		want, err := macCompute(a, secret, version, rootID)
		if err == nil && hmac.Equal(stored, want) {
			return true
		}
	}
	return false
}

// AppendConsumedID records a consumed approval ID in the caller-owned ledger,
// MAC'd with the caller-owned secret. Append-only small writes under O_APPEND
// are atomic on POSIX.
func AppendConsumedID(path string, secret []byte, id string) error {
	if len(secret) != sha256.Size {
		return fmt.Errorf("exec approval secret is %d bytes, want %d", len(secret), sha256.Size)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(id))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = fmt.Fprintf(f, "%s %s\n", id, hex.EncodeToString(mac.Sum(nil)))
	return err
}

// ConsumedIDs returns the set of approval IDs already consumed, verifying each
// entry's HMAC. A missing ledger is an empty set; an unreadable or corrupt
// ledger is an error so callers can fail closed.
func ConsumedIDs(path string, secret []byte) (map[string]bool, error) {
	if len(secret) != sha256.Size {
		return nil, fmt.Errorf("exec approval secret is %d bytes, want %d", len(secret), sha256.Size)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]bool{}, nil
		}
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
			return nil, fmt.Errorf("exec consumed ledger %s: corrupt line %q", path, line)
		}
		stored, derr := hex.DecodeString(parts[1])
		if derr != nil || len(stored) != sha256.Size {
			return nil, fmt.Errorf("exec consumed ledger %s: corrupt entry", path)
		}
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write([]byte(parts[0]))
		if !hmac.Equal(stored, mac.Sum(nil)) {
			return nil, fmt.Errorf("exec consumed ledger %s: entry fails integrity check", path)
		}
		consumed[parts[0]] = true
	}
	return consumed, nil
}
