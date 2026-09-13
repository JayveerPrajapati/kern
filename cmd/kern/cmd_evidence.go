package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/evidence"
	"github.com/JayveerPrajapati/kern/internal/fetch"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// runEvidence handles evidence export, verify, and explain subcommands.
func runEvidence(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "kern evidence: subcommand required (export|verify|explain)")
		return 1
	}
	switch args[0] {
	case "export":
		return runEvidenceExport(args[1:])
	case "verify":
		return runEvidenceVerify(args[1:])
	case "explain":
		return runEvidenceExplain(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "kern evidence: unknown subcommand %q (export|verify|explain)\n", args[0])
		return 1
	}
}

// runEvidenceExport builds a signed evidence bundle for a repo.
func runEvidenceExport(rest []string) int {
	fs := flag.NewFlagSet("evidence export", flag.ContinueOnError)
	var (
		root    = fs.String("root", ".", "project root")
		agentID = fs.String("agent-id", "default", "agent ID the authorization is scoped to")
		task    = fs.String("task", "", "task ID the authorization is scoped to")
		out     = fs.String("out", "-", "output path (\"-\" = stdout)")
		jsonOut = fs.Bool("json", true, "emit JSON (the only form; default true)")
		sign    = fs.Bool("sign", false, "sign the bundle with the project key (.kern/keys/, created on first use)")
	)
	if err := fs.Parse(rest); err != nil {
		return 1
	}
	_ = jsonOut // the bundle has no text form; JSON is the wire format

	ix, err := loadOrBuild(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence export: %v\n", err)
		return 1
	}

	b, err := evidence.Generate(*root, *agentID, *task, ix)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence export: %v\n", err)
		return 1
	}

	if *sign {
		kp, err := evidence.LoadOrCreateKeys(*root)
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

	if *out == "-" {
		_, _ = os.Stdout.Write(data)
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence export: %v\n", err)
		return 1
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence export: write %s: %v\n", *out, err)
		return 1
	}
	fmt.Printf("wrote evidence bundle %s to %s", b.BundleID, *out)
	if *sign {
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
	fs := flag.NewFlagSet("evidence verify", flag.ContinueOnError)
	var (
		file     = fs.String("file", "", "bundle JSON file (default: read from stdin)")
		url      = fs.String("url", "", "bundle URL to fetch and verify without cloning")
		root     = fs.String("root", "", "repo root to verify the audit chain against (default: bundle's repo_root)")
		expectFP = fs.String("expect-fingerprint", "", "require the bundle to be signed by this key fingerprint (the trust anchor)")
	)
	if err := fs.Parse(rest); err != nil {
		return 1
	}
	// F-012: accept the bundle path as a positional argument as well as
	// --file, so `kern evidence verify /tmp/b.json` behaves exactly like
	// `kern evidence verify --file /tmp/b.json` (and stdin when absent).
	if *file == "" && len(fs.Args()) > 0 {
		*file = fs.Args()[0]
	}
	data, err := evidenceSource(*file, *url)
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
	vres, err := b.VerifyWithAnchor(*expectFP)
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
	if *root != "" {
		repoRoot = *root
	}
	if *url != "" && *root == "" {
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
	fs := flag.NewFlagSet("evidence explain", flag.ContinueOnError)
	var (
		file = fs.String("file", "", "bundle JSON file (default: read from stdin)")
		url  = fs.String("url", "", "bundle URL to fetch and explain without cloning")
	)
	if err := fs.Parse(rest); err != nil {
		return 1
	}
	// F-012: accept the bundle path as a positional argument as well as
	// --file (same plumbing as evidence verify).
	if *file == "" && len(fs.Args()) > 0 {
		*file = fs.Args()[0]
	}
	data, err := evidenceSource(*file, *url)
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
