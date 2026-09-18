package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/audit"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/bpreceipt/receipt"
)

// runVerifyReceipt implements `blueprint verify-receipt` — the merge-time
// enforcement half of the P1.4 tamper-evident receipt. Given a receipt id
// (or the latest receipt), it independently verifies:
//
//  1. the receipt's own signature (sha256 over its canonical JSON with
//     Signature cleared) and schema version — a tampered receipt fails;
//  2. the receipt carries a non-empty audit_chain_hash — a receipt with no
//     chain binding cannot be validated (H4);
//  3. the local audit chain (.blueprint/audit/audit.jsonl) is unbroken —
//     every record's hash recomputes and each PreviousHash links to the
//     preceding record's hash;
//  4. the receipt's audit_chain_hash appears SOMEWHERE in the chain (H3) —
//     prefix-tolerant: every later CI run appends a record, so an earlier
//     receipt's endpoint hash is no longer the last hash but is still a
//     genuine record hash in the chain;
//  5. when the receipt cites kern's chain hash, that hash appears in kern's
//     audit trail (H5) — best-effort: a missing/broken kern only warns.
//
// This is what a CI status check or PR review runs to make the receipt a
// merge requirement that `git commit --no-verify` cannot bypass (the hook is
// a local gate; the receipt is the branch-protection evidence).
//
// Exit codes: 0 = valid, 2 = tampered receipt or broken audit chain, 3 =
// receipt not found.
func runVerifyReceipt(args []string) int {
	fs, err := parseVerifyReceiptFlags(args)
	if err != nil {
		return emitVerifyParseFailure(args, err)
	}
	repoRoot := fs.Lookup("repo").Value.(flag.Getter).Get().(string)
	receiptID := fs.Lookup("receipt-id").Value.(flag.Getter).Get().(string)
	jsonOut := fs.Lookup("json").Value.(flag.Getter).Get().(bool)
	sarifOut := fs.Lookup("sarif").Value.(flag.Getter).Get().(bool)
	inTotoOut := fs.Lookup("in-toto").Value.(flag.Getter).Get().(bool)
	checkDiff := fs.Lookup("check-diff").Value.(flag.Getter).Get().(bool)
	format := verifyOutputFormat(jsonOut, sarifOut, inTotoOut)

	absRoot, err := resolveReceiptRoot(repoRoot)
	if err != nil {
		return emitVerifyFailure(format, nil, err.Error(), err.Error(), 2)
	}

	// Load the receipt: by explicit id, the most recent one, or a positional
	// JSON file (a receipt, or the CI artifact `kern ci --artifact-file`
	// wrote — see loadReceiptFile).
	explicitID := receiptID != "" || len(fs.Args()) > 0
	var r *receipt.Receipt
	if files := fs.Args(); len(files) > 0 {
		// A positional that names an existing file on disk is the historical
		// file-path form (a receipt, or a CI artifact — see loadReceiptFile).
		// A positional that is NOT a file (e.g. the `kern ci` hint
		// "Verify with: kern verify-receipt bp-<id>") resolves as a receipt
		// ID from the store.
		if _, statErr := os.Stat(files[0]); statErr == nil {
			var effRoot string
			r, effRoot, err = loadReceiptFile(files[0], absRoot)
			if err != nil {
				if errors.Is(err, errReceiptNotSealed) {
					return emitVerifyFailure(format, nil, err.Error(), err.Error(), 3)
				}
				if errors.Is(err, receipt.ErrNotFound) {
					msg := fmt.Sprintf("Receipt not found: %v", err)
					return emitVerifyFailure(format, nil, msg, msg, 3)
				}
				return emitVerifyFailure(format, nil, err.Error(), fmt.Sprintf("Receipt INVALID: %v", err), 2)
			}
			if effRoot != "" {
				absRoot = effRoot
			}
		} else {
			r, err = receipt.NewStore(absRoot).Get(stripReceiptJSONSuffix(files[0]))
			if err != nil {
				if errors.Is(err, receipt.ErrNotFound) {
					return emitVerifyFailure(format, nil, "receipt not found", "Receipt not found.", 3)
				}
				return emitVerifyFailure(format, nil, err.Error(), fmt.Sprintf("Receipt INVALID: %v", err), 2)
			}
		}
	} else {
		store := receipt.NewStore(absRoot)
		if receiptID != "" {
			r, err = store.Get(stripReceiptJSONSuffix(receiptID))
		} else {
			r, err = store.Latest()
		}
	}
	if err != nil {
		if errors.Is(err, receipt.ErrNotFound) {
			return emitVerifyFailure(format, nil, "receipt not found", "Receipt not found.", 3)
		}
		// A receipt exists but fails verification (tampered) or is unreadable.
		return emitVerifyFailure(format, nil, err.Error(), fmt.Sprintf("Receipt INVALID: %v", err), 2)
	}

	// 1. Receipt self-integrity: signature + schema version.
	if err := r.Verify(); err != nil {
		return emitVerifyFailure(format, r, err.Error(), fmt.Sprintf("Receipt %s INVALID: %v", r.ReceiptID, err), 2)
	}

	// 2. A receipt with no audit chain binding cannot be validated (H4): if
	// the audit write failed or the chain was empty at seal time, the receipt
	// stamped AuditChainHash "" and must fail closed.
	if r.AuditChainHash == "" {
		msg := "receipt has no audit chain binding (empty chain hash)"
		return emitVerifyFailure(format, r, msg, fmt.Sprintf("Receipt %s INVALID: %s", r.ReceiptID, msg), 2)
	}

	// 3. Audit chain integrity: re-read the JSONL and walk the hashes.
	auditWriter := audit.NewWriter(filepath.Join(absRoot, ".blueprint", "audit", "audit.jsonl"))
	lastHash, err := auditWriter.VerifyChain()
	if err != nil {
		return emitVerifyFailure(format, r, "audit chain broken: "+err.Error(), fmt.Sprintf("Receipt %s INVALID: audit chain broken: %v", r.ReceiptID, err), 2)
	}

	// 4. The receipt's audit_chain_hash must appear somewhere in the chain
	// (H3). Every later CI run appends a record, so the sealed endpoint is no
	// longer the last hash — but it must still be a genuine record hash. This
	// makes receipts prefix-tolerant: only the NEWEST receipt ever validated
	// before; now any receipt that was sealed at a real point in the chain
	// validates.
	found, err := auditWriter.ChainContainsHash(r.AuditChainHash)
	if err != nil {
		return emitVerifyFailure(format, r, "audit chain unreadable: "+err.Error(), fmt.Sprintf("Receipt %s INVALID: audit chain unreadable: %v", r.ReceiptID, err), 2)
	}
	if !found {
		msg := fmt.Sprintf("audit_chain_hash %q not found in audit chain (chain last hash %q)", r.AuditChainHash, lastHash)
		return emitVerifyFailure(format, r, msg, fmt.Sprintf("Receipt %s INVALID: %s", r.ReceiptID, msg), 2)
	}

	// 5. Cross-chain link to kern (H5), best-effort: when the receipt cites
	// kern's chain hash, check it appears in kern's audit trail. A missing or
	// broken kern binary only warns (the local chain is authoritative); a kern
	// that RUNS but does not contain the hash is a hard failure — the receipt
	// claims a kern binding that does not exist.
	if r.KernChainHash != "" {
		if err := verifyKernChainHash(absRoot, r.KernChainHash); err != nil {
			if errors.Is(err, errKernChainHashNotFound) {
				return emitVerifyFailure(format, r, err.Error(), fmt.Sprintf("Receipt %s INVALID: %s", r.ReceiptID, err), 2)
			}
			// Best-effort skip: kern unavailable or the query failed. Warn but
			// do not fail — the local chain binding above already validated.
			fmt.Fprintf(os.Stderr, "WARN: %v\n", err)
		}
	}

	// 6. Diff integrity check: verify PR git state / diff has not been tampered with
	if checkDiff {
		if err := receipt.VerifyDiffIntegrity(r, absRoot); err != nil {
			return emitVerifyFailure(format, r, err.Error(), fmt.Sprintf("Receipt %s INVALID: %v", r.ReceiptID, err), 2)
		}
	}

	// Best-effort staleness note for the implicit "latest receipt" path: with
	// no --receipt-id, the resolved receipt is the most recent one. If a LATER
	// `blueprint ci` run was BLOCKED or errored — such runs seal no receipt
	// (see ci.go sealReceipt) — this receipt predates that red run and the
	// user must be told. Advisory only: any read/parse failure skips silently
	// and the note never changes the verification result or exit code.
	note := ""
	if !explicitID {
		note = ciStalenessNote(r.ReceiptID)
	}
	switch format {
	case "sarif":
		if rerr := renderReceiptSARIF(absRoot, r); rerr != nil {
			return 2
		}
		return 0
	case "in-toto":
		if rerr := renderReceiptInToto(absRoot, r); rerr != nil {
			return 2
		}
		return 0
	case "json":
		renderReceiptJSON(r, "", note)
		return 0
	default:
		fmt.Printf("Receipt %s VALID. Status: %s. Base: %s Head: %s. Audit chain intact (%d records). Signature verified.\n",
			r.ReceiptID, r.Status, r.BaseRevision, r.HeadRevision, auditWriter.RecordCount())
		if note != "" {
			fmt.Fprintln(os.Stderr, note)
		}
		return 0
	}
}

// parseVerifyReceiptFlags builds the verify-receipt flag set, parses args,
// and returns the parsed FlagSet so callers can read fs.Args() and flag
// values via fs.Lookup. Parse errors are reported on stderr by the flag
// package itself (fs.SetOutput(os.Stderr)) and returned to the caller.
func parseVerifyReceiptFlags(args []string) (*flag.FlagSet, error) {
	fs := flag.NewFlagSet("verify-receipt", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.String("repo", "", "repository root (default: current directory)")
	fs.String("receipt-id", "", "receipt id to verify (default: latest receipt)")
	fs.Bool("json", false, "emit JSON instead of human-readable text")
	fs.Bool("sarif", false, "emit SARIF 2.1.0 JSON report")
	fs.Bool("in-toto", false, "emit in-toto v0.2 supply-chain attestation statement")
	fs.Bool("check-diff", false, "verify PR git revision / diff matches receipt fingerprint")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return fs, nil
}

// resolveReceiptRoot resolves the --repo value (or the current working
// directory when empty) to an absolute repository root.
func resolveReceiptRoot(root string) (string, error) {
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("blueprint: cannot determine working directory: %w", err)
		}
		root = cwd
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("blueprint: invalid repository path %q: %v", root, err)
	}
	return absRoot, nil
}

// stripReceiptJSONSuffix removes a trailing ".json" from a receipt
// identifier so `kern verify-receipt bp-<id>.json` (the full filename form)
// resolves to the stored receipt id bp-<id> instead of failing lookup with
// "Receipt not found". Receipt ids never legitimately end in ".json" — the
// store names files <id>.json, so stripping the suffix is unambiguous.
func stripReceiptJSONSuffix(id string) string {
	return strings.TrimSuffix(id, ".json")
}

// renderReceiptJSON is the single funnel for JSON emission from
// runVerifyReceipt: it wraps every emitVerifyReceiptJSON call so the JSON
// output path is centralized. The caller keeps the `if jsonOut` guard so the
// exact guard semantics are preserved (JSON is only emitted when the json
// flag is set); this helper guarantees every JSON path renders through one
// place.
func renderReceiptJSON(r *receipt.Receipt, verifyErr string, notes ...string) {
	emitVerifyReceiptJSON(r, verifyErr, notes...)
}

// renderReceiptSARIF emits the SARIF 2.1.0 report for a verified receipt
// with its artifact findings. Errors are reported on stderr and returned;
// the caller maps them to the exit code.
func renderReceiptSARIF(absRoot string, r *receipt.Receipt) error {
	findings := loadArtifactFindings(absRoot)
	data, err := receipt.RenderSARIF(r, findings)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Receipt %s: SARIF export failed: %v\n", r.ReceiptID, err)
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

// renderReceiptInToto emits the in-toto v0.1/v0.2 supply-chain attestation
// statement for a verified receipt. Errors are reported on stderr and
// returned; the caller maps them to the exit code.
func renderReceiptInToto(absRoot string, r *receipt.Receipt) error {
	data, err := receipt.RenderInToto(r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Receipt %s: in-toto export failed: %v\n", r.ReceiptID, err)
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

// ciArtifactDefaultFile mirrors `kern ci`'s default --artifact-file value
// (see defaultCIArtifactFile in ci.go): repo-root-relative under .kern/.
// ci writes the artifact with os.WriteFile relative to the process working
// directory, so the staleness read mirrors the same cwd-relative resolution
// (running `kern ci` / `kern verify-receipt` from the repo root).
const ciArtifactDefaultFile = ".kern/blueprint-result.json"

// errReceiptNotSealed marks a CI artifact that carries no receipt id because
// the run it records never reached PASS/WARN (receipts are only sealed for
// successful runs). Maps to exit 3 ("no receipt") with an actionable message.
var errReceiptNotSealed = errors.New("no receipt was sealed")

// loadReceiptFile resolves a positional JSON file passed to `verify-receipt`.
// Two shapes share the filename space:
//
//   - a receipt (schema_version + receipt_id + signature): returned as-is for
//     full verification, with the receipt's own RepoRoot as the effective root
//     so the audit chain is re-read from the repository that sealed it;
//   - a CI artifact written by `kern ci --artifact-file` (repo + status +
//     exit_code): when it cites a receipt_id (PASS/WARN runs), the correlated
//     receipt is loaded from the artifact's repo and its base/head are
//     cross-checked against the artifact; when no receipt was sealed, an
//     errReceiptNotSealed explaining why is returned.
//
// A file that is neither shape returns a descriptive error.
func loadReceiptFile(path, fallbackRoot string) (*receipt.Receipt, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("%w: cannot read %q: %v", receipt.ErrNotFound, path, err)
	}
	var r receipt.Receipt
	if err := json.Unmarshal(data, &r); err == nil && r.ReceiptID != "" && r.Signature != "" {
		root := r.RepoRoot
		if root == "" {
			root = fallbackRoot
		}
		return &r, root, nil
	}
	var artifact struct {
		Repo      string `json:"repo"`
		Base      string `json:"base"`
		Head      string `json:"head"`
		Status    string `json:"status"`
		ExitCode  int    `json:"exit_code"`
		ReceiptID string `json:"receipt_id"`
	}
	if err := json.Unmarshal(data, &artifact); err == nil && artifact.Status != "" {
		if artifact.ReceiptID == "" {
			return nil, "", fmt.Errorf("%w: this file is a %s CI artifact, not a receipt — receipts are only sealed for PASS/WARN runs. Re-run `kern ci` on a passing change, or verify a receipt JSON from .blueprint/receipts/", errReceiptNotSealed, artifact.Status)
		}
		// Prefer the artifact's own repo root (ci stamps Repo=absRoot) so the
		// correlated receipt is found even when the caller verifies the file
		// from outside the repository; fall back to the --repo/cwd root.
		root := fallbackRoot
		if artifact.Repo != "" {
			if _, statErr := os.Stat(filepath.Join(artifact.Repo, ".blueprint")); statErr == nil {
				root = artifact.Repo
			}
		}
		store := receipt.NewStore(root)
		rec, gerr := store.Get(artifact.ReceiptID)
		if gerr != nil {
			return nil, "", fmt.Errorf("artifact references receipt %s but it cannot be verified: %v", artifact.ReceiptID, gerr)
		}
		if (artifact.Base != "" && rec.BaseRevision != artifact.Base) || (artifact.Head != "" && rec.HeadRevision != artifact.Head) {
			return nil, "", fmt.Errorf("artifact base/head (%s..%s) do not match receipt %s (%s..%s) — tampered artifact",
				artifact.Base, artifact.Head, artifact.ReceiptID, rec.BaseRevision, rec.HeadRevision)
		}
		return rec, root, nil
	}
	return nil, "", fmt.Errorf("cannot verify %q: file is neither a receipt (missing signature+receipt_id) nor a CI artifact (missing status)", path)
}

// ciStalenessNote returns a best-effort advisory for the implicit
// "latest receipt" verification path (no --receipt-id). The most recent
// `blueprint ci` run may have been BLOCKED or errored; such runs seal no
// receipt (see ci.go sealReceipt), so the latest receipt predates a later red
// run. When the CI artifact at the default location records a BLOCK/ERROR
// status, the returned note tells the user the receipt is from an earlier
// successful run. Best-effort contract: any read/parse failure returns "" and
// the note NEVER changes the verification result or exit code.
func ciStalenessNote(receiptID string) string {
	b, err := os.ReadFile(ciArtifactDefaultFile)
	if err != nil {
		return ""
	}
	var artifact struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(b, &artifact); err != nil {
		return ""
	}
	if artifact.Status != string(domain.StatusBlock) && artifact.Status != string(domain.StatusError) {
		return ""
	}
	return fmt.Sprintf("note: the most recent ci run was %s and has no receipt; this receipt %s is from an earlier successful run", artifact.Status, receiptID)
}

// Sentinel errors for the best-effort kern cross-chain check (H5).
var (
	// errKernChainHashNotFound is a HARD failure: kern ran successfully and
	// its audit trail does not contain the receipt's kern_chain_hash. The
	// receipt claims a kern binding that does not exist → exit 2.
	errKernChainHashNotFound = errors.New("kern chain hash not found in kern's audit trail")
	// errKernChainCheckSkipped is a SOFT failure: kern is unavailable or the
	// audit query failed, so the cross-link cannot be checked. The caller
	// warns and continues (exit 0) — the local chain binding is authoritative.
	errKernChainCheckSkipped = errors.New("kern chain check skipped")
	// ErrKernChainHashNotFound is the exported form of the HARD-failure
	// sentinel, for the legacy cmd/blueprint compatibility shim's tests.
	ErrKernChainHashNotFound = errKernChainHashNotFound
	// ErrKernChainCheckSkipped is the exported form of the SOFT-failure
	// sentinel, for the legacy cmd/blueprint compatibility shim's tests.
	ErrKernChainCheckSkipped = errKernChainCheckSkipped
)

// kernChainHashVerifyTimeout bounds the `kern audit` subprocess. Best-effort
// contract: a hung kern must never stall a merge gate. A var (not const) so
// tests can shorten the window.
var kernChainHashVerifyTimeout = 15 * time.Second

// VerifyKernChainHash checks that expectedHash appears in kern's audit trail
// by running `kern audit --root <root> --json` (H5). Resolution order mirrors
// adapters/kern and audit/kern_link.go: KERN_BINARY, $PATH, then
// ../kern/bin/kern.
//
// Kern scopes its chain by the root path the entry was appended under.
// `blueprint ci` validates in a throwaway worktree and links kern with the
// record's RepoRoot — the worktree path — so the receipt's repo alone does
// not name the chain. The candidate roots are therefore every distinct
// RepoRoot found in the local audit file; the receipt's repo is only a
// candidate when the chain is empty (nothing to discover the real root from).
//
// Return contract:
//   - nil when the hash is found (kern verified the cross-link);
//   - errKernChainHashNotFound when EVERY candidate chain was readable and
//     none contains the hash — kern succeeded and says the binding does not
//     exist: hard failure for the caller;
//   - a wrapped errKernChainCheckSkipped when kern is missing, or any
//     candidate chain was unreadable (e.g. a CI worktree deleted after the
//     run — kern requires the root dir to exist even to list a chain), so
//     the cross-link cannot be conclusively checked: the caller must warn
//     and continue (best-effort).
func VerifyKernChainHash(repo string, expectedHash string) error {
	return verifyKernChainHash(repo, expectedHash)
}

// VerifyKernChainHash exposes verifyKernChainHash for the legacy cmd/blueprint
// compatibility shim's tests.
func verifyKernChainHash(repo string, expectedHash string) error {
	if expectedHash == "" {
		return nil // nothing to check
	}
	bin := os.Getenv("KERN_BINARY")
	if bin == "" {
		p, err := exec.LookPath("kern")
		if err == nil {
			bin = p
		} else if candidate := filepath.Join("bin", "kern"); fileExists(candidate) {
			bin = candidate
		} else if candidate := filepath.Join("..", "kern", "bin", "kern"); fileExists(candidate) {
			bin = candidate
		}
	}
	if bin == "" {
		return fmt.Errorf("%w: kern binary not found (set KERN_BINARY or add kern to PATH)", errKernChainCheckSkipped)
	}

	candidates := audit.NewWriter(filepath.Join(repo, ".blueprint", "audit", "audit.jsonl")).DistinctRepoRoots()
	if len(candidates) == 0 {
		candidates = []string{repo}
	}

	var lastErr error
	readable, unreadable := 0, 0
	for _, root := range candidates {
		ctx, cancel := context.WithTimeout(context.Background(), kernChainHashVerifyTimeout)
		out, err := exec.CommandContext(ctx, bin, "audit", "--root", root, "--json").CombinedOutput()
		cancel()
		if err != nil {
			unreadable++
			lastErr = fmt.Errorf("%w: kern audit --root %q failed: %v: %s", errKernChainCheckSkipped, root, err, strings.TrimSpace(string(out)))
			continue
		}
		readable++
		if kernOutputContainsHash(string(out), expectedHash) {
			return nil
		}
	}
	if unreadable > 0 {
		// Some chain the receipt might reference could not be read (the
		// common CI case: the worktree was cleaned up). We cannot conclude
		// the binding is absent — warn and let the local chain binding stand.
		return lastErr
	}
	return errKernChainHashNotFound
}

// kernAuditEntryLite mirrors the fields of kern's audit entries (kern's
// structs have no json tags, so encoding/json uses the exported Go field
// names) needed to check for a chain hash in `kern audit --json` output.
type kernAuditEntryLite struct {
	Hash string `json:"Hash"`
}

// kernOutputContainsHash reports whether expectedHash appears in `kern audit
// --json` output. It parses the JSON array of entries when possible and
// matches the Hash field exactly; for unknown output shapes (older kern,
// different format) it falls back to a whole-word substring search so a
// format change degrades to best-effort, never to a false "not found" on
// parseable output.
func kernOutputContainsHash(out, expectedHash string) bool {
	var entries []kernAuditEntryLite
	if err := json.Unmarshal([]byte(out), &entries); err == nil {
		for _, e := range entries {
			if e.Hash == expectedHash {
				return true
			}
		}
		return false // parsed cleanly: authoritative answer
	}
	return strings.Contains(out, expectedHash)
}

// fileExists reports whether path names an existing non-directory file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// verifyReceiptJSON is the --json output shape.
type verifyReceiptJSON struct {
	Valid          bool   `json:"valid"`
	ReceiptID      string `json:"receipt_id,omitempty"`
	Status         string `json:"status,omitempty"`
	BaseRevision   string `json:"base_revision,omitempty"`
	HeadRevision   string `json:"head_revision,omitempty"`
	AuditChainHash string `json:"audit_chain_hash,omitempty"`
	KernChainHash  string `json:"kern_chain_hash,omitempty"`
	Error          string `json:"error,omitempty"`
	Note           string `json:"note,omitempty"`
}

// emitVerifyReceiptJSON prints the verification verdict as one JSON object.
// notes is variadic so existing call sites are unchanged: when a note is
// supplied (and non-empty) it is carried as the additive "note" field.
func emitVerifyReceiptJSON(r *receipt.Receipt, verifyErr string, notes ...string) {
	out := verifyReceiptJSON{Valid: r != nil && verifyErr == ""}
	if r != nil {
		out.ReceiptID = r.ReceiptID
		out.Status = r.Status
		out.BaseRevision = r.BaseRevision
		out.HeadRevision = r.HeadRevision
		out.AuditChainHash = r.AuditChainHash
		out.KernChainHash = r.KernChainHash
	}
	if verifyErr != "" {
		out.Error = verifyErr
	}
	if len(notes) > 0 && notes[0] != "" {
		out.Note = notes[0]
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

type artifactSummary struct {
	Repo          string `json:"repo"`
	Base          string `json:"base"`
	Head          string `json:"head"`
	Status        string `json:"status"`
	ExitCode      int    `json:"exit_code"`
	FindingsCount int    `json:"findings_count"`
	Findings      []struct {
		RuleID   string `json:"rule_id"`
		Severity string `json:"severity"`
		Category string `json:"category"`
		File     string `json:"file"`
		Line     int    `json:"line"`
		Message  string `json:"message"`
	} `json:"findings"`
}

func loadArtifactFindings(absRoot string) []domain.Finding {
	p := filepath.Join(absRoot, ciArtifactDefaultFile)
	b, err := os.ReadFile(p)
	if err != nil {
		b, err = os.ReadFile(ciArtifactDefaultFile)
		if err != nil {
			return nil
		}
	}
	var art artifactSummary
	if err := json.Unmarshal(b, &art); err != nil {
		return nil
	}
	var findings []domain.Finding
	for _, f := range art.Findings {
		findings = append(findings, domain.Finding{
			RuleID:   f.RuleID,
			Severity: domain.Severity(f.Severity),
			Category: domain.Category(f.Category),
			File:     f.File,
			Line:     f.Line,
			Message:  f.Message,
		})
	}
	return findings
}

// verifyOutputFormat resolves the single output format the user requested.
// Precedence mirrors the historical success-path order: --sarif beats
// --in-toto beats --json; "text" (human-readable) when no format flag is
// set. Using one resolution everywhere keeps success and failure paths
// consistent — a CI parser that asked for a structured format always gets it,
// even when the receipt is rejected.
func verifyOutputFormat(jsonOut, sarifOut, inTotoOut bool) string {
	switch {
	case sarifOut:
		return "sarif"
	case inTotoOut:
		return "in-toto"
	case jsonOut:
		return "json"
	default:
		return "text"
	}
}

// verifyArgsFormat scans raw args for a structured-output flag when flag
// parsing failed and the FlagSet values are unavailable. Precedence mirrors
// verifyOutputFormat.
func verifyArgsFormat(args []string) string {
	jsonOut, sarifOut, inTotoOut := false, false, false
	for _, a := range args {
		switch {
		case a == "-json" || a == "--json" || strings.HasPrefix(a, "--json="):
			jsonOut = true
		case a == "-sarif" || a == "--sarif" || strings.HasPrefix(a, "--sarif="):
			sarifOut = true
		case a == "-in-toto" || a == "--in-toto" || strings.HasPrefix(a, "--in-toto="):
			inTotoOut = true
		}
	}
	return verifyOutputFormat(jsonOut, sarifOut, inTotoOut)
}

// emitVerifyFailure is the single funnel for verification-failure output: it
// routes the error through the serializer matching the requested format
// (--json / --sarif / --in-toto) so CI parsers always receive the shape they
// asked for, even on a rejected receipt. With no format flag it falls back to
// plain text on stderr (plainMsg). structuredMsg is embedded in the
// structured documents; code is the exit code the caller must return (3 =
// receipt not found, 2 = any other failure).
func emitVerifyFailure(format string, r *receipt.Receipt, structuredMsg, plainMsg string, code int) int {
	switch format {
	case "json":
		renderReceiptJSON(r, structuredMsg)
	case "sarif":
		if err := renderFailureSARIF(r, structuredMsg); err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: SARIF failure export failed: %v\n", err)
		}
	case "in-toto":
		if err := renderFailureInToto(r, structuredMsg); err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: in-toto failure export failed: %v\n", err)
		}
	default:
		fmt.Fprintln(os.Stderr, plainMsg)
	}
	return code
}

// emitVerifyParseFailure reports a flag-parse failure. The flag package has
// already printed the error and usage to stderr; when the (unparseable) args
// requested a structured format, an error document of that format is emitted
// to stdout so CI parsers still receive the shape they asked for. Plain-text
// mode relies on the flag package's own stderr output.
func emitVerifyParseFailure(args []string, err error) int {
	format := verifyArgsFormat(args)
	if format == "text" {
		return 2
	}
	return emitVerifyFailure(format, nil, "verify-receipt: "+err.Error(), "", 2)
}

// renderFailureSARIF emits a SARIF 2.1.0 document encoding a verification
// failure as a single error-level result, so a CI SARIF consumer always
// receives the format it asked for even when the receipt is rejected.
func renderFailureSARIF(r *receipt.Receipt, msg string) error {
	const ruleID = "VERIFY_RECEIPT_FAILED"
	doc := map[string]any{
		"$schema": "https://schemastore.azurewebsites.net/schemas/json/sarif-2.1.0-rtm.5.json",
		"version": "2.1.0",
		"runs": []any{
			map[string]any{
				"tool": map[string]any{
					"driver": map[string]any{
						"name":           "kernops",
						"version":        "2.1.0",
						"informationUri": "https://github.com/JayveerPrajapati/kern",
						"rules": []any{
							map[string]any{
								"id":               ruleID,
								"name":             ruleID,
								"shortDescription": map[string]string{"text": "blueprint receipt verification failed"},
							},
						},
					},
				},
				"results": []any{
					map[string]any{
						"ruleId":    ruleID,
						"level":     "error",
						"message":   map[string]string{"text": msg},
						"locations": []any{},
					},
				},
				"invocations": []any{
					map[string]any{
						"executionSuccessful": false,
					},
				},
			},
		},
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// inTotoFailurePredicate is the predicate of the in-toto statement emitted
// when verification fails: predicate.type = "failure" carries the verdict and
// predicate.message the reason, so an attestation consumer always receives
// the format it asked for even when the receipt is rejected.
type inTotoFailurePredicate struct {
	Type      string `json:"type"`
	Message   string `json:"message"`
	ReceiptID string `json:"receipt_id,omitempty"`
	Status    string `json:"status,omitempty"`
}

// inTotoFailureStatement is the in-toto v0.1 statement emitted on a failed
// verification. It mirrors receipt.InTotoStatement but carries the failure
// predicate (the receipt package's predicate type has no failure shape).
type inTotoFailureStatement struct {
	Type          string                  `json:"_type"`
	Subject       []receipt.InTotoSubject `json:"subject"`
	PredicateType string                  `json:"predicateType"`
	Predicate     inTotoFailurePredicate  `json:"predicate"`
}

// renderFailureInToto emits the in-toto v0.1 statement for a verification
// failure: the error travels in the predicate as {type: "failure", message:
// ...}. When the receipt is known its id and status are carried alongside.
func renderFailureInToto(r *receipt.Receipt, msg string) error {
	pred := inTotoFailurePredicate{Type: "failure", Message: msg}
	if r != nil {
		pred.ReceiptID = r.ReceiptID
		pred.Status = r.Status
	}
	stmt := inTotoFailureStatement{
		Type:          "https://in-toto.io/Statement/v0.1",
		Subject:       []receipt.InTotoSubject{},
		PredicateType: "https://kernops.dev/attestation/v1",
		Predicate:     pred,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(stmt)
}
