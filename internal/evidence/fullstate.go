// Full-state export/restore of the evidence store (Feature Batch J).
//
// The evidence store is the per-project directory <root>/.kern/evidence/
// holding one JSON record per key (a persisted evidence bundle named after
// its bundle id — the shape written by the evidence export surface). A
// full-state bundle captures the COMPLETE store state as one deterministic,
// tamper-evident JSON document: every record with its raw content, store
// metadata (count, sorted keys), a per-record SHA-256 checksum, and a
// bundle-level digest over the whole document (reusing evidence.Digest).
//
// The document is fully deterministic — no timestamps, no random ids, no
// repo-root field — so exporting the same store twice yields byte-identical
// bundles, and Export -> Restore into a fresh store -> Export reproduces the
// original bundle exactly (the round-trip guarantee).
//
// Restore is all-or-nothing on tamper: the bundle digest and EVERY per-record
// checksum are verified before a single byte is written, so a tampered bundle
// never partially applies.
package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"github.com/JayveerPrajapati/kern/internal/storage"
)

// FullStateSchemaName identifies the full-state bundle document type.
const FullStateSchemaName = "kern-evidence-full-state"

// FullStateSchemaVersion is the version of the full-state bundle schema.
// Bump it (breaking change) and restore-side validation rejects older bundles.
const FullStateSchemaVersion = 1

// FullStateRecord is one persisted evidence record (a JSON file in the store)
// bundled with its SHA-256 checksum. Content carries the raw persisted record
// bytes as a string so a restore reproduces the store byte-for-byte (and thus
// re-exports the same bundle — the round-trip guarantee). A string field is
// byte-exact through JSON marshal/unmarshal; json.RawMessage is not (the
// encoder re-indents it).
type FullStateRecord struct {
	Key     string `json:"key"`
	Digest  string `json:"digest"`  // sha256 hex of the raw record content
	Content string `json:"content"` // the raw persisted record bytes (a JSON document, string-escaped)
}

// FullStateStoreMeta is the store-level metadata carried by a full-state
// bundle: the record count and the sorted record keys.
type FullStateStoreMeta struct {
	Count int      `json:"count"`
	Keys  []string `json:"keys"`
}

// FullStateBundle is the deterministic full-state document: schema identity,
// store metadata, every record with its per-record checksum, and a bundle
// digest over everything above (BundleDigest cleared).
type FullStateBundle struct {
	Schema        string             `json:"schema"`
	SchemaVersion int                `json:"schema_version"`
	Store         FullStateStoreMeta `json:"store"`
	Records       []FullStateRecord  `json:"records"`
	BundleDigest  string             `json:"bundle_digest"`
}

// evidenceStoreDir is the per-project evidence store directory.
func evidenceStoreDir(root string) string {
	return filepath.Join(root, ".kern", "evidence")
}

// ExportFullState bundles the complete evidence store state for the project
// at root into one deterministic JSON document and writes it to w. The
// bundle contains every record (raw content + per-record SHA-256 checksum),
// store metadata (count, sorted keys), and a bundle-level digest reusing
// evidence.Digest. Records are ordered by key; an empty or absent store
// yields a valid empty bundle. Export never fails on store content: a record
// that is not valid JSON is a store-integrity error (backing up garbage
// silently would defeat the tamper evidence).
func ExportFullState(root string, w io.Writer) error {
	if root == "" {
		root = "."
	}
	store := storage.NewLocal(evidenceStoreDir(root))
	entries, err := store.List(context.Background())
	if err != nil {
		return fmt.Errorf("evidence: list store at %s: %w", evidenceStoreDir(root), err)
	}

	// entries arrive sorted by key (storage.LocalStore.List contract); keep
	// that order for a deterministic document. A record must be valid JSON —
	// its bytes are embedded verbatim in the bundle.
	records := make([]FullStateRecord, 0, len(entries))
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		if !json.Valid(e.Value) {
			return fmt.Errorf("evidence: store record %q at %s is not valid JSON — refusing to bundle corrupt state", e.Key, evidenceStoreDir(root))
		}
		records = append(records, FullStateRecord{
			Key:     e.Key,
			Digest:  Digest(string(e.Value)),
			Content: string(e.Value),
		})
		keys = append(keys, e.Key)
	}

	b := &FullStateBundle{
		Schema:        FullStateSchemaName,
		SchemaVersion: FullStateSchemaVersion,
		Store:         FullStateStoreMeta{Count: len(entries), Keys: keys},
		Records:       records,
	}
	// Seal: bundle digest over the canonical JSON with the digest field
	// cleared (same scheme as the evidence Bundle seal).
	b.BundleDigest = computeFullStateDigest(b)

	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("evidence: marshal full-state bundle: %w", err)
	}
	data = append(data, '\n')
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("evidence: write full-state bundle: %w", err)
	}
	return nil
}

// RestoreFullState reads a full-state bundle from r, verifies the bundle
// digest and EVERY per-record checksum (failing with a clear tamper error on
// any mismatch), then restores the records into the store at root by upsert
// (put per key; existing records not present in the bundle are left intact).
// All verification happens BEFORE any write, so a tampered bundle never
// partially applies.
func RestoreFullState(root string, r io.Reader) error {
	if root == "" {
		root = "."
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("evidence: read full-state bundle: %w", err)
	}
	var b FullStateBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return fmt.Errorf("evidence: parse full-state bundle: %w", err)
	}

	// Schema identity and version.
	if b.Schema != FullStateSchemaName {
		return fmt.Errorf("evidence: full-state bundle schema %q, want %q", b.Schema, FullStateSchemaName)
	}
	if b.SchemaVersion != FullStateSchemaVersion {
		return fmt.Errorf("evidence: full-state bundle schema version %d, want %d", b.SchemaVersion, FullStateSchemaVersion)
	}
	if b.BundleDigest == "" {
		return errors.New("evidence: full-state bundle has no bundle_digest")
	}

	// Bundle-level tamper check: recompute the digest over the document with
	// the digest field cleared. Any modification — content, checksum, metadata
	// — is caught here before any per-record or write work.
	if got := computeFullStateDigest(&b); got != b.BundleDigest {
		return errors.New("evidence: full-state bundle digest mismatch — bundle content was modified after export (tampered)")
	}

	// Store metadata must be self-consistent with the records (count and
	// sorted keys), so a hand-edited bundle cannot smuggle in records that
	// the metadata does not account for.
	if b.Store.Count != len(b.Records) {
		return fmt.Errorf("evidence: full-state store metadata inconsistent: count %d but bundle carries %d records", b.Store.Count, len(b.Records))
	}
	recordKeys := make([]string, 0, len(b.Records))
	for _, rec := range b.Records {
		recordKeys = append(recordKeys, rec.Key)
	}
	sort.Strings(recordKeys)
	if len(b.Store.Keys) != len(recordKeys) {
		return errors.New("evidence: full-state store metadata inconsistent: keys do not match record keys")
	}
	for i := range b.Store.Keys {
		if b.Store.Keys[i] != recordKeys[i] {
			return errors.New("evidence: full-state store metadata inconsistent: keys do not match record keys")
		}
	}

	// Per-record tamper checks: verify ALL checksums before writing anything,
	// so a corrupted record fails the whole restore with the store untouched.
	for _, rec := range b.Records {
		if got := Digest(rec.Content); got != rec.Digest {
			return fmt.Errorf("evidence: full-state record %q checksum mismatch — record content was modified (tampered)", rec.Key)
		}
	}

	// All verified: upsert each record into the store. storage.LocalStore.Put
	// is atomic per key (temp-file rename) and validates the key; the content
	// bytes are written verbatim, reproducing the original record files.
	store := storage.NewLocal(evidenceStoreDir(root))
	for _, rec := range b.Records {
		if err := store.Put(context.Background(), rec.Key, json.RawMessage(rec.Content)); err != nil {
			return fmt.Errorf("evidence: restore record %q: %w", rec.Key, err)
		}
	}
	return nil
}

// computeFullStateDigest is the bundle's tamper-evidence seal: SHA-256 (via
// evidence.Digest) over the canonical JSON of the bundle with BundleDigest
// cleared. The document contains no maps and all slices are sorted, so the
// marshaled bytes — and therefore the digest — are reproducible across runs,
// writers, and verifiers.
func computeFullStateDigest(b *FullStateBundle) string {
	if b == nil {
		return ""
	}
	cpy := *b
	cpy.BundleDigest = ""
	data, err := json.Marshal(&cpy)
	if err != nil {
		return ""
	}
	return Digest(string(data))
}
