package kern

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// Hermetic unit tests for the content-aware SecretCheck path (proposed file
// content is scanned from a temp directory). These use fake runners — no kern
// binary required — so the non-gate suite covers the pre-write content path.

// TestSecretCheckContentScanFindsProposedSecret: a file with Content != ""
// (not on disk) is scanned from a temp directory and its secret is reported as
// a redacted BLOCK finding, with the proposed file's repo-relative path.
func TestSecretCheckContentScanFindsProposedSecret(t *testing.T) {
	client := &KernClient{
		binaryPath: "kern",
		runner:     fakeRunner(`{"schema_version":2,"findings":[{"file":"config.go","line":2,"rule":"hardcoded-secret","severity":"error","message":"hardcoded secret: AWS","snippet":"AKIA..."}]}`, "", 1, nil),
	}
	chk := NewSecretCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Files:          []domain.FileChange{{Path: "config.go", Content: "package main\nconst k = \"AKIA...\"\n"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want %q (proposed secret must block)", cr.Status, domain.StatusBlock)
	}
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1", len(cr.Findings))
	}
	f := cr.Findings[0]
	if f.File != "config.go" {
		t.Errorf("File = %q, want config.go (temp-relative == repo-relative)", f.File)
	}
	if !f.Redacted {
		t.Error("Redacted = false, want true")
	}
	// The raw snippet must never propagate (redaction is absolute).
	if strings.Contains(f.Explanation, "AKIA") || strings.Contains(f.Message, "AKIA") {
		t.Error("snippet leaked into the finding")
	}
}

// TestSecretCheckContentScanUsesTempDir: the content scan must invoke `kern
// sec --json .` against a temp directory (never against the repository), so
// proposed content is checked without touching the repo on disk.
func TestSecretCheckContentScanUsesTempDir(t *testing.T) {
	var workdirs []string
	client := &KernClient{
		binaryPath: "kern",
		runner: func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
			workdirs = append(workdirs, workdir)
			return `{"schema_version":2,"findings":[]}`, "", 0, nil
		},
	}
	chk := NewSecretCheck(client)
	root := repoRoot(t)
	if _, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "nested/dir/config.go", Content: "package main\n"}},
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(workdirs) != 1 {
		t.Fatalf("SecScan calls = %d, want 1 (temp dir scan only)", len(workdirs))
	}
	wd := workdirs[0]
	if wd == root {
		t.Fatal("content scan must not run against the repository directory")
	}
	if !strings.Contains(wd, "blueprint-sec-content") {
		t.Errorf("content scan workdir = %q, want a blueprint temp dir", wd)
	}
	// The temp dir must be cleaned up after the scan.
	if _, err := os.Stat(wd); err == nil {
		t.Errorf("temp dir %q was not removed after the scan", wd)
	}
}

// TestSecretCheckContentScanPathEscapeRejected: a proposed path that would
// escape the temp dir ("..") is rejected with a StatusError naming the
// problem, and the scanner is never invoked.
func TestSecretCheckContentScanPathEscapeRejected(t *testing.T) {
	var calls int
	client := &KernClient{
		binaryPath: "kern",
		runner: func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
			calls++
			return `{"schema_version":2,"findings":[]}`, "", 0, nil
		},
	}
	chk := NewSecretCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Files:          []domain.FileChange{{Path: "../escape.go", Content: "package main\n"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
	if !strings.Contains(cr.Error, "invalid path in proposed files") {
		t.Errorf("Error = %q, want mention of invalid proposed path", cr.Error)
	}
	if calls != 0 {
		t.Errorf("scanner invoked %d times, want 0 (rejected before scanning)", calls)
	}
}

// TestSecretCheckContentExcludedFromDiskScan: a file that exists on disk AND
// carries proposed content must not be double-reported — its disk findings are
// excluded from the disk-scan filter, and only the content scan reports it.
func TestSecretCheckContentExcludedFromDiskScan(t *testing.T) {
	root := repoRoot(t)
	// The content file exists on disk too (with a secret on disk), so without
	// the exclusion it would be reported twice.
	if err := writeFileHelper(root, "config.go", "package main\nconst k = \"AKIA-ON-DISK\"\n"); err != nil {
		t.Fatalf("write disk file: %v", err)
	}
	const diskOut = `{"schema_version":2,"findings":[
		{"file":"config.go","line":2,"rule":"hardcoded-secret","severity":"error","message":"hardcoded secret: AWS","snippet":"AKIA-ON-DISK"},
		{"file":"other.go","line":1,"rule":"hardcoded-secret","severity":"error","message":"hardcoded secret: AWS","snippet":"AKIA-OTHER"}
	]}`
	client := &KernClient{
		binaryPath: "kern",
		runner:     fakeRunner(diskOut, "", 1, nil),
	}
	chk := NewSecretCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files: []domain.FileChange{
			{Path: "config.go", Content: "package main\nconst k = \"AKIA-PROPOSED\"\n"},
			{Path: "other.go"}, // disk-only
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusBlock)
	}
	// config.go: exactly one finding (from the content scan, which by this
	// fake reports the same canned path — dedupe collapses the disk finding).
	// other.go: exactly one finding from the disk scan.
	var configFindings, otherFindings int
	for _, f := range cr.Findings {
		switch f.File {
		case "config.go":
			configFindings++
		case "other.go":
			otherFindings++
		}
	}
	if configFindings != 1 {
		t.Errorf("config.go findings = %d, want 1 (no double report)", configFindings)
	}
	if otherFindings != 1 {
		t.Errorf("other.go findings = %d, want 1 (disk scan still reports disk files)", otherFindings)
	}
}

// TestSecretCheckContentAndDiskMerged: findings from the content scan and the
// disk scan are merged into a single result.
func TestSecretCheckContentAndDiskMerged(t *testing.T) {
	root := repoRoot(t)
	if err := writeFileHelper(root, "disk.go", "package main\n"); err != nil {
		t.Fatalf("write disk file: %v", err)
	}
	client := &KernClient{
		binaryPath: "kern",
		runner: func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
			// The content scan (temp dir) reports the proposed file's secret;
			// the disk scan reports the disk file's secret.
			if strings.Contains(workdir, "blueprint-sec-content") {
				return `{"schema_version":2,"findings":[{"file":"content.go","line":2,"rule":"hardcoded-secret","severity":"error","message":"hardcoded secret: AWS","snippet":"AKIA-CONTENT"}]}`, "", 1, nil
			}
			return `{"schema_version":2,"findings":[{"file":"disk.go","line":1,"rule":"hardcoded-secret","severity":"error","message":"hardcoded secret: AWS","snippet":"AKIA-DISK"}]}`, "", 1, nil
		},
	}
	chk := NewSecretCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files: []domain.FileChange{
			{Path: "content.go", Content: "package main\nconst k = \"AKIA-CONTENT\"\n"},
			{Path: "disk.go"},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	files := map[string]bool{}
	for _, f := range cr.Findings {
		files[f.File] = true
	}
	if !files["content.go"] {
		t.Errorf("missing content.go finding (content scan result not merged)")
	}
	if !files["disk.go"] {
		t.Errorf("missing disk.go finding (disk scan result not merged)")
	}
}

// writeFileHelper writes a file for the hermetic secret content tests.
func writeFileHelper(root, rel, content string) error {
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o644)
}
