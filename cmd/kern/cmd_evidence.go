package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/evidence"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// runEvidence handles evidence export and verify subcommands.
func runEvidence(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "kern evidence: subcommand required (export|verify)")
		return 1
	}
	switch args[0] {
	case "export":
		return runEvidenceExport(args[1:])
	case "verify":
		return runEvidenceVerify(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "kern evidence: unknown subcommand %q (export|verify)\n", args[0])
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
	fmt.Printf("wrote evidence bundle %s to %s\n", b.BundleID, *out)
	return 0
}

// runEvidenceVerify validates an evidence bundle's seal and audit chain.
func runEvidenceVerify(rest []string) int {
	fs := flag.NewFlagSet("evidence verify", flag.ContinueOnError)
	var (
		file = fs.String("file", "", "bundle JSON file (default: read from stdin)")
		root = fs.String("root", "", "repo root to verify the audit chain against (default: bundle's repo_root)")
	)
	if err := fs.Parse(rest); err != nil {
		return 1
	}

	var data []byte
	var err error
	if *file != "" {
		data, err = os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kern evidence verify: read %s: %v\n", *file, err)
			return 1
		}
	} else {
		data, err = readStdin()
		if err != nil {
			fmt.Fprintf(os.Stderr, "kern evidence verify: read stdin: %v\n", err)
			return 1
		}
	}

	b, err := evidence.Parse(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence verify: %v\n", err)
		return 1
	}

	if err := b.Verify(); err != nil {
		fmt.Fprintf(os.Stderr, "kern evidence verify: %v\n", err)
		return 2
	}

	// Verify the audit chain the bundle claims, against the on-disk trail.
	repoRoot := b.RepoRoot
	if *root != "" {
		repoRoot = *root
	}
	store := storage.NewLocal(filepath.Join(repoRoot, ".kern", "audit"))
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
