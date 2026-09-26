package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// evidenceStoreFixture creates a temp repo with a populated evidence store
// (.kern/evidence/<key>.json records). Full-state export/restore need no
// index, so this stays much lighter than evidenceFixture.
func evidenceStoreFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	storeDir := filepath.Join(dir, ".kern", "evidence")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", storeDir, err)
	}
	records := map[string]string{
		"bundle-111": `{"type":"fact","statement":"one","digest":"d1"}`,
		"bundle-222": "{\"type\":\"graph\",\"statement\":\"two\",\"digest\":\"d2\"}\n",
		"bundle-333": `{"type":"test","statement":"three","digest":"d3"}`,
	}
	for key, content := range records {
		if err := os.WriteFile(filepath.Join(storeDir, key+".json"), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
	}
	return dir
}

// TestEvidenceFullState_Stdout: `kern evidence --full-state --root <dir>`
// emits a valid deterministic bundle on stdout with store metadata,
// per-record checksums, and a bundle digest; exit 0.
func TestEvidenceFullState_Stdout(t *testing.T) {
	dir := evidenceStoreFixture(t)

	var out string
	code := -1
	out = captureStdout(t, func() {
		code = runEvidence([]string{"--full-state", "--root", dir})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr above)", code)
	}
	var b struct {
		Schema        string `json:"schema"`
		SchemaVersion int    `json:"schema_version"`
		Store         struct {
			Count int      `json:"count"`
			Keys  []string `json:"keys"`
		} `json:"store"`
		Records []struct {
			Key     string `json:"key"`
			Digest  string `json:"digest"`
			Content string `json:"content"`
		} `json:"records"`
		BundleDigest string `json:"bundle_digest"`
	}
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out)
	}
	if b.Schema != "kern-evidence-full-state" || b.SchemaVersion != 1 {
		t.Errorf("schema = %s v%d, want kern-evidence-full-state v1", b.Schema, b.SchemaVersion)
	}
	if b.Store.Count != 3 || len(b.Store.Keys) != 3 {
		t.Errorf("store count = %d keys = %v, want 3 records", b.Store.Count, b.Store.Keys)
	}
	if b.Store.Keys[0] != "bundle-111" || b.Store.Keys[1] != "bundle-222" || b.Store.Keys[2] != "bundle-333" {
		t.Errorf("keys not sorted: %v", b.Store.Keys)
	}
	if len(b.Records) != 3 {
		t.Fatalf("records = %d, want 3", len(b.Records))
	}
	for i, r := range b.Records {
		if r.Digest == "" {
			t.Errorf("record %d (%s) has no digest", i, r.Key)
		}
		if !json.Valid([]byte(r.Content)) {
			t.Errorf("record %d (%s) content is not valid JSON: %q", i, r.Key, r.Content)
		}
	}
	if b.BundleDigest == "" {
		t.Error("bundle has no bundle_digest")
	}
}

// TestEvidenceFullState_File: `--out <file>` writes the bundle to disk,
// reports the record count, and exits 0.
func TestEvidenceFullState_File(t *testing.T) {
	dir := evidenceStoreFixture(t)
	outPath := filepath.Join(t.TempDir(), "full-state.json")

	var out string
	code := -1
	out = captureStdout(t, func() {
		code = runEvidence([]string{"--full-state", "--root", dir, "--out", outPath})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr above)", code)
	}
	if !strings.Contains(out, "3 records") {
		t.Errorf("output does not report record count: %s", out)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read %s: %v", outPath, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("file is not valid JSON: %v", err)
	}
	if m["bundle_digest"] == "" {
		t.Error("file bundle has no bundle_digest")
	}
}

// TestEvidenceFullState_RoundTrip: export -> restore into a fresh repo ->
// export again yields a byte-identical bundle (the round-trip guarantee).
func TestEvidenceFullState_RoundTrip(t *testing.T) {
	src := evidenceStoreFixture(t)
	bundlePath := filepath.Join(t.TempDir(), "full-state.json")

	if code := runEvidence([]string{"--full-state", "--root", src, "--out", bundlePath}); code != 0 {
		t.Fatalf("export exit code = %d, want 0", code)
	}
	first, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}

	dst := t.TempDir()
	var out string
	code := -1
	out = captureStdout(t, func() {
		code = runEvidence([]string{"--restore", bundlePath, "--root", dst})
	})
	if code != 0 {
		t.Fatalf("restore exit code = %d, want 0 (stderr above)", code)
	}
	if !strings.Contains(out, "restored evidence full state") {
		t.Errorf("restore output unexpected: %s", out)
	}

	secondPath := filepath.Join(t.TempDir(), "full-state-2.json")
	if code := runEvidence([]string{"--full-state", "--root", dst, "--out", secondPath}); code != 0 {
		t.Fatalf("re-export exit code = %d, want 0", code)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatalf("read re-exported bundle: %v", err)
	}
	if string(second) != string(first) {
		t.Errorf("round-trip bundles differ:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestEvidenceRestore_MissingFile: a nonexistent bundle file is a clear
// error, exit 1.
func TestEvidenceRestore_MissingFile(t *testing.T) {
	code := runEvidence([]string{"--restore", filepath.Join(t.TempDir(), "nope.json")})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// TestEvidenceRestore_NoFile: no bundle path at all is a usage error, exit 1.
func TestEvidenceRestore_NoFile(t *testing.T) {
	if code := runEvidence([]string{"--restore"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// TestEvidenceRestore_CorruptBundle: a non-JSON file fails with a clear
// parse error, exit 1, and writes nothing.
func TestEvidenceRestore_CorruptBundle(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(bad, []byte("this is not a bundle"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	dst := t.TempDir()
	code := runEvidence([]string{"--restore", bad, "--root", dst})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if _, err := os.Stat(filepath.Join(dst, ".kern", "evidence")); !os.IsNotExist(err) {
		t.Errorf("store dir created despite corrupt bundle: %v", err)
	}
}

// TestEvidenceRestore_Tampered: a bundle whose record content was modified
// (digest stale) fails with a clear tamper error, exit 1, and the store is
// left untouched.
func TestEvidenceRestore_Tampered(t *testing.T) {
	src := evidenceStoreFixture(t)
	bundlePath := filepath.Join(t.TempDir(), "full-state.json")
	if code := runEvidence([]string{"--full-state", "--root", src, "--out", bundlePath}); code != 0 {
		t.Fatalf("export exit code = %d, want 0", code)
	}

	// Flip a byte inside the first record's content (the record JSON is
	// string-escaped in the bundle, so match the escaped form).
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	tampered := strings.Replace(string(data), `statement\":\"one`, `statement\":\"ONE`, 1)
	if tampered == string(data) {
		t.Fatal("tamper substitution did not match")
	}
	if err := os.WriteFile(bundlePath, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write tampered bundle: %v", err)
	}

	dst := t.TempDir()
	code := runEvidence([]string{"--restore", bundlePath, "--root", dst})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 for a tampered bundle", code)
	}
	if _, err := os.Stat(filepath.Join(dst, ".kern", "evidence")); !os.IsNotExist(err) {
		t.Errorf("store dir created despite tampered bundle: %v", err)
	}
}
