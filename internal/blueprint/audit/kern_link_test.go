package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// writeFakeKern creates an executable shim that records its argv and piped
// stdin to logPath, then exits 0 — standing in for the real `kern audit
// append` subprocess.
func writeFakeKern(t *testing.T, logPath string) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "fake-kern")
	content := "#!/bin/sh\n" +
		"echo \"ARGS:$*\" >> \"$FAKE_KERN_LOG\"\n" +
		"cat >> \"$FAKE_KERN_LOG\"\n" +
		"echo >> \"$FAKE_KERN_LOG\"\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake kern: %v", err)
	}
	return script
}

// TestWriterWritesLocalAndAttemptsKernChain: a successful Write keeps the
// existing local JSONL behavior AND pipes a mapped entry to `kern audit
// append --root <repoRoot>`.
func TestWriterWritesLocalAndAttemptsKernChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".blueprint", "audit", "audit.jsonl")
	logPath := filepath.Join(dir, "kern-calls.log")
	t.Setenv("FAKE_KERN_LOG", logPath)
	bin := writeFakeKern(t, logPath)

	w := NewWriter(path).WithKernBinary(bin)
	r := testRecord() // RepoRoot = /tmp/repo, Status = BLOCK
	if err := w.Write(r); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// 1. Local JSONL is still written (existing behavior).
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("local audit file not written: %v", err)
	}
	var onDisk Record
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("parse local audit: %v", err)
	}
	if onDisk.Hash == "" || onDisk.Status != domain.StatusBlock {
		t.Errorf("local record = %+v, want a self-hashed BLOCK record", onDisk)
	}

	// 2. The fake kern binary was invoked with `audit append --root <repo>`.
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("kern shim was not invoked: %v", err)
	}
	log := string(logData)
	if !strings.Contains(log, "ARGS:audit append --root /tmp/repo") {
		t.Errorf("kern shim args = %q, want \"ARGS:audit append --root /tmp/repo\"", firstLine(log))
	}

	// 3. The piped JSON body is the mapped AuditEntry (ID/Hash empty so kern
	//    assigns/computes them).
	body := strings.TrimPrefix(log, firstLine(log)+"\n")
	var entry map[string]any
	if err := json.Unmarshal([]byte(body), &entry); err != nil {
		t.Fatalf("kern shim stdin is not valid JSON: %v\n%s", err, body)
	}
	if entry["AgentID"] != "agent-1" {
		t.Errorf("AgentID = %v, want agent-1", entry["AgentID"])
	}
	if entry["Action"] != "commit" {
		t.Errorf("Action = %v, want commit", entry["Action"])
	}
	if entry["Resource"] != "/tmp/repo" {
		t.Errorf("Resource = %v, want /tmp/repo", entry["Resource"])
	}
	if entry["Result"] != "BLOCK" {
		t.Errorf("Result = %v, want BLOCK", entry["Result"])
	}
	if entry["TaskID"] != "bp-123" {
		t.Errorf("TaskID = %v, want bp-123", entry["TaskID"])
	}
	if entry["Approved"] != false {
		t.Errorf("Approved = %v, want false (validation result, not approval)", entry["Approved"])
	}
	if id, _ := entry["ID"].(string); id != "" {
		t.Errorf("ID = %q, want empty (kern auto-assigns)", id)
	}
	if h, _ := entry["Hash"].(string); h != "" {
		t.Errorf("Hash = %q, want empty (kern computes)", h)
	}
	risk, ok := entry["Risk"].(map[string]any)
	if !ok || risk["Level"] != "HIGH" {
		t.Errorf("Risk = %v, want Level HIGH for a BLOCK result", entry["Risk"])
	}
}

// TestWriterKernChainBestEffort: with no kern binary reachable, the local
// JSONL is still written, the chain attempt fails silently (stderr warning),
// and Write returns nil — the critical best-effort contract.
func TestWriterKernChainBestEffort(t *testing.T) {
	// Force resolution to fail even if a real kern exists on this machine.
	t.Setenv("KERN_BINARY", filepath.Join(t.TempDir(), "no-such-kern"))

	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w := NewWriter(path)

	if err := w.Write(testRecord()); err != nil {
		t.Fatalf("Write with no kern binary = %v, want nil (best-effort)", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("local JSONL not written when kern is unavailable: %v", err)
	}
	var onDisk Record
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("parse local audit: %v", err)
	}
	if onDisk.Hash == "" {
		t.Error("local record has no self-hash when kern is unavailable")
	}
}

// TestWriterKernChainBinaryMissing: a forced binary path that does not exist
// must also be tolerated (exec failure path of the best-effort contract).
func TestWriterKernChainBinaryMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w := NewWriter(path).WithKernBinary(filepath.Join(dir, "missing-kern"))

	if err := w.Write(testRecord()); err != nil {
		t.Fatalf("Write with missing forced binary = %v, want nil (best-effort)", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("local JSONL not written: %v", err)
	}
}

// TestKernEntryMapping covers the Record → AuditEntry mapping table directly.
func TestKernEntryMapping(t *testing.T) {
	r := testRecord() // BLOCK
	e := kernEntry(r)
	if e.AgentID != "agent-1" || e.Action != "commit" || e.Resource != "/tmp/repo" {
		t.Errorf("mapped identity fields = %+v", e)
	}
	if e.TaskID != "bp-123" || e.Result != "BLOCK" || e.Approved {
		t.Errorf("mapped result fields = %+v", e)
	}
	if e.Risk.Level != "HIGH" {
		t.Errorf("BLOCK risk level = %q, want HIGH", e.Risk.Level)
	}

	r2 := r
	r2.AgentID = ""
	if e2 := kernEntry(r2); e2.AgentID != string(domain.SourceAgent) {
		t.Errorf("empty AgentID should fall back to source %q, got %q", domain.SourceAgent, e2.AgentID)
	}

	for status, want := range map[domain.Status]string{
		domain.StatusBlock: "HIGH",
		domain.StatusError: "HIGH",
		domain.StatusWarn:  "MEDIUM",
		domain.StatusPass:  "LOW",
		domain.StatusSkip:  "LOW",
	} {
		if got := kernRiskFromStatus(status); got != want {
			t.Errorf("kernRiskFromStatus(%s) = %q, want %q", status, got, want)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// TestKernEntryCarriesContextProvenance (P1.2): a Record with a
// ContextProvenance maps it into the kern AuditEntry JSON so the chain link
// cites the provenance linking the decision to its context authorization. A
// governed entry round-trips its authorizing_rule; a raw-mode entry
// (mode="raw", no authorizing_rule) is accepted and recorded; a record with
// no provenance omits the field entirely.
func TestKernEntryCarriesContextProvenance(t *testing.T) {
	gov := testRecord()
	gov.ContextProvenance = &domain.ContextProvenance{
		SchemaVersion: domain.ContextProvenanceSchemaVersion,
		Mode:          "governed",
		AuthorizingRule: &domain.AuthorizingRule{
			PolicySource: "task-scope",
			Policy:       "deny-unlisted",
			Fingerprint:  "sha256:abc123",
			DecidedAt:    "2026-08-30T12:00:00Z",
		},
		Index: domain.IndexProvenance{
			TreeOID:          "abc123",
			ContentRoot:      "sha256:def456",
			GitCommit:        "def456",
			BuiltAt:          "2026-08-30T11:59:00Z",
			FreshnessVerdict: "fresh",
		},
		Symbols: []domain.SymbolProvenance{
			{Name: "FuncName", Qualified: "pkg.FuncName", File: "path/to/file.go", Line: 42},
		},
	}

	payload, err := json.Marshal(kernEntry(gov))
	if err != nil {
		t.Fatalf("marshal kern entry: %v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal(payload, &entry); err != nil {
		t.Fatalf("kern entry is not valid JSON: %v", err)
	}
	prov, ok := entry["ContextProvenance"].(map[string]any)
	if !ok {
		t.Fatalf("ContextProvenance missing from kern entry: %s", payload)
	}
	// The provenance object must carry the kern schema keys verbatim.
	if sv, _ := prov["schema_version"].(float64); int(sv) != 1 {
		t.Errorf("schema_version = %v, want 1", prov["schema_version"])
	}
	if prov["mode"] != "governed" {
		t.Errorf("mode = %v, want governed", prov["mode"])
	}
	rule, ok := prov["authorizing_rule"].(map[string]any)
	if !ok {
		t.Fatalf("authorizing_rule missing from governed provenance: %s", payload)
	}
	if rule["policy_source"] != "task-scope" || rule["policy"] != "deny-unlisted" || rule["fingerprint"] != "sha256:abc123" || rule["decided_at"] != "2026-08-30T12:00:00Z" {
		t.Errorf("authorizing_rule = %v", rule)
	}
	idx, ok := prov["index"].(map[string]any)
	if !ok || idx["tree_oid"] != "abc123" || idx["content_root"] != "sha256:def456" || idx["git_commit"] != "def456" || idx["freshness_verdict"] != "fresh" {
		t.Errorf("index = %v", prov["index"])
	}
	syms, ok := prov["symbols"].([]any)
	if !ok || len(syms) != 1 {
		t.Fatalf("symbols = %v, want 1 entry", prov["symbols"])
	}
	s0, ok := syms[0].(map[string]any)
	if !ok || s0["name"] != "FuncName" || s0["qualified"] != "pkg.FuncName" || s0["file"] != "path/to/file.go" || s0["line"] != float64(42) {
		t.Errorf("symbols[0] = %v", syms[0])
	}

	// Raw mode: mode="raw", authorizing_rule absent, index/symbols present.
	raw := testRecord()
	raw.ContextProvenance = &domain.ContextProvenance{
		SchemaVersion: domain.ContextProvenanceSchemaVersion,
		Mode:          "raw",
		Index: domain.IndexProvenance{
			TreeOID:          "abc123",
			ContentRoot:      "sha256:def456",
			GitCommit:        "def456",
			BuiltAt:          "2026-08-30T11:59:00Z",
			FreshnessVerdict: "fresh",
		},
		Symbols: []domain.SymbolProvenance{
			{Name: "FuncName", Qualified: "pkg.FuncName", File: "path/to/file.go", Line: 42},
		},
	}
	rawPayload, err := json.Marshal(kernEntry(raw))
	if err != nil {
		t.Fatalf("marshal raw kern entry: %v", err)
	}
	var rawEntry map[string]any
	if err := json.Unmarshal(rawPayload, &rawEntry); err != nil {
		t.Fatalf("raw kern entry is not valid JSON: %v", err)
	}
	rawProv, ok := rawEntry["ContextProvenance"].(map[string]any)
	if !ok {
		t.Fatalf("raw-mode ContextProvenance missing from kern entry: %s", rawPayload)
	}
	if rawProv["mode"] != "raw" {
		t.Errorf("raw mode = %v, want raw", rawProv["mode"])
	}
	if _, has := rawProv["authorizing_rule"]; has {
		t.Errorf("raw provenance must not carry authorizing_rule: %s", rawPayload)
	}
	if _, has := rawProv["index"]; !has {
		t.Errorf("raw provenance must carry index: %s", rawPayload)
	}

	// No provenance: the field must be omitted entirely.
	plain, err := json.Marshal(kernEntry(testRecord()))
	if err != nil {
		t.Fatalf("marshal plain kern entry: %v", err)
	}
	var plainEntry map[string]any
	if err := json.Unmarshal(plain, &plainEntry); err != nil {
		t.Fatalf("plain kern entry is not valid JSON: %v", err)
	}
	if _, has := plainEntry["ContextProvenance"]; has {
		t.Errorf("ContextProvenance must be omitted when the record carries none: %s", plain)
	}
}

// --- P0.4 ValidationOutcome mapping tests ---
//
// The audit chain link carries blueprint's validation outcome to kern so kern
// can mark blocked context stale. The wire format is kern's AuditEntry field
// name ("ValidationOutcome") with the untagged Go field names of
// domain.ValidationOutcome as the nested keys.

func TestKernEntry_ValidationOutcome_Populated(t *testing.T) {
	r := testRecord() // BLOCK, ExitCode 1, CorrelationID bp-123
	// Extend with a duplicate blocking path (must dedup) and a non-blocking
	// finding (must be excluded from BlockedFiles).
	r.Findings = append(r.Findings,
		FindingMeta{RuleID: "arch:layers", Severity: domain.SeverityBlock, Category: domain.CategoryArchitecture, File: "web/web.go", Line: 11},
		FindingMeta{RuleID: "dup", Severity: domain.SeverityWarn, Category: domain.CategoryDuplication, File: "other.go"},
	)

	e := kernEntry(r)
	if e.ValidationOutcome == nil {
		t.Fatal("ValidationOutcome = nil, want populated")
	}
	vo := e.ValidationOutcome
	if vo.Status != "BLOCK" {
		t.Errorf("Status = %q, want BLOCK", vo.Status)
	}
	if vo.ExitCode != r.ExitCode {
		t.Errorf("ExitCode = %d, want %d", vo.ExitCode, r.ExitCode)
	}
	if vo.CorrelationID != "bp-123" {
		t.Errorf("CorrelationID = %q, want bp-123", vo.CorrelationID)
	}
	if vo.Findings != len(r.Findings) {
		t.Errorf("Findings = %d, want %d", vo.Findings, len(r.Findings))
	}
	if len(vo.BlockedFiles) != 1 || vo.BlockedFiles[0] != "web/web.go" {
		t.Errorf("BlockedFiles = %v, want [web/web.go] (unique, BLOCK-severity paths only)", vo.BlockedFiles)
	}

	// Wire format check: the JSON key is kern's exported field name, nested
	// keys are the untagged Go field names.
	payload, err := json.Marshal(kernEntry(r))
	if err != nil {
		t.Fatalf("marshal kern entry: %v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal(payload, &entry); err != nil {
		t.Fatalf("kern entry is not valid JSON: %v", err)
	}
	voRaw, ok := entry["ValidationOutcome"].(map[string]any)
	if !ok {
		t.Fatalf("ValidationOutcome missing from kern entry JSON: %s", payload)
	}
	for _, key := range []string{"Status", "ExitCode", "BlockedFiles", "CorrelationID", "Findings"} {
		if _, ok := voRaw[key]; !ok {
			t.Errorf("ValidationOutcome JSON missing key %q: %s", key, payload)
		}
	}
	if voRaw["Status"] != "BLOCK" || voRaw["CorrelationID"] != "bp-123" {
		t.Errorf("ValidationOutcome wire values = %v", voRaw)
	}
}

func TestKernEntry_ValidationOutcome_Pass(t *testing.T) {
	r := testRecord()
	r.Status = domain.StatusPass
	r.ExitCode = 0
	r.Findings = nil

	e := kernEntry(r)
	if e.ValidationOutcome == nil {
		t.Fatal("ValidationOutcome = nil, want populated even for PASS (kern consumes status)")
	}
	if e.ValidationOutcome.Status != "PASS" {
		t.Errorf("Status = %q, want PASS", e.ValidationOutcome.Status)
	}
	if e.ValidationOutcome.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", e.ValidationOutcome.ExitCode)
	}
	if len(e.ValidationOutcome.BlockedFiles) != 0 {
		t.Errorf("BlockedFiles = %v, want empty for PASS", e.ValidationOutcome.BlockedFiles)
	}
	if e.ValidationOutcome.Findings != 0 {
		t.Errorf("Findings = %d, want 0", e.ValidationOutcome.Findings)
	}
}

// --- P1.4 kern chain-hash capture tests ---

// TestLinkToKernChain_CapturesHash: a fake kern that prints kern's
// confirmation line (`appended <id> (hash <h>)`) on stdout — the chain hash
// must be captured on the Writer for the receipt's cross-chain link (P1.4).
func TestLinkToKernChain_CapturesHash(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-kern-hash")
	content := "#!/bin/sh\ncat > /dev/null\necho 'appended audit-5 (hash abc123)'\nexit 0\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake kern: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w := NewWriter(path).WithKernBinary(script)

	if err := w.Write(testRecord()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := w.LastKernChainHash(); got != "abc123" {
		t.Errorf("LastKernChainHash = %q, want abc123", got)
	}
}

// TestLinkToKernChain_NoHashParsed: kern output without a parseable
// confirmation line must leave the captured hash empty without failing Write
// (best-effort contract).
func TestLinkToKernChain_NoHashParsed(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-kern-plain")
	content := "#!/bin/sh\ncat > /dev/null\necho 'ok'\nexit 0\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake kern: %v", err)
	}

	dir := t.TempDir()
	w := NewWriter(filepath.Join(dir, "audit.jsonl")).WithKernBinary(script)
	if err := w.Write(testRecord()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := w.LastKernChainHash(); got != "" {
		t.Errorf("LastKernChainHash = %q, want empty for non-parseable output", got)
	}
}

func TestParseKernChainHash(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"appended audit-5 (hash abc123)\n", "abc123"},
		{"appended audit-5 (hash a1b2c3d4e5f67890a1b2c3d4e5f67890)\n", "a1b2c3d4e5f67890a1b2c3d4e5f67890"},
		{"prefix\nappended audit-9 (hash deadbeef)\nsuffix\n", "deadbeef"},
		{"", ""},
		{"some unrelated output\n", ""},
		{"appended audit-5 (hash )\n", ""},
		{"Appended audit-5 (hash abc123)\n", ""}, // wrong case — not kern's format
	}
	for _, c := range cases {
		if got := parseKernChainHash(c.in); got != c.want {
			t.Errorf("parseKernChainHash(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestWriterKernChainTimeout: a kern binary that never exits must not block
// Write — the chain link is bounded by kernLinkTimeout, the subprocess group
// is killed, and the local JSONL is still written (best-effort contract).
// Regression test for the unbounded-subprocess hang: before the timeout was
// added, a wedged `kern audit append` could stall a validation forever.
func TestWriterKernChainTimeout(t *testing.T) {
	old := kernLinkTimeout
	kernLinkTimeout = 500 * time.Millisecond
	defer func() { kernLinkTimeout = old }()

	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	// A shim that never exits (simulates a wedged kern binary). The `sleep`
	// grandchild inherits the stdout/stderr pipes, so this exercises the
	// process-group kill path, not just the direct-child kill.
	script := filepath.Join(t.TempDir(), "hung-kern")
	content := "#!/bin/sh\nsleep 3600\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write hung kern: %v", err)
	}

	w := NewWriter(path).WithKernBinary(script)
	start := time.Now()
	if err := w.Write(testRecord()); err != nil {
		t.Fatalf("Write with hung kern = %v, want nil (best-effort)", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Write blocked %v with a hung kern binary; want ~%v", elapsed, kernLinkTimeout)
	}

	// Local JSONL is still written.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("local JSONL not written when kern hangs: %v", err)
	}
	var onDisk Record
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("parse local audit: %v", err)
	}
	if onDisk.Hash == "" {
		t.Error("local record has no self-hash when kern hangs")
	}
	// The chain hash is not reported after a timeout.
	if got := w.LastKernChainHash(); got != "" {
		t.Errorf("LastKernChainHash = %q, want empty after timeout", got)
	}
}
