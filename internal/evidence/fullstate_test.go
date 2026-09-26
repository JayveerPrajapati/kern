package evidence

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeStoreRecord writes one raw JSON record file into <root>/.kern/evidence,
// exactly as the store persists it (bytes are preserved verbatim).
func writeStoreRecord(t *testing.T, root, key, content string) {
	t.Helper()
	dir := filepath.Join(root, ".kern", "evidence")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, key+".json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write record %s: %v", key, err)
	}
}

func readStoreRecord(t *testing.T, root, key string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".kern", "evidence", key+".json"))
	if err != nil {
		t.Fatalf("read record %s: %v", key, err)
	}
	return string(b)
}

func exportToString(t *testing.T, root string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := ExportFullState(root, &buf); err != nil {
		t.Fatalf("ExportFullState(%s): %v", root, err)
	}
	return buf.String()
}

// TestFullStateRoundTrip: Export -> Restore into a fresh store -> Export
// produces a byte-identical bundle. Record bytes (including a trailing
// newline in one record) are preserved exactly across the round trip.
func TestFullStateRoundTrip(t *testing.T) {
	src := t.TempDir()
	writeStoreRecord(t, src, "bundle-aaa", `{"type":"fact","statement":"A","digest":"d1"}`)
	writeStoreRecord(t, src, "bundle-bbb", "{\"type\":\"graph\",\"statement\":\"B\",\"digest\":\"d2\"}\n")
	writeStoreRecord(t, src, "bundle-ccc", `{"type":"test","statement":"C","digest":"d3"}`)

	first := exportToString(t, src)

	dst := t.TempDir()
	if err := RestoreFullState(dst, strings.NewReader(first)); err != nil {
		t.Fatalf("RestoreFullState: %v", err)
	}

	// The restored store holds byte-identical record files.
	if got := readStoreRecord(t, dst, "bundle-aaa"); got != `{"type":"fact","statement":"A","digest":"d1"}` {
		t.Errorf("restored bundle-aaa = %q, want byte-identical content", got)
	}
	if got := readStoreRecord(t, dst, "bundle-bbb"); got != "{\"type\":\"graph\",\"statement\":\"B\",\"digest\":\"d2\"}\n" {
		t.Errorf("restored bundle-bbb = %q, want byte-identical content (trailing newline preserved)", got)
	}

	second := exportToString(t, dst)
	if second != first {
		t.Errorf("round-trip bundle differs:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestFullStateEmptyStore: an empty/absent store exports a valid empty bundle
// (count 0, empty keys, records, valid digest) that restores cleanly.
func TestFullStateEmptyStore(t *testing.T) {
	src := t.TempDir()
	out := exportToString(t, src)

	var b FullStateBundle
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("empty bundle is not valid JSON: %v", err)
	}
	if b.Schema != FullStateSchemaName || b.SchemaVersion != FullStateSchemaVersion {
		t.Errorf("schema identity = %s v%d, want %s v%d", b.Schema, b.SchemaVersion, FullStateSchemaName, FullStateSchemaVersion)
	}
	if b.Store.Count != 0 || len(b.Store.Keys) != 0 || len(b.Records) != 0 {
		t.Errorf("empty bundle not empty: count=%d keys=%v records=%d", b.Store.Count, b.Store.Keys, len(b.Records))
	}
	if b.BundleDigest == "" {
		t.Error("empty bundle has no bundle_digest")
	}

	dst := t.TempDir()
	if err := RestoreFullState(dst, strings.NewReader(out)); err != nil {
		t.Fatalf("restore empty bundle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".kern", "evidence")); !os.IsNotExist(err) {
		t.Fatalf("restore of empty bundle should leave the store dir absent, got stat err=%v", err)
	}
}

// TestFullStateChecksumTamper: flipping a byte inside a record's content
// fails restore on the bundle-digest check with a clear tamper error and the
// store is left unchanged (no partial apply). A second scenario recomputes
// the bundle digest but corrupts a per-record checksum: restore fails on the
// checksum check, again before any write.
func TestFullStateChecksumTamper(t *testing.T) {
	src := t.TempDir()
	writeStoreRecord(t, src, "bundle-aaa", `{"type":"fact","statement":"AAA","digest":"d1"}`)
	writeStoreRecord(t, src, "bundle-bbb", `{"type":"graph","statement":"BBB","digest":"d2"}`)
	out := exportToString(t, src)

	// Scenario 1: flip a content byte, leave the bundle digest stale.
	var b FullStateBundle
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatal(err)
	}
	tampered := []byte(b.Records[0].Content)
	for i := range tampered {
		if tampered[i] != 'x' {
			tampered[i] = 'x'
			break
		}
	}
	b.Records[0].Content = string(tampered)
	wire1, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	before := exportToString(t, dst) // empty store snapshot
	err = RestoreFullState(dst, bytes.NewReader(wire1))
	if err == nil {
		t.Fatal("restore of tampered bundle succeeded, want error")
	}
	if !strings.Contains(err.Error(), "digest mismatch") {
		t.Errorf("tamper error not clear: %v", err)
	}
	// Store unchanged: the failed restore wrote nothing.
	if after := exportToString(t, dst); after != before {
		t.Errorf("store changed after failed tampered restore:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}

	// Scenario 2: recompute the bundle digest but corrupt a per-record
	// checksum — the checksum check (not the digest) must fail, still before
	// any write.
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatal(err)
	}
	// Corrupt the checksum of record 0 (hex flip) and re-seal the bundle.
	flipped := b.Records[0].Digest
	flipByte := byte('0')
	if flipped[0] == '0' {
		flipByte = '1'
	}
	b.Records[0].Digest = string(flipByte) + flipped[1:]
	b.BundleDigest = ""
	seal, err := json.Marshal(&b)
	if err != nil {
		t.Fatal(err)
	}
	b.BundleDigest = Digest(string(seal))
	wire2, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	dst2 := t.TempDir()
	before2 := exportToString(t, dst2)
	err = RestoreFullState(dst2, bytes.NewReader(wire2))
	if err == nil {
		t.Fatal("restore with corrupted record checksum succeeded, want error")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("checksum tamper error not clear: %v", err)
	}
	if after := exportToString(t, dst2); after != before2 {
		t.Errorf("store changed after failed checksum restore:\n--- before ---\n%s\n--- after ---\n%s", before2, after)
	}
}

// TestFullStateRestoreUpsert: restoring into a store that already has records
// upserts by key — bundle records overwrite same-key records, new keys are
// added, and records NOT in the bundle are left untouched.
func TestFullStateRestoreUpsert(t *testing.T) {
	src := t.TempDir()
	writeStoreRecord(t, src, "a", `{"statement":"A-v1"}`)
	writeStoreRecord(t, src, "b", `{"statement":"B-v1"}`)
	out := exportToString(t, src)

	dst := t.TempDir()
	writeStoreRecord(t, dst, "a", `{"statement":"A-v0"}`)
	writeStoreRecord(t, dst, "c", `{"statement":"C-v0"}`)

	if err := RestoreFullState(dst, strings.NewReader(out)); err != nil {
		t.Fatalf("RestoreFullState: %v", err)
	}
	if got := readStoreRecord(t, dst, "a"); got != `{"statement":"A-v1"}` {
		t.Errorf("key a after upsert = %q, want A-v1", got)
	}
	if got := readStoreRecord(t, dst, "b"); got != `{"statement":"B-v1"}` {
		t.Errorf("key b after restore = %q, want B-v1", got)
	}
	if got := readStoreRecord(t, dst, "c"); got != `{"statement":"C-v0"}` {
		t.Errorf("key c must be untouched by upsert, got %q", got)
	}
}

// TestFullStateExportRejectsCorruptStore: a store record that is not valid
// JSON fails export loudly instead of silently bundling corrupt state.
func TestFullStateExportRejectsCorruptStore(t *testing.T) {
	src := t.TempDir()
	writeStoreRecord(t, src, "bad", `this is not json`)
	var buf bytes.Buffer
	err := ExportFullState(src, &buf)
	if err == nil {
		t.Fatal("export of a corrupt store record succeeded, want error")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("corrupt-store error not clear: %v", err)
	}
}

// TestFullStateRestoreRejectsBadSchema: a bundle with the wrong schema name
// or version is rejected with a clear error.
func TestFullStateRestoreRejectsBadSchema(t *testing.T) {
	src := t.TempDir()
	writeStoreRecord(t, src, "a", `{"statement":"A"}`)
	out := exportToString(t, src)

	var b FullStateBundle
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatal(err)
	}
	b.Schema = "other-schema"
	wire, _ := json.MarshalIndent(b, "", "  ")
	if err := RestoreFullState(t.TempDir(), bytes.NewReader(wire)); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Errorf("wrong schema name not rejected clearly: %v", err)
	}

	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatal(err)
	}
	b.SchemaVersion = 99
	wire, _ = json.MarshalIndent(b, "", "  ")
	if err := RestoreFullState(t.TempDir(), bytes.NewReader(wire)); err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Errorf("wrong schema version not rejected clearly: %v", err)
	}
}
