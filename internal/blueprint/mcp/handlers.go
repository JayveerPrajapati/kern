package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/gitleaks"
	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/jscpd"
	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/audit"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/metrics"
	"github.com/JayveerPrajapati/kern/internal/blueprint/policy"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
)

// --- ValidateStagedHandler ---

// ValidateStagedHandler implements the `blueprint_validate_staged` MCP tool.
// An agent calls this with a repository path to validate the staged changes
// against Blueprint policy. The result is a structured ValidationResult that
// the agent can use to repair and retry (G5: "blocked response is
// machine-readable" + "agent can use the result to repair and retry").
//
// Advisory semantics (spec Critical safety rule, lines 970-974): this tool
// validates proposed changes when the agent calls it. It is NOT an OS-level
// file-write interception. The agent opts in by calling this tool.
type ValidateStagedHandler struct{}

func (ValidateStagedHandler) Name() string { return "blueprint_validate_staged" }

func (ValidateStagedHandler) Description() string {
	return "Validate staged git changes against Blueprint policy (architecture boundaries, secrets). Returns a structured ValidationResult. Advisory: the agent must call this voluntarily; it is not a hard pre-write boundary."
}

func (ValidateStagedHandler) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"repo": map[string]interface{}{
				"type":        "string",
				"description": "Absolute path to the repository root. Defaults to the server's working directory if omitted.",
			},
			"source": map[string]interface{}{
				"type":        "string",
				"description": "Change source: agent|ide|human|refactor|dep-bot|ci. Defaults to 'agent'.",
			},
		},
	}
}

// validateStagedArgs is the parsed arguments for blueprint_validate_staged.
type validateStagedArgs struct {
	Repo   string `json:"repo"`
	Source string `json:"source"`
}

func (h ValidateStagedHandler) Handle(ctx context.Context, args json.RawMessage) ToolResult {
	// G5: "malformed tool payload" — reject invalid JSON.
	var a validateStagedArgs
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &a); err != nil {
			return NewErrorResult(fmt.Sprintf("invalid arguments: %v", err))
		}
	}

	// G5: "missing repository context" — resolve repo path.
	repo := a.Repo
	if repo == "" {
		repo, _ = os.Getwd()
	}
	absRepo, err := filepath.Abs(repo)
	if err != nil {
		return NewErrorResult(fmt.Sprintf("invalid repository path %q: %v", repo, err))
	}
	if _, err := os.Stat(absRepo); err != nil {
		return NewErrorResult(fmt.Sprintf("repository not found: %s", absRepo))
	}

	// G5: "agent identity missing/unknown" — default source to "agent".
	source := a.Source
	if source == "" {
		source = "agent"
	}

	// Load config (validates the configuration; the service applies it).
	cfg, err := policy.Load(absRepo)
	if err != nil {
		return NewErrorResult(fmt.Sprintf("invalid configuration: %v", err))
	}

	// Discover staged changes.
	changes, err := discoverStaged(absRepo)
	if err != nil {
		return NewErrorResult(fmt.Sprintf("cannot discover staged changes: %v", err))
	}

	// G5: "failed tool execution" — if kern binary is unavailable.
	client, err := kern.NewKernClient()
	if err != nil {
		return NewErrorResult(fmt.Sprintf("kern binary not found: %v", err))
	}
	// P2-4: probe the kern version once for provenance stamping. Best-effort:
	// an empty string on probe failure must never fail validation.
	kernVersion, _ := client.Version()

	// Build and run the validation.
	req := domain.ChangeRequest{
		RepositoryRoot: absRepo,
		Source:         domain.Source(source),
		Operation:      domain.OpCommit,
		Files:          changes,
	}

	archCheck := kern.NewArchitectureCheck(client)
	// T2.1: detection delegated to incumbents (gitleaks, jscpd); each falls
	// back to its in-house check when the binary is absent.
	secretCheck := gitleaks.NewCheck(client)
	dupCheck := jscpd.NewCheck(client)
	svc := service.New([]service.Check{archCheck, secretCheck, dupCheck},
		service.WithConfig(cfg.Service),
		service.WithKernVersion(kernVersion),
		service.WithPolicy(policy.NewEngine(cfg.Policy)),
		service.WithAudit(audit.NewWriter(filepath.Join(absRepo, ".blueprint", "audit", "audit.jsonl"))))
	if m, err := metrics.Load(metrics.DefaultPath(absRepo)); err == nil {
		svc = service.New([]service.Check{archCheck, secretCheck, dupCheck},
			service.WithConfig(cfg.Service),
			service.WithKernVersion(kernVersion),
			service.WithPolicy(policy.NewEngine(cfg.Policy)),
			service.WithMetrics(m, metrics.DefaultPath(absRepo)),
			service.WithAudit(audit.NewWriter(filepath.Join(absRepo, ".blueprint", "audit", "audit.jsonl"))),
		)
	}

	result := svc.Validate(ctx, req)
	// P2.3: enrich the ValidationResult with a per-leg verdict section
	// (leg_verdicts + verdict_basis) so the caller sees each check's verdict
	// individually and which legs can block vs which are advisory-only.
	return NewJSONResult(buildValidateResponse(result))
}

// --- ValidateProposedHandler ---

// ValidateProposedHandler implements the `blueprint_validate_proposed` MCP
// tool. An agent calls this with a repository path and a set of proposed file
// changes (path + content, NOT yet written to disk) to validate them against
// Blueprint policy BEFORE writing. The staged-only tool cannot see content an
// agent proposes before staging; this tool can.
//
// Advisory semantics (spec Critical safety rule, lines 970-974): this tool
// validates proposed changes when the agent calls it. It is NOT an OS-level
// file-write interception. The agent opts in by calling this tool.
type ValidateProposedHandler struct{}

func (ValidateProposedHandler) Name() string { return "blueprint_validate_proposed" }

func (ValidateProposedHandler) Description() string {
	return "Validate proposed file content (not yet written to disk) against Blueprint policy (secrets, duplication). Pass files as [{path, content, op}]. Returns a structured ValidationResult. Advisory: the agent must call this voluntarily; it is not a hard pre-write boundary. Limitation: architecture boundaries for new files are enforced once the file exists on disk; secrets and duplication are validated against proposed content."
}

func (ValidateProposedHandler) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"repo": map[string]interface{}{
				"type":        "string",
				"description": "Absolute path to the repository root. Defaults to the server's working directory if omitted.",
			},
			"source": map[string]interface{}{
				"type":        "string",
				"description": "Change source: agent|ide|human|refactor|dep-bot|ci. Defaults to 'agent'.",
			},
			"files": map[string]interface{}{
				"type":        "array",
				"description": "Proposed file changes, not yet written to disk: [{path: repo-relative path, content: proposed content, op: write|edit|delete|rename|commit}]. op defaults to 'write'.",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Repository-relative file path.",
						},
						"content": map[string]interface{}{
							"type":        "string",
							"description": "Proposed file content (pre-write).",
						},
						"op": map[string]interface{}{
							"type":        "string",
							"description": "Change operation: write|edit|delete|rename|commit. Defaults to 'write'.",
						},
					},
					"required": []string{"path", "content"},
				},
			},
			"context_provenance": map[string]interface{}{
				"type":        "object",
				"description": "Optional retrieval provenance echoed from kern's result.provenance (kern_explore/kern_context/kern_graph). Links this change decision to its context authorization in the audit trail: {schema_version, mode, authorizing_rule?, index, symbols}.",
			},
		},
		"required": []string{"files"},
	}
}

// validateProposedArgs is the parsed arguments for blueprint_validate_proposed.
type validateProposedArgs struct {
	Repo   string               `json:"repo"`
	Source string               `json:"source"`
	Files  []proposedFileChange `json:"files"`
	// ContextProvenance (P1.2) is the retrieval provenance the agent echoes
	// from kern's result.provenance (kern_explore/kern_context/kern_graph).
	// Optional: nil for changes that carry no provenance (raw/ungoverned).
	ContextProvenance *domain.ContextProvenance `json:"context_provenance,omitempty"`
}

// proposedFileChange is one proposed file in blueprint_validate_proposed.
type proposedFileChange struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Op      string `json:"op"`
}

func (h ValidateProposedHandler) Handle(ctx context.Context, args json.RawMessage) ToolResult {
	// G5: "malformed tool payload" — reject invalid JSON.
	var a validateProposedArgs
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &a); err != nil {
			return NewErrorResult(fmt.Sprintf("invalid arguments: %v", err))
		}
	}

	// Reject empty or incomplete proposed files: every entry needs a path AND
	// content (the proposed content is the whole point of this tool).
	if len(a.Files) == 0 {
		return NewErrorResult("invalid arguments: files must contain at least one proposed file (path + content)")
	}
	files := make([]domain.FileChange, 0, len(a.Files))
	for i, pf := range a.Files {
		if pf.Path == "" {
			return NewErrorResult(fmt.Sprintf("invalid arguments: files[%d].path is required", i))
		}
		if pf.Content == "" {
			return NewErrorResult(fmt.Sprintf("invalid arguments: files[%d].content is required", i))
		}
		op := domain.Operation(pf.Op)
		if op == "" {
			op = domain.OpWrite
		}
		switch op {
		case domain.OpWrite, domain.OpEdit, domain.OpDelete, domain.OpRename, domain.OpCommit:
		default:
			return NewErrorResult(fmt.Sprintf("invalid arguments: files[%d].op %q is not a valid operation", i, pf.Op))
		}
		files = append(files, domain.FileChange{Path: pf.Path, Op: op, Content: pf.Content})
	}

	// G5: "missing repository context" — resolve repo path.
	repo := a.Repo
	if repo == "" {
		repo, _ = os.Getwd()
	}
	absRepo, err := filepath.Abs(repo)
	if err != nil {
		return NewErrorResult(fmt.Sprintf("invalid repository path %q: %v", repo, err))
	}
	if _, err := os.Stat(absRepo); err != nil {
		return NewErrorResult(fmt.Sprintf("repository not found: %s", absRepo))
	}

	// G5: "agent identity missing/unknown" — default source to "agent".
	source := a.Source
	if source == "" {
		source = "agent"
	}

	// Load config (validates the configuration; the service applies it).
	cfg, err := policy.Load(absRepo)
	if err != nil {
		return NewErrorResult(fmt.Sprintf("invalid configuration: %v", err))
	}

	// G5: "failed tool execution" — if kern binary is unavailable.
	client, err := kern.NewKernClient()
	if err != nil {
		return NewErrorResult(fmt.Sprintf("kern binary not found: %v", err))
	}
	// P2-4: probe the kern version once for provenance stamping. Best-effort:
	// an empty string on probe failure must never fail validation.
	kernVersion, _ := client.Version()

	// Build and run the validation.
	req := domain.ChangeRequest{
		RepositoryRoot:    absRepo,
		Source:            domain.Source(source),
		Operation:         domain.OpWrite,
		Files:             files,
		ContextProvenance: a.ContextProvenance,
	}

	archCheck := kern.NewArchitectureCheck(client)
	// T2.1: detection delegated to incumbents (gitleaks, jscpd); each falls
	// back to its in-house check when the binary is absent.
	secretCheck := gitleaks.NewCheck(client)
	dupCheck := jscpd.NewCheck(client)
	svc := service.New([]service.Check{archCheck, secretCheck, dupCheck},
		service.WithConfig(cfg.Service),
		service.WithKernVersion(kernVersion),
		service.WithPolicy(policy.NewEngine(cfg.Policy)),
		service.WithAudit(audit.NewWriter(filepath.Join(absRepo, ".blueprint", "audit", "audit.jsonl"))))
	if m, err := metrics.Load(metrics.DefaultPath(absRepo)); err == nil {
		svc = service.New([]service.Check{archCheck, secretCheck, dupCheck},
			service.WithConfig(cfg.Service),
			service.WithKernVersion(kernVersion),
			service.WithPolicy(policy.NewEngine(cfg.Policy)),
			service.WithMetrics(m, metrics.DefaultPath(absRepo)),
			service.WithAudit(audit.NewWriter(filepath.Join(absRepo, ".blueprint", "audit", "audit.jsonl"))),
		)
	}

	result := svc.Validate(ctx, req)
	// P2.3: enrich the ValidationResult with a per-leg verdict section
	// (leg_verdicts + verdict_basis) so the caller sees each check's verdict
	// individually and which legs can block vs which are advisory-only.
	return NewJSONResult(buildValidateResponse(result))
}

// --- ExplainFindingHandler ---

// ExplainFindingHandler implements the `blueprint_explain_finding` MCP tool.
// An agent calls this with a finding to get a human-readable explanation and
// suggested fix, helping it repair and retry.
type ExplainFindingHandler struct{}

func (ExplainFindingHandler) Name() string { return "blueprint_explain_finding" }

func (ExplainFindingHandler) Description() string {
	return "Explain a validation finding in human-readable terms and suggest a fix. Helps the agent repair and retry."
}

func (ExplainFindingHandler) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"finding": map[string]interface{}{
				"type":        "object",
				"description": "A Finding object from a ValidationResult.",
			},
		},
		"required": []string{"finding"},
	}
}

type explainFindingArgs struct {
	Finding domain.Finding `json:"finding"`
}

func (h ExplainFindingHandler) Handle(ctx context.Context, args json.RawMessage) ToolResult {
	var a explainFindingArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return NewErrorResult(fmt.Sprintf("invalid arguments: %v", err))
	}
	f := a.Finding
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Finding: %s\n", f.RuleID))
	sb.WriteString(fmt.Sprintf("Severity: %s\n", f.Severity))
	sb.WriteString(fmt.Sprintf("File: %s:%d\n", f.File, f.Line))
	sb.WriteString(fmt.Sprintf("Message: %s\n", f.Message))
	if f.Explanation != "" {
		sb.WriteString(fmt.Sprintf("Explanation: %s\n", f.Explanation))
	}
	if f.SuggestedFix != "" {
		sb.WriteString(fmt.Sprintf("Suggested fix: %s\n", f.SuggestedFix))
	}
	if len(f.Evidence) > 0 {
		sb.WriteString("Evidence:\n")
		for _, e := range f.Evidence {
			sb.WriteString(fmt.Sprintf("  - %s at %s\n", e.Description, e.Location))
		}
	}
	// P2.3: advisory legs cannot block — surface that so the agent does not
	// over-correct for a WARN that can never gate a change. Only
	// duplication:confirmed-block is block-eligible; every other duplication
	// finding stays advisory (P1.1 two-pass triage).
	switch f.RuleID {
	case "duplication:advisory", "duplication:jscpd:clone":
		sb.WriteString("Advisory: this finding is advisory-only. The duplication leg reports WARN/INFO and cannot block a change on its own; only duplication:confirmed-block is block-eligible. Rationale: the duplication benchmark (docs/duplication-benchmark.md) shows precision 0.50 / FPR 0.75 at the 0.60 threshold — below production-grade targets, so the leg stays advisory.\n")
	}
	return NewTextResult(sb.String())
}

// --- RepairGuidanceHandler ---

// RepairGuidanceHandler implements the `blueprint_repair_guidance` MCP tool.
// Given a validation finding, it returns a structured, machine-readable repair
// contract (rule_id, what failed, why, suggested fix, evidence, and a
// re-validation hint) that an agent can act on deterministically. This is the
// structured, agent-consumable counterpart of blueprint_explain_finding (which
// returns prose): the agent_contract block is the machine equivalent of G7's
// assertFeedbackContract plus the TestG7_VagueResponseRejected rejection
// rules, so an agent can programmatically verify a finding is actionable
// before attempting repair. Call it after blueprint_validate_staged or
// blueprint_validate_proposed returns a BLOCK, apply the suggested_fix, then
// re-validate.
type RepairGuidanceHandler struct{}

func (RepairGuidanceHandler) Name() string { return "blueprint_repair_guidance" }

func (RepairGuidanceHandler) Description() string {
	return "Given a validation finding, return a structured, machine-readable repair contract (rule_id, what failed, why, suggested fix, evidence, and a re-validation hint) that an agent can act on deterministically. Use this after blueprint_validate_staged or blueprint_validate_proposed returns a BLOCK to get actionable repair guidance, then re-validate after applying the fix."
}

func (RepairGuidanceHandler) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"finding": map[string]interface{}{
				"type":        "object",
				"description": "A Finding object from a ValidationResult.",
			},
		},
		"required": []string{"finding"},
	}
}

// repairGuidanceArgs is the parsed arguments for blueprint_repair_guidance.
// Same shape as explainFindingArgs: a single Finding object.
type repairGuidanceArgs struct {
	Finding domain.Finding `json:"finding"`
}

func (h RepairGuidanceHandler) Handle(ctx context.Context, args json.RawMessage) ToolResult {
	var a repairGuidanceArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return NewErrorResult(fmt.Sprintf("invalid arguments: %v", err))
	}
	f := a.Finding

	// agent_contract is the structured equivalent of G7's assertFeedbackContract
	// + TestG7_VagueResponseRejected: an agent can verify programmatically that
	// a finding is actionable before attempting repair.
	requiredFieldsPresent := f.RuleID != "" && f.Message != "" && f.File != ""
	isActionable := f.SuggestedFix != "" && !isVagueMessage(f.Message)

	// The `why` field falls back to a derived explanation when the finding
	// carries none, so the contract is always complete.
	why := f.Explanation
	if why == "" {
		why = fmt.Sprintf("Finding %s (%s) at %s:%d: %s", f.RuleID, f.Category, f.File, f.Line, f.Message)
	}
	suggestedFix := f.SuggestedFix
	if suggestedFix == "" {
		suggestedFix = "No automated fix suggestion available; review the evidence and rule documentation"
	}

	result := map[string]interface{}{
		"rule_id":  f.RuleID,
		"severity": f.Severity,
		"category": f.Category,
		"location": map[string]interface{}{
			"file": f.File,
			"line": f.Line,
		},
		"what_failed":   f.Message,
		"why":           why,
		"suggested_fix": suggestedFix,
		"evidence":      evidenceList(f.Evidence),
		"repair_loop": map[string]interface{}{
			"step":                     "repair",
			"guidance":                 "Apply the suggested_fix to the file at the given location, then call blueprint_validate_staged (or blueprint_validate_proposed for pre-write checks) to re-validate. Repeat until the result is PASS with no BLOCK findings.",
			"re_validate_with":         "blueprint_validate_staged",
			"vague_responses_rejected": true,
		},
		"agent_contract": map[string]interface{}{
			"required_fields_present": requiredFieldsPresent,
			"is_actionable":           isActionable,
			"vague_phrases_rejected":  vaguePhrases,
		},
	}
	return NewJSONResult(result)
}

// vaguePhrases is the exact list of vague messages Blueprint must never
// produce as a finding message (spec lines 1130-1134, enforced by
// TestG7_VagueResponseRejected).
var vaguePhrases = []string{"architecture error", "validation failed", "check failed", "error"}

// isVagueMessage reports whether msg is one of the vague responses Blueprint
// forbids. Mirrors TestG7_VagueResponseRejected: the lowercased message must
// EQUAL a vague phrase — a specific message may legitimately contain the word
// "error" as part of a larger sentence and is not vague.
func isVagueMessage(msg string) bool {
	lower := strings.ToLower(msg)
	for _, vague := range vaguePhrases {
		if lower == vague {
			return true
		}
	}
	return false
}

// evidenceList renders finding evidence as the compact {description, location}
// pairs used in the repair contract.
func evidenceList(evidence []domain.Evidence) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(evidence))
	for _, e := range evidence {
		out = append(out, map[string]interface{}{
			"description": e.Description,
			"location":    e.Location,
		})
	}
	return out
}

// --- Helpers ---

// discoverStaged returns the staged file changes via `git diff --cached`.
// This is the same logic as cmd/blueprint/check.go but factored for reuse.
func discoverStaged(repoRoot string) ([]domain.FileChange, error) {
	// Use git diff --cached --name-status to get staged changes.
	out, err := gitOutput(repoRoot, "diff", "--cached", "--name-status")
	if err != nil {
		return nil, fmt.Errorf("git diff --cached: %w", err)
	}
	var changes []domain.FileChange
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		status, path := parts[0], parts[1]
		op := domain.OpWrite
		switch {
		case strings.HasPrefix(status, "D"):
			op = domain.OpDelete
		case strings.HasPrefix(status, "R"):
			op = domain.OpRename
		case strings.HasPrefix(status, "A"):
			op = domain.OpWrite
		case strings.HasPrefix(status, "M"):
			op = domain.OpEdit
		}
		changes = append(changes, domain.FileChange{Path: path, Op: op})
	}
	return changes, nil
}

// gitOutput runs git in dir and returns stdout.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
