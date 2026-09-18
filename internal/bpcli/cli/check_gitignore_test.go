package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureBlueprintRuntimeGitignoredIdempotent locks F-WARN: a legacy
// (pre-marker) blueprint block must be replaced, not duplicated, and
// .blueprint/sec-cache.json must be part of the generated block.
func TestEnsureBlueprintRuntimeGitignoredIdempotent(t *testing.T) {
	root := t.TempDir()
	gi := filepath.Join(root, ".gitignore")
	// Legacy unmarked entries + user content.
	legacy := "user-entry\n.blueprint/audit/\n.blueprint/receipts/\n.blueprint/verdict-cache/\n.blueprint/fingerprint-cache/\n.blueprint/metrics.json\nother-user\n"
	if err := os.WriteFile(gi, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	ensureBlueprintRuntimeGitignored(root)
	ensureBlueprintRuntimeGitignored(root) // second run must not duplicate

	data, err := os.ReadFile(gi)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)

	if got := strings.Count(text, blueprintGitignoreMarker); got != 1 {
		t.Fatalf("expected exactly 1 marked block, got %d\n%s", got, text)
	}
	// Legacy entries must be gone (folded into the marked block).
	if strings.Contains(text, "user-entry") == false {
		t.Fatalf("user content was removed:\n%s", text)
	}
	for _, e := range blueprintRuntimeEntries {
		if strings.Count(text, e) != 1 {
			t.Fatalf("entry %q must appear exactly once (in the marked block):\n%s", e, text)
		}
	}
	if !strings.Contains(text, ".blueprint/sec-cache.json") {
		t.Fatalf("sec-cache.json missing from generated block:\n%s", text)
	}
	if !strings.Contains(text, "other-user") {
		t.Fatalf("user content after block was removed:\n%s", text)
	}
}

// TestEnsureBlueprintRuntimeGitignoredMarkedBlockStable verifies a properly
// marked existing block is not duplicated by re-runs.
func TestEnsureBlueprintRuntimeGitignoredMarkedBlockStable(t *testing.T) {
	root := t.TempDir()
	gi := filepath.Join(root, ".gitignore")
	block := "x\n" + blueprintGitignoreMarker + "\n" +
		strings.Join(blueprintRuntimeEntries, "\n") + "\n" +
		"# --- end blueprint runtime state ---\n" + "y\n"
	if err := os.WriteFile(gi, []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		ensureBlueprintRuntimeGitignored(root)
	}
	data, _ := os.ReadFile(gi)
	if got := strings.Count(string(data), blueprintGitignoreMarker); got != 1 {
		t.Fatalf("marked block duplicated across runs: %d blocks\n%s", got, data)
	}
}
