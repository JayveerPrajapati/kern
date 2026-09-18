package gitleaks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// fakeKernRunner is a commandRunner for the in-house kern client used in
// fallback tests: it answers `version` probes and returns canned `sec`
// output. The findings list is substituted at call time.
func fakeKernRunner(secJSON string) kern.CommandRunner {
	return func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		if len(args) > 0 && args[0] == "version" {
			return "kern dev", "", 0, nil
		}
		if len(args) > 0 && args[0] == "sec" {
			if strings.Contains(secJSON, `"findings":[]`) {
				return secJSON, "", 0, nil
			}
			return secJSON, "", 1, nil
		}
		return `{"schema_version":2}`, "", 0, nil
	}
}

func TestGitleaksCheckEmptyChange(t *testing.T) {
	chk := NewCheck(nil, WithBinary("gitleaks"), WithRunner(fakeGitleaksRunner(gitleaksFindingJSON)))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{RepositoryRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Errorf("Status = %q, want %q (no files => nothing to scan)", cr.Status, domain.StatusPass)
	}
	if len(cr.Findings) != 0 {
		t.Errorf("Findings = %d, want 0", len(cr.Findings))
	}
}

func TestGitleaksCheckMissingRepoRoot(t *testing.T) {
	chk := NewCheck(nil, WithBinary("gitleaks"), WithRunner(fakeGitleaksRunner(gitleaksFindingJSON)))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		Files: []domain.FileChange{{Path: "config.go", Op: domain.OpWrite, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
	if cr.Error != "repository root required" {
		t.Errorf("Error = %q, want repository root required", cr.Error)
	}
}

// TestGitleaksCheckOnDiskFile: files without proposed content are read from
// disk (mirrored into the temp scan dir) and scanned.
func TestGitleaksCheckOnDiskFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.go"), []byte("const AWSKey = \"AKIA1234567890ABCDEF\"\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	chk := NewCheck(nil, WithBinary("gitleaks"), WithRunner(fakeGitleaksRunner(gitleaksFindingJSON)))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "config.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusBlock)
	}
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1", len(cr.Findings))
	}
	if cr.Findings[0].File != "config.go" {
		t.Errorf("File = %q, want config.go", cr.Findings[0].File)
	}
}

// TestGitleaksCheckPathEscapeRejected: a proposed path that escapes the temp
// scan dir must fail the check, never write outside it.
func TestGitleaksCheckPathEscapeRejected(t *testing.T) {
	chk := NewCheck(nil, WithBinary("gitleaks"), WithRunner(fakeGitleaksRunner(`[]`)))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "../../etc/passwd", Op: domain.OpWrite, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
	if !strings.Contains(cr.Error, "invalid path") {
		t.Errorf("Error = %q, want mention of invalid path", cr.Error)
	}
}

// TestGitleaksCheckVersionProbeFailure: a failing `gitleaks version` probe
// must not fail the validation — findings are stamped without a version.
func TestGitleaksCheckVersionProbeFailure(t *testing.T) {
	runner := func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		if len(args) > 0 && args[0] == "version" {
			return "", "gitleaks: version error", 2, nil
		}
		report := strings.ReplaceAll(gitleaksFindingJSON, "{{WORKDIR}}", workdir)
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "--report-path" {
				_ = os.WriteFile(args[i+1], []byte(report), 0o644)
			}
		}
		return "", "", 1, nil
	}
	chk := NewCheck(nil, WithBinary("gitleaks"), WithRunner(runner))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "config.go", Op: domain.OpWrite, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want %q", cr.Status, domain.StatusBlock)
	}
	if cr.Findings[0].RuleVersion != "" {
		t.Errorf("RuleVersion = %q, want empty (probe failed)", cr.Findings[0].RuleVersion)
	}
}

// --- Fallback behavior (incumbent binary absent) ---

func TestGitleaksFallbackInHouseBlocks(t *testing.T) {
	// In-house kern scanner finds a secret => BLOCK must win over the
	// fallback WARN (fail closed, monotonic).
	client, err := kern.NewKernClient(kern.WithBinary("kern"), kern.WithRunner(fakeKernRunner(
		`{"schema_version":2,"findings":[{"file": "main.go", "line": 9, "rule": "hardcoded-secret", "severity": "error", "message": "hardcoded API key detected", "snippet": "sk_live_ABC123"}]}`,
	)))
	if err != nil {
		t.Fatalf("NewKernClient: %v", err)
	}

	// WithBinary("") explicitly disables the incumbent => fallback path.
	chk := NewCheck(client, WithBinary(""))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "main.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Name != "secret:gitleaks" {
		t.Errorf("Name = %q, want secret:gitleaks", cr.Name)
	}
	if cr.Status != domain.StatusBlock {
		t.Errorf("Status = %q, want %q (in-house BLOCK wins over fallback WARN)", cr.Status, domain.StatusBlock)
	}

	var hasFallback bool
	for _, f := range cr.Findings {
		if f.RuleID == "secret:incumbent-unavailable" {
			hasFallback = true
			if f.Severity != domain.SeverityWarn {
				t.Errorf("fallback finding Severity = %q, want %q", f.Severity, domain.SeverityWarn)
			}
			if !strings.Contains(f.Message, "gitleaks not found") {
				t.Errorf("fallback Message = %q, want mention of gitleaks not found", f.Message)
			}
			// Redaction: the in-house snippet must not leak through either.
			blob := f.Message + f.Explanation
			if strings.Contains(blob, "sk_live_ABC123") {
				t.Errorf("fallback finding leaked secret: %q", f.Message)
			}
		}
	}
	if !hasFallback {
		t.Error("missing secret:incumbent-unavailable fallback finding")
	}
}

func TestGitleaksFallbackInHousePassWarns(t *testing.T) {
	client, err := kern.NewKernClient(kern.WithBinary("kern"), kern.WithRunner(fakeKernRunner(
		`{"schema_version":2,"findings":[]}`,
	)))
	if err != nil {
		t.Fatalf("NewKernClient: %v", err)
	}

	chk := NewCheck(client, WithBinary(""))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "main.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusWarn {
		t.Errorf("Status = %q, want %q (clean in-house scan still surfaces the fallback WARN)", cr.Status, domain.StatusWarn)
	}
	if len(cr.Findings) != 1 || cr.Findings[0].RuleID != "secret:incumbent-unavailable" {
		t.Fatalf("Findings = %+v, want exactly the fallback finding", cr.Findings)
	}
}

func TestGitleaksFallbackNoClient(t *testing.T) {
	// No kern client and no gitleaks: still surface the WARN fallback signal
	// (never a silent pass).
	chk := NewCheck(nil, WithBinary(""))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "main.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusWarn {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusWarn)
	}
	if len(cr.Findings) != 1 || cr.Findings[0].RuleID != "secret:incumbent-unavailable" {
		t.Fatalf("Findings = %+v, want exactly the fallback finding", cr.Findings)
	}
}

// TestGitleaksFallbackInHouseErrorPreserved: if the in-house fallback also
// fails, the result stays ERROR (fail closed — never downgrade to a pass).
func TestGitleaksFallbackInHouseErrorPreserved(t *testing.T) {
	runner := func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		return "", "kern: not installed", 2, nil
	}
	client, err := kern.NewKernClient(kern.WithBinary("kern"), kern.WithRunner(runner))
	if err != nil {
		t.Fatalf("NewKernClient: %v", err)
	}
	chk := NewCheck(client, WithBinary(""))
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: t.TempDir(),
		Files:          []domain.FileChange{{Path: "main.go", Op: domain.OpWrite}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Errorf("Status = %q, want %q (in-house ERROR preserved)", cr.Status, domain.StatusError)
	}
}

func TestGitleaksCheckAddedLineFilter(t *testing.T) {
	report := `[
{
"RuleID": "generic-api-key",
"Description": "pre-existing secret on an unchanged line",
"StartLine": 5,
"EndLine": 5,
"StartColumn": 8,
"EndColumn": 38,
"Match": "AWSKey = \"AKIA1234567890ABCDEF\"",
"Secret": "AKIA1234567890ABCDEF",
"File": "{{WORKDIR}}/config.go",
"SymlinkFile": "",
"Commit": "",
"Entropy": 4.08,
"Author": "",
"Email": "",
"Date": "",
"Message": "",
"Tags": [],
"Fingerprint": "f1"
},
{
"RuleID": "github-pat",
"Description": "new token on an added line",
"StartLine": 12,
"EndLine": 12,
"StartColumn": 8,
"EndColumn": 48,
"Match": "tok = \"ghp_16C7e42F292c6912E7710c838347Ae178B4a\"",
"Secret": "ghp_16C7e42F292c6912E7710c838347Ae178B4a",
"File": "{{WORKDIR}}/config.go",
"SymlinkFile": "",
"Commit": "",
"Entropy": 4.08,
"Author": "",
"Email": "",
"Date": "",
"Message": "",
"Tags": [],
"Fingerprint": "f2"
}
]`
	chk := NewCheck(nil, WithBinary("gitleaks"), WithRunner(fakeGitleaksRunner(report)))
	root := t.TempDir()
	// Disk content carries BOTH secrets (line 5 pre-existing, line 12 added).
	if err := os.WriteFile(filepath.Join(root, "config.go"), []byte("line1\nline2\nline3\nline4\nconst AWSKey = \"AKIA1234567890ABCDEF\"\nline6\nline7\nline8\nline9\nline10\nline11\nconst tok = \"ghp_16C7e42F292c6912E7710c838347Ae178B4a\"\n"), 0o644); err != nil {
		t.Fatalf("write config.go: %v", err)
	}
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: root,
		Files: []domain.FileChange{{
			Path:  "config.go",
			Op:    domain.OpEdit,
			Added: []string{"12"}, // only line 12 is a genuinely added line
		}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Fatalf("Status = %q, want %q (the added line carries a secret)", cr.Status, domain.StatusBlock)
	}
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1 (only the added line; the pre-existing line-5 secret must not be attributed to this change)", len(cr.Findings))
	}
	f := cr.Findings[0]
	if f.Line != 12 {
		t.Errorf("Finding.Line = %d, want 12 (the actual added line)", f.Line)
	}
	if !strings.Contains(f.RuleID, "github-pat") {
		t.Errorf("Finding.RuleID = %q, want the github-pat rule of the added line", f.RuleID)
	}
}
