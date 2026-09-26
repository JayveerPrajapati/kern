package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/evidence"
	"github.com/JayveerPrajapati/kern/internal/fetch"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// runEvidence handles evidence export, verify, and explain subcommands plus
// the flag-form full-state operations (--full-state export, --restore).
func runEvidence(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "kern evidence: subcommand required (export|verify|explain|--full-state|--restore)")
		return 2
	}
	// Flag-form operations (Feature Batch J): `kern evidence --full-state
	// [--out FILE]` and `kern evidence --restore FILE`. Detected by scanning
	// (not args[0]) so --root/--out may appear before or after the flag.
	if hasFlag(args, "--full-state") {
		return runEvidenceFullState(args)
	}
	if hasFlag(args, "--restore") {
		return runEvidenceRestore(args)
	}
	switch args[0] {
	case "export":
		return runEvidenceExport(args[1:])
	case "verify":
		return runEvidenceVerify(args[1:])
	case "explain":
		return runEvidenceExplain(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "kern evidence: unknown subcommand %q (export|verify|explain|--full-state|--restore)\n", args[0])
		return 2
	}
}

// runEvidenceFullState exports the complete evidence store state for --root
// as a deterministic, tamper-evident full-state bundle: every record with
// per-record SHA-256 checksums plus a bundle digest. Default output is stdout;
// --out writes the file (same convention as evidence export).
func runEvidenceFullState(rest []string) int {
	f, _, err := parseFlags(rest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence --full-state: %v\n", err)
		return 1
	}
	root := projectRoot(f)
	out := f.out
	if out == "" {
		out = "-"
	}

	var buf bytes.Buffer
	if err := evidence.ExportFullState(root, &buf); err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence --full-state: %v\n", err)
		return 1
	}

	if out == "-" {
		_, _ = os.Stdout.Write(buf.Bytes())
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence --full-state: %v\n", err)
		return 1
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence --full-state: write %s: %v\n", out, err)
		return 1
	}
	count := 0
	var meta struct {
		Store struct {
			Count int `json:"count"`
		} `json:"store"`
	}
	if json.Unmarshal(buf.Bytes(), &meta) == nil {
		count = meta.Store.Count
	}
	fmt.Printf("wrote evidence full-state bundle (%d records) to %s\n", count, out)
	return 0
}

// runEvidenceRestore restores the evidence store for --root from a full-state
// bundle file. The bundle's digest and every per-record checksum are verified
// before any write; a tampered bundle fails with a clear error and the store
// is left untouched. The bundle path is the --restore value, or the first
// positional (same convention as evidence verify's --file).
func runEvidenceRestore(rest []string) int {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence --restore: %v\n", err)
		return 1
	}
	root := projectRoot(f)
	file := f.restore
	if file == "" && len(pos) > 0 {
		file = pos[0]
	}
	if file == "" {
		fmt.Fprintln(os.Stderr, "kern evidence --restore: missing bundle file (usage: kern evidence --restore FILE [--root ROOT])")
		return 1
	}
	r, err := os.Open(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence --restore: %v\n", err)
		return 1
	}
	defer r.Close()
	if err := evidence.RestoreFullState(root, r); err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence --restore: %v\n", err)
		return 1
	}
	fmt.Printf("restored evidence full state into %s\n", filepath.Join(root, ".kern", "evidence"))
	return 0
}

// runEvidenceExport builds a signed evidence bundle for a repo.
func runEvidenceExport(rest []string) int {
	f, _, err := parseFlags(rest)
	if err != nil {
		return 1
	}
	root := f.root
	agentID := f.agentID
	if agentID == "" {
		agentID = "default" // --agent-id default "default"
	}
	task := f.task
	out := f.out
	if out == "" {
		out = "-" // --out default "-" (stdout)
	}
	sign := f.sign
	_ = f.json // the bundle has no text form; JSON is the wire format

	ix, err := loadOrBuild(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence export: %v\n", err)
		return 1
	}

	b, err := evidence.Generate(root, agentID, task, ix)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence export: %v\n", err)
		return 1
	}

	if sign {
		kp, err := evidence.LoadOrCreateKeys(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kern evidence export: %v\n", err)
			return 1
		}
		if err := b.Sign(kp); err != nil {
			fmt.Fprintf(os.Stderr, "kern evidence export: %v\n", err)
			return 1
		}
	}

	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence export: marshal bundle: %v\n", err)
		return 1
	}
	data = append(data, '\n')

	if out == "-" {
		_, _ = os.Stdout.Write(data)
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence export: %v\n", err)
		return 1
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence export: write %s: %v\n", out, err)
		return 1
	}
	fmt.Printf("wrote evidence bundle %s to %s", b.BundleID, out)
	if sign {
		fmt.Printf(" (signed with key %s)", b.Signature.KeyFingerprint)
	}
	fmt.Println()
	return 0
}

// runEvidenceVerify validates an evidence bundle's seal and audit chain.
// The bundle can come from a local file, stdin, or --url (verified without
// cloning the repo — C4); the audit-chain replay needs the repo on disk and
// is skipped for URL bundles unless --root is given.
func runEvidenceVerify(rest []string) int {
	f, pos, err := parseFlags(rest)
	if err != nil {
		return 1
	}
	file := f.file
	url := f.url
	root := f.root
	expectFP := f.expectFingerprint
	// Accept the bundle path as a positional argument as well as
	// --file, so `kern evidence verify /tmp/b.json` behaves exactly like
	// `kern evidence verify --file /tmp/b.json` (and stdin when absent).
	if file == "" && len(pos) > 0 {
		file = pos[0]
	}
	data, err := evidenceSource(file, url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence verify: %v\n", err)
		return 1
	}

	b, err := evidence.Parse(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence verify: %v\n", err)
		return 1
	}

	// Key identity (C4): VerifyWithAnchor validates the signature against the
	// embedded key and, when --expect-fingerprint is given, anchors it to the
	// caller-supplied trust anchor (an unsigned bundle never satisfies it).
	// The result makes the trust basis explicit in the output: without an
	// expected fingerprint, a valid signature is SELF-ATTESTED — verified
	// against the bundle-embedded key only, which a self-consistent attacker
	// can satisfy.
	vres, err := b.VerifyWithAnchor(expectFP)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence verify: %v\n", err)
		return 2
	}
	switch vres.TrustAnchor {
	case evidence.TrustAnchorAnchored:
		fmt.Printf("signature: valid, key %s ANCHORED — matches the expected fingerprint %s\n",
			b.Signature.KeyFingerprint, vres.ExpectedFingerprint)
	case evidence.TrustAnchorSelfAttested:
		fmt.Printf("signature: valid (SELF-ATTESTED — verified against the bundle-embedded key %s only; supply --expect-fingerprint to anchor trust)\n",
			b.Signature.KeyFingerprint)
	default:
		fmt.Println("signature: unsigned (digest-only seal)")
	}

	// Verify the audit chain the bundle claims, against the on-disk trail.
	repoRoot := b.RepoRoot
	if root != "" {
		repoRoot = root
	}
	if url != "" && root == "" {
		// URL bundle with no local repo: the seal is verified above; the
		// on-disk chain replay requires a clone, so report what holds.
		fmt.Printf("Bundle %s VALID (seal). Schema v%d. Audit chain NOT replayed (no local repo — pass --root to replay).\n",
			b.BundleID, b.SchemaVersion)
		return 0
	}
	store := storage.NewLog(filepath.Join(repoRoot, ".kern", "audit"))
	log := governance.NewAuditLog().WithStore(store)
	if _, err := log.Replay(); err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence verify: replay audit chain at %s: %v\n", repoRoot, err)
		return 1
	}
	if !log.VerifyChain() {
		fmt.Fprintf(os.Stderr, "kern evidence verify: audit chain at %s is broken (tampered)\n", repoRoot)
		return 2
	}
	disk := log.All()
	if len(disk) < len(b.AuditTrail) {
		fmt.Fprintf(os.Stderr, "kern evidence verify: bundle claims %d audit entries but on-disk chain has %d\n",
			len(b.AuditTrail), len(disk))
		return 2
	}
	for i := range b.AuditTrail {
		if disk[i].Hash != b.AuditTrail[i].Hash {
			fmt.Fprintf(os.Stderr, "kern evidence verify: audit trail mismatch at entry %d (bundle %s…, disk %s…)\n",
				i, shortHash8(b.AuditTrail[i].Hash), shortHash8(disk[i].Hash))
			return 2
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
		lastHash = shortHash8(b.AuditChainHash) + "…"
	}
	fmt.Printf("Bundle %s VALID. Schema v%d. Audit chain intact (%d entries, last hash %s). Authorization: %s. Freshness: %s.\n",
		b.BundleID, b.SchemaVersion, len(disk), lastHash, decision, verdict)
	return 0
}

// shortHash8 abbreviates a hex hash for display (first 8 chars).
func shortHash8(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}

// evidenceSource loads a bundle from --file, --url, or stdin.
func evidenceSource(file, url string) ([]byte, error) {
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
	return readStdin()
}

// runEvidenceExplain renders a bundle in plain language: what it proves and
// how to verify it (C4 reviewer-side trust). Source: --file, --url, or stdin.
func runEvidenceExplain(rest []string) int {
	f, pos, err := parseFlags(rest)
	if err != nil {
		return 1
	}
	file := f.file
	url := f.url
	// Accept the bundle path as a positional argument as well as
	// --file (same plumbing as evidence verify).
	if file == "" && len(pos) > 0 {
		file = pos[0]
	}
	data, err := evidenceSource(file, url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence explain: %v\n", err)
		return 1
	}
	b, err := evidence.Parse(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence explain: %v\n", err)
		return 1
	}
	if err := b.Verify(); err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence explain: bundle failed verification: %v\n", err)
		return 2
	}
	fmt.Print(b.Explain())
	return 0
}
