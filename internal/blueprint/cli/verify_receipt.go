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
	"github.com/JayveerPrajapati/kern/internal/blueprint/receipt"
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
	fs := flag.NewFlagSet("verify-receipt", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	repoRoot := fs.String("repo", "", "repository root (default: current directory)")
	receiptID := fs.String("receipt-id", "", "receipt id to verify (default: latest receipt)")
	jsonOut := fs.Bool("json", false, "emit JSON instead of human-readable text")
	sarifOut := fs.Bool("sarif", false, "emit SARIF 2.1.0 JSON report")
	inTotoOut := fs.Bool("in-toto", false, "emit in-toto v0.2 supply-chain attestation statement")
	checkDiff := fs.Bool("check-diff", false, "verify PR git revision / diff matches receipt fingerprint")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root := *repoRoot
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: cannot determine working directory: %v\n", err)
			return 2
		}
		root = cwd
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: invalid repository path %q: %v\n", root, err)
		return 2
	}

	// Load the receipt: by explicit id, or the most recent one.
	store := receipt.NewStore(absRoot)
	var r *receipt.Receipt
	if *receiptID != "" {
		r, err = store.Get(*receiptID)
	} else {
		r, err = store.Latest()
	}
	if err != nil {
		if errors.Is(err, receipt.ErrNotFound) {
			if *jsonOut {
				emitVerifyReceiptJSON(nil, "receipt not found")
			} else {
				fmt.Fprintf(os.Stderr, "Receipt not found.\n")
			}
			return 3
		}
		// A receipt exists but fails verification (tampered) or is unreadable.
		if *jsonOut {
			emitVerifyReceiptJSON(nil, err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "Receipt INVALID: %v\n", err)
		}
		return 2
	}

	// 1. Receipt self-integrity: signature + schema version.
	if err := r.Verify(); err != nil {
		if *jsonOut {
			emitVerifyReceiptJSON(nil, err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "Receipt %s INVALID: %v\n", r.ReceiptID, err)
		}
		return 2
	}

	// 2. A receipt with no audit chain binding cannot be validated (H4): if
	// the audit write failed or the chain was empty at seal time, the receipt
	// stamped AuditChainHash "" and must fail closed.
	if r.AuditChainHash == "" {
		msg := "receipt has no audit chain binding (empty chain hash)"
		if *jsonOut {
			emitVerifyReceiptJSON(nil, msg)
		} else {
			fmt.Fprintf(os.Stderr, "Receipt %s INVALID: %s\n", r.ReceiptID, msg)
		}
		return 2
	}

	// 3. Audit chain integrity: re-read the JSONL and walk the hashes.
	auditWriter := audit.NewWriter(filepath.Join(absRoot, ".blueprint", "audit", "audit.jsonl"))
	lastHash, err := auditWriter.VerifyChain()
	if err != nil {
		if *jsonOut {
			emitVerifyReceiptJSON(nil, "audit chain broken: "+err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "Receipt %s INVALID: audit chain broken: %v\n", r.ReceiptID, err)
		}
		return 2
	}

	// 4. The receipt's audit_chain_hash must appear somewhere in the chain
	// (H3). Every later CI run appends a record, so the sealed endpoint is no
	// longer the last hash — but it must still be a genuine record hash. This
	// makes receipts prefix-tolerant: only the NEWEST receipt ever validated
	// before; now any receipt that was sealed at a real point in the chain
	// validates.
	found, err := auditWriter.ChainContainsHash(r.AuditChainHash)
	if err != nil {
		if *jsonOut {
			emitVerifyReceiptJSON(nil, "audit chain unreadable: "+err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "Receipt %s INVALID: audit chain unreadable: %v\n", r.ReceiptID, err)
		}
		return 2
	}
	if !found {
		msg := fmt.Sprintf("audit_chain_hash %q not found in audit chain (chain last hash %q)", r.AuditChainHash, lastHash)
		if *jsonOut {
			emitVerifyReceiptJSON(nil, msg)
		} else {
			fmt.Fprintf(os.Stderr, "Receipt %s INVALID: %s\n", r.ReceiptID, msg)
		}
		return 2
	}

	// 5. Cross-chain link to kern (H5), best-effort: when the receipt cites
	// kern's chain hash, check it appears in kern's audit trail. A missing or
	// broken kern binary only warns (the local chain is authoritative); a kern
	// that RUNS but does not contain the hash is a hard failure — the receipt
	// claims a kern binding that does not exist.
	if r.KernChainHash != "" {
		if err := verifyKernChainHash(absRoot, r.KernChainHash); err != nil {
			if errors.Is(err, errKernChainHashNotFound) {
				if *jsonOut {
					emitVerifyReceiptJSON(nil, err.Error())
				} else {
					fmt.Fprintf(os.Stderr, "Receipt %s INVALID: %s\n", r.ReceiptID, err)
				}
				return 2
			}
			// Best-effort skip: kern unavailable or the query failed. Warn but
			// do not fail — the local chain binding above already validated.
			fmt.Fprintf(os.Stderr, "WARN: %v\n", err)
		}
	}

	// 6. Diff integrity check: verify PR git state / diff has not been tampered with
	if *checkDiff {
		if err := receipt.VerifyDiffIntegrity(r, absRoot); err != nil {
			if *jsonOut {
				emitVerifyReceiptJSON(nil, err.Error())
			} else {
				fmt.Fprintf(os.Stderr, "Receipt %s INVALID: %v\n", r.ReceiptID, err)
			}
			return 2
		}
	}

	if *sarifOut {
		data, err := receipt.RenderSARIF(r, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Receipt %s: SARIF export failed: %v\n", r.ReceiptID, err)
			return 2
		}
		os.Stdout.Write(data)
		return 0
	}

	if *inTotoOut {
		data, err := receipt.RenderInToto(r)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Receipt %s: in-toto export failed: %v\n", r.ReceiptID, err)
			return 2
		}
		os.Stdout.Write(data)
		return 0
	}

	// Best-effort staleness note for the implicit "latest receipt" path: with
	// no --receipt-id, the resolved receipt is the most recent one. If a LATER
	// `blueprint ci` run was BLOCKED or errored — such runs seal no receipt
	// (see ci.go sealReceipt) — this receipt predates that red run and the
	// user must be told. Advisory only: any read/parse failure skips silently
	// and the note never changes the verification result or exit code.
	note := ""
	if *receiptID == "" {
		note = ciStalenessNote(r.ReceiptID)
	}
	if *jsonOut {
		emitVerifyReceiptJSON(r, "", note)
	} else {
		fmt.Printf("Receipt %s VALID. Status: %s. Base: %s Head: %s. Audit chain intact (%d records). Signature verified.\n",
			r.ReceiptID, r.Status, r.BaseRevision, r.HeadRevision, auditWriter.RecordCount())
		if note != "" {
			fmt.Fprintln(os.Stderr, note)
		}
	}
	return 0
}

// ciArtifactDefaultFile mirrors `blueprint ci`'s default --artifact-file value
// (see parseCIFlags). ci writes the artifact with os.WriteFile relative to the
// process working directory, so the staleness read mirrors the same
// cwd-relative resolution.
const ciArtifactDefaultFile = "blueprint-result.json"

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
)

// kernChainHashVerifyTimeout bounds the `kern audit` subprocess. Best-effort
// contract: a hung kern must never stall a merge gate. A var (not const) so
// tests can shorten the window.
var kernChainHashVerifyTimeout = 15 * time.Second

// verifyKernChainHash checks that expectedHash appears in kern's audit trail
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
