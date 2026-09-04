package loop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	blueprintdomain "github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// mockValidator implements FirewallValidator for deterministic testing.
type mockValidator struct {
	validateFunc func(ctx context.Context, req blueprintdomain.ChangeRequest) blueprintdomain.ValidationResult
}

func (m *mockValidator) Validate(ctx context.Context, req blueprintdomain.ChangeRequest) blueprintdomain.ValidationResult {
	if m.validateFunc != nil {
		return m.validateFunc(ctx, req)
	}
	return blueprintdomain.ValidationResult{Status: blueprintdomain.StatusPass}
}

func TestGovernedLoopAutoRepairBoundaryViolation(t *testing.T) {
	tmp := t.TempDir()
	authFile := filepath.Join(tmp, "internal", "auth", "middleware.go")

	attempts := 0
	validator := &mockValidator{
		validateFunc: func(ctx context.Context, req blueprintdomain.ChangeRequest) blueprintdomain.ValidationResult {
			attempts++
			for _, f := range req.Files {
				if f.Path == filepath.Join("internal", "auth", "middleware.go") || strings.HasSuffix(f.Path, "middleware.go") {
					if strings.Contains(f.Content, "internal/database") {
						return blueprintdomain.ValidationResult{
							Status: blueprintdomain.StatusBlock,
							Findings: []blueprintdomain.Finding{
								{
									RuleID:       "disallow_db_in_handlers",
									Category:     blueprintdomain.CategoryArchitecture,
									Severity:     blueprintdomain.SeverityBlock,
									File:         "internal/auth/middleware.go",
									Line:         42,
									Message:      "package auth cannot import internal/database directly",
									SuggestedFix: "use interface auth.UserRepository instead of database.Client",
								},
							},
						}
					}
				}
			}
			return blueprintdomain.ValidationResult{
				Status: blueprintdomain.StatusPass,
			}
		},
	}

	adapter := NewFirewallAdapter(validator, tmp)
	lp, err := NewLoop(LoopConfig{
		Root:              tmp,
		Level:             L3,
		Mem:               memory.NewMemoryStore(t.TempDir()),
		Firewall:          adapter,
		MaxRepairAttempts: 3,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	repairCalled := false
	res, err := lp.Run("implement auth middleware", func(stage, intent string, wt *execution.Worktree, r *Result) (string, error) {
		switch stage {
		case stagePlan:
			return "plan: auth middleware", nil
		case stageCode:
			// Deliberate boundary violation on initial generation:
			targetDir := filepath.Join(wt.Dir(), "internal", "auth")
			_ = os.MkdirAll(targetDir, 0o755)
			badCode := "package auth\n\nimport \"internal/database\"\n\nfunc Auth() {}\n"
			if err := os.WriteFile(filepath.Join(targetDir, "middleware.go"), []byte(badCode), 0o644); err != nil {
				return "", err
			}
			return "wrote auth middleware with db import", nil
		case "repair":
			repairCalled = true
			if !strings.Contains(intent, "G2_BOUNDARIES") {
				t.Fatalf("expected repair prompt to mention G2_BOUNDARIES, got: %s", intent)
			}
			if !strings.Contains(intent, "use interface auth.UserRepository") {
				t.Fatalf("expected repair prompt to include suggested fix, got: %s", intent)
			}
			// Apply the auto-repair fix: remove forbidden import
			fixedCode := "package auth\n\ntype UserRepository interface{}\n\nfunc Auth() {}\n"
			targetDir := filepath.Join(wt.Dir(), "internal", "auth")
			if err := os.WriteFile(filepath.Join(targetDir, "middleware.go"), []byte(fixedCode), 0o644); err != nil {
				return "", err
			}
			return "repaired auth middleware: replaced direct database import with interface", nil
		}
		return "", nil
	})

	if err != nil {
		t.Fatalf("Loop.Run failed: %v", err)
	}
	if !repairCalled {
		t.Fatal("expected repair step to be called")
	}
	if attempts != 3 {
		t.Fatalf("expected 3 validation attempts (2 in repair loop, 1 in stageVerify), got %d", attempts)
	}
	if res.RepairAttempts != 1 {
		t.Fatalf("expected 1 repair attempt recorded, got %d", res.RepairAttempts)
	}

	// Verify the stages completed successfully
	for _, s := range res.Stages {
		if s.Status == "error" {
			t.Fatalf("stage %s failed with output: %s", s.Stage, s.Output)
		}
	}
	_ = authFile
}

func TestGovernedLoopAutoRepairExhausted(t *testing.T) {
	tmp := t.TempDir()

	validator := &mockValidator{
		validateFunc: func(ctx context.Context, req blueprintdomain.ChangeRequest) blueprintdomain.ValidationResult {
			return blueprintdomain.ValidationResult{
				Status: blueprintdomain.StatusBlock,
				Findings: []blueprintdomain.Finding{
					{
						RuleID:   "secret:raw_key",
						Category: blueprintdomain.CategorySecret,
						Severity: blueprintdomain.SeverityBlock,
						File:     "api/key.go",
						Line:     10,
						Message:  "hardcoded secret detected",
					},
				},
			}
		},
	}

	adapter := NewFirewallAdapter(validator, tmp)
	lp, err := NewLoop(LoopConfig{
		Root:              tmp,
		Level:             L3,
		Firewall:          adapter,
		MaxRepairAttempts: 2,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	repairs := 0
	res, err := lp.Run("write api key", func(stage, intent string, wt *execution.Worktree, r *Result) (string, error) {
		switch stage {
		case stageCode:
			p := filepath.Join(wt.Dir(), "key.go")
			_ = os.WriteFile(p, []byte("package api\nconst key = \"123\"\n"), 0o644)
			return "wrote key", nil
		case "repair":
			repairs++
			return "tried repair", nil
		}
		return "", nil
	})

	if err == nil {
		t.Fatal("expected loop to fail closed after repair attempts exhausted")
	}
	if !strings.Contains(err.Error(), "blocked by gate G1_SECRETS") {
		t.Fatalf("expected error to cite G1_SECRETS, got: %v", err)
	}
	if repairs != 1 {
		t.Fatalf("expected 1 repair invocation before reaching max attempts (2), got %d", repairs)
	}
	_ = res
}

func TestRepairContractFormatting(t *testing.T) {
	contracts := []RepairContract{
		{
			TaskID:       "task-1",
			Iteration:    1,
			GateID:       "G2_BOUNDARIES",
			CheckType:    "boundary",
			IsActionable: true,
			TargetFile:   "internal/auth/middleware.go",
			LineNumber:   42,
			RuleName:     "disallow_db_in_handlers",
			RawMessage:   "package auth cannot import internal/database directly",
			SuggestedFix: "use interface auth.UserRepository instead of database.Client",
			ContextSlice: "import \"internal/database\"",
		},
	}

	prompt := FormatRepairPrompt(contracts)
	if !strings.Contains(prompt, "[G2_BOUNDARIES - disallow_db_in_handlers]") {
		t.Errorf("prompt missing gate and rule: %s", prompt)
	}
	if !strings.Contains(prompt, "internal/auth/middleware.go:42") {
		t.Errorf("prompt missing file:line: %s", prompt)
	}
	if !strings.Contains(prompt, "use interface auth.UserRepository") {
		t.Errorf("prompt missing suggested fix: %s", prompt)
	}

	jsonStr, err := FormatRepairContractsJSON(contracts)
	if err != nil {
		t.Fatalf("FormatRepairContractsJSON: %v", err)
	}
	if !strings.Contains(jsonStr, `"gate_id": "G2_BOUNDARIES"`) {
		t.Fatalf("json missing gate_id: %s", jsonStr)
	}
}

func TestGovernedLoopProtectApprovalGate(t *testing.T) {
	tmp := t.TempDir()

	validator := &mockValidator{
		validateFunc: func(ctx context.Context, req blueprintdomain.ChangeRequest) blueprintdomain.ValidationResult {
			return blueprintdomain.ValidationResult{
				Status: blueprintdomain.StatusBlock,
				Findings: []blueprintdomain.Finding{
					{
						RuleID:   "approval:gate",
						Category: blueprintdomain.CategoryApproval,
						Severity: blueprintdomain.SeverityBlock,
						File:     "billing/charge.go",
						Message:  "high-risk financial mutation requires two-person approval",
					},
				},
			}
		},
	}

	adapter := NewFirewallAdapter(validator, tmp)
	lp, err := NewLoop(LoopConfig{
		Root:     tmp,
		Level:    L4, // Protect stage is active at L4
		Mem:      memory.NewMemoryStore(t.TempDir()),
		Firewall: adapter,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	res, err := lp.Run("charge user", func(stage, intent string, wt *execution.Worktree, r *Result) (string, error) {
		if stage == stageCode {
			p := filepath.Join(wt.Dir(), "charge.go")
			_ = os.WriteFile(p, []byte("package billing\nfunc Charge() {}\n"), 0o644)
		}
		return "", nil
	})

	if err == nil {
		t.Fatal("expected error due to unapproved high-risk change")
	}
	if !res.Paused || res.PauseReason != "approval" {
		t.Fatalf("expected loop to pause with reason 'approval', got paused=%v reason=%s", res.Paused, res.PauseReason)
	}
}
