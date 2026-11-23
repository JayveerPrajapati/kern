package govaudit

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/storage"
)

// rekeyTestChain writes n entries with the CURRENT secret and old
// timestamps into a fresh root's audit store, returning the root.
func rekeyTestChain(t *testing.T, n int) string {
	t.Helper()
	root := t.TempDir()
	auditDir := filepath.Join(root, ".kern", "audit")
	old := time.Now().Add(-2 * time.Hour)
	l := NewAuditLog().WithStore(storage.NewLog(auditDir)).WithLockPath(filepath.Join(auditDir, ".lock"))
	for i := 0; i < n; i++ {
		e := entry("", "agent")
		e.Timestamp = old
		l.Record(e)
	}
	return root
}

// rekeyVerified replays the root's chain and reports (firstBroken, verified)
// — firstBroken < 0 means the chain verifies end to end.
func rekeyVerified(t *testing.T, root string) (int, int) {
	t.Helper()
	auditDir := filepath.Join(root, ".kern", "audit")
	l := NewAuditLog().WithStore(storage.NewLog(auditDir)).WithLockPath(filepath.Join(auditDir, ".lock"))
	if _, err := l.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	fb, v := l.VerifyChainReport()
	return fb, v
}

// TestAutoRepairIfRekeyedAfterKeyRotation pins the reinstall flow: entries
// written under a destroyed key verify against the reminted one, and
// AutoRepairIfRekeyed re-chains them (this is the operator's uninstall →
// install → setup sequence, minus the manual `kern audit repair`).
func TestAutoRepairIfRekeyedAfterKeyRotation(t *testing.T) {
	overrideAuditChainSecret(t) // key A
	root := rekeyTestChain(t, 3)
	if fb, _ := rekeyVerified(t, root); fb >= 0 {
		t.Fatalf("precondition: chain must be broken after key rotation (firstBroken=%d)", fb)
	}

	overrideAuditChainSecret(t) // key B — the rotation
	n, err := AutoRepairIfRekeyed(root)
	if err != nil {
		t.Fatalf("AutoRepairIfRekeyed: %v", err)
	}
	if n != 3 {
		t.Fatalf("all 3 entries must be re-chained, got %d", n)
	}
	if fb, _ := rekeyVerified(t, root); fb >= 0 {
		t.Fatalf("chain must verify after auto-repair (firstBroken=%d)", fb)
	}
}

// TestAutoRepairIfRekeyedMixedChain pins rotation plus post-rotation
// appends: the old entries fail, the new ones pass under the fresh key —
// still the rekey signature, still auto-repaired.
func TestAutoRepairIfRekeyedMixedChain(t *testing.T) {
	overrideAuditChainSecret(t) // key A
	root := rekeyTestChain(t, 2)

	overrideAuditChainSecret(t) // key B — the rotation
	auditDir := filepath.Join(root, ".kern", "audit")
	fresh := time.Now().Add(time.Hour) // postdates the mint deterministically
	l := NewAuditLog().WithStore(storage.NewLog(auditDir)).WithLockPath(filepath.Join(auditDir, ".lock"))
	e := entry("", "post-rotation")
	e.Timestamp = fresh
	l.Record(e)

	n, err := AutoRepairIfRekeyed(root)
	if err != nil {
		t.Fatalf("AutoRepairIfRekeyed: %v", err)
	}
	if n < 2 {
		t.Fatalf("the old entries must be re-chained, got %d", n)
	}
	if fb, _ := rekeyVerified(t, root); fb >= 0 {
		t.Fatalf("chain must verify after auto-repair (firstBroken=%d)", fb)
	}
}

// TestAutoRepairIfRekeyedRefusesMidChainTamper pins the fail-loud side: a
// corrupted middle entry under the CURRENT key (entries before it verify)
// must NOT be auto-repaired — that shape is indistinguishable from
// tampering and stays for a human (`kern audit repair`).
func TestAutoRepairIfRekeyedRefusesMidChainTamper(t *testing.T) {
	overrideAuditChainSecret(t)
	root := rekeyTestChain(t, 3)
	auditDir := filepath.Join(root, ".kern", "audit")

	// Corrupt the SECOND entry's content (same-key tamper shape).
	l := NewAuditLog().WithStore(storage.NewLog(auditDir)).WithLockPath(filepath.Join(auditDir, ".lock"))
	if _, err := l.Replay(); err != nil {
		t.Fatal(err)
	}
	second := l.All()[1]
	second.Action = "tampered-action"
	data, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.NewLog(auditDir).Put(context.Background(), "audit-"+second.ID, data); err != nil {
		t.Fatal(err)
	}

	n, err := AutoRepairIfRekeyed(root)
	if err != nil {
		t.Fatalf("AutoRepairIfRekeyed: %v", err)
	}
	if n != 0 {
		t.Fatalf("mid-chain tamper must not be auto-repaired, re-chained %d entries", n)
	}
	if fb, _ := rekeyVerified(t, root); fb < 0 {
		t.Fatal("the tampered chain must still fail verification (auto-repair must not have laundered it)")
	}
}

// TestAutoRepairIfRekeyedRefusesEntryOneTamper pins the sharpest case: an
// edited FIRST entry on an otherwise-verifying chain under the CURRENT key.
// The passing suffix predates the key mint, so the signature must refuse —
// auto-repair here would launder a workspace-level tamper.
func TestAutoRepairIfRekeyedRefusesEntryOneTamper(t *testing.T) {
	overrideAuditChainSecret(t)
	root := rekeyTestChain(t, 3)
	auditDir := filepath.Join(root, ".kern", "audit")

	l := NewAuditLog().WithStore(storage.NewLog(auditDir)).WithLockPath(filepath.Join(auditDir, ".lock"))
	if _, err := l.Replay(); err != nil {
		t.Fatal(err)
	}
	first := l.All()[0]
	first.Hash = strings.Repeat("0", 64)
	data, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.NewLog(auditDir).Put(context.Background(), "audit-"+first.ID, data); err != nil {
		t.Fatal(err)
	}

	n, err := AutoRepairIfRekeyed(root)
	if err != nil {
		t.Fatalf("AutoRepairIfRekeyed: %v", err)
	}
	if n != 0 {
		t.Fatalf("entry-1 tamper must not be auto-repaired, re-chained %d entries", n)
	}
}

// TestAutoRepairIfRekeyedHealthyNoop pins the no-op: a verifying chain is
// never touched.
func TestAutoRepairIfRekeyedHealthyNoop(t *testing.T) {
	overrideAuditChainSecret(t)
	root := rekeyTestChain(t, 3)
	if fb, _ := rekeyVerified(t, root); fb >= 0 {
		t.Fatalf("precondition: chain must verify (firstBroken=%d)", fb)
	}
	n, err := AutoRepairIfRekeyed(root)
	if err != nil {
		t.Fatalf("AutoRepairIfRekeyed: %v", err)
	}
	if n != 0 {
		t.Fatalf("healthy chain must not be re-chained, got %d", n)
	}
}

// TestAutoRepairIfRekeyedNoStoreNoop pins the no-store case (a repo that
// never had an audit trail — most repos): a clean nil-style no-op.
func TestAutoRepairIfRekeyedNoStoreNoop(t *testing.T) {
	overrideAuditChainSecret(t)
	root := t.TempDir() // no .kern/audit
	n, err := AutoRepairIfRekeyed(root)
	if err != nil || n != 0 {
		t.Fatalf("absent store must be a no-op, got n=%d err=%v", n, err)
	}
}
