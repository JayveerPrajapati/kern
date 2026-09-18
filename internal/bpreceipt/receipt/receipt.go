// Package receipt implements the P1.4 tamper-evident validation receipt.
//
// A receipt is the merge-time enforcement artifact: `blueprint ci` generates
// one for every PASS/WARN validation and stores it under
// .blueprint/receipts/<id>.json. It binds together:
//
//   - the validation outcome (status, exit code, findings count, and a
//     ValidationHash over the canonical ValidationResult),
//   - the local audit chain endpoint (AuditChainHash = the last record's hash
//     in .blueprint/audit/audit.jsonl, itself chained so any tamper breaks
//     every subsequent hash),
//   - the cross-chain link to kern (KernChainHash, when kern returned one),
//
// and seals all of it with a Signature: sha256 over the canonical JSON of the
// receipt with Signature cleared. Verifying a receipt is therefore
// independent of blueprint internals: any consumer (CI status check, PR
// review bot, `blueprint verify-receipt`) recomputes the signature and walks
// the audit chain.
package receipt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// SchemaVersion is the current receipt wire format. Bump it (and the Verify
// check) whenever the Receipt struct changes shape.
const SchemaVersion = 1

// Receipt is the tamper-evident evidence bundle for one validated change.
type Receipt struct {
	SchemaVersion  int       `json:"schema_version"`
	ReceiptID      string    `json:"receipt_id"`                // correlation id of the validation run
	RepoRoot       string    `json:"repo_root"`                 // repository the validation ran against
	BaseRevision   string    `json:"base_revision"`             // base revision given to `blueprint ci`
	HeadRevision   string    `json:"head_revision"`             // proposed revision given to `blueprint ci`
	Status         string    `json:"status"`                    // PASS|WARN|BLOCK|ERROR|SKIP
	ExitCode       int       `json:"exit_code"`                 // pipeline exit code
	ValidationHash string    `json:"validation_hash"`           // sha256 of the canonical ValidationResult
	AuditChainHash string    `json:"audit_chain_hash"`          // last hash in the local audit chain
	AuditRecordID  string    `json:"audit_record_id"`           // correlation id of the audit record
	KernChainHash  string    `json:"kern_chain_hash,omitempty"` // kern's returned chain hash (if linked)
	FindingsCount  int       `json:"findings_count"`            // number of findings in the validation result
	GeneratedAt    time.Time `json:"generated_at"`              // RFC3339Nano (time.Time's default JSON encoding)
	GeneratedBy    string    `json:"generated_by"`              // "blueprint-ci" | "blueprint-check"
	Signature      string    `json:"signature"`                 // sha256 over all the above (tamper-evident)
}

// Generate builds a receipt for a completed validation. auditChainHash is the
// last hash of the local audit chain (audit.Writer.LastHash()); kernChainHash
// is kern's chain hash for the same run ("" when kern is unavailable). The
// receipt id and audit record id are both the validation's correlation id, so
// the receipt points at its audit record.
//
// generatedBy is variadic for backward compatibility: the first (non-empty)
// value, when supplied, stamps the receipt's GeneratedBy field — callers such
// as a `blueprint check`-driven flow pass "blueprint-check" to distinguish
// their receipts. When omitted (or empty), it defaults to "blueprint-ci" so
// existing callers and receipts keep their historical value.
func Generate(result domain.ValidationResult, repoRoot, base, head string, auditChainHash, kernChainHash string, generatedBy ...string) *Receipt {
	by := "blueprint-ci"
	if len(generatedBy) > 0 && generatedBy[0] != "" {
		by = generatedBy[0]
	}
	r := &Receipt{
		SchemaVersion:  SchemaVersion,
		ReceiptID:      result.CorrelationID,
		RepoRoot:       repoRoot,
		BaseRevision:   base,
		HeadRevision:   head,
		Status:         string(result.Status),
		ExitCode:       result.ExitCode,
		ValidationHash: hashValidation(result),
		AuditChainHash: auditChainHash,
		AuditRecordID:  result.CorrelationID,
		KernChainHash:  kernChainHash,
		FindingsCount:  len(result.Findings),
		GeneratedAt:    time.Now().UTC(),
		GeneratedBy:    by,
	}
	r.Signature = r.ComputeSignature()
	return r
}

// hashValidation returns sha256 over the canonical JSON of a validation
// result, so a receipt pins the exact verdict it attests.
func hashValidation(result domain.ValidationResult) string {
	b, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ComputeSignature returns sha256 over the canonical JSON of the receipt with
// Signature cleared. Deterministic for a given receipt, so it can be
// recomputed at any time to detect tampering.
func (r *Receipt) ComputeSignature() string {
	unsigned := *r
	unsigned.Signature = ""
	b, err := json.Marshal(unsigned)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Verify recomputes the signature and checks the schema version. A nil error
// means the receipt is internally consistent (not tampered). The audit-chain
// linkage is verified separately by the caller (blueprint verify-receipt)
// against .blueprint/audit/audit.jsonl.
func (r *Receipt) Verify() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("receipt schema_version %d, want %d", r.SchemaVersion, SchemaVersion)
	}
	if r.Signature == "" {
		return fmt.Errorf("receipt has no signature (tampered or malformed)")
	}
	if want := r.ComputeSignature(); r.Signature != want {
		return fmt.Errorf("receipt signature mismatch (tampered): stored %q, recomputed %q", r.Signature, want)
	}
	return nil
}
