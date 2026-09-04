package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/gitleaks"
	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/jscpd"
	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	blueprintdomain "github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/policy"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
	"github.com/JayveerPrajapati/kern/internal/execution"
)

// RepairContract represents a machine-actionable repair contract for an agent (KernOps spec Section 6.1).
// When a Blueprint gate check fails (VerdictBlock), findings are mapped into
// RepairContracts so the agent can deterministically fix the issue without hallucinating.
type RepairContract struct {
	TaskID       string `json:"task_id"`
	Iteration    int    `json:"iteration"`
	GateID       string `json:"gate_id"`       // e.g. "G1_SECRETS", "G2_BOUNDARIES", "G6_DUPLICATION", "G8_SANDBOX_TESTS", "G24_APPROVAL"
	CheckType    string `json:"check_type"`    // e.g. "boundary", "secret", "duplication", "tests", "approval"
	IsActionable bool   `json:"is_actionable"` // true
	TargetFile   string `json:"target_file"`   // e.g. "internal/auth/middleware.go"
	LineNumber   int    `json:"line_number"`   // e.g. 42
	RuleName     string `json:"rule_name"`     // e.g. "disallow_db_in_handlers"
	RawMessage   string `json:"raw_message"`   // Human readable error
	SuggestedFix string `json:"suggested_fix"` // Contextual advice
	ContextSlice string `json:"context_slice"` // AST-minimal code slice or evidence
}

// FindingToRepairContract maps a single Blueprint finding to an actionable RepairContract.
func FindingToRepairContract(taskID string, iteration int, f blueprintdomain.Finding) RepairContract {
	return RepairContract{
		TaskID:       taskID,
		Iteration:    iteration,
		GateID:       gateIDFromCategory(f.Category, f.RuleID),
		CheckType:    checkTypeFromCategory(f.Category),
		IsActionable: true,
		TargetFile:   f.File,
		LineNumber:   f.Line,
		RuleName:     f.RuleID,
		RawMessage:   f.Message,
		SuggestedFix: f.SuggestedFix,
		ContextSlice: formatEvidence(f.Evidence),
	}
}

// FindingsToRepairContracts maps all blocking/actionable findings into RepairContracts.
func FindingsToRepairContracts(taskID string, iteration int, findings []blueprintdomain.Finding) []RepairContract {
	var contracts []RepairContract
	for _, f := range findings {
		if f.Severity == blueprintdomain.SeverityBlock || f.Severity == blueprintdomain.SeverityError {
			contracts = append(contracts, FindingToRepairContract(taskID, iteration, f))
		}
	}
	return contracts
}

// FormatRepairPrompt converts repair contracts into an actionable prompt for coding agents.
func FormatRepairPrompt(contracts []RepairContract) string {
	if len(contracts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("The proposed code triggered Blueprint firewall violations. Please fix the following issues:\n\n")
	for i, c := range contracts {
		fmt.Fprintf(&b, "Issue #%d [%s - %s]:\n", i+1, c.GateID, c.RuleName)
		if c.TargetFile != "" {
			if c.LineNumber > 0 {
				fmt.Fprintf(&b, "  File: %s:%d\n", c.TargetFile, c.LineNumber)
			} else {
				fmt.Fprintf(&b, "  File: %s\n", c.TargetFile)
			}
		}
		if c.RawMessage != "" {
			fmt.Fprintf(&b, "  Violation: %s\n", c.RawMessage)
		}
		if c.SuggestedFix != "" {
			fmt.Fprintf(&b, "  Suggested Fix: %s\n", c.SuggestedFix)
		}
		if c.ContextSlice != "" {
			fmt.Fprintf(&b, "  Evidence: %s\n", c.ContextSlice)
		}
		b.WriteString("\n")
	}
	b.WriteString("Modify the affected files to resolve all violations and ensure all gates pass.")
	return b.String()
}

// FormatRepairContractsJSON marshals contracts into indented JSON for audit or tool consumption.
func FormatRepairContractsJSON(contracts []RepairContract) (string, error) {
	bytes, err := json.MarshalIndent(contracts, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func gateIDFromCategory(cat blueprintdomain.Category, ruleID string) string {
	switch cat {
	case blueprintdomain.CategorySecret:
		return "G1_SECRETS"
	case blueprintdomain.CategoryArchitecture:
		return "G2_BOUNDARIES"
	case blueprintdomain.CategoryDuplication:
		return "G6_DUPLICATION"
	case blueprintdomain.CategoryTests, blueprintdomain.CategoryBuild:
		return "G8_SANDBOX_TESTS"
	case blueprintdomain.CategoryResilience:
		return "G9_RESILIENCE"
	case blueprintdomain.CategoryApproval:
		return "G24_APPROVAL"
	default:
		return "G0_FIREWALL"
	}
}

func checkTypeFromCategory(cat blueprintdomain.Category) string {
	switch cat {
	case blueprintdomain.CategoryArchitecture:
		return "boundary"
	case blueprintdomain.CategorySecret:
		return "secret"
	case blueprintdomain.CategoryDuplication:
		return "duplication"
	case blueprintdomain.CategoryTests:
		return "tests"
	case blueprintdomain.CategoryBuild:
		return "build"
	case blueprintdomain.CategoryResilience:
		return "resilience"
	case blueprintdomain.CategoryApproval:
		return "approval"
	default:
		return string(cat)
	}
}

func formatEvidence(ev []blueprintdomain.Evidence) string {
	if len(ev) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ev))
	for _, e := range ev {
		if e.Location != "" {
			parts = append(parts, fmt.Sprintf("%s (%s: %s)", e.Description, e.Kind, e.Location))
		} else {
			parts = append(parts, fmt.Sprintf("%s (%s)", e.Description, e.Kind))
		}
	}
	return strings.Join(parts, "; ")
}

// ProtectStage defines the hook for the protect governance gate.
type ProtectStage interface {
	Protect(ctx context.Context, intent string, wt *execution.Worktree) (string, error)
}

// VerifyStage defines the verification engine interface.
type VerifyStage interface {
	Verify(ctx context.Context, intent string, wt *execution.Worktree) (string, error)
}

// FirewallValidator is the contract satisfied by Blueprint's canonical validation pipeline.
type FirewallValidator interface {
	Validate(ctx context.Context, req blueprintdomain.ChangeRequest) blueprintdomain.ValidationResult
}

// FirewallAdapter unifies Kern's autonomous loop with Blueprint's canonical validation pipeline (Phase 1).
type FirewallAdapter struct {
	validator FirewallValidator
	repoRoot  string
}

// NewFirewallAdapter constructs a FirewallAdapter backed by the given validator.
func NewFirewallAdapter(validator FirewallValidator, repoRoot string) *FirewallAdapter {
	return &FirewallAdapter{
		validator: validator,
		repoRoot:  repoRoot,
	}
}

// NewDefaultFirewallAdapter creates a ready-to-use FirewallAdapter loading policy from repoRoot.
func NewDefaultFirewallAdapter(repoRoot string) (*FirewallAdapter, error) {
	loadedCfg, err := policy.Load(repoRoot)
	if err != nil || loadedCfg == nil {
		loadedCfg = policy.DefaultConfig()
	}

	client, _ := kern.NewKernClient()
	kernVersion := ""
	if client != nil {
		kernVersion, _ = client.Version()
	}
	checks := []service.Check{
		kern.NewArchitectureCheck(client),
		gitleaks.NewCheck(client),
		jscpd.NewCheck(client),
	}

	opts := []service.Option{
		service.WithConfig(loadedCfg.Service),
		service.WithPolicy(policy.NewEngine(loadedCfg.Policy)),
		service.WithKernVersion(kernVersion),
	}

	svc := service.New(checks, opts...)
	return NewFirewallAdapter(svc, repoRoot), nil
}

// Validator returns the underlying FirewallValidator.
func (a *FirewallAdapter) Validator() FirewallValidator {
	return a.validator
}

// ValidateWorktree runs the Blueprint validation pipeline against the worktree state.
func (a *FirewallAdapter) ValidateWorktree(ctx context.Context, wt *execution.Worktree, taskID, intent string, iteration int) (blueprintdomain.ValidationResult, []RepairContract, error) {
	if a.validator == nil {
		return blueprintdomain.ValidationResult{Status: blueprintdomain.StatusPass}, nil, nil
	}

	changes, err := a.ExtractChanges(wt)
	if err != nil {
		return blueprintdomain.ValidationResult{Status: blueprintdomain.StatusError}, nil, err
	}

	req := blueprintdomain.ChangeRequest{
		RepositoryRoot: wt.Dir(),
		Source:         blueprintdomain.SourceAgent,
		Operation:      blueprintdomain.OpCommit,
		Files:          changes,
		AgentID:        "kernops-agent",
		Metadata: map[string]string{
			"task":    taskID,
			"intent":  intent,
			"task-id": taskID,
		},
	}

	result := a.validator.Validate(ctx, req)
	contracts := FindingsToRepairContracts(taskID, iteration, result.Findings)
	return result, contracts, nil
}

// Verify implements VerifyStage by invoking the Blueprint firewall for code verification (G0-G23).
func (a *FirewallAdapter) Verify(ctx context.Context, intent string, wt *execution.Worktree) (string, error) {
	res, contracts, err := a.ValidateWorktree(ctx, wt, intent, intent, 1)
	if err != nil {
		return "", err
	}
	// Filter out G24_APPROVAL: approval is evaluated at stageProtect
	var verifyContracts []RepairContract
	for _, c := range contracts {
		if c.GateID != "G24_APPROVAL" {
			verifyContracts = append(verifyContracts, c)
		}
	}
	if len(verifyContracts) > 0 || res.Status == blueprintdomain.StatusError {
		msg := "firewall blocked"
		if len(verifyContracts) > 0 {
			msg = fmt.Sprintf("firewall: gate %s blocked: %s", verifyContracts[0].GateID, verifyContracts[0].RawMessage)
		}
		return msg, errors.New(msg)
	}
	summary := fmt.Sprintf("firewall status %s: %d warnings, %d errors", res.Status, res.Summary.Warnings, res.Summary.Errors)
	return summary, nil
}

// Protect implements ProtectStage by checking approval rules and gate verdicts.
func (a *FirewallAdapter) Protect(ctx context.Context, intent string, wt *execution.Worktree) (string, error) {
	res, contracts, err := a.ValidateWorktree(ctx, wt, intent, intent, 1)
	if err != nil {
		return "", err
	}
	for _, c := range contracts {
		if c.GateID == "G24_APPROVAL" {
			return "approval required: " + c.RawMessage, errors.New("approval required: " + c.RawMessage)
		}
	}
	if res.Status == blueprintdomain.StatusBlock {
		return "approval blocked: gate violations present", errors.New("approval blocked: gate violations present")
	}
	return "approved: all gates passed", nil
}

// ExtractChanges discovers modified, added, and deleted files in the worktree.
func (a *FirewallAdapter) ExtractChanges(wt *execution.Worktree) ([]blueprintdomain.FileChange, error) {
	if wt == nil {
		return nil, nil
	}

	// 1. Try git diff first if available
	diff, _ := wt.Diff()
	if diff != "" {
		changes := parseDiffToChanges(diff, wt.Dir())
		if len(changes) > 0 {
			return changes, nil
		}
	}

	// 2. Fallback: walk wt.Dir() and compare with wt.SourceRoot()
	var changes []blueprintdomain.FileChange
	srcRoot := wt.SourceRoot()
	workDir := wt.Dir()

	err := filepath.WalkDir(workDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			name := d.Name()
			if d.IsDir() && (name == ".git" || name == ".kern" || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}

		rel, relErr := filepath.Rel(workDir, path)
		if relErr != nil {
			return nil
		}

		workContent, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		srcPath := filepath.Join(srcRoot, rel)
		srcContent, srcReadErr := os.ReadFile(srcPath)

		if os.IsNotExist(srcReadErr) {
			changes = append(changes, blueprintdomain.FileChange{
				Path:    rel,
				Op:      blueprintdomain.OpWrite,
				Content: string(workContent),
			})
		} else if srcReadErr == nil && string(workContent) != string(srcContent) {
			changes = append(changes, blueprintdomain.FileChange{
				Path:    rel,
				Op:      blueprintdomain.OpEdit,
				Content: string(workContent),
			})
		}
		return nil
	})

	return changes, err
}

func parseDiffToChanges(diff string, workDir string) []blueprintdomain.FileChange {
	if strings.TrimSpace(diff) == "" {
		return nil
	}

	var changes []blueprintdomain.FileChange
	rawSections := strings.Split(diff, "diff --git ")
	for _, sec := range rawSections {
		sec = strings.TrimSpace(sec)
		if sec == "" {
			continue
		}
		lines := strings.Split(sec, "\n")
		header := lines[0]
		parts := strings.Split(header, " ")
		if len(parts) < 2 {
			continue
		}
		pathB := parts[1]
		relPath := strings.TrimPrefix(pathB, "b/")
		relPath = strings.TrimPrefix(relPath, "a/")

		op := blueprintdomain.OpEdit
		if strings.Contains(sec, "new file mode") {
			op = blueprintdomain.OpWrite
		} else if strings.Contains(sec, "deleted file mode") {
			op = blueprintdomain.OpDelete
		}

		fc := blueprintdomain.FileChange{
			Path: relPath,
			Op:   op,
			Diff: "diff --git " + sec,
		}

		if op != blueprintdomain.OpDelete {
			fullPath := filepath.Join(workDir, relPath)
			if data, err := os.ReadFile(fullPath); err == nil {
				fc.Content = string(data)
			}
		}

		changes = append(changes, fc)
	}
	return changes
}
