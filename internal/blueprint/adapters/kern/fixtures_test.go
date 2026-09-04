package kern

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFixturesBuild verifies that every fixture materializes a valid git repo
// with a boundaries file and staged files. It does not run kern; that is the
// G2 test's job.
func TestFixturesBuild(t *testing.T) {
	fixtures := []struct {
		name  string
		build func(t *testing.T) FixtureResult
	}{
		{"ArchitectureClean", ArchitectureClean},
		{"LegalDependency", LegalDependency},
		{"IllegalDependency", IllegalDependency},
		{"MultipleViolations", MultipleViolations},
		{"PreexistingViolationUnrelatedChange", PreexistingViolationUnrelatedChange},
		{"RenameScenario", RenameScenario},
		{"NewFileScenario", NewFileScenario},
		{"DeletedImportScenario", DeletedImportScenario},
	}

	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			res := f.build(t)
			if res.RepoPath == "" {
				t.Fatal("RepoPath is empty")
			}
			if fi, err := os.Stat(res.RepoPath); err != nil || !fi.IsDir() {
				t.Fatalf("RepoPath %q is not a directory: %v", res.RepoPath, err)
			}
			if _, err := os.Stat(filepath.Join(res.RepoPath, ".git")); err != nil {
				t.Fatalf("%q is not a git repo: %v", res.RepoPath, err)
			}
			if _, err := os.Stat(filepath.Join(res.RepoPath, ".kern", "boundaries.json")); err != nil {
				t.Fatalf(".kern/boundaries.json missing: %v", err)
			}
			if len(res.StagedFiles) == 0 {
				t.Fatal("StagedFiles is empty")
			}
			for _, sf := range res.StagedFiles {
				if _, err := os.Stat(filepath.Join(res.RepoPath, sf)); err != nil {
					t.Fatalf("staged file %q missing: %v", sf, err)
				}
			}
		})
	}
}
