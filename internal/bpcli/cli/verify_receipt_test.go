package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/audit"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/bpreceipt/receipt"
)

// verifyReceiptFixture builds a repository with a one-record audit chain and
// a valid sealed receipt bound to it (H3/H4 satisfied), and returns the repo
// root and receipt id. The chain write may attempt a best-effort kern link
// (bounded; failures only log to stderr) — same pattern as the other cli
// tests, so no special env is needed.
func verifyReceiptFixture(t *testing.T) (root, id string) {
	t.Helper()
	root = t.TempDir()
	res := domain.ValidationResult{
		Status:        domain.StatusPass,
		ExitCode:      0,
		CorrelationID: "bp-d7",
		DurationMs:    7,
		Summary:       domain.Summary{Total: 1},
	}
	w := audit.NewWriter(filepath.Join(root, ".blueprint", "audit", "audit.jsonl"))
	writeCINoopAuditRecord(w, res, root)
	chainHash := w.LastHash()
	if chainHash == "" {
		t.Fatal("fixture: audit chain produced no hash")
	}
	r := receipt.Generate(res, root, "main", "HEAD", chainHash, "")
	if err := receipt.NewStore(root).Save(r); err != nil {
		t.Fatalf("fixture: save receipt: %v", err)
	}
	return root, r.ReceiptID
}

// tamperReceipt rewrites the receipt on disk so its signature no longer
// recomputes — a real tampered-checksum failure.
func tamperReceipt(t *testing.T, root, id string) {
	t.Helper()
	p := filepath.Join(root, ".blueprint", "receipts", id+".json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	tampered := strings.Replace(string(data), `"status": "PASS"`, `"status": "FAIL"`, 1)
	if tampered == string(data) {
		t.Fatalf("fixture: no %q to tamper in %s", `"status": "PASS"`, p)
	}
	if err := os.WriteFile(p, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write tampered receipt: %v", err)
	}
}

// captureRun swaps os.Stdout/os.Stderr for pipes, runs fn, and returns the
// captured streams. Not parallel-safe by design: the swapped streams are
// process-wide, so callers must not use t.Parallel.
func captureRun(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = wOut, wErr
	defer func() {
		os.Stdout, os.Stderr = oldOut, oldErr
	}()
	fn()
	_ = wOut.Close()
	_ = wErr.Close()
	outB, _ := io.ReadAll(rOut)
	errB, _ := io.ReadAll(rErr)
	return string(outB), string(errB)
}

// runVerifyCaptured runs runVerifyReceipt with os.Stdout/os.Stderr captured
// and returns (stdout, stderr, exit code).
func runVerifyCaptured(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	code := 0
	stdout, stderr := captureRun(t, func() {
		code = runVerifyReceipt(args)
	})
	return stdout, stderr, code
}

// jsonVerdict is the --json output shape.
type jsonVerdict struct {
	Valid     bool   `json:"valid"`
	ReceiptID string `json:"receipt_id"`
	Error     string `json:"error"`
}

// sarifDoc is the subset of the SARIF 2.1.0 document the tests assert on.
type sarifDoc struct {
	Version string `json:"version"`
	Runs    []struct {
		Results []struct {
			RuleID  string `json:"ruleId"`
			Level   string `json:"level"`
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
		} `json:"results"`
		Invocations []struct {
			ExecutionSuccessful bool `json:"executionSuccessful"`
		} `json:"invocations"`
	} `json:"runs"`
}

// inTotoDoc is the subset of the in-toto statement the tests assert on.
type inTotoDoc struct {
	Type      string `json:"_type"`
	Predicate struct {
		Type      string `json:"type"`
		Message   string `json:"message"`
		ReceiptID string `json:"receipt_id"`
	} `json:"predicate"`
}

// TestVerifyReceipt_InvalidFormatsStructured (D7): on an INVALID (tampered)
// receipt, every structured-output flag must emit a parseable document of the
// requested shape that carries the error — no more plain text on stderr for
// --sarif/--in-toto while --json alone gets a document. Plain text remains
// only when no format flag is given.
func TestVerifyReceipt_InvalidFormatsStructured(t *testing.T) {
	root, id := verifyReceiptFixture(t)
	tamperReceipt(t, root, id)

	cases := []struct {
		name  string
		flags []string
		check func(t *testing.T, stdout string)
	}{
		{
			name:  "json",
			flags: []string{"--json"},
			check: func(t *testing.T, stdout string) {
				var v jsonVerdict
				if err := json.Unmarshal([]byte(stdout), &v); err != nil {
					t.Fatalf("stdout is not parseable JSON: %v\n%s", err, stdout)
				}
				if v.Valid {
					t.Error("valid = true, want false on a tampered receipt")
				}
				if !strings.Contains(v.Error, "signature mismatch") {
					t.Errorf("error = %q, want it to carry the signature-mismatch failure", v.Error)
				}
			},
		},
		{
			name:  "sarif",
			flags: []string{"--sarif"},
			check: func(t *testing.T, stdout string) {
				var d sarifDoc
				if err := json.Unmarshal([]byte(stdout), &d); err != nil {
					t.Fatalf("stdout is not parseable JSON: %v\n%s", err, stdout)
				}
				if d.Version != "2.1.0" {
					t.Errorf("version = %q, want 2.1.0", d.Version)
				}
				if len(d.Runs) != 1 || len(d.Runs[0].Results) != 1 {
					t.Fatalf("runs/results = %d/%d, want 1 error result", len(d.Runs), len(d.Runs[0].Results))
				}
				res := d.Runs[0].Results[0]
				if res.Level != "error" {
					t.Errorf("result level = %q, want error", res.Level)
				}
				if !strings.Contains(res.Message.Text, "signature mismatch") {
					t.Errorf("result message = %q, want it to carry the failure", res.Message.Text)
				}
				if inv := d.Runs[0].Invocations; len(inv) != 1 || inv[0].ExecutionSuccessful {
					t.Error("invocations[0].executionSuccessful = true, want false on failure")
				}
			},
		},
		{
			name:  "in-toto",
			flags: []string{"--in-toto"},
			check: func(t *testing.T, stdout string) {
				var d inTotoDoc
				if err := json.Unmarshal([]byte(stdout), &d); err != nil {
					t.Fatalf("stdout is not parseable JSON: %v\n%s", err, stdout)
				}
				if d.Type != "https://in-toto.io/Statement/v0.1" {
					t.Errorf("_type = %q, want in-toto statement", d.Type)
				}
				if d.Predicate.Type != "failure" {
					t.Errorf("predicate.type = %q, want failure", d.Predicate.Type)
				}
				if !strings.Contains(d.Predicate.Message, "signature mismatch") {
					t.Errorf("predicate.message = %q, want it to carry the failure", d.Predicate.Message)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--repo", root, "--receipt-id", id}, tc.flags...)
			stdout, _, code := runVerifyCaptured(t, args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2 on a tampered receipt", code)
			}
			tc.check(t, stdout)
		})
	}

	// Plain text remains only when NO format flag is given — and it goes to
	// stderr, never stdout (so a format-flag parser is never fed it).
	stdout, stderr, code := runVerifyCaptured(t, "--repo", root, "--receipt-id", id)
	if code != 2 {
		t.Fatalf("plain-text exit = %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("plain-text mode wrote %q to stdout, want nothing", stdout)
	}
	if !strings.Contains(stderr, "INVALID") {
		t.Errorf("plain-text mode stderr missing INVALID marker:\n%s", stderr)
	}
}

// TestVerifyReceipt_NotFoundFormatsStructured (D7): a missing receipt is a
// failure (exit 3) and must also emit the requested structured shape with the
// error embedded — previously --sarif/--in-toto rendered a fallback document
// and even exited 0, which a CI gate could mistake for a verified receipt.
func TestVerifyReceipt_NotFoundFormatsStructured(t *testing.T) {
	root := t.TempDir() // no .blueprint/ at all: nothing to find

	cases := []struct {
		name  string
		flags []string
		check func(t *testing.T, stdout string)
	}{
		{
			name:  "json",
			flags: []string{"--json"},
			check: func(t *testing.T, stdout string) {
				var v jsonVerdict
				if err := json.Unmarshal([]byte(stdout), &v); err != nil {
					t.Fatalf("stdout is not parseable JSON: %v\n%s", err, stdout)
				}
				if v.Valid {
					t.Error("valid = true, want false")
				}
				if !strings.Contains(v.Error, "not found") {
					t.Errorf("error = %q, want it to carry the not-found failure", v.Error)
				}
			},
		},
		{
			name:  "sarif",
			flags: []string{"--sarif"},
			check: func(t *testing.T, stdout string) {
				var d sarifDoc
				if err := json.Unmarshal([]byte(stdout), &d); err != nil {
					t.Fatalf("stdout is not parseable JSON: %v\n%s", err, stdout)
				}
				if d.Version != "2.1.0" {
					t.Errorf("version = %q, want 2.1.0", d.Version)
				}
				if len(d.Runs) != 1 || len(d.Runs[0].Results) != 1 {
					t.Fatalf("runs/results = %d/%d, want 1 error result", len(d.Runs), len(d.Runs[0].Results))
				}
				res := d.Runs[0].Results[0]
				if res.Level != "error" {
					t.Errorf("result level = %q, want error", res.Level)
				}
				if !strings.Contains(res.Message.Text, "not found") {
					t.Errorf("result message = %q, want it to carry the not-found failure", res.Message.Text)
				}
			},
		},
		{
			name:  "in-toto",
			flags: []string{"--in-toto"},
			check: func(t *testing.T, stdout string) {
				var d inTotoDoc
				if err := json.Unmarshal([]byte(stdout), &d); err != nil {
					t.Fatalf("stdout is not parseable JSON: %v\n%s", err, stdout)
				}
				if d.Type != "https://in-toto.io/Statement/v0.1" {
					t.Errorf("_type = %q, want in-toto statement", d.Type)
				}
				if d.Predicate.Type != "failure" {
					t.Errorf("predicate.type = %q, want failure", d.Predicate.Type)
				}
				if !strings.Contains(d.Predicate.Message, "not found") {
					t.Errorf("predicate.message = %q, want it to carry the not-found failure", d.Predicate.Message)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--repo", root, "--receipt-id", "bogus"}, tc.flags...)
			stdout, _, code := runVerifyCaptured(t, args...)
			if code != 3 {
				t.Fatalf("exit = %d, want 3 on a missing receipt", code)
			}
			tc.check(t, stdout)
		})
	}
}

// TestVerifyReceipt_ValidFormatsUnchanged (D7): a VALID receipt must produce
// exactly the output it produced before — same shapes, same exit codes — for
// each format flag and for plain text.
func TestVerifyReceipt_ValidFormatsUnchanged(t *testing.T) {
	root, id := verifyReceiptFixture(t)

	// --json: the historical verdict object with valid=true.
	stdout, _, code := runVerifyCaptured(t, "--repo", root, "--receipt-id", id, "--json")
	if code != 0 {
		t.Fatalf("--json exit = %d, want 0", code)
	}
	var v jsonVerdict
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("--json stdout not parseable: %v\n%s", err, stdout)
	}
	if !v.Valid || v.ReceiptID != id || v.Error != "" {
		t.Errorf("--json = %+v, want valid=true receipt_id=%q error=\"\"", v, id)
	}

	// --sarif: SARIF 2.1.0 with a successful invocation.
	stdout, _, code = runVerifyCaptured(t, "--repo", root, "--receipt-id", id, "--sarif")
	if code != 0 {
		t.Fatalf("--sarif exit = %d, want 0", code)
	}
	var s sarifDoc
	if err := json.Unmarshal([]byte(stdout), &s); err != nil {
		t.Fatalf("--sarif stdout not parseable: %v\n%s", err, stdout)
	}
	if s.Version != "2.1.0" || len(s.Runs) != 1 {
		t.Fatalf("--sarif version/runs = %q/%d, want 2.1.0/1", s.Version, len(s.Runs))
	}
	if inv := s.Runs[0].Invocations; len(inv) != 1 || !inv[0].ExecutionSuccessful {
		t.Errorf("--sarif invocations[0].executionSuccessful = %+v, want true", inv)
	}

	// --in-toto: in-toto statement with the receipt in the predicate.
	stdout, _, code = runVerifyCaptured(t, "--repo", root, "--receipt-id", id, "--in-toto")
	if code != 0 {
		t.Fatalf("--in-toto exit = %d, want 0", code)
	}
	var it inTotoDoc
	if err := json.Unmarshal([]byte(stdout), &it); err != nil {
		t.Fatalf("--in-toto stdout not parseable: %v\n%s", err, stdout)
	}
	if it.Type != "https://in-toto.io/Statement/v0.1" || it.Predicate.ReceiptID != id {
		t.Errorf("--in-toto _type/predicate.receipt_id = %q/%q, want statement/%q", it.Type, it.Predicate.ReceiptID, id)
	}

	// No flag: the historical human-readable verdict on stdout.
	stdout, _, code = runVerifyCaptured(t, "--repo", root, "--receipt-id", id)
	if code != 0 {
		t.Fatalf("plain-text exit = %d, want 0", code)
	}
	for _, marker := range []string{"VALID", "Status: PASS", "Audit chain intact (1 records)", "Signature verified"} {
		if !strings.Contains(stdout, marker) {
			t.Errorf("plain-text output missing %q:\n%s", marker, stdout)
		}
	}
}

// TestVerifyReceiptPositionalIDResolves locks the positional receipt-ID form:
// a positional that is NOT an existing file on disk (the `kern ci` hint
// "Verify with: kern verify-receipt bp-<id>") resolves through the store
// instead of being treated as a file path.
func TestVerifyReceiptPositionalIDResolves(t *testing.T) {
	root, id := verifyReceiptFixture(t)
	stdout, _, code := runVerifyCaptured(t, "--repo", root, id)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 for a valid positional receipt id", code)
	}
	for _, marker := range []string{"VALID", "Receipt " + id} {
		if !strings.Contains(stdout, marker) {
			t.Errorf("output missing %q:\n%s", marker, stdout)
		}
	}
}

// TestVerifyReceiptFullFilenameResolves locks the full-filename positional
// form: `kern verify-receipt bp-<id>.json` (the store names files <id>.json)
// must strip the trailing ".json" and resolve to the stored receipt id
// instead of failing lookup with "Receipt not found". The --receipt-id flag
// form gets the same treatment.
func TestVerifyReceiptFullFilenameResolves(t *testing.T) {
	root, id := verifyReceiptFixture(t)

	// Positional form: bp-<id>.json is not a file in the working directory,
	// so it resolves as a receipt id with the ".json" suffix stripped.
	stdout, _, code := runVerifyCaptured(t, "--repo", root, id+".json")
	if code != 0 {
		t.Fatalf("positional exit = %d, want 0 for the full receipt filename", code)
	}
	for _, marker := range []string{"VALID", "Receipt " + id} {
		if !strings.Contains(stdout, marker) {
			t.Errorf("positional output missing %q:\n%s", marker, stdout)
		}
	}

	// --receipt-id flag form with a trailing ".json".
	stdout, _, code = runVerifyCaptured(t, "--repo", root, "--receipt-id", id+".json")
	if code != 0 {
		t.Fatalf("--receipt-id exit = %d, want 0 for the full receipt filename", code)
	}
	if !strings.Contains(stdout, "Receipt "+id) {
		t.Errorf("--receipt-id output missing %q:\n%s", "Receipt "+id, stdout)
	}
}
