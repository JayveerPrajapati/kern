package audit

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// testRecord returns a representative Record exercising every field.
func testRecord() Record {
	return Record{
		CorrelationID: "bp-123",
		Timestamp:     time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC),
		Source:        domain.SourceAgent,
		AgentID:       "agent-1",
		Operation:     domain.OpCommit,
		RepoRoot:      "/tmp/repo",
		Status:        domain.StatusBlock,
		ExitCode:      1,
		Summary:       SummaryMeta{Total: 2, Warnings: 1, Blocks: 1},
		Findings: []FindingMeta{
			{RuleID: "arch:layers", Severity: domain.SeverityBlock, Category: domain.CategoryArchitecture, File: "web/web.go", Line: 10},
		},
		DurationMs: 42,
	}
}

func TestAudit_AppendsJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".blueprint", "audit", "audit.jsonl")
	w := NewWriter(path)

	r1 := testRecord()
	r1.CorrelationID = "bp-1"
	r2 := testRecord()
	r2.CorrelationID = "bp-2"

	if err := w.Write(r1); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := w.Write(r2); err != nil {
		t.Fatalf("write 2: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (one per Write)", len(lines))
	}

	var got Record
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("line 1 is not valid JSON: %v", err)
	}
	if got.CorrelationID != "bp-1" {
		t.Errorf("correlation_id = %q, want bp-1", got.CorrelationID)
	}
	if got.Status != domain.StatusBlock || got.ExitCode != 1 {
		t.Errorf("status/exit_code round-trip = %s/%d, want BLOCK/1", got.Status, got.ExitCode)
	}
	if got.Source != domain.SourceAgent || got.Operation != domain.OpCommit {
		t.Errorf("source/operation round-trip = %s/%s, want agent/commit", got.Source, got.Operation)
	}
	if got.AgentID != "agent-1" || got.RepoRoot != "/tmp/repo" {
		t.Errorf("agent_id/repo_root round-trip = %q/%q", got.AgentID, got.RepoRoot)
	}
	if got.DurationMs != 42 {
		t.Errorf("duration_ms = %d, want 42", got.DurationMs)
	}
	if len(got.Findings) != 1 || got.Findings[0].RuleID != "arch:layers" || got.Findings[0].File != "web/web.go" || got.Findings[0].Line != 10 {
		t.Errorf("findings meta round-trip = %+v", got.Findings)
	}
	if got.Summary.Total != 2 || got.Summary.Blocks != 1 {
		t.Errorf("summary round-trip = %+v", got.Summary)
	}
	if got.Timestamp.IsZero() {
		t.Error("timestamp did not round-trip")
	}
	if got.Hash == "" {
		t.Error("record on disk has empty hash")
	}
}

func TestAudit_SelfHashIntegrity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w := NewWriter(path)

	r := testRecord()
	if err := w.Write(r); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read the record back from disk: the Hash field is set by Write on the
	// appended line, not on the caller's value copy.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	var onDisk Record
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("parse audit: %v", err)
	}
	if onDisk.Hash == "" {
		t.Fatal("record on disk has empty hash")
	}

	// The recorded Hash must equal sha256 over the canonical JSON of the
	// record with Hash cleared.
	unsigned := onDisk
	unsigned.Hash = ""
	b, err := json.Marshal(unsigned)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sum := sha256.Sum256(b)
	if want := hex.EncodeToString(sum[:]); onDisk.Hash != want {
		t.Errorf("Hash = %q, want %q", onDisk.Hash, want)
	}

	// On-disk tamper simulation: change a field and verify that re-hashing the
	// tampered record (with Hash cleared) no longer matches the recorded hash.
	tampered := onDisk
	tampered.Findings[0].Line = 999
	tampered.Hash = ""
	tb, err := json.Marshal(tampered)
	if err != nil {
		t.Fatalf("marshal tampered: %v", err)
	}
	tsum := sha256.Sum256(tb)
	if hex.EncodeToString(tsum[:]) == onDisk.Hash {
		t.Error("tampered record still hashes to the recorded hash — integrity check ineffective")
	}
}

func TestAudit_MkdirAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "audit.jsonl")
	w := NewWriter(path)

	if err := w.Write(testRecord()); err != nil {
		t.Fatalf("write: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("audit file not created at nested path: %v", err)
	}
	if fi.Size() == 0 {
		t.Error("audit file is empty")
	}
}

// TestG20_AuditSuppressedMeta: a suppressed finding's audit meta records the
// suppression flag and the owner, so the suppression lift itself stays
// auditable (P1-2).
func TestG20_AuditSuppressedMeta(t *testing.T) {
	r := testRecord()
	r.Findings[0].Suppressed = true
	r.Findings[0].Owner = "platform-eng"
	r.Findings[0].Severity = domain.SeverityInfo

	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	if err := NewWriter(path).Write(r); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got Record
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(got.Findings))
	}
	if !got.Findings[0].Suppressed {
		t.Error("Suppressed = false, want true in audit meta")
	}
	if got.Findings[0].Owner != "platform-eng" {
		t.Errorf("Owner = %q, want platform-eng in audit meta", got.Findings[0].Owner)
	}
	// The raw JSONL line must carry the suppression keys.
	if !strings.Contains(string(raw), `"suppressed":true`) {
		t.Errorf("audit line lacks suppressed=true: %s", raw)
	}
	if !strings.Contains(string(raw), `"owner":"platform-eng"`) {
		t.Errorf("audit line lacks owner: %s", raw)
	}
}

// --- P1.4 hash-chaining tests ---

// readRecordsFrom reads every JSONL record back from path.
func readRecordsFrom(t *testing.T, path string) []Record {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	var recs []Record
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("parse record line: %v\n%s", err, line)
		}
		recs = append(recs, rec)
	}
	return recs
}

// TestWriter_HashChaining (P1.4): three records written through one Writer
// form a chain — each PreviousHash links to the preceding record's Hash, the
// genesis record carries "", the Writer tracks LastHash, and VerifyChain
// walks the whole chain without error.
func TestWriter_HashChaining(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w := NewWriter(path)

	for i := 1; i <= 3; i++ {
		r := testRecord()
		r.CorrelationID = fmt.Sprintf("bp-%d", i)
		if err := w.Write(r); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	got := readRecordsFrom(t, path)
	if len(got) != 3 {
		t.Fatalf("records = %d, want 3", len(got))
	}

	// Genesis has no PreviousHash; each later record links to the previous.
	if got[0].PreviousHash != "" {
		t.Errorf("genesis previous_hash = %q, want empty", got[0].PreviousHash)
	}
	if got[1].PreviousHash != got[0].Hash {
		t.Errorf("record 2 previous_hash = %q, want record 1 hash %q", got[1].PreviousHash, got[0].Hash)
	}
	if got[2].PreviousHash != got[1].Hash {
		t.Errorf("record 3 previous_hash = %q, want record 2 hash %q", got[2].PreviousHash, got[1].Hash)
	}
	// Distinct correlation ids → distinct content → distinct hashes.
	if got[0].Hash == got[1].Hash || got[1].Hash == got[2].Hash {
		t.Error("chained records must have distinct hashes")
	}

	if w.LastHash() != got[2].Hash {
		t.Errorf("LastHash() = %q, want %q", w.LastHash(), got[2].Hash)
	}

	last, err := w.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if last != got[2].Hash {
		t.Errorf("VerifyChain last hash = %q, want %q", last, got[2].Hash)
	}
}

// TestWriter_VerifyChain_DetectsTamper (P1.4): modifying a middle record's
// Status on disk must break VerifyChain at exactly that record.
func TestWriter_VerifyChain_DetectsTamper(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w := NewWriter(path)
	for i := 1; i <= 3; i++ {
		r := testRecord()
		r.CorrelationID = fmt.Sprintf("bp-%d", i)
		if err := w.Write(r); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	// Tamper with the middle record's Status on disk (its stored Hash no
	// longer recomputes).
	got := readRecordsFrom(t, path)
	mid := got[1]
	mid.Status = domain.StatusWarn
	b, err := json.Marshal(mid)
	if err != nil {
		t.Fatalf("marshal tampered record: %v", err)
	}
	lines := []string{string(mustMarshalJSON(t, got[0])), string(b), string(mustMarshalJSON(t, got[2]))}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("rewrite audit: %v", err)
	}

	_, err = w.VerifyChain()
	if err == nil {
		t.Fatal("VerifyChain = nil, want error for tampered middle record")
	}
	if !strings.Contains(err.Error(), "bp-2") {
		t.Errorf("error should name the tampered record (bp-2), got: %v", err)
	}
}

// TestWriter_VerifyChain_LegacyBackwardCompat (P1.4): a file containing
// pre-P1.4 records (self-hashed, no PreviousHash) plus new chained records
// verifies as one chain — legacy records are anchors, and the chain starts at
// the first record that carries the field.
func TestWriter_VerifyChain_LegacyBackwardCompat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	// Write two legacy records exactly as the pre-P1.4 Write produced them.
	for i := 1; i <= 2; i++ {
		rec := testRecord()
		rec.CorrelationID = fmt.Sprintf("legacy-%d", i)
		unsigned := rec
		unsigned.Hash = ""
		b, err := json.Marshal(unsigned)
		if err != nil {
			t.Fatalf("marshal legacy %d: %v", i, err)
		}
		sum := sha256.Sum256(b)
		rec.Hash = hex.EncodeToString(sum[:])
		final, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal final legacy %d: %v", i, err)
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("open legacy %d: %v", i, err)
		}
		if _, err := f.Write(append(final, '\n')); err != nil {
			f.Close()
			t.Fatalf("write legacy %d: %v", i, err)
		}
		f.Close()
	}

	// A new chained record must continue from the file's last hash.
	w := NewWriter(path)
	r3 := testRecord()
	r3.CorrelationID = "chained-3"
	if err := w.Write(r3); err != nil {
		t.Fatalf("write chained record: %v", err)
	}

	got := readRecordsFrom(t, path)
	if len(got) != 3 {
		t.Fatalf("records = %d, want 3", len(got))
	}
	if got[2].PreviousHash != got[1].Hash {
		t.Errorf("chained record previous_hash = %q, want legacy record 2 hash %q", got[2].PreviousHash, got[1].Hash)
	}

	last, err := w.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain on mixed legacy/chained file: %v", err)
	}
	if last != got[2].Hash {
		t.Errorf("VerifyChain last hash = %q, want %q", last, got[2].Hash)
	}
}

func mustMarshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// selfHashedRecord returns rec with a valid self-hash (Hash covers the
// canonical JSON with Hash cleared) but WITHOUT touching PreviousHash — the
// shape a forged "anchor" record (empty PreviousHash) would have on disk.
func selfHashedRecord(t *testing.T, rec Record) Record {
	t.Helper()
	unsigned := rec
	unsigned.Hash = ""
	b, err := json.Marshal(unsigned)
	if err != nil {
		t.Fatalf("marshal unsigned: %v", err)
	}
	sum := sha256.Sum256(b)
	rec.Hash = hex.EncodeToString(sum[:])
	return rec
}

// writeRecordsFile replaces the audit file with the given records, marshaled
// verbatim (no re-hashing) — simulates an on-disk tamper.
func writeRecordsFile(t *testing.T, path string, recs ...Record) {
	t.Helper()
	lines := make([]string, 0, len(recs))
	for _, r := range recs {
		lines = append(lines, string(mustMarshalJSON(t, r)))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write audit file: %v", err)
	}
}

// TestWriter_DistinctRepoRoots: DistinctRepoRoots returns the unique, first-
// seen RepoRoot values across the chain — used by verify-receipt to discover
// the kern chain root(s) records were linked under (worktree paths for CI).
func TestWriter_DistinctRepoRoots(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w := NewWriter(path)

	// Empty file → no roots.
	if roots := w.DistinctRepoRoots(); len(roots) != 0 {
		t.Fatalf("DistinctRepoRoots on empty file = %v, want none", roots)
	}

	for _, root := range []string{"/repo/a", "/repo/a", "/repo/b", "/repo/a"} {
		r := testRecord()
		r.CorrelationID = fmt.Sprintf("root-%s", root)
		r.RepoRoot = root
		if err := w.Write(r); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	got := w.DistinctRepoRoots()
	want := []string{"/repo/a", "/repo/b"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("DistinctRepoRoots = %v, want %v (first-seen order)", got, want)
	}

	// Missing file → nil.
	if roots := NewWriter(filepath.Join(dir, "nope", "audit.jsonl")).DistinctRepoRoots(); len(roots) != 0 {
		t.Fatalf("DistinctRepoRoots on missing file = %v, want none", roots)
	}
}

// --- H6: genesis protection (head-prepend) ---

// TestWriter_VerifyChain_HeadPrependDetected (H6): a forged record with empty
// PreviousHash inserted AFTER the chain has started must fail verification.
// Only the legacy prefix — records before the first non-empty-PreviousHash
// record — may carry empty anchors.
func TestWriter_VerifyChain_HeadPrependDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w := NewWriter(path)
	for i := 1; i <= 3; i++ {
		r := testRecord()
		r.CorrelationID = fmt.Sprintf("bp-%d", i)
		if err := w.Write(r); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	got := readRecordsFrom(t, path)

	// Insert a forged anchor (empty PreviousHash, valid self-hash) between
	// records 2 and 3. Its own hash recomputes fine — the empty PreviousHash
	// after the chain started is the tamper signal.
	forged := testRecord()
	forged.CorrelationID = "forged-anchor"
	forged.PreviousHash = ""
	forged = selfHashedRecord(t, forged)
	writeRecordsFile(t, path, got[0], got[1], forged, got[2])

	_, err := w.VerifyChain()
	if err == nil {
		t.Fatal("VerifyChain = nil, want error for forged empty-previous_hash anchor mid-chain")
	}
	if !strings.Contains(err.Error(), "empty previous_hash after the chain has started") {
		t.Errorf("error should name the head-prepend rule, got: %v", err)
	}

	// The same forged record at the very HEAD (before any chained record) is
	// indistinguishable from a legacy prefix and must still verify — H6 only
	// guards the post-chain region.
	writeRecordsFile(t, path, forged, got[0], got[1], got[2])
	if _, err := w.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain with forged anchor in legacy prefix should pass: %v", err)
	}
}

// TestWriter_VerifyChain_EmptyAnchorAfterChain (H6): appending a record with
// empty PreviousHash after a valid chained tail is also tampering.
func TestWriter_VerifyChain_EmptyAnchorAfterChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w := NewWriter(path)
	for i := 1; i <= 2; i++ {
		r := testRecord()
		r.CorrelationID = fmt.Sprintf("bp-%d", i)
		if err := w.Write(r); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	got := readRecordsFrom(t, path)

	tail := testRecord()
	tail.CorrelationID = "unlinked-tail"
	tail.PreviousHash = "" // breaks the chain: does not reference got[1].Hash
	writeRecordsFile(t, path, got[0], got[1], tail)

	_, err := w.VerifyChain()
	if err == nil {
		t.Fatal("VerifyChain = nil, want error for unlinked tail with empty previous_hash")
	}
}

// --- H3: prefix-tolerant receipt verification ---

// TestWriter_ChainContainsHash (H3): ChainContainsHash finds any record hash
// anywhere in the chain (not just the last), rejects unknown hashes, treats
// an empty hash as "not found", and treats a missing file as "not found"
// without error.
func TestWriter_ChainContainsHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w := NewWriter(path)
	for i := 1; i <= 3; i++ {
		r := testRecord()
		r.CorrelationID = fmt.Sprintf("bp-%d", i)
		if err := w.Write(r); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	got := readRecordsFrom(t, path)

	// Middle and last hashes are both found.
	for _, rec := range got {
		found, err := w.ChainContainsHash(rec.Hash)
		if err != nil {
			t.Fatalf("ChainContainsHash(%q): %v", rec.Hash, err)
		}
		if !found {
			t.Errorf("ChainContainsHash(%q) = false, want true (record %s)", rec.Hash, rec.CorrelationID)
		}
	}

	// Unknown hash → not found, no error.
	found, err := w.ChainContainsHash(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatalf("ChainContainsHash(unknown): %v", err)
	}
	if found {
		t.Error("ChainContainsHash(unknown) = true, want false")
	}

	// Empty hash matches nothing (receipts with no binding are rejected by the
	// caller before this is ever consulted).
	found, err = w.ChainContainsHash("")
	if err != nil {
		t.Fatalf("ChainContainsHash(\"\"): %v", err)
	}
	if found {
		t.Error("ChainContainsHash(\"\") = true, want false")
	}

	// Missing file → (false, nil): no chain, nothing to contain the hash.
	w2 := NewWriter(filepath.Join(dir, "nope", "audit.jsonl"))
	found, err = w2.ChainContainsHash("abc123")
	if err != nil {
		t.Fatalf("ChainContainsHash on missing file: %v", err)
	}
	if found {
		t.Error("ChainContainsHash on missing file = true, want false")
	}

	// A malformed line is an error: an unreadable chain must not silently
	// validate a receipt.
	w3 := NewWriter(filepath.Join(dir, "broken", "audit.jsonl"))
	if err := os.MkdirAll(filepath.Join(dir, "broken"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(w3.path, []byte("{not json\n"), 0o644); err != nil {
		t.Fatalf("write broken file: %v", err)
	}
	if _, err := w3.ChainContainsHash("abc"); err == nil {
		t.Fatal("ChainContainsHash on malformed chain = nil error, want parse error")
	}
}

// --- H7: cross-process flock + fsync ---

// TestWriter_ConcurrentWritesNoFork (H7): two Writer instances (separate
// in-process mutexes) appending concurrently from many goroutines must
// serialize via the flock on <path>.lock: every record lands, the chain
// verifies, and there is exactly ONE genesis — the chain did not fork into
// two heads (which is what happens without the cross-writer lock when two
// writers both read genesis before either appends).
func TestWriter_ConcurrentWritesNoFork(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	// Point both writers at a nonexistent kern binary so the best-effort kern
	// chain link fails fast instead of holding the flock for the subprocess.
	noKern := filepath.Join(dir, "no-such-kern")
	w1 := NewWriter(path).WithKernBinary(noKern)
	w2 := NewWriter(path).WithKernBinary(noKern)

	const per = 25 // 50 goroutines total across two writers
	var wg sync.WaitGroup
	for i := 0; i < per; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			r := testRecord()
			r.CorrelationID = fmt.Sprintf("w1-%d", n)
			if err := w1.Write(r); err != nil {
				t.Errorf("w1 write %d: %v", n, err)
			}
		}(i)
		go func(n int) {
			defer wg.Done()
			r := testRecord()
			r.CorrelationID = fmt.Sprintf("w2-%d", n)
			if err := w2.Write(r); err != nil {
				t.Errorf("w2 write %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	if n := w1.RecordCount(); n != per*2 {
		t.Fatalf("RecordCount = %d, want %d (every concurrent write must land)", n, per*2)
	}
	last, err := w1.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain after concurrent writes: %v", err)
	}
	if last == "" {
		t.Fatal("chain is empty after concurrent writes")
	}

	got := readRecordsFrom(t, path)
	if len(got) != per*2 {
		t.Fatalf("records = %d, want %d", len(got), per*2)
	}
	genesis := 0
	for _, rec := range got {
		if rec.PreviousHash == "" {
			genesis++
		}
	}
	if genesis != 1 {
		t.Fatalf("genesis records = %d, want exactly 1 (chain forked: %d heads)", genesis, genesis)
	}
	// Every record after the first must chain onto its predecessor.
	for i := 1; i < len(got); i++ {
		if got[i].PreviousHash != got[i-1].Hash {
			t.Fatalf("record %d previous_hash = %q, want %q (broken link after concurrent writes)", i, got[i].PreviousHash, got[i-1].Hash)
		}
	}
}

// TestWriter_WriteFsyncsAndLocks (H7): after a write, the lock file exists
// (flock target) and the audit file contains the fsynced line; a second
// Writer on the same path continues the chain (lock released between writes).
func TestWriter_WriteFsyncsAndLocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w := NewWriter(path)
	if err := w.Write(testRecord()); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Errorf("lock file %s.lock not created: %v", path, err)
	}

	// A fresh Writer must chain onto the existing tail (the lock was released,
	// and the fsynced line is visible).
	w2 := NewWriter(path)
	r := testRecord()
	r.CorrelationID = "bp-second"
	if err := w2.Write(r); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got := readRecordsFrom(t, path)
	if len(got) != 2 {
		t.Fatalf("records = %d, want 2", len(got))
	}
	if got[1].PreviousHash != got[0].Hash {
		t.Errorf("second record previous_hash = %q, want %q", got[1].PreviousHash, got[0].Hash)
	}
}
