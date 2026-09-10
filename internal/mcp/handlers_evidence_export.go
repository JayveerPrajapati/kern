package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/evidence"
	"github.com/JayveerPrajapati/kern/internal/fetch"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// EvidenceVerifyReport is the JSON verification report returned by
// kern_evidence action=verify. It mirrors the facts `kern evidence verify`
// prints: tamper-seal status, key identity (signature), the audit-chain
// replay result and the authorization/freshness pillars. Exit-code behaviour
// in the CLI becomes error strings here.
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

// handleEvidence implements kern_evidence: the signed-evidence read path
// (tracker C12). It mirrors `kern evidence export|verify|explain` — the same
// internal/evidence calls, with the CLI's exit codes translated into error
// strings. It never shells out: bundles are loaded, verified and rendered
// through internal/evidence directly.
func (s *Server) handleEvidence(ctx context.Context, args map[string]any) (string, error) {
	action := argString(args, "action")
	if action == "" {
		action = "verify"
	}
	switch action {
	case "verify":
		return s.evidenceVerify(ctx, args)
	case "explain":
		return s.evidenceExplain(ctx, args)
	case "export":
		return s.evidenceExport(ctx, args)
	default:
		return "", fmt.Errorf("kern_evidence: unknown action %q (verify|explain|export)", action)
	}
}

// evidenceVerify mirrors `kern evidence verify`: it loads a bundle from
// --file or --url, checks the tamper seal (b.Verify), enforces the
// --expect-fingerprint trust anchor when given, and replays the on-disk
// audit chain at <root>/.kern/audit (skipped for URL bundles without a
// local root, exactly like the CLI). The CLI's exit code 2 (tampered /
// signature mismatch / broken chain) becomes an error string here; a clean
// run returns the verification report as JSON.
func (s *Server) evidenceVerify(ctx context.Context, args map[string]any) (string, error) {
	file := argString(args, "file")
	url := argString(args, "url")
	rootArg := argString(args, "root")
	expectFP := argString(args, "expect_fingerprint")
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
	// Verify the seal + signature and resolve the trust anchor: anchored
	// when expect_fingerprint is supplied and matches; SELF-ATTESTED when
	// verification relied only on the bundle-embedded key (no anchor given)
	// — an attacker can craft a self-consistent bundle, so the distinction
	// must be explicit in the report.
	vres, err := b.VerifyWithAnchor(expectFP)
	if err != nil {
		// Exit 2 in the CLI: tamper seal broken, or trust-anchor mismatch.
		return "", fmt.Errorf("evidence verify: %w", err)
	}
	rep := EvidenceVerifyReport{
		BundleID:      b.BundleID,
		SchemaVersion: b.SchemaVersion,
		Valid:         true,
		TamperSeal:    "intact",
	}
	// Key identity (C4): surface the signature status and trust anchor.
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
	// Verify the audit chain the bundle claims, against the on-disk trail.
	repoRoot := b.RepoRoot
	if rootArg != "" {
		repoRoot = rootArg
	}
	if url != "" && rootArg == "" {
		// URL bundle with no local repo: the seal is verified above; the
		// on-disk chain replay requires a clone, so report what holds.
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

// evidenceExplain mirrors `kern evidence explain`: it loads a bundle from
// --file or --url, verifies it (exit 2 in the CLI becomes an error string)
// and returns the plain-language explanation rendered by internal/evidence.
func (s *Server) evidenceExplain(ctx context.Context, args map[string]any) (string, error) {
	file := argString(args, "file")
	url := argString(args, "url")
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

// evidenceExport mirrors `kern evidence export`: it builds a signed-evidence
// bundle from the project's evidence store for args.task_id (empty = the
// CLI's default, an unscoped bundle for the current state), via
// evidence.Generate over the loaded index. The CLI writes the bundle to
// --out (default stdout); here the bundle is persisted under
// <root>/.kern/evidence/<bundle_id>.json and the path + id are returned as
// JSON so the agent can hand the file to kern evidence verify/explain.
func (s *Server) evidenceExport(ctx context.Context, args map[string]any) (string, error) {
	root := resolveRoot(argString(args, "root"))
	agentID := argString(args, "agent_id")
	if agentID == "" {
		agentID = "default" // CLI default
	}
	task := argString(args, "task_id")

	ix, err := s.loadIndex(ctx, root)
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

// marshalEvidenceReport renders the verification report as indented JSON.
func marshalEvidenceReport(rep EvidenceVerifyReport) (string, error) {
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return "", fmt.Errorf("evidence verify: marshal report: %w", err)
	}
	return string(out), nil
}

// evidenceBundleSource loads a bundle from --file or --url, mirroring the
// CLI's evidenceSource (stdin has no MCP equivalent, so file/url are
// required upstream). A URL bundle is fetched and verified without cloning.
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

// evidenceShortHash8 abbreviates a hex hash for display (first 8 chars),
// mirroring the CLI's shortHash8.
func evidenceShortHash8(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}
