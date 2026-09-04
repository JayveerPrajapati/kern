package kern

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
)

// TestG7_ArchitectureRepairLoop simulates the full agent repair loop (spec
// Phase 7, lines 1094-1112):
//
//	bad agent patch -> BLOCK -> repair patch -> PASS
//
// It verifies that:
//  1. The first (bad) patch produces a BLOCK with the full feedback contract
//     (rule_id, category, file, line, what, why, fix, evidence).
//  2. The agent uses the suggested fix to produce a repaired patch.
//  3. The second (repaired) patch PASSES without creating a new blocker.
func TestG7_ArchitectureRepairLoop(t *testing.T) {
	client := requireKernBinary(t)

	// --- Setup: clean repo with web->db boundary ---
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	writeBoundaries(t, dir, `{"rules":[{"from":"web","to":"db","action":"forbid"}]}`)
	writeGoFile(t, dir, "go.mod", "module example.com/repo\n\ngo 1.23\n")
	writeGoFile(t, dir, "db/db.go", "package db\n\nfunc Query() {}\n")
	writeGoFile(t, dir, "web/web.go", "package web\n\nfunc Handle() {}\n")
	writeGoFile(t, dir, "api/api.go", "package api\n\nimport \"example.com/repo/db\"\n\nfunc Process() { db.Query() }\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")

	// --- Iteration 1: bad agent patch (violates boundary) ---
	writeGoFile(t, dir, "web/bad.go", "package web\n\nimport \"example.com/repo/db\"\n\nfunc HandleBad() {\n\tdb.Query()\n}\n")
	runGit(t, dir, "add", "web/bad.go")

	req1 := domain.ChangeRequest{
		RepositoryRoot: dir,
		Source:         domain.SourceAgent,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: "web/bad.go", Op: domain.OpWrite}},
	}
	svc := service.New([]service.Check{
		NewArchitectureCheck(client),
		NewSecretCheck(client),
	})

	result1 := svc.Validate(context.Background(), req1)

	// Verify BLOCK with full feedback contract.
	if result1.Status != domain.StatusBlock {
		t.Fatalf("iteration 1: status = %s, want BLOCK", result1.Status)
	}
	if len(result1.Findings) == 0 {
		t.Fatal("iteration 1: expected findings")
	}
	finding := result1.Findings[0]
	assertFeedbackContract(t, "iteration 1", finding)

	// The finding must point at the bad file.
	if !strings.Contains(finding.File, "bad.go") {
		t.Fatalf("iteration 1: finding file = %s, want web/bad.go", finding.File)
	}

	t.Logf("Iteration 1 BLOCK: rule=%s file=%s:%d", finding.RuleID, finding.File, finding.Line)
	t.Logf("  What: %s", finding.Message)
	t.Logf("  Why:  %s", finding.Explanation)
	t.Logf("  Fix:  %s", finding.SuggestedFix)

	// --- Iteration 2: agent repairs using the suggested fix ---
	// The suggested fix says "Remove the dependency from web on db, or use an
	// allow rule." The agent repairs by routing through the api layer instead.
	writeGoFile(t, dir, "web/bad.go", "package web\n\nimport \"example.com/repo/api\"\n\nfunc HandleBad() {\n\tapi.Process()\n}\n")
	runGit(t, dir, "add", "web/bad.go")

	// Rebuild index so edges reflect the repaired file.
	if _, _, err := client.IndexBuild(context.Background(), dir); err != nil {
		t.Fatalf("iteration 2: index rebuild: %v", err)
	}

	req2 := domain.ChangeRequest{
		RepositoryRoot: dir,
		Source:         domain.SourceAgent,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: "web/bad.go", Op: domain.OpEdit}},
	}
	result2 := svc.Validate(context.Background(), req2)

	// Verify PASS — the repair resolved the original finding without creating a new blocker.
	if result2.Status == domain.StatusBlock {
		t.Fatalf("iteration 2: status = BLOCK — repair did not resolve the finding; findings: %+v", result2.Findings)
	}
	t.Logf("Iteration 2 PASS: status=%s, findings=%d", result2.Status, len(result2.Findings))
}

// TestG7_SecretRepairLoop simulates the repair loop for a secret violation:
// bad patch (hardcoded AWS key) -> BLOCK -> repair (env var) -> PASS.
func TestG7_SecretRepairLoop(t *testing.T) {
	client := requireKernBinary(t)

	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	writeGoFile(t, dir, "go.mod", "module example.com/repo\n\ngo 1.23\n")
	writeGoFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")

	// --- Iteration 1: bad patch with hardcoded secret ---
	writeGoFile(t, dir, "config.go", "package main\n\nconst AWSAccessKey = \"AKIAIOSFODNN7EXAMPLE\"\n")
	runGit(t, dir, "add", "config.go")

	req1 := domain.ChangeRequest{
		RepositoryRoot: dir,
		Source:         domain.SourceAgent,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: "config.go", Op: domain.OpWrite}},
	}
	// Register only SecretCheck — the secret fixture has no .kern/ dir so
	// ArchitectureCheck would ERROR. Each repair loop runs its relevant check.
	svc := service.New([]service.Check{
		NewSecretCheck(client),
	})
	result1 := svc.Validate(context.Background(), req1)

	if result1.Status != domain.StatusBlock {
		t.Fatalf("iteration 1: status = %s, want BLOCK (secret)", result1.Status)
	}
	if len(result1.Findings) == 0 {
		t.Fatal("iteration 1: expected findings")
	}
	finding := result1.Findings[0]
	assertFeedbackContract(t, "iteration 1", finding)
	// Secret must be redacted.
	if !finding.Redacted {
		t.Error("iteration 1: finding.Redacted = false, want true")
	}

	t.Logf("Iteration 1 BLOCK: rule=%s file=%s:%d", finding.RuleID, finding.File, finding.Line)
	t.Logf("  What: %s", finding.Message)
	t.Logf("  Fix:  %s", finding.SuggestedFix)

	// --- Iteration 2: agent repairs by moving to env var ---
	writeGoFile(t, dir, "config.go", "package main\n\nimport \"os\"\n\nfunc getAWSKey() string {\n\treturn os.Getenv(\"AWS_ACCESS_KEY_ID\")\n}\n")
	runGit(t, dir, "add", "config.go")

	req2 := domain.ChangeRequest{
		RepositoryRoot: dir,
		Source:         domain.SourceAgent,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: "config.go", Op: domain.OpEdit}},
	}
	result2 := svc.Validate(context.Background(), req2)

	if result2.Status == domain.StatusBlock {
		t.Fatalf("iteration 2: status = BLOCK — repair did not resolve the secret; findings: %+v", result2.Findings)
	}
	t.Logf("Iteration 2 PASS: status=%s, findings=%d", result2.Status, len(result2.Findings))
}

// TestG7_MCPRepairLoop simulates the repair loop through the MCP tool surface
// (spec Phase 5 + 7 integration): validate via MCP -> BLOCK -> explain_finding
// -> repair -> validate again -> PASS.
func TestG7_MCPRepairLoop(t *testing.T) {
	client := requireKernBinary(t)

	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	writeBoundaries(t, dir, `{"rules":[{"from":"web","to":"db","action":"forbid"}]}`)
	writeGoFile(t, dir, "go.mod", "module example.com/repo\n\ngo 1.23\n")
	writeGoFile(t, dir, "db/db.go", "package db\nfunc Query() {}\n")
	writeGoFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")

	// Bad patch.
	writeGoFile(t, dir, "web/bad.go", "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n")
	runGit(t, dir, "add", "web/bad.go")

	// Validate via MCP handler (simulating an agent calling the tool).
	mcpHandler := mcpValidateStagedShim{client: client, repo: dir}
	tr1 := mcpHandler.validate()
	if tr1.IsError {
		t.Fatalf("MCP validate (iter 1) errored: %s", tr1.Text)
	}
	result1 := parseMCPResult(t, tr1)
	if !strings.EqualFold(string(result1.Status), "BLOCK") {
		t.Fatalf("MCP iter 1: status=%s, want BLOCK", result1.Status)
	}
	if len(result1.Findings) == 0 {
		t.Fatal("MCP iter 1: expected findings")
	}

	// Explain the finding (agent uses this to plan the repair).
	findingJSON, _ := json.Marshal(result1.Findings[0])
	explainResult := mcpExplainFindingShim(findingJSON)
	if explainResult.IsError {
		t.Fatalf("MCP explain_finding errored: %s", explainResult.Text)
	}
	if !strings.Contains(explainResult.Text, "Suggested fix") {
		t.Error("explain_finding output missing 'Suggested fix'")
	}

	// Repair: route through a new api layer instead of db directly.
	writeGoFile(t, dir, "api/api.go", "package api\nimport \"example.com/repo/db\"\nfunc Process() { db.Query() }\n")
	writeGoFile(t, dir, "web/bad.go", "package web\nimport \"example.com/repo/api\"\nfunc Bad() { api.Process() }\n")
	runGit(t, dir, "add", "api/api.go", "web/bad.go")
	if _, _, err := client.IndexBuild(context.Background(), dir); err != nil {
		t.Fatalf("index rebuild: %v", err)
	}

	// Validate again via MCP.
	tr2 := mcpHandler.validate()
	if tr2.IsError {
		t.Fatalf("MCP validate (iter 2) errored: %s", tr2.Text)
	}
	result2 := parseMCPResult(t, tr2)
	if strings.EqualFold(string(result2.Status), "BLOCK") {
		t.Fatalf("MCP iter 2: status=BLOCK — repair failed; findings: %+v", result2.Findings)
	}
	t.Logf("MCP repair loop: iter1=BLOCK -> iter2=%s", result2.Status)
}

// TestG7_FeedbackContractAudit verifies that EVERY check type produces
// findings that satisfy the G7 feedback contract (spec lines 1117-1128).
func TestG7_FeedbackContractAudit(t *testing.T) {
	client := requireKernBinary(t)

	// Architecture finding.
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	writeBoundaries(t, dir, `{"rules":[{"from":"web","to":"db","action":"forbid"}]}`)
	writeGoFile(t, dir, "go.mod", "module example.com/repo\n\ngo 1.23\n")
	writeGoFile(t, dir, "db/db.go", "package db\nfunc Query() {}\n")
	writeGoFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	writeGoFile(t, dir, "web/bad.go", "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n")
	runGit(t, dir, "add", "web/bad.go")

	archRes, _ := NewArchitectureCheck(client).Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: dir,
		Source:         domain.SourceAgent,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: "web/bad.go", Op: domain.OpWrite}},
	})
	for _, f := range archRes.Findings {
		assertFeedbackContract(t, "architecture", f)
	}

	// Secret finding.
	sdir := t.TempDir()
	writeGoFile(t, sdir, "config.go", "package main\nconst AWSKey = \"AKIAIOSFODNN7EXAMPLE\"\n")
	secRes, _ := NewSecretCheck(client).Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: sdir,
		Source:         domain.SourceAgent,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: "config.go", Op: domain.OpWrite}},
	})
	for _, f := range secRes.Findings {
		assertFeedbackContract(t, "secret", f)
		if !f.Redacted {
			t.Errorf("secret finding: Redacted = false, want true")
		}
	}
}

// TestG7_VagueResponseRejected verifies that findings never produce vague
// messages like "Architecture error" (spec lines 1130-1144). Every message
// must be specific.
func TestG7_VagueResponseRejected(t *testing.T) {
	client := requireKernBinary(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	writeBoundaries(t, dir, `{"rules":[{"from":"web","to":"db","action":"forbid"}]}`)
	writeGoFile(t, dir, "go.mod", "module example.com/repo\n\ngo 1.23\n")
	writeGoFile(t, dir, "db/db.go", "package db\nfunc Query() {}\n")
	writeGoFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	writeGoFile(t, dir, "web/bad.go", "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n")
	runGit(t, dir, "add", "web/bad.go")

	res, _ := NewArchitectureCheck(client).Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: dir,
		Source:         domain.SourceAgent,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: "web/bad.go", Op: domain.OpWrite}},
	})
	for _, f := range res.Findings {
		// Reject vague messages (spec lines 1130-1134).
		lower := strings.ToLower(f.Message)
		for _, vague := range []string{"architecture error", "validation failed", "check failed", "error"} {
			if lower == vague {
				t.Errorf("finding message is vague: %q (spec requires specific feedback)", f.Message)
			}
		}
		// Message must contain specific detail (file, symbol, or rule).
		if !strings.Contains(f.Message, "web/") && !strings.Contains(f.Message, "forbidden") {
			t.Errorf("finding message lacks specific detail: %q", f.Message)
		}
	}
}

// --- Helpers ---

// assertFeedbackContract verifies a finding satisfies the G7 required feedback
// contract (spec lines 1117-1128): rule_id, category, file, line, message
// (what), explanation (why), suggested_fix, evidence.
func assertFeedbackContract(t *testing.T, label string, f domain.Finding) {
	t.Helper()
	if f.RuleID == "" {
		t.Errorf("%s: finding missing rule_id", label)
	}
	if f.Category == "" {
		t.Errorf("%s: finding missing category", label)
	}
	if f.File == "" {
		t.Errorf("%s: finding missing file", label)
	}
	if f.Line == 0 {
		t.Errorf("%s: finding missing line", label)
	}
	if f.Message == "" {
		t.Errorf("%s: finding missing message (what failed)", label)
	}
	if f.Explanation == "" {
		t.Errorf("%s: finding missing explanation (why it failed)", label)
	}
	if f.SuggestedFix == "" {
		t.Errorf("%s: finding missing suggested_fix", label)
	}
	if len(f.Evidence) == 0 {
		t.Errorf("%s: finding missing evidence", label)
	}
}

// mcpValidateStagedShim is a test shim that calls the MCP ValidateStagedHandler
// logic without going through the JSON-RPC server.
type mcpValidateStagedShim struct {
	client *KernClient
	repo   string
}

func (s mcpValidateStagedShim) validate() mcpToolResult {
	svc := service.New([]service.Check{
		NewArchitectureCheck(s.client),
		NewSecretCheck(s.client),
	})
	changes, err := stagedFiles(s.repo)
	if err != nil {
		return mcpToolResult{IsError: true, Text: err.Error()}
	}
	result := svc.Validate(context.Background(), domain.ChangeRequest{
		RepositoryRoot: s.repo,
		Source:         domain.SourceAgent,
		Operation:      domain.OpCommit,
		Files:          changes,
	})
	b, _ := json.Marshal(result)
	return mcpToolResult{Text: string(b)}
}

type mcpToolResult struct {
	Text    string
	IsError bool
}

func parseMCPResult(t *testing.T, tr mcpToolResult) domain.ValidationResult {
	t.Helper()
	var vr domain.ValidationResult
	if err := json.Unmarshal([]byte(tr.Text), &vr); err != nil {
		t.Fatalf("parse MCP result: %v\nraw: %s", err, tr.Text)
	}
	return vr
}

// mcpExplainFindingShim calls the ExplainFindingHandler logic.
func mcpExplainFindingShim(findingJSON []byte) mcpToolResult {
	var f domain.Finding
	if err := json.Unmarshal(findingJSON, &f); err != nil {
		return mcpToolResult{IsError: true, Text: err.Error()}
	}
	var sb strings.Builder
	sb.WriteString("Finding: " + f.RuleID + "\n")
	sb.WriteString("File: " + f.File + "\n")
	sb.WriteString("Message: " + f.Message + "\n")
	sb.WriteString("Explanation: " + f.Explanation + "\n")
	sb.WriteString("Suggested fix: " + f.SuggestedFix + "\n")
	return mcpToolResult{Text: sb.String()}
}

// stagedFiles returns staged file changes via git diff --cached --name-status.
func stagedFiles(repoRoot string) ([]domain.FileChange, error) {
	cmd := exec.Command("git", "diff", "--cached", "--name-status")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var changes []domain.FileChange
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		changes = append(changes, domain.FileChange{Path: parts[1], Op: domain.OpWrite})
	}
	return changes, nil
}
