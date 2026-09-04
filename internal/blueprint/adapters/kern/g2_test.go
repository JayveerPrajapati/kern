package kern

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// requireKernBinary skips the test if the kern binary isn't available. G2
// tests exercise the real kern binary against fixture repos.
func requireKernBinary(t *testing.T) *KernClient {
	t.Helper()
	client, err := NewKernClient()
	if err != nil {
		t.Skipf("kern binary not available, skipping integration test: %v", err)
	}
	return client
}

// requireFingerprintBinary skips unless the installed kern binary supports the
// `fingerprint` subcommand (blueprint's duplication oracle). The full-CLI test
// runs `blueprint check`, which now includes the duplication check backed by
// `kern fingerprint`; until the subcommand ships in the kern release this test
// skips so the suite stays green pre-integration.
func requireFingerprintBinary(t *testing.T) {
	t.Helper()
	if _, err := NewKernClient(); err != nil {
		t.Skipf("kern binary not available, skipping integration test: %v", err)
	}
	bin, err := resolveKernBinary()
	if err != nil {
		t.Skipf("kern binary not available: %v", err)
	}
	out, err := exec.Command(bin, "--help").CombinedOutput()
	if err != nil {
		t.Skipf("kern --help failed (%v); skipping fingerprint-backed assertions", err)
	}
	if !strings.Contains(string(out), "fingerprint") {
		t.Skipf("installed kern does not support `kern fingerprint` (needs the P1-3 kern release); skipping fingerprint-backed assertions")
	}
}

// changeReq builds a minimal ChangeRequest for a fixture, deriving staged
// file paths from the fixture result.
func changeReq(t *testing.T, fr FixtureResult) domain.ChangeRequest {
	t.Helper()
	files := make([]domain.FileChange, 0, len(fr.StagedFiles))
	for _, p := range fr.StagedFiles {
		op := domain.OpEdit
		if _, err := os.Stat(filepath.Join(fr.RepoPath, p)); os.IsNotExist(err) {
			op = domain.OpDelete
		}
		files = append(files, domain.FileChange{Path: p, Op: op})
	}
	return domain.ChangeRequest{
		RepositoryRoot: fr.RepoPath,
		Source:         domain.SourceCI,
		Operation:      domain.OpCommit,
		Files:          files,
	}
}

// TestG2_ArchitectureClean verifies a clean architecture with no violations
// passes the architecture check.
func TestG2_ArchitectureClean(t *testing.T) {
	client := requireKernBinary(t)
	fr := ArchitectureClean(t)
	req := changeReq(t, fr)
	check := NewArchitectureCheck(client)
	res, err := check.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS; findings: %+v", res.Status, res.Findings)
	}
}

// TestG2_LegalDependency verifies a legal cross-boundary dependency (web -> api
// -> db, where only web -> db is forbidden) passes.
func TestG2_LegalDependency(t *testing.T) {
	client := requireKernBinary(t)
	fr := LegalDependency(t)
	req := changeReq(t, fr)
	check := NewArchitectureCheck(client)
	res, err := check.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS; findings: %+v", res.Status, res.Findings)
	}
}

// TestG2_IllegalDependency verifies a newly-introduced illegal dependency
// (new file importing a forbidden package) is blocked.
func TestG2_IllegalDependency(t *testing.T) {
	client := requireKernBinary(t)
	fr := IllegalDependency(t)
	req := changeReq(t, fr)
	check := NewArchitectureCheck(client)
	res, err := check.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("status = %s, want BLOCK", res.Status)
	}
	if len(res.Findings) == 0 {
		t.Fatal("expected at least 1 finding for illegal dependency")
	}
	// Verify the finding points at the new file.
	found := false
	for _, f := range res.Findings {
		if strings.Contains(f.File, "web2.go") || strings.Contains(f.File, "web/") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no finding references the new web file; got: %+v", res.Findings)
	}
}

// TestG2_MultipleViolations verifies a change introducing 2+ violations
// reports all of them and blocks.
func TestG2_MultipleViolations(t *testing.T) {
	client := requireKernBinary(t)
	fr := MultipleViolations(t)
	req := changeReq(t, fr)
	check := NewArchitectureCheck(client)
	res, err := check.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("status = %s, want BLOCK", res.Status)
	}
	// Should have at least 2 findings (one per violating file).
	if len(res.Findings) < 2 {
		t.Fatalf("expected >=2 findings, got %d: %+v", len(res.Findings), res.Findings)
	}
}

// TestG2_PreexistingViolationUnrelatedChange is the KEY new-change-principle
// test: a pre-existing violation in the base commit must NOT block an
// unrelated clean staged change.
func TestG2_PreexistingViolationUnrelatedChange(t *testing.T) {
	client := requireKernBinary(t)
	fr := PreexistingViolationUnrelatedChange(t)
	req := changeReq(t, fr)
	check := NewArchitectureCheck(client) // new-change mode (default)
	res, err := check.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS (pre-existing violation must not block unrelated clean change); findings: %+v",
			res.Status, res.Findings)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected 0 findings (new-change principle), got %d: %+v", len(res.Findings), res.Findings)
	}
}

// TestG2_PreexistingViolation_StrictBaseline verifies that strict-baseline
// mode DOES report the pre-existing violation.
func TestG2_PreexistingViolation_StrictBaseline(t *testing.T) {
	client := requireKernBinary(t)
	fr := PreexistingViolationUnrelatedChange(t)
	req := changeReq(t, fr)
	check := NewArchitectureCheckStrict(client) // strict mode
	res, err := check.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("strict mode: status = %s, want BLOCK (pre-existing violation must be reported)", res.Status)
	}
	if len(res.Findings) == 0 {
		t.Fatal("strict mode: expected at least 1 finding for pre-existing violation")
	}
}

// TestG2_RenameScenario verifies a file rename with no new violation passes.
func TestG2_RenameScenario(t *testing.T) {
	client := requireKernBinary(t)
	fr := RenameScenario(t)
	req := changeReq(t, fr)
	check := NewArchitectureCheck(client)
	res, err := check.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS; findings: %+v", res.Status, res.Findings)
	}
}

// TestG2_NewFileScenario verifies a newly-added clean file passes.
func TestG2_NewFileScenario(t *testing.T) {
	client := requireKernBinary(t)
	fr := NewFileScenario(t)
	req := changeReq(t, fr)
	check := NewArchitectureCheck(client)
	res, err := check.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS; findings: %+v", res.Status, res.Findings)
	}
}

// TestG2_DeletedImportScenario verifies that a change which REMOVES an illegal
// import (improving the codebase) passes — no new violations introduced.
func TestG2_DeletedImportScenario(t *testing.T) {
	client := requireKernBinary(t)
	fr := DeletedImportScenario(t)
	req := changeReq(t, fr)
	check := NewArchitectureCheck(client)
	res, err := check.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS (removing a violation is not a new violation); findings: %+v",
			res.Status, res.Findings)
	}
}

// TestG2_CLI_EndToEnd verifies the full CLI path: `blueprint check --json`
// against the IllegalDependency fixture produces a BLOCK result with correct
// JSON structure.
func TestG2_CLI_EndToEnd(t *testing.T) {
	client := requireKernBinary(t)
	_ = client // just ensuring kern is available
	requireFingerprintBinary(t)
	fr := IllegalDependency(t)

	// Build the blueprint binary once (cached).
	binPath := buildBlueprintBinary(t)

	// Run `blueprint check --json --repo <fixture> --source ci`.
	// KERN_BINARY is inherited from the test environment.
	out, code := runBlueprintCheck(t, binPath, fr.RepoPath, "--json")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (BLOCK); output:\n%s", code, out)
	}
	if !strings.Contains(out, "BLOCK") {
		t.Fatalf("output missing BLOCK status:\n%s", out)
	}
	if !strings.Contains(out, "architecture:boundary-violation") {
		t.Fatalf("output missing architecture:boundary-violation rule:\n%s", out)
	}
}
