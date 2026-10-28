package auditdiag

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsRunDir(t *testing.T) {
	for name, want := range map[string]bool{
		"20260101-000000.000000001": true,
		"20261028-123456.123456789": true,
		"archive-20260101-000000":   false,
		"keepme":                    false,
		"20260101-000000":           false,
	} {
		if got := IsRunDir(name); got != want {
			t.Errorf("IsRunDir(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestPruneRunDirs(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"20260101-000000.000000001",
		"20260102-000000.000000002",
		"20260103-000000.000000003",
		"20260104-000000.000000004",
		"20260105-000000.000000005",
		"archive-20251201-000000",
		"operator-notes",
	}
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(dir, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if removed := PruneRunDirs(dir, 2); removed != 3 {
		t.Fatalf("PruneRunDirs removed %d, want 3", removed)
	}
	for _, n := range names[:3] {
		if _, err := os.Stat(filepath.Join(dir, n)); !os.IsNotExist(err) {
			t.Errorf("old run %s must be removed (stat err=%v)", n, err)
		}
	}
	for _, n := range append(names[3:], "archive-20251201-000000", "operator-notes") {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s must be preserved: %v", n, err)
		}
	}
	if PruneRunDirs(dir, 0) != 0 {
		t.Error("keep=0 must not delete anything")
	}
}

func TestFindingsRetentionThreshold(t *testing.T) {
	root := t.TempDir()
	audit := filepath.Join(root, ".kern", "audit")
	for _, n := range []string{"20260101-000000.000000001", "20260102-000000.000000002", "20260103-000000.000000003"} {
		if err := os.MkdirAll(filepath.Join(audit, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("KERN_AUDIT_WARN_RUNS", "2")
	t.Setenv("KERN_AUDIT_WARN_BYTES", "0")
	found := false
	for _, f := range Findings(root) {
		if f.Check == "audit retention" {
			found = true
			if f.Level != "warn" {
				t.Errorf("audit retention level = %q, want warn; detail=%s", f.Level, f.Detail)
			}
		}
	}
	if !found {
		t.Fatal("Findings must include the audit retention check")
	}

	t.Setenv("KERN_AUDIT_WARN_RUNS", "100")
	for _, f := range Findings(root) {
		if f.Check == "audit retention" && f.Level != "ok" {
			t.Errorf("under the run budget the level must be ok, got %q (%s)", f.Level, f.Detail)
		}
	}
}
