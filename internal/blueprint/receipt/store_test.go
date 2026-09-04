package receipt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStoreSaveGet: Save writes <receipt_id>.json and Get reads it back with
// the signature intact.
func TestStoreSaveGet(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	r := Generate(testResult(), dir, "main", "HEAD", "auditabc", "")
	if err := s.Save(r); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Get(r.ReceiptID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ReceiptID != r.ReceiptID || got.AuditChainHash != "auditabc" || got.Status != "PASS" {
		t.Errorf("round-trip receipt = %+v", got)
	}
	if got.Signature != r.Signature {
		t.Error("signature did not round-trip")
	}
	if err := got.Verify(); err != nil {
		t.Fatalf("stored receipt must verify: %v", err)
	}

	// File must be at .blueprint/receipts/<id>.json.
	if _, err := os.Stat(filepath.Join(dir, ".blueprint", "receipts", r.ReceiptID+".json")); err != nil {
		t.Errorf("receipt file not at expected path: %v", err)
	}
}

// TestStoreNotFound: a missing id and an empty store map to ErrNotFound.
func TestStoreNotFound(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Get("bogus"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(bogus) err = %v, want ErrNotFound", err)
	}
	if _, err := s.Latest(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Latest on empty store err = %v, want ErrNotFound", err)
	}
}

// TestStoreLatestByGeneratedAt: Latest returns the most recent receipt by
// GeneratedAt, not by filename order.
func TestStoreLatestByGeneratedAt(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	old := Generate(testResult(), dir, "main", "HEAD", "oldhash", "")
	old.GeneratedAt = old.GeneratedAt.Add(-time.Hour)
	newer := Generate(testResult(), dir, "main", "HEAD", "newhash", "")
	if err := s.Save(old); err != nil {
		t.Fatalf("Save old: %v", err)
	}
	if err := s.Save(newer); err != nil {
		t.Fatalf("Save newer: %v", err)
	}

	latest, err := s.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.ReceiptID != newer.ReceiptID {
		t.Errorf("Latest = %s, want %s (most recent GeneratedAt)", latest.ReceiptID, newer.ReceiptID)
	}
	if latest.AuditChainHash != "newhash" {
		t.Errorf("Latest audit_chain_hash = %q, want newhash", latest.AuditChainHash)
	}
}

// TestStoreDetectsTamperAfterSave: modifying the receipt JSON on disk makes
// Get fail verification (the receipt is tampered), while Latest still
// surfaces the tampered receipt so the verify command reports exit 2 rather
// than "not found" (exit 3).
func TestStoreDetectsTamperAfterSave(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	r := Generate(testResult(), dir, "main", "HEAD", "auditabc", "")
	if err := s.Save(r); err != nil {
		t.Fatalf("Save: %v", err)
	}

	p := filepath.Join(dir, ".blueprint", "receipts", r.ReceiptID+".json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read saved receipt: %v", err)
	}
	tampered := strings.Replace(string(data), `"status": "PASS"`, `"status": "FAIL"`, 1)
	if tampered == string(data) {
		t.Fatal("test setup: status field not found in saved receipt")
	}
	if err := os.WriteFile(p, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write tampered receipt: %v", err)
	}

	if _, err := s.Get(r.ReceiptID); err == nil {
		t.Fatal("Get on tampered receipt = nil error, want verify failure")
	}
	latest, err := s.Latest()
	if err != nil {
		t.Fatalf("Latest on tampered receipt: %v", err)
	}
	if latest.ReceiptID != r.ReceiptID {
		t.Errorf("Latest = %s, want %s (tampered receipt must still surface)", latest.ReceiptID, r.ReceiptID)
	}
	if err := latest.Verify(); err == nil {
		t.Fatal("Latest returned a receipt whose signature verifies — tamper undetected")
	}
}
