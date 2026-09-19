// Package evidence owns the signed-evidence MCP tool bodies (kern_evidence
// action=verify|explain|export and kern_evidence_anchor) as plain functions.
package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/evidence"
	"github.com/JayveerPrajapati/kern/internal/fetch"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// Hooks carries kernel dependencies required by evidence operations.
type Hooks struct {
	LoadIndex func(ctx context.Context, root string) (*index.Index, error)
}

func resolveRoot(root string) string {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Clean(cwd)
		}
		return "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return root
}

// EvidenceProof provides an auditable, deterministic certificate verifying
// that an AI-cited file, line range, or symbol actually exists in the codebase.
type EvidenceProof struct {
	EvidenceID   string `json:"evidence_id"`
	Verified     bool   `json:"verified"`
	Target       string `json:"target"`
	Symbol       string `json:"symbol,omitempty"`
	File         string `json:"file"`
	Line         int    `json:"line"`
	EndLine      int    `json:"end_line,omitempty"`
	LineDrift    int    `json:"line_drift"` // Drift from requested line (+/- N lines)
	ContentHash  string `json:"content_hash,omitempty"`
	Snippet      string `json:"snippet,omitempty"`
	Timestamp    string `json:"timestamp"`
	Verification string `json:"verification_detail"`
}

// Anchor validates citations made by LLMs, corrects line drift, and issues
// a deterministic SHA-256 evidence certificate.
func Anchor(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	file := mcpargs.ArgString(args, "file")
	symbol := mcpargs.ArgString(args, "symbol")
	lineStr := mcpargs.ArgString(args, "line")
	claim := mcpargs.ArgString(args, "claim")
	root := resolveRoot(mcpargs.ArgString(args, "root"))

	// Auto-extract from claim if file/symbol are missing
	if file == "" && symbol == "" && claim != "" {
		if parts := strings.Split(claim, ":"); len(parts) >= 2 {
			file = strings.TrimSpace(parts[0])
			lineStr = strings.TrimSpace(parts[1])
		} else {
			symbol = strings.TrimSpace(claim)
		}
	}

	if file == "" && symbol == "" {
		return "", fmt.Errorf("at least one of 'file', 'symbol', or 'claim' must be provided")
	}

	reqLine := 0
	if lineStr != "" {
		if n, err := strconv.Atoi(lineStr); err == nil && n > 0 {
			reqLine = n
		}
	}

	ix, err := h.LoadIndex(ctx, root)
	if err != nil {
		return "", fmt.Errorf("load index: %w", err)
	}

	proof := EvidenceProof{
		Target:    claim,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if proof.Target == "" {
		if file != "" && reqLine > 0 {
			proof.Target = fmt.Sprintf("%s:%d", file, reqLine)
		} else if file != "" {
			proof.Target = file
		} else {
			proof.Target = symbol
		}
	}

	// 1. Check by symbol
	var matchedSym *index.Symbol
	if symbol != "" {
		for _, sym := range ix.Symbols {
			if sym.Name == symbol || sym.FullName() == symbol {
				matchedSym = &sym
				break
			}
		}
		if matchedSym == nil {
			matches := ix.Search(symbol, 1)
			if len(matches) > 0 {
				matchedSym = &matches[0]
			}
		}
	}

	// If symbol matched, resolve file and lines
	if matchedSym != nil {
		proof.Symbol = matchedSym.FullName()
		proof.File = matchedSym.File
		proof.Line = matchedSym.Line
		proof.EndLine = matchedSym.End
		proof.Verified = true
		proof.Verification = fmt.Sprintf("Symbol %s resolved in AST index at %s:%d", proof.Symbol, proof.File, proof.Line)
		if reqLine > 0 {
			proof.LineDrift = proof.Line - reqLine
		}
	} else if file != "" {
		// 2. Check by file + line
		cleanRel := filepath.Clean(file)
		if filepath.IsAbs(cleanRel) {
			if rel, err := filepath.Rel(root, cleanRel); err == nil {
				cleanRel = rel
			}
		}

		fullPath := filepath.Join(root, cleanRel)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			proof.Verified = false
			proof.Verification = fmt.Sprintf("File %s not accessible on disk: %v", cleanRel, err)
		} else {
			lines := strings.Split(string(data), "\n")
			proof.File = cleanRel
			proof.Verified = true

			if reqLine > 0 && reqLine <= len(lines) {
				proof.Line = reqLine
				proof.Verification = fmt.Sprintf("File and line confirmed (%s:%d)", cleanRel, reqLine)
				start := reqLine - 1
				end := reqLine + 2
				if end > len(lines) {
					end = len(lines)
				}
				proof.Snippet = strings.TrimSpace(strings.Join(lines[start:end], "\n"))
			} else if reqLine > len(lines) {
				proof.Line = len(lines)
				proof.LineDrift = len(lines) - reqLine
				proof.Verification = fmt.Sprintf("Line %d exceeds file length (%d lines); anchored to file tail", reqLine, len(lines))
			} else {
				proof.Line = 1
				proof.Verification = fmt.Sprintf("File confirmed (%d total lines)", len(lines))
			}
		}
	} else {
		proof.Verified = false
		proof.Verification = fmt.Sprintf("Symbol %s not found in AST index", symbol)
	}

	// Compute deterministic SHA-256 evidence certificate
	hHash := sha256.New()
	_, _ = fmt.Fprintf(hHash, "%s|%s|%d|%v|%s", proof.File, proof.Symbol, proof.Line, proof.Verified, ix.Root)
	proof.EvidenceID = "evidence-sha256:" + hex.EncodeToString(hHash.Sum(nil))[:16]

	// D4: compact text summary by default; full JSON behind format=json.
	if strings.ToLower(mcpargs.ArgString(args, "format")) == "json" {
		out, _ := json.MarshalIndent(proof, "", "  ")
		return string(out), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "evidence: %s\n", proof.EvidenceID)
	fmt.Fprintf(&sb, "verified: %v\n", proof.Verified)
	if proof.Target != "" {
		fmt.Fprintf(&sb, "target: %s\n", proof.Target)
	}
	if proof.Symbol != "" {
		fmt.Fprintf(&sb, "symbol: %s at %s:%d\n", proof.Symbol, proof.File, proof.Line)
	}
	if proof.LineDrift != 0 {
		fmt.Fprintf(&sb, "line drift: %d\n", proof.LineDrift)
	}
	if proof.Verification != "" {
		fmt.Fprintf(&sb, "verification: %s\n", proof.Verification)
	}
	if proof.Snippet != "" {
		limit := 200
		if len(proof.Snippet) > limit {
			proof.Snippet = proof.Snippet[:limit] + "..."
		}
		fmt.Fprintf(&sb, "snippet: %s\n", proof.Snippet)
	}
	return sb.String(), nil
}

// EvidenceVerifyReport is the JSON verification report returned by
// kern_evidence action=verify.
type EvidenceVerifyReport struct {
	BundleID            string `json:"bundle_id"`
	SchemaVersion       int    `json:"schema_version"`
	Valid               bool   `json:"valid"`
	TamperSeal          string `json:"tamper_seal"` // "intact" | "tampered"
	Signature           string `json:"signature"`   // "unsigned (digest-only seal)" | "<algorithm> <fp>"
	KeyFingerprint      string `json:"key_fingerprint,omitempty"`
	ExpectedFingerprint string `json:"expected_fingerprint,omitempty"`
	FingerprintMatch    *bool  `json:"fingerprint_match,omitempty"`
	TrustAnchor         string `json:"trust_anchor,omitempty"` // "anchored" | "self-attested"
	SelfAttested        bool   `json:"self_attested,omitempty"`
	AuditChain          string `json:"audit_chain"`   // "intact (N entries, last hash …)" | "not replayed (no local repo)"
	Authorization       string `json:"authorization"` // "allowed" | "denied" | "n/a"
	Freshness           string `json:"freshness"`     // freshness verdict or "n/a"
	Detail              string `json:"detail"`
}

// Handle routes a kern_evidence invocation to verify, explain, or export.
func Handle(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	action := mcpargs.ArgString(args, "action")
	if action == "" {
		action = "verify"
	}
	switch action {
	case "verify":
		return Verify(ctx, args)
	case "explain":
		return Explain(ctx, args)
	case "export":
		return Export(ctx, h, args)
	default:
		return "", fmt.Errorf("kern_evidence: unknown action %q (verify|explain|export)", action)
	}
}

// Verify loads and verifies an evidence bundle against signatures and audit trail.
func Verify(ctx context.Context, args map[string]any) (string, error) {
	file := mcpargs.ArgString(args, "file")
	url := mcpargs.ArgString(args, "url")
	rootArg := mcpargs.ArgString(args, "root")
	expectFP := mcpargs.ArgString(args, "expect_fingerprint")
	if file == "" && url == "" {
		return "", fmt.Errorf("evidence verify: one of 'file' or 'url' is required")
	}

	data, err := evidenceBundleSource(file, url)
	if err != nil {
		return "", fmt.Errorf("evidence verify: %w", err)
	}
	b, err := evidence.Parse(data)
	if err != nil {
		return "", fmt.Errorf("evidence verify: %w", err)
	}

	vres, err := b.VerifyWithAnchor(expectFP)
	if err != nil {
		return "", fmt.Errorf("evidence verify: %w", err)
	}
	rep := EvidenceVerifyReport{
		BundleID:      b.BundleID,
		SchemaVersion: b.SchemaVersion,
		Valid:         true,
		TamperSeal:    "intact",
	}

	rep.TrustAnchor = string(vres.TrustAnchor)
	rep.SelfAttested = vres.SelfAttested
	if expectFP != "" {
		rep.ExpectedFingerprint = expectFP
		match := true
		rep.FingerprintMatch = &match
	}
	if b.Signature != nil {
		rep.Signature = fmt.Sprintf("%s %s", b.Signature.Algorithm, b.Signature.KeyFingerprint)
		rep.KeyFingerprint = b.Signature.KeyFingerprint
	} else {
		rep.Signature = "unsigned (digest-only seal)"
	}

	repoRoot := b.RepoRoot
	if rootArg != "" {
		repoRoot = rootArg
	}
	if url != "" && rootArg == "" {
		rep.AuditChain = "not replayed (no local repo — pass root to replay)"
		return marshalEvidenceReport(rep)
	}

	store := storage.NewLog(filepath.Join(repoRoot, ".kern", "audit"))
	log := governance.NewAuditLog().WithStore(store)
	if _, err := log.Replay(); err != nil {
		return "", fmt.Errorf("evidence verify: replay audit chain at %s: %w", repoRoot, err)
	}
	if !log.VerifyChain() {
		return "", fmt.Errorf("evidence verify: audit chain at %s is broken (tampered)", repoRoot)
	}
	disk := log.All()
	if len(disk) < len(b.AuditTrail) {
		return "", fmt.Errorf("evidence verify: bundle claims %d audit entries but on-disk chain has %d",
			len(b.AuditTrail), len(disk))
	}
	for i := range b.AuditTrail {
		if disk[i].Hash != b.AuditTrail[i].Hash {
			return "", fmt.Errorf("evidence verify: audit trail mismatch at entry %d (bundle %s…, disk %s…)",
				i, evidenceShortHash8(b.AuditTrail[i].Hash), evidenceShortHash8(disk[i].Hash))
		}
	}

	decision := "n/a"
	if b.Authorization != nil {
		if b.Authorization.Proof.Decision.Allowed {
			decision = "allowed"
		} else {
			decision = "denied"
		}
	}
	verdict := "n/a"
	if b.Freshness != nil {
		verdict = string(b.Freshness.Proof.Verdict)
	}
	lastHash := "none"
	if b.AuditChainHash != "" {
		lastHash = evidenceShortHash8(b.AuditChainHash) + "…"
	}
	rep.AuditChain = fmt.Sprintf("intact (%d entries, last hash %s)", len(disk), lastHash)
	rep.Authorization = decision
	rep.Freshness = verdict
	rep.Detail = fmt.Sprintf("Bundle %s VALID. Schema v%d. Audit chain intact (%d entries, last hash %s). Authorization: %s. Freshness: %s.",
		b.BundleID, b.SchemaVersion, len(disk), lastHash, decision, verdict)

	return marshalEvidenceReport(rep)
}

// Explain returns a plain-language explanation of an evidence bundle.
func Explain(ctx context.Context, args map[string]any) (string, error) {
	file := mcpargs.ArgString(args, "file")
	url := mcpargs.ArgString(args, "url")
	if file == "" && url == "" {
		return "", fmt.Errorf("evidence explain: one of 'file' or 'url' is required")
	}
	data, err := evidenceBundleSource(file, url)
	if err != nil {
		return "", fmt.Errorf("evidence explain: %w", err)
	}
	b, err := evidence.Parse(data)
	if err != nil {
		return "", fmt.Errorf("evidence explain: %w", err)
	}
	if err := b.Verify(); err != nil {
		return "", fmt.Errorf("evidence explain: bundle failed verification: %w", err)
	}
	return b.Explain(), nil
}

// Export generates and persists a signed-evidence bundle.
func Export(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = filepath.Clean(abs)
	}
	agentID := mcpargs.ArgString(args, "agent_id")
	if agentID == "" {
		agentID = "default"
	}
	task := mcpargs.ArgString(args, "task_id")

	ix, err := h.LoadIndex(ctx, root)
	if err != nil {
		return "", fmt.Errorf("evidence export: %w", err)
	}
	b, err := evidence.Generate(root, agentID, task, ix)
	if err != nil {
		return "", fmt.Errorf("evidence export: %w", err)
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return "", fmt.Errorf("evidence export: marshal bundle: %w", err)
	}
	outDir := filepath.Join(root, ".kern", "evidence")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("evidence export: mkdir %s: %w", outDir, err)
	}
	outPath := filepath.Join(outDir, b.BundleID+".json")
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return "", fmt.Errorf("evidence export: write %s: %w", outPath, err)
	}

	resp := map[string]any{
		"bundle_id":      b.BundleID,
		"path":           outPath,
		"schema_version": b.SchemaVersion,
		"task_id":        task,
		"agent_id":       agentID,
		"signed":         b.Signature != nil,
	}
	out, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", fmt.Errorf("evidence export: marshal result: %w", err)
	}
	return string(out), nil
}

func marshalEvidenceReport(rep EvidenceVerifyReport) (string, error) {
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return "", fmt.Errorf("evidence verify: marshal report: %w", err)
	}
	return string(out), nil
}

func evidenceBundleSource(file, url string) ([]byte, error) {
	if url != "" {
		res, err := fetch.Fetch(url, 0)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", url, err)
		}
		if res.Truncated {
			return nil, fmt.Errorf("fetch %s: bundle exceeded the fetch size cap", url)
		}
		return []byte(res.Text), nil
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("one of 'file' or 'url' is required")
}

func evidenceShortHash8(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}
