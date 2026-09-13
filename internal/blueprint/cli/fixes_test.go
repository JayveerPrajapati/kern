package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/audit"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// TestFixExitCode_WarnExitsZero (F-021): informational WARN findings (e.g.
// architecture:not-enforced on an unindexed repo) must not block a fix —
// exit 0 with warnings; exit 1 is reserved for BLOCK findings; StatusError
// stays a tool failure (2).
func TestFixExitCode_WarnExitsZero(t *testing.T) {
	cases := []struct {
		name   string
		status domain.Status
		want   int
	}{
		{"pass", domain.StatusPass, 0},
		{"warn-only", domain.StatusWarn, 0},
		{"block", domain.StatusBlock, 1},
		{"skip", domain.StatusSkip, 0},
		{"error", domain.StatusError, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := domain.ValidationResult{Status: tc.status, ExitCode: 0}
			if got := fixExitCode(res); got != tc.want {
				t.Errorf("fixExitCode(%s) = %d, want %d", tc.status, got, tc.want)
			}
		})
	}
}

// TestIsBlueprintRuntimeArtifact (F-020): kern/blueprint runtime state paths
// must be excluded from change discovery so the gates never re-scan the
// tool's own output, while user-authored config stays a real change.
func TestIsBlueprintRuntimeArtifact(t *testing.T) {
	ignored := []string{
		".blueprint/audit/audit.jsonl",
		".blueprint/receipts/bp-1.json",
		".blueprint/verdict-cache/x.json",
		".blueprint/fingerprint-cache/fingerprints.json",
		".blueprint/metrics.json",
		".kern/blueprint-result.json",
	}
	for _, p := range ignored {
		if !isBlueprintRuntimeArtifact(p) {
			t.Errorf("isBlueprintRuntimeArtifact(%q) = false, want true", p)
		}
	}
	kept := []string{
		".blueprint/config.yaml",
		".blueprint/suppressions.yaml",
		".blueprint/owners.yaml",
		".kern/boundaries.json",
		"internal/repo/repo.go",
		".blueprint/audit-config.yaml", // user file that merely looks similar
	}
	for _, p := range kept {
		if isBlueprintRuntimeArtifact(p) {
			t.Errorf("isBlueprintRuntimeArtifact(%q) = true, want false", p)
		}
	}
}

// TestEnsureBlueprintRuntimeGitignored (F-023c): the first run must add the
// blueprint runtime-state paths to .gitignore (idempotently), while leaving
// user-authored .blueprint/config.yaml committable and existing user
// .gitignore content untouched.
func TestEnsureBlueprintRuntimeGitignored(t *testing.T) {
	root := t.TempDir()
	gitignore := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(gitignore, []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	ensureBlueprintRuntimeGitignored(root)
	data, err := os.ReadFile(gitignore)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	out := string(data)
	for _, entry := range []string{
		".blueprint/audit/",
		".blueprint/receipts/",
		".blueprint/verdict-cache/",
		".blueprint/fingerprint-cache/",
		".blueprint/metrics.json",
	} {
		if !strings.Contains(out, entry) {
			t.Errorf(".gitignore missing %q:\n%s", entry, out)
		}
	}
	if !strings.Contains(out, "node_modules/") {
		t.Errorf(".gitignore lost pre-existing user content:\n%s", out)
	}
	if strings.Contains(out, ".blueprint/config.yaml") {
		t.Errorf(".gitignore must NOT ignore user config .blueprint/config.yaml:\n%s", out)
	}
	// Idempotent: a second run must not duplicate the block.
	ensureBlueprintRuntimeGitignored(root)
	data2, err := os.ReadFile(gitignore)
	if err != nil {
		t.Fatalf("re-read .gitignore: %v", err)
	}
	if strings.Count(string(data2), ".blueprint/audit/") != 1 {
		t.Errorf(".gitignore duplicated the blueprint block after a second run:\n%s", data2)
	}
}

// TestWriteCINoopAuditRecord (F-019): a no-op ci run (empty diff) writes no
// service audit record (G1 NOOP contract), so ci appends its own record —
// the receipt then binds to a real chain endpoint instead of an empty hash.
func TestWriteCINoopAuditRecord(t *testing.T) {
	dir := t.TempDir()
	w := audit.NewWriter(filepath.Join(dir, ".blueprint", "audit", "audit.jsonl"))
	result := domain.ValidationResult{
		Status:        domain.StatusPass,
		ExitCode:      0,
		CorrelationID: "bp-noop-1",
	}
	writeCINoopAuditRecord(w, result, dir)
	if got := w.LastHash(); got == "" {
		t.Fatal("LastHash() = \"\", want a real chain endpoint after the NOOP record")
	}
	// The chain must verify and contain the sealed hash (verify-receipt H3/H4).
	last, err := w.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if last != w.LastHash() {
		t.Errorf("VerifyChain() = %q, want LastHash() %q", last, w.LastHash())
	}
	if found, err := w.ChainContainsHash(w.LastHash()); err != nil || !found {
		t.Errorf("ChainContainsHash(LastHash) = %v/%v, want true/nil", found, err)
	}
}
