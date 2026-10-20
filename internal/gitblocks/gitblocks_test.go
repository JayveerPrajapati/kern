package gitblocks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempHome points the global home resolution (HOME + XDG_CONFIG_HOME) at a
// temp dir for the duration of a test, so global wiring never touches the
// real home.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	return dir
}

func TestGitignoreGenerated(t *testing.T) {
	dir := t.TempDir()
	// Existing .gitignore content is preserved.
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("bin/\n"), 0o644)
	st := GitignoreGenerated(dir)
	if !st.Installed {
		t.Fatalf("gitignore update failed: %s", st.Note)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	content := string(b)
	if !strings.Contains(content, ".mcp.json") || !strings.Contains(content, ".claude/") {
		t.Fatalf("generated entries missing:\n%s", content)
	}
	if !strings.HasPrefix(content, "bin/\n") {
		t.Fatal("existing .gitignore content was not preserved")
	}
	// Idempotent: second run adds nothing.
	before := content
	GitignoreGenerated(dir)
	b, _ = os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(b) != before {
		t.Fatal("gitignore block duplicated on re-run")
	}
}

func TestWireLocalGitExclude(t *testing.T) {
	dir := t.TempDir()
	// Test on non-git dir: should fail cleanly
	st := WireLocalGitExclude(dir)
	if st.Installed {
		t.Fatal("expected not installed for non-git dir")
	}

	// Create a simulated .git directory
	gitDir := filepath.Join(dir, ".git")
	_ = os.MkdirAll(filepath.Join(gitDir, "info"), 0o755)

	st = WireLocalGitExclude(dir)
	if !st.Installed {
		t.Fatalf("expected installed, got error: %s", st.Note)
	}

	excludePath := filepath.Join(gitDir, "info", "exclude")
	b, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("failed to read exclude: %v", err)
	}
	if !strings.Contains(string(b), ".kern/") {
		t.Fatalf("expected .kern/ in exclude, got:\n%s", string(b))
	}

	// Idempotent: second run does not duplicate
	before := string(b)
	WireLocalGitExclude(dir)
	b, _ = os.ReadFile(excludePath)
	if string(b) != before {
		t.Fatal("exclude duplicated on re-run")
	}
}

func TestGitignoreGeneratedBlueprintRuntime(t *testing.T) {
	dir := t.TempDir()
	st := GitignoreGenerated(dir)
	if !st.Installed {
		t.Fatalf("gitignore update failed: %s", st.Note)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	// The .blueprint runtime entries are owned by internal/bpcli/cli's
	// ensureBlueprintRuntimeGitignored (a separate marked block appended by
	// `kern check`), NOT by the kern-generated block. Re-adding them
	// here would be stripped as legacy on the next check, dirtying the
	// file on every setup→check cycle.
	for _, banned := range []string{
		".blueprint/audit/",
		".blueprint/receipts/",
		".blueprint/verdict-cache/",
		".blueprint/fingerprint-cache/",
		".blueprint/metrics.json",
	} {
		if strings.Contains(content, banned) {
			t.Errorf(".gitignore must not contain %q (owned by kern check's blueprint block):\n%s", banned, content)
		}
	}
	if !strings.Contains(content, ".kern/") {
		t.Errorf(".gitignore missing %q:\n%s", ".kern/", content)
	}
	// No wholesale .blueprint/ ignore and no config-file ignores.
	for _, banned := range []string{
		"\n.blueprint/\n",
		"config.yaml",
		"suppressions.yaml",
		"owners.yaml",
	} {
		if strings.Contains(content, banned) {
			t.Errorf(".gitignore must not contain %q (user config stays committable):\n%s", banned, content)
		}
	}
	// Idempotent re-run.
	before := content
	GitignoreGenerated(dir)
	b, _ = os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(b) != before {
		t.Fatal("gitignore block changed on re-run")
	}
	if got := strings.Count(string(b), ".blueprint/audit/"); got != 0 {
		t.Fatalf("blueprint audit entry appears %d times, want 0", got)
	}
}

func TestGitignoreGeneratedIdempotentReplace(t *testing.T) {
	dir := t.TempDir()
	legacy := "# user section\nfoo/\n" + gitignoreMarker + "\n.old-entry/\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	st := GitignoreGenerated(dir)
	if !st.Installed {
		t.Fatalf("gitignore update failed: %s", st.Note)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	content := string(b)
	if !strings.HasPrefix(content, "# user section\nfoo/\n") {
		t.Fatalf("user content not preserved:\n%s", content)
	}
	if got := strings.Count(content, gitignoreMarker); got != 1 {
		t.Fatalf("expected exactly one kern block, got %d markers:\n%s", got, content)
	}
	if strings.Contains(content, ".old-entry/") {
		t.Fatalf("legacy block not replaced (stale entry still present):\n%s", content)
	}
	// Run twice: still one block, byte-identical.
	before := content
	GitignoreGenerated(dir)
	b, _ = os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(b) != before {
		t.Fatal("second run changed the file")
	}
	if got := strings.Count(string(b), gitignoreMarker); got != 1 {
		t.Fatalf("second run duplicated the kern block: %d markers", got)
	}
}

// TestGitignoreGeneratedCoversNewAgents verifies the generated .gitignore
// block ignores every machine-specific agent config, including the newer
// .continue/ and .windsurf/ rule files and the .vscode/ adapter config
// (F10: setup writes .vscode/mcp.json but never ignored it — untracked noise).
func TestGitignoreGeneratedCoversNewAgents(t *testing.T) {
	dir := t.TempDir()
	st := GitignoreGenerated(dir)
	if !st.Installed {
		t.Fatalf("gitignore update failed: %s", st.Note)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("gitignore not written: %v", err)
	}
	for _, want := range []string{".continue/", ".windsurf/", ".kern/", ".gemini/", ".kiro/", ".vscode/", ".github/hooks/", ".agents/rules/kern.md", ".agents/hooks.json"} {
		if !strings.Contains(string(b), want) {
			t.Errorf(".gitignore missing %q:\n%s", want, b)
		}
	}
}

// TestBackupFilePrunesOldBackups pins the backup-retention contract (4.10):
// writing a managed file keeps at most the 3 most recent .bak.<timestamp>
// backups, so repeated setup runs cannot litter config dirs (the same
// keep-3 policy internal/agentwire applies — gitblocks' backupFile previously
// wrote .bak.<timestamp> copies without ever pruning them).
func TestBackupFilePrunesOldBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gitignore")
	if err := os.WriteFile(path, []byte("managed content"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Four pre-existing backups, oldest first in name (fixed-width
	// YYYYMMDD-HHMMSS sorts lexicographically).
	for _, ts := range []string{
		"20200101-000000",
		"20200102-000000",
		"20200103-000000",
		"20200104-000000",
	} {
		if err := os.WriteFile(path+".bak."+ts, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// backupFile writes a fifth (.bak.<now>) and must prune back to the 3
	// newest: the just-written backup plus 20200104 and 20200103.
	if err := backupFile(path); err != nil {
		t.Fatalf("backupFile: %v", err)
	}
	entries, err := filepath.Glob(path + ".bak.*")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("after backupFile, %d backups remain, want 3: %v", len(entries), entries)
	}
	joined := ""
	for _, e := range entries {
		joined += filepath.Base(e) + " "
	}
	for _, want := range []string{".bak.20200104-000000", ".bak.20200103-000000"} {
		if !contains(entries, want) {
			t.Errorf("newest pre-existing backup %s was pruned; remaining: %s", want, joined)
		}
	}
	for _, gone := range []string{".bak.20200102-000000", ".bak.20200101-000000"} {
		if contains(entries, gone) {
			t.Errorf("old backup %s should have been pruned; remaining: %s", gone, joined)
		}
	}
}

// TestBackupFileKeepsFewerThanFour pins the no-op side: with 3 or fewer
// backups already present, backupFile prunes nothing.
func TestBackupFileKeepsFewerThanFour(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gitignore")
	if err := os.WriteFile(path, []byte("managed content"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, ts := range []string{"20200101-000000", "20200102-000000"} {
		if err := os.WriteFile(path+".bak."+ts, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := backupFile(path); err != nil {
		t.Fatalf("backupFile: %v", err)
	}
	entries, err := filepath.Glob(path + ".bak.*")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("with 2 pre-existing backups, %d remain after backupFile, want 3 (nothing pruned): %v", len(entries), entries)
	}
}

func contains(entries []string, suffix string) bool {
	for _, e := range entries {
		if strings.Contains(filepath.Base(e), suffix) {
			return true
		}
	}
	return false
}

func TestWireGlobalGitignoreBlueprintRuntime(t *testing.T) {
	dir := withTempHome(t) // HOME + XDG_CONFIG_HOME -> dir/.config
	if err := os.MkdirAll(filepath.Join(dir, ".config", "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	st := WireGlobalGitignore()
	if !st.Installed {
		t.Fatalf("global gitignore failed: %s", st.Note)
	}
	ignorePath := filepath.Join(dir, ".config", "git", "ignore")
	b, err := os.ReadFile(ignorePath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	for _, want := range append([]string{".kern/"}, blueprintRuntimeEntries...) {
		if !strings.Contains(content, want) {
			t.Errorf("global git ignore missing %q:\n%s", want, content)
		}
	}
	for _, banned := range []string{"config.yaml", "suppressions.yaml", "owners.yaml"} {
		if strings.Contains(content, banned) {
			t.Errorf("global git ignore must not contain %q:\n%s", banned, content)
		}
	}
	// Idempotent re-run: nothing added, status says already configured.
	before := content
	st2 := WireGlobalGitignore()
	b, _ = os.ReadFile(ignorePath)
	if string(b) != before {
		t.Fatal("global git ignore changed on re-run")
	}
	if st2.Note != "global git ignore already configured" {
		t.Fatalf("re-run note = %q, want already-configured", st2.Note)
	}
}
