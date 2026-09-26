package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/evidence"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// evidenceFixtureRoot creates a temp repo with a Go file and builds an index
// over it, mirroring internal/evidence's fixtureRoot so the bundle fixture is
// generated exactly like a real export sees it.
func evidenceFixtureRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "public", "a.go")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte("package public\n\nfunc PublicA() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	_ = ix // the handler builds its own index via loadIndex
	return dir
}

// evidenceBundleFile generates a valid signed-evidence bundle for root and
// writes its JSON to a fresh temp file, returning the path.
func evidenceBundleFile(t *testing.T, root string) string {
	t.Helper()
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	b, err := evidence.Generate(root, "default", "T-1", ix)
	if err != nil {
		t.Fatalf("generate bundle: %v", err)
	}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	path := filepath.Join(t.TempDir(), "bundle.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	return path
}

// TestHandleEvidence_VerifyIntact: an unmodified bundle verifies clean — the
// tamper seal is intact and the report carries the bundle id and schema.
func TestHandleEvidence_VerifyIntact(t *testing.T) {
	t.Parallel()
	s := newTestServer()
	root := evidenceFixtureRoot(t)
	bundlePath := evidenceBundleFile(t, root)

	res, err := s.handleEvidence(context.Background(), map[string]any{
		"action": "verify",
		"file":   bundlePath,
	})
	if err != nil {
		t.Fatalf("verify intact bundle error: %v", err)
	}
	var rep EvidenceVerifyReport
	if err := json.Unmarshal([]byte(res), &rep); err != nil {
		t.Fatalf("decode report: %v; raw=%s", err, res)
	}
	if !rep.Valid {
		t.Errorf("expected valid=true for intact bundle")
	}
	if rep.TamperSeal != "intact" {
		t.Errorf("expected tamper_seal=intact, got %q", rep.TamperSeal)
	}
	if rep.BundleID == "" {
		t.Errorf("expected non-empty bundle_id in report")
	}
	if rep.SchemaVersion != evidence.SchemaVersion {
		t.Errorf("expected schema_version %d, got %d", evidence.SchemaVersion, rep.SchemaVersion)
	}
	if !strings.Contains(rep.Detail, "VALID") {
		t.Errorf("expected VALID detail, got %q", rep.Detail)
	}
	// The fixture bundle is unsigned (digest-only): no signature to
	// attest, so no trust anchor may be claimed.
	if rep.SelfAttested || rep.TrustAnchor != "" {
		t.Errorf("unsigned digest-only bundle must not claim a trust anchor, got %+v", rep)
	}
}

// TestHandleEvidence_VerifyTrustAnchor: a SIGNED bundle verified without
// expect_fingerprint reports SELF-ATTESTED (the key is bundle-embedded);
// verified with the matching fingerprint it reports ANCHORED.
func TestHandleEvidence_VerifyTrustAnchor(t *testing.T) {
	t.Parallel()
	s := newTestServer()
	root := evidenceFixtureRoot(t)
	data, err := os.ReadFile(evidenceBundleFile(t, root))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	b, err := evidence.Parse(data)
	if err != nil {
		t.Fatalf("parse bundle: %v", err)
	}
	kp, err := evidence.LoadOrCreateKeys(root)
	if err != nil {
		t.Fatalf("load keys: %v", err)
	}
	if err := b.Sign(kp); err != nil {
		t.Fatalf("sign bundle: %v", err)
	}
	signed, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal signed bundle: %v", err)
	}
	signedPath := filepath.Join(t.TempDir(), "signed.json")
	if err := os.WriteFile(signedPath, signed, 0o644); err != nil {
		t.Fatalf("write signed bundle: %v", err)
	}
	verify := func(args map[string]any) EvidenceVerifyReport {
		t.Helper()
		res, err := s.handleEvidence(context.Background(), args)
		if err != nil {
			t.Fatalf("verify error: %v", err)
		}
		var rep EvidenceVerifyReport
		if err := json.Unmarshal([]byte(res), &rep); err != nil {
			t.Fatalf("decode report: %v; raw=%s", err, res)
		}
		return rep
	}
	// Self-attested: signed, but no out-of-band fingerprint supplied.
	rep := verify(map[string]any{"action": "verify", "file": signedPath})
	if !rep.SelfAttested || rep.TrustAnchor != "self-attested" {
		t.Errorf("expected self-attested anchor, got %+v", rep)
	}
	// Anchored: matching expect_fingerprint.
	rep = verify(map[string]any{"action": "verify", "file": signedPath, "expect_fingerprint": b.Signature.KeyFingerprint})
	if rep.SelfAttested || rep.TrustAnchor != "anchored" || rep.FingerprintMatch == nil || !*rep.FingerprintMatch {
		t.Errorf("expected anchored trust, got %+v", rep)
	}
}

// TestHandleEvidence_VerifyTampered: mutating any content field breaks the
// SHA-256 seal, so verify must fail with the tamper error (the CLI's exit-2
// path, surfaced as an error string).
func TestHandleEvidence_VerifyTampered(t *testing.T) {
	t.Parallel()
	s := newTestServer()
	root := evidenceFixtureRoot(t)
	bundlePath := evidenceBundleFile(t, root)

	// Tamper: flip the task id inside the bundle without re-sealing.
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var b evidence.Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	b.TaskID = "TAMPERED"
	tampered, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal tampered bundle: %v", err)
	}
	tamperedPath := filepath.Join(t.TempDir(), "tampered.json")
	if err := os.WriteFile(tamperedPath, tampered, 0o644); err != nil {
		t.Fatalf("write tampered bundle: %v", err)
	}

	_, err = s.handleEvidence(context.Background(), map[string]any{
		"action": "verify",
		"file":   tamperedPath,
	})
	if err == nil {
		t.Fatal("expected verify of tampered bundle to fail")
	}
	if !strings.Contains(err.Error(), "tampered") {
		t.Errorf("expected tamper error, got: %v", err)
	}
}

// TestHandleEvidence_UnknownAction: an unsupported action is rejected before
// touching any bundle.
func TestHandleEvidence_UnknownAction(t *testing.T) {
	t.Parallel()
	s := newTestServer()
	_, err := s.handleEvidence(context.Background(), map[string]any{
		"action": "frobnicate",
	})
	if err == nil {
		t.Fatal("expected unknown action error")
	}
	if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("expected unknown-action error, got: %v", err)
	}
}
