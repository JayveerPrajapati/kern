package agentwire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBackupFilePrunesOldBackups pins the backup-retention contract: writing
// a managed file keeps at most the 3 most recent .bak.<timestamp> backups,
// so repeated setup runs cannot litter config dirs (observed: 14 files
// across ~/.codex, ~/.cursor/rules, ~/.gemini).
func TestBackupFilePrunesOldBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
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
	// BackupFile writes a fifth (.bak.<now>) and must prune back to the 3
	// newest: the just-written backup plus 20200104 and 20200103.
	if err := BackupFile(path); err != nil {
		t.Fatalf("BackupFile: %v", err)
	}
	entries, err := filepath.Glob(path + ".bak.*")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("after BackupFile, %d backups remain, want 3: %v", len(entries), entries)
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
// backups already present, BackupFile prunes nothing.
func TestBackupFileKeepsFewerThanFour(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte("managed content"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, ts := range []string{"20200101-000000", "20200102-000000"} {
		if err := os.WriteFile(path+".bak."+ts, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := BackupFile(path); err != nil {
		t.Fatalf("BackupFile: %v", err)
	}
	entries, err := filepath.Glob(path + ".bak.*")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("with 2 pre-existing backups, %d remain after BackupFile, want 3 (nothing pruned): %v", len(entries), entries)
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
