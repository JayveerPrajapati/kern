package govaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/JayveerPrajapati/kern/internal/storage"
)

// AutoRepairIfRekeyed converges a repo's audit chain after the machine key
// was destroyed and reminted — the reinstall flow: `kern uninstall` removes
// <UserConfigDir>/kern (including the audit-chain HMAC key), the reinstall
// mints a fresh key, and every previously written record then verifies
// against nothing. Called by `kern setup` so that install → setup → doctor
// is green with no manual `kern audit repair`.
//
// It only repairs the deterministic rekey SIGNATURE (see rekeySignature):
// entry 1 already fails, every entry that passes under the current key was
// appended after the key was minted, and every entry that fails predates
// the mint. Every other break shape — mid-chain tampering, the concurrent-
// writer corruption class, an entry-1 edit on an otherwise-verifying chain
// — keeps failing loudly for a human (`kern audit`; `kern audit repair`),
// because auto-repairing those would launder the tamper. Returns the number
// of entries re-chained (0 when no repair fired).
func AutoRepairIfRekeyed(root string) (int, error) {
	auditDir := filepath.Join(root, ".kern", "audit")
	if fi, err := os.Stat(auditDir); err != nil || !fi.IsDir() {
		return 0, nil // no audit store here: nothing to converge
	}
	l := NewAuditLog().
		WithStore(storage.NewLog(auditDir)).
		WithLockPath(filepath.Join(auditDir, ".lock")).
		WithMirror(MirrorFor(auditDir))
	if _, err := l.Replay(); err != nil {
		return 0, fmt.Errorf("rekey check: replay: %w", err)
	}
	if !l.rekeySignature() {
		return 0, nil
	}
	return l.RepairChain()
}

// rekeySignature reports whether the persisted chain carries the machine-
// rekey signature described in AutoRepairIfRekeyed. The threat it defends
// against: an in-workspace writer tampering with the audit store must not
// get its edits re-signed. The partition by recorded entry Timestamps
// (pre-mint failing / post-mint passing) blocks every same-key break shape;
// a writer who could forge the partition (backdated content, appended
// post-mint entries) could equally destroy the store outright — the chain
// is tamper-evident against workspace writers, not against determined
// destruction. The key file lives outside the workspace on purpose and its
// mint time is the one signal such a writer cannot produce.
func (l *AuditLog) rekeySignature() bool {
	keyPath, err := auditChainSecretPath()
	if err != nil {
		return false
	}
	keyMint := statMtime(keyPath) // zero when absent: the walk below mints it

	entries := func() []AuditEntry {
		l.mu.Lock()
		defer l.mu.Unlock()
		es, _ := l.storedEntriesLocked()
		return es
	}()
	if len(entries) == 0 {
		return false
	}

	// Single collected pass: the hash walk runs first (it lazily mints an
	// absent key), THEN the mint time is re-read for the zero case.
	type verdict struct {
		passes bool
		ts     time.Time
	}
	vs := make([]verdict, len(entries))
	prev := ""
	for i, e := range entries {
		vs[i] = verdict{
			passes: e.Hash == computeAuditHash(e, prev) ||
				e.Hash == computeAuditHashPlain(e, prev) ||
				e.Hash == computeAuditHashLegacy(e, prev),
			ts: e.Timestamp,
		}
		prev = e.Hash
	}
	if keyMint.IsZero() {
		keyMint = statMtime(keyPath)
		if keyMint.IsZero() {
			return false
		}
	}
	if vs[0].passes {
		return false // a chain whose first entry verifies is not a rekey
	}
	for _, v := range vs {
		if v.passes && !v.ts.After(keyMint) {
			return false // passing entries must postdate the fresh key
		}
		if !v.passes && v.ts.After(keyMint) {
			return false // failing entries must predate it
		}
	}
	return true
}

// statMtime returns a file's modification time, or the zero Time when the
// file is absent or unreadable.
func statMtime(p string) time.Time {
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}
