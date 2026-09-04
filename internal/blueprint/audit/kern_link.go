package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// This file implements the E-3 kern chain link: each blueprint audit record is
// best-effort appended to kern's tamper-evident hash chain via
// `kern audit append`. The mapping types below mirror kern's
// internal/governance/audit.AuditEntry JSON shape exactly (kern's struct has
// no json tags, so encoding/json uses the exported Go field names) — blueprint
// must not import the kern module, so the entry travels as JSON on stdin.

// kernRisk mirrors the JSON shape of kern's domain.Risk so a mapped entry
// round-trips through `kern audit append` unchanged.
type kernRisk struct {
	Level            string   `json:"Level"`
	Factors          []string `json:"Factors"`
	Score            float64  `json:"Score"`
	Mitigation       string   `json:"Mitigation"`
	Blocked          bool     `json:"Blocked"`
	ApprovalRequired bool     `json:"ApprovalRequired"`
}

// kernAuditEntry mirrors kern's internal/governance/audit.AuditEntry field
// names (kern marshals them verbatim). ID and Hash are left empty so kern
// auto-assigns the ID and computes the chain hash.
type kernAuditEntry struct {
	ID        string    `json:"ID"`
	Timestamp time.Time `json:"Timestamp"`
	AgentID   string    `json:"AgentID"`
	Action    string    `json:"Action"`
	Resource  string    `json:"Resource"`
	Risk      kernRisk  `json:"Risk"`
	Approved  bool      `json:"Approved"`
	Result    string    `json:"Result"`
	Hash      string    `json:"Hash"`
	TaskID    string    `json:"TaskID"`
	// ValidationOutcome (P0.4) carries Blueprint's validation result for this
	// entry. Kern's AuditEntry consumes it on `kern audit append` to mark the
	// blocked context stale. Omitted when the record carries none; the key is
	// kern's exported field name ("ValidationOutcome") with the untagged Go
	// field names of domain.ValidationOutcome as the nested wire format.
	ValidationOutcome *domain.ValidationOutcome `json:"ValidationOutcome,omitempty"`
	// ContextProvenance (P1.2) cites the retrieval provenance linking this
	// decision to its context authorization. Omitted when the record carries
	// none. Kern's AuditEntry ignores unknown JSON fields until the field
	// lands on its side; the key is kern's exported field name once added.
	ContextProvenance *domain.ContextProvenance `json:"ContextProvenance,omitempty"`
}

// kernRiskFromStatus maps a validation verdict to kern's risk levels:
// BLOCK/ERROR → HIGH, WARN → MEDIUM, everything else → LOW.
func kernRiskFromStatus(s domain.Status) string {
	switch s {
	case domain.StatusBlock, domain.StatusError:
		return "HIGH"
	case domain.StatusWarn:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// kernEntry maps a blueprint audit Record onto a kern AuditEntry. ID and Hash
// stay empty so kern assigns/computes them; Approved is false because a
// validation result is not an approval decision.
func kernEntry(r Record) kernAuditEntry {
	agentID := r.AgentID
	if agentID == "" {
		agentID = string(r.Source)
	}
	return kernAuditEntry{
		AgentID:           agentID,
		Action:            string(r.Operation),
		Resource:          r.RepoRoot,
		Timestamp:         r.Timestamp,
		Risk:              kernRisk{Level: kernRiskFromStatus(r.Status)},
		Approved:          false,
		Result:            string(r.Status),
		TaskID:            r.CorrelationID,
		ValidationOutcome: validationOutcomeFor(r),
		ContextProvenance: r.ContextProvenance,
	}
}

// validationOutcomeFor builds the P0.4 ValidationOutcome from a validation
// Record: the status, pipeline exit code, the unique paths of BLOCK-severity
// findings, the correlation id, and the finding count. Always non-nil for a
// validation record (kern consumes it to invalidate blocked context).
func validationOutcomeFor(r Record) *domain.ValidationOutcome {
	return &domain.ValidationOutcome{
		Status:        string(r.Status),
		ExitCode:      r.ExitCode,
		BlockedFiles:  blockedFiles(r.Findings),
		CorrelationID: r.CorrelationID,
		Findings:      len(r.Findings),
	}
}

// blockedFiles extracts the unique, ordered paths of BLOCK-severity findings.
// A file with multiple blocking findings is listed once.
func blockedFiles(findings []FindingMeta) []string {
	seen := make(map[string]bool, len(findings))
	files := make([]string, 0, len(findings))
	for _, f := range findings {
		if f.Severity != domain.SeverityBlock || f.File == "" {
			continue
		}
		if seen[f.File] {
			continue
		}
		seen[f.File] = true
		files = append(files, f.File)
	}
	return files
}

// resolveKernBinary locates the kern executable in the same order as
// adapters/kern: KERN_BINARY env, kern on $PATH, then ../kern/bin/kern
// relative to the current working directory. Returns "" when unavailable so
// the caller can fall back to local-only audit.
func resolveKernBinary() string {
	if p := os.Getenv("KERN_BINARY"); p != "" {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
		return ""
	}
	for _, candidate := range []string{
		filepath.Join("bin", "kern"),
		filepath.Join("..", "kern", "bin", "kern"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if p, err := exec.LookPath("kern"); err == nil {
		return p
	}
	return ""
}

// kernChainHashPattern matches kern's `kern audit append` confirmation line:
//
//	appended <id> (hash <sha256>)
//
// The hash is kern's chain hash for the appended entry; blueprint captures it
// so a P1.4 receipt can cross-link its local chain with kern's.
var kernChainHashPattern = regexp.MustCompile(`appended\s+(\S+)\s+\(hash\s+([0-9a-fA-F]+)\)`)

// parseKernChainHash extracts the chain hash from `kern audit append` stdout.
// Returns "" when kern returned no parseable confirmation line (older kern,
// different output shape, or an empty/failed append).
func parseKernChainHash(out string) string {
	if m := kernChainHashPattern.FindStringSubmatch(out); len(m) == 3 {
		return m[2]
	}
	return ""
}

// kernLinkTimeout bounds the `kern audit append` subprocess. The chain link
// is best-effort (the local JSONL is authoritative), so a kern binary that
// wedges — hung lock, deadlock, runaway child — must never stall a validation
// or a CI run: after this window the whole subprocess group is killed and the
// failure is logged as a warning, exactly like any other kern failure.
// A var (not const) so tests can shorten the window.
var kernLinkTimeout = 10 * time.Second

// linkToKernChain appends a mapped copy of r to kern's tamper-evident chain
// via `kern audit append --root <repoRoot>` with the entry JSON piped on
// stdin. Best-effort in both directions: a missing binary, launch failure,
// non-zero exit, or timeout only writes a warning to stderr — the local JSONL
// remains the authoritative record and a validation can never fail because
// kern is unavailable. The subprocess runs in its own process group and is
// killed (group-wide) after kernLinkTimeout, so a hung kern cannot hold the
// stdout/stderr pipes open and block the caller indefinitely. Returns the
// chain hash kern reported ("" when kern was unavailable, failed, timed out,
// or returned nothing parseable) and the launch error (nil when kern was
// skipped or succeeded, so callers can ignore it).
func (w *Writer) linkToKernChain(r Record) (string, error) {
	if r.RepoRoot == "" {
		return "", nil
	}
	bin := w.kernBinary
	if bin == "" {
		bin = resolveKernBinary()
	}
	if bin == "" {
		fmt.Fprintf(os.Stderr, "blueprint: audit: kern binary not found; skipping chain link for %s\n", r.RepoRoot)
		return "", nil
	}

	payload, err := json.Marshal(kernEntry(r))
	if err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: audit: marshal kern entry: %v\n", err)
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), kernLinkTimeout)
	defer cancel()

	cmd := exec.Command(bin, "audit", "append", "--root", r.RepoRoot)
	// Run kern in its own process group so a timeout can kill the whole group
	// — if kern ever spawns a child that inherits our stdout/stderr pipes,
	// killing only the parent would leave the child holding the pipes open
	// and cmd.Wait() would block until the child exits.
	setProcessGroup(cmd)
	cmd.Stdin = bytes.NewReader(payload)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		msg := strings.TrimSpace(outBuf.String() + errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		fmt.Fprintf(os.Stderr, "blueprint: audit: kern audit append failed for %s: %s\n", r.RepoRoot, msg)
		return "", err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			msg := strings.TrimSpace(outBuf.String() + errBuf.String())
			if msg == "" {
				msg = err.Error()
			}
			fmt.Fprintf(os.Stderr, "blueprint: audit: kern audit append failed for %s: %s\n", r.RepoRoot, msg)
			return "", err
		}
	case <-ctx.Done():
		// Timeout: kill the entire process group so grandchildren release the
		// pipes, then wait for Wait() to return before reporting the failure.
		killProcessGroup(cmd)
		<-done
		fmt.Fprintf(os.Stderr, "blueprint: audit: kern audit append timed out after %s for %s\n", kernLinkTimeout, r.RepoRoot)
		return "", ctx.Err()
	}
	return parseKernChainHash(outBuf.String()), nil
}
