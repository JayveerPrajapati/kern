package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/receipt"
)

// runVerifyReceipt runs `blueprint verify-receipt --repo dir [--receipt-id
// id]` and returns (combined output, exit code).
func runVerifyReceiptCmd(t *testing.T, binPath, dir, id string) (string, int) {
	t.Helper()
	args := []string{"verify-receipt", "--repo", dir}
	if id != "" {
		args = append(args, "--receipt-id", id)
	}
	cmd := exec.Command(binPath, args...)
	cmd.Env = append(os.Environ(), "KERN_BINARY="+os.Getenv("KERN_BINARY"))
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run blueprint verify-receipt: %v\n%s", err, out)
		}
	}
	return string(out), code
}

// runVerifyReceiptInDir runs `blueprint verify-receipt --repo dir` with the
// process working directory set to workDir (mirroring where `blueprint ci`
// writes its default blueprint-result.json artifact) and returns (combined
// output, exit code). extra args (e.g. --json) are appended verbatim.
func runVerifyReceiptInDir(t *testing.T, binPath, dir, workDir, id string, extra ...string) (string, int) {
	t.Helper()
	args := []string{"verify-receipt", "--repo", dir}
	if id != "" {
		args = append(args, "--receipt-id", id)
	}
	args = append(args, extra...)
	cmd := exec.Command(binPath, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "KERN_BINARY="+os.Getenv("KERN_BINARY"))
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run blueprint verify-receipt: %v\n%s", err, out)
		}
	}
	return string(out), code
}

// runVerifyReceiptInDirSplit is like runVerifyReceiptInDir but returns stdout
// and stderr separately: the JSON verdict is written to stdout while
// best-effort kern WARN lines (and the text-mode staleness note) go to stderr.
func runVerifyReceiptInDirSplit(t *testing.T, binPath, dir, workDir, id string, extra ...string) (string, string, int) {
	t.Helper()
	args := []string{"verify-receipt", "--repo", dir}
	if id != "" {
		args = append(args, "--receipt-id", id)
	}
	args = append(args, extra...)
	cmd := exec.Command(binPath, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "KERN_BINARY="+os.Getenv("KERN_BINARY"))
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run blueprint verify-receipt: %v", err)
		}
	}
	return stdout.String(), stderr.String(), code
}

var receiptIDRe = regexp.MustCompile(`Receipt (\S+) generated`)

// receiptIDFromOutput extracts the receipt id from `blueprint ci` stderr.
func receiptIDFromOutput(t *testing.T, out string) string {
	t.Helper()
	m := receiptIDRe.FindStringSubmatch(out)
	if len(m) != 2 {
		t.Fatalf("no receipt id in ci output:\n%s", out)
	}
	return m[1]
}

// TestVerifyReceipt_EndToEnd (P1.4): blueprint ci (PASS) generates a receipt;
// verify-receipt reports VALID (exit 0); a tampered receipt reports INVALID
// (exit 2); a bogus id reports not-found (exit 3); a tampered audit chain
// invalidates the receipt (exit 2).
func TestVerifyReceipt_EndToEnd(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{
			"web/clean.go": "package web\nfunc Clean() {}\n",
		},
	)

	// 1. blueprint ci (PASS) → receipt generated and announced on stderr.
	_, stderr, exitCode, artifact := runCICommand(t, binPath, dir, kernPath)
	if exitCode != 0 {
		t.Fatalf("ci exit=%d want 0 (PASS); stderr:\n%s", exitCode, stderr)
	}
	if artifact.Status != "PASS" {
		t.Fatalf("ci status = %s, want PASS", artifact.Status)
	}
	if !strings.Contains(stderr, "Receipt ") || !strings.Contains(stderr, "Verify with: blueprint verify-receipt") {
		t.Fatalf("ci output missing receipt announcement:\n%s", stderr)
	}
	id := receiptIDFromOutput(t, stderr)
	receiptPath := filepath.Join(dir, ".blueprint", "receipts", id+".json")
	if _, err := os.Stat(receiptPath); err != nil {
		t.Fatalf("receipt file not written at %s: %v", receiptPath, err)
	}

	// 2. verify-receipt → VALID, exit 0.
	out, code := runVerifyReceiptCmd(t, binPath, dir, id)
	if code != 0 {
		t.Fatalf("verify-receipt exit=%d want 0; output:\n%s", code, out)
	}
	if !strings.Contains(out, "VALID") ||
		!strings.Contains(out, "Status: PASS") ||
		!strings.Contains(out, "Audit chain intact (1 records)") ||
		!strings.Contains(out, "Signature verified") {
		t.Fatalf("verify-receipt output missing VALID markers:\n%s", out)
	}

	// 3. Tamper with the receipt on disk → verify-receipt → exit 2.
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	tampered := strings.Replace(string(data), `"status": "PASS"`, `"status": "FAIL"`, 1)
	if tampered == string(data) {
		t.Fatal("test setup: status field not found in receipt JSON")
	}
	if err := os.WriteFile(receiptPath, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write tampered receipt: %v", err)
	}
	out, code = runVerifyReceiptCmd(t, binPath, dir, id)
	if code != 2 {
		t.Fatalf("verify-receipt on tampered receipt exit=%d want 2; output:\n%s", code, out)
	}
	if !strings.Contains(out, "INVALID") {
		t.Fatalf("verify-receipt output missing INVALID marker:\n%s", out)
	}
	// Restore the original receipt so the later prefix-tolerant step (5b) can
	// verify this same, now-untampered receipt at an earlier chain point.
	if err := os.WriteFile(receiptPath, data, 0o644); err != nil {
		t.Fatalf("restore receipt: %v", err)
	}

	// 4. verify-receipt --receipt-id bogus → exit 3 (not found).
	out, code = runVerifyReceiptCmd(t, binPath, dir, "bogus")
	if code != 3 {
		t.Fatalf("verify-receipt bogus id exit=%d want 3; output:\n%s", code, out)
	}

	// 5. Fresh CI run (--no-cache, so validation actually runs and a second
	// receipt is generated), then tamper with the audit chain → verify-receipt
	// → exit 2.
	_, stderr2, exitCode2, _ := runCICommand(t, binPath, dir, kernPath, "--no-cache")
	if exitCode2 != 0 {
		t.Fatalf("second ci exit=%d want 0; stderr:\n%s", exitCode2, stderr2)
	}
	id2 := receiptIDFromOutput(t, stderr2)

	// 5b. Prefix-tolerant verification (H3): the chain now has TWO records, so
	// the FIRST receipt's audit_chain_hash is no longer the chain's last hash.
	// It must still verify (exit 0) — its hash is a genuine record hash
	// somewhere in the chain. Before this fix, only the newest receipt ever
	// validated.
	out, code = runVerifyReceiptCmd(t, binPath, dir, id)
	if code != 0 {
		t.Fatalf("verify-receipt on OLDER receipt after chain grew exit=%d want 0 (prefix-tolerant); output:\n%s", code, out)
	}
	if !strings.Contains(out, "VALID") {
		t.Fatalf("verify-receipt output missing VALID marker for older receipt:\n%s", out)
	}

	auditPath := filepath.Join(dir, ".blueprint", "audit", "audit.jsonl")
	adata, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit chain: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(adata), "\n"), "\n")
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("parse first audit record: %v", err)
	}
	rec["status"] = "BLOCK" // tamper: stored hash no longer recomputes
	b, _ := json.Marshal(rec)
	lines[0] = string(b)
	if err := os.WriteFile(auditPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write tampered audit chain: %v", err)
	}

	out, code = runVerifyReceiptCmd(t, binPath, dir, id2)
	if code != 2 {
		t.Fatalf("verify-receipt on broken audit chain exit=%d want 2; output:\n%s", code, out)
	}
	if !strings.Contains(out, "audit chain broken") {
		t.Fatalf("verify-receipt output missing broken-chain marker:\n%s", out)
	}
}

// TestVerifyReceipt_NoReceipt: a repo without receipts reports exit 3 (not
// found) — the "receipt required for merge" gate fails closed.
func TestVerifyReceipt_NoReceipt(t *testing.T) {
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{"web/web.go": "package web\nfunc Handle() {}\n"},
		map[string]string{},
	)

	out, code := runVerifyReceiptCmd(t, binPath, dir, "")
	if code != 3 {
		t.Fatalf("verify-receipt on repo without receipts exit=%d want 3; output:\n%s", code, out)
	}
	if !strings.Contains(out, "not found") {
		t.Fatalf("verify-receipt output missing not-found marker:\n%s", out)
	}
}

// TestVerifyReceipt_EmptyChainHashRejected (H4): a receipt whose
// audit_chain_hash is empty (the audit write failed or the chain was empty at
// seal time) must fail closed with exit 2. An empty binding is NOT a valid
// binding — before this fix `"" != ""` evaluated false and the receipt passed.
func TestVerifyReceipt_EmptyChainHashRejected(t *testing.T) {
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{"web/web.go": "package web\nfunc Handle() {}\n"},
		map[string]string{},
	)

	// Craft a signed receipt with an empty audit chain binding and save it
	// into the store. Generate seals it normally; only the binding is missing.
	res := domain.ValidationResult{
		Status:        domain.StatusPass,
		ExitCode:      0,
		CorrelationID: "bp-nobinding",
	}
	r := receipt.Generate(res, dir, "main", "HEAD", "", "")
	if err := receipt.NewStore(dir).Save(r); err != nil {
		t.Fatalf("save receipt: %v", err)
	}

	out, code := runVerifyReceiptCmd(t, binPath, dir, "bp-nobinding")
	if code != 2 {
		t.Fatalf("verify-receipt on empty-chain-hash receipt exit=%d want 2; output:\n%s", code, out)
	}
	if !strings.Contains(out, "no audit chain binding") {
		t.Fatalf("verify-receipt output missing no-binding marker:\n%s", out)
	}
}

// TestVerifyKernChainHash (H5): verifyKernChainHash checks the receipt's kern
// chain hash against `kern audit --json`. Covered: hash found (nil), hash
// absent (hard errKernChainHashNotFound), missing binary (soft
// errKernChainCheckSkipped), failing kern (soft skip), and empty expected hash
// (nil — nothing to check).
func TestVerifyKernChainHash(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const foundHash = "aabbccdd"
	const otherHash = "11223344"

	fakeKern := filepath.Join(dir, "kern")
	kernScript := "#!/bin/sh\n" +
		"if [ \"$1\" = \"audit\" ]; then\n" +
		"  cat <<'EOF'\n" +
		"[\n" +
		`  {"ID":"audit-1","Hash":"` + otherHash + `"},` + "\n" +
		`  {"ID":"audit-2","Hash":"` + foundHash + `"}` + "\n" +
		"]\n" +
		"EOF\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(fakeKern, []byte(kernScript), 0o755); err != nil {
		t.Fatalf("write fake kern: %v", err)
	}

	t.Run("hash found", func(t *testing.T) {
		t.Setenv("KERN_BINARY", fakeKern)
		if err := verifyKernChainHash(repo, foundHash); err != nil {
			t.Fatalf("verifyKernChainHash = %v, want nil", err)
		}
	})
	t.Run("hash not found is hard failure", func(t *testing.T) {
		t.Setenv("KERN_BINARY", fakeKern)
		err := verifyKernChainHash(repo, "deadbeef")
		if !errors.Is(err, errKernChainHashNotFound) {
			t.Fatalf("err = %v, want errKernChainHashNotFound", err)
		}
	})
	t.Run("empty expected hash is a no-op", func(t *testing.T) {
		t.Setenv("KERN_BINARY", fakeKern)
		if err := verifyKernChainHash(repo, ""); err != nil {
			t.Fatalf("verifyKernChainHash(\"\") = %v, want nil", err)
		}
	})
	t.Run("missing binary is soft skip", func(t *testing.T) {
		t.Setenv("KERN_BINARY", filepath.Join(dir, "no-such-kern"))
		err := verifyKernChainHash(repo, foundHash)
		if !errors.Is(err, errKernChainCheckSkipped) {
			t.Fatalf("err = %v, want errKernChainCheckSkipped", err)
		}
	})
	t.Run("failing kern is soft skip", func(t *testing.T) {
		failKern := filepath.Join(dir, "fail-kern")
		if err := os.WriteFile(failKern, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
			t.Fatalf("write failing kern: %v", err)
		}
		t.Setenv("KERN_BINARY", failKern)
		err := verifyKernChainHash(repo, foundHash)
		if !errors.Is(err, errKernChainCheckSkipped) {
			t.Fatalf("err = %v, want errKernChainCheckSkipped", err)
		}
	})
	t.Run("successful kern without hash is hard failure", func(t *testing.T) {
		// kern runs fine (exit 0) but its output does not contain the hash:
		// the receipt claims a kern binding that does not exist.
		garbageKern := filepath.Join(dir, "garbage-kern")
		if err := os.WriteFile(garbageKern, []byte("#!/bin/sh\nprintf 'no entries here\\n'\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write garbage kern: %v", err)
		}
		t.Setenv("KERN_BINARY", garbageKern)
		err := verifyKernChainHash(repo, foundHash)
		if !errors.Is(err, errKernChainHashNotFound) {
			t.Fatalf("err = %v, want errKernChainHashNotFound", err)
		}
	})
}

// TestVerifyReceipt_LatestNotesBlockedCIRun (staleness note): verify-receipt
// WITHOUT --receipt-id resolves to the latest receipt. When the most recent
// `blueprint ci` run was BLOCKED (or errored) — such runs seal no receipt
// (see ci.go sealReceipt) — the latest receipt predates that red run and the
// user must be told. The note is best-effort: a missing/unparseable artifact
// or a non-BLOCK/ERROR status yields no note, and the verification result and
// exit code (0) never change. An explicit --receipt-id also suppresses it.
func TestVerifyReceipt_LatestNotesBlockedCIRun(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{
			"web/clean.go": "package web\nfunc Clean() {}\n",
		},
	)

	// A PASS `blueprint ci` run seals the receipt that verify-receipt will
	// resolve as "latest".
	_, stderr, exitCode, artifact := runCICommand(t, binPath, dir, kernPath)
	if exitCode != 0 || artifact.Status != "PASS" {
		t.Fatalf("ci exit=%d status=%s want 0/PASS; stderr:\n%s", exitCode, artifact.Status, stderr)
	}
	id := receiptIDFromOutput(t, stderr)

	// A later red `blueprint ci` run (BLOCK) seals no receipt — simulate its
	// artifact at the default location ci writes it (cwd-relative
	// blueprint-result.json).
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "blueprint-result.json"), []byte(`{"status":"BLOCK"}`), 0o644); err != nil {
		t.Fatalf("write blocked ci artifact: %v", err)
	}

	// No --receipt-id + stale BLOCK artifact → exit 0 with the staleness note.
	out, code := runVerifyReceiptInDir(t, binPath, dir, workDir, "")
	if code != 0 {
		t.Fatalf("verify-receipt exit=%d want 0; output:\n%s", code, out)
	}
	wantNote := fmt.Sprintf("note: the most recent ci run was BLOCK and has no receipt; this receipt %s is from an earlier successful run", id)
	if !strings.Contains(out, wantNote) {
		t.Fatalf("output missing staleness note %q:\n%s", wantNote, out)
	}

	// An ERROR artifact also notes.
	if err := os.WriteFile(filepath.Join(workDir, "blueprint-result.json"), []byte(`{"status":"ERROR"}`), 0o644); err != nil {
		t.Fatalf("write errored ci artifact: %v", err)
	}
	out, code = runVerifyReceiptInDir(t, binPath, dir, workDir, "")
	if code != 0 {
		t.Fatalf("verify-receipt (ERROR artifact) exit=%d want 0; output:\n%s", code, out)
	}
	wantErrNote := fmt.Sprintf("note: the most recent ci run was ERROR and has no receipt; this receipt %s is from an earlier successful run", id)
	if !strings.Contains(out, wantErrNote) {
		t.Fatalf("output missing ERROR staleness note %q:\n%s", wantErrNote, out)
	}

	// No artifact → silent skip, still exit 0.
	emptyDir := t.TempDir()
	out, code = runVerifyReceiptInDir(t, binPath, dir, emptyDir, "")
	if code != 0 {
		t.Fatalf("verify-receipt without artifact exit=%d want 0; output:\n%s", code, out)
	}
	if strings.Contains(out, "note: the most recent ci run") {
		t.Fatalf("output has a staleness note without an artifact:\n%s", out)
	}

	// Explicit --receipt-id → no note even with a stale artifact.
	out, code = runVerifyReceiptInDir(t, binPath, dir, workDir, id)
	if code != 0 {
		t.Fatalf("verify-receipt --receipt-id exit=%d want 0; output:\n%s", code, out)
	}
	if strings.Contains(out, "note: the most recent ci run") {
		t.Fatalf("output has a staleness note with explicit --receipt-id:\n%s", out)
	}

	// JSON mode: additive "note" field, still exit 0. The JSON verdict is on
	// stdout (best-effort kern WARN lines go to stderr), so parse stdout.
	// Restore the BLOCK artifact first — the earlier ERROR sub-case overwrote
	// it, and the note must match the artifact that is actually present.
	if err := os.WriteFile(filepath.Join(workDir, "blueprint-result.json"), []byte(`{"status":"BLOCK"}`), 0o644); err != nil {
		t.Fatalf("rewrite blocked ci artifact: %v", err)
	}
	outJSON, _, code := runVerifyReceiptInDirSplit(t, binPath, dir, workDir, "", "--json")
	if code != 0 {
		t.Fatalf("verify-receipt --json exit=%d want 0; stdout:\n%s", code, outJSON)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(outJSON), &raw); err != nil {
		t.Fatalf("parse verify-receipt json output: %v\n%s", err, outJSON)
	}
	var note string
	if err := json.Unmarshal(raw["note"], &note); err != nil {
		t.Fatalf("json missing parseable \"note\" field: %v; output:\n%s", err, outJSON)
	}
	if note != wantNote {
		t.Fatalf("json note = %q, want %q", note, wantNote)
	}
}

func TestVerifyReceipt_SARIF_InToto_CheckDiff(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{
			"pkg/service.go": "package pkg\nfunc Run() {}\n",
		},
		map[string]string{
			"pkg/clean.go": "package pkg\nfunc Clean() {}\n",
		},
	)

	_, stderr, exitCode, _ := runCICommand(t, binPath, dir, kernPath)
	if exitCode != 0 {
		t.Fatalf("ci exit=%d want 0; stderr:\n%s", exitCode, stderr)
	}
	id := receiptIDFromOutput(t, stderr)

	// 1. Test --sarif export
	sarifOut, _, code := runVerifyReceiptInDirSplit(t, binPath, dir, dir, id, "--sarif")
	if code != 0 {
		t.Fatalf("verify-receipt --sarif exit=%d want 0; output:\n%s", code, sarifOut)
	}
	var sarifDoc map[string]any
	if err := json.Unmarshal([]byte(sarifOut), &sarifDoc); err != nil {
		t.Fatalf("failed to parse SARIF JSON: %v; output:\n%s", err, sarifOut)
	}
	if sarifDoc["version"] != "2.1.0" {
		t.Fatalf("expected SARIF 2.1.0, got: %v", sarifDoc["version"])
	}

	// 2. Test --in-toto export
	inTotoOut, _, code := runVerifyReceiptInDirSplit(t, binPath, dir, dir, id, "--in-toto")
	if code != 0 {
		t.Fatalf("verify-receipt --in-toto exit=%d want 0; output:\n%s", code, inTotoOut)
	}
	var inTotoDoc map[string]any
	if err := json.Unmarshal([]byte(inTotoOut), &inTotoDoc); err != nil {
		t.Fatalf("failed to parse in-toto JSON: %v; output:\n%s", err, inTotoOut)
	}
	if inTotoDoc["_type"] != "https://in-toto.io/Statement/v0.1" {
		t.Fatalf("expected in-toto statement, got: %v", inTotoDoc["_type"])
	}

	// 3. Test --check-diff on clean repository
	g11Git(t, dir, "checkout", "feature")
	checkDiffOut, _, code := runVerifyReceiptInDirSplit(t, binPath, dir, dir, id, "--check-diff")
	if code != 0 {
		t.Fatalf("verify-receipt --check-diff exit=%d want 0; output:\n%s", code, checkDiffOut)
	}

	// 4. Test --check-diff detects tamper when tracked file is mutated after receipt
	g11Write(t, dir, "pkg/service.go", "package pkg\nfunc Run() { panic(\"injected\") }\n")
	tamperOut, tamperErr, code := runVerifyReceiptInDirSplit(t, binPath, dir, dir, id, "--check-diff")
	if code != 2 {
		t.Fatalf("expected exit 2 on tampered working tree, got exit=%d; output:\n%s\nstderr:\n%s", code, tamperOut, tamperErr)
	}
	if !strings.Contains(tamperErr, "tamper detected") && !strings.Contains(tamperOut, "tamper detected") {
		t.Fatalf("expected 'tamper detected' in output, got out=%q err=%q", tamperOut, tamperErr)
	}
}
