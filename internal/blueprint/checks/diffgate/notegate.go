package diffgate

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/note"
)

// G37 — note:format
//
// NoteFormatCheck (G37) validates the committed docs/notes/ decision-record
// tree (the kern analogue of dsh's Agent Notes). It BLOCKs on any format
// violation: path-encoded lifecycle/class agreement, the `# Agent Note:`
// / `Status:` / blank-line header, the `## Problem` opening, and the
// archived-frozen marker — the same contract `kern note validate` reports.
type NoteFormatCheck struct {
	root string
}

// NewNoteFormatCheck constructs the note-format check bound to a repo root.
func NewNoteFormatCheck(root string) *NoteFormatCheck {
	return &NoteFormatCheck{root: root}
}

// Name is the stable check identifier.
func (c *NoteFormatCheck) Name() string { return "note:format" }

// Run scans the docs/notes tree and reports every violation as BLOCK.
func (c *NoteFormatCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	if c.root == "" {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "repository root required"}, nil
	}
	violations, err := note.ValidateTree(c.root)
	if err != nil {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: err.Error()}, nil
	}
	if len(violations) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}
	findings := make([]domain.Finding, 0, len(violations))
	for _, v := range violations {
		findings = append(findings, domain.Finding{
			RuleID:       "note:format",
			Severity:     domain.SeverityBlock,
			Category:     domain.CategoryPolicy,
			Message:      v.Message,
			Explanation:  "The docs/notes/ decision-record tree must keep its path-encoded lifecycle/class contract and the gate-enforced in-file format.",
			SuggestedFix: "Run `kern note validate` for the full report, then fix the file or `kern note status <file> --set <lifecycle>` to move it.",
			RuleVersion:  "1",
			Confidence:   1.0,
			Scope:        "repo",
			Evidence:     []domain.Evidence{{Kind: "file", Description: v.Path}},
		})
	}
	return domain.CheckResult{Name: c.Name(), Status: domain.StatusBlock, Findings: findings}, nil
}

// G38 — note:missing
//
// NoteMissingCheck (G38) mirrors the changelog gate: it WARNs when the
// changed set contains non-doc source changes but no docs/notes/ note —
// the "every non-trivial change ships a note" rule as advisory, matching
// dsh's policy without forcing it.
type NoteMissingCheck struct{}

// NewNoteMissingCheck constructs the note-missing diff-gate check.
func NewNoteMissingCheck() *NoteMissingCheck { return &NoteMissingCheck{} }

// Name is the stable check identifier.
func (NoteMissingCheck) Name() string { return "note:missing" }

// Run returns a WARN finding when source changes lack a docs/notes entry.
func (c *NoteMissingCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	hasSource := false
	hasNote := false
	for _, fc := range req.Files {
		p := filepath.ToSlash(fc.Path)
		if strings.HasPrefix(p, "docs/notes/") {
			hasNote = true
			continue
		}
		if isDocOnlyPath(p) {
			continue
		}
		hasSource = true
	}
	if !hasSource || hasNote {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}
	return domain.CheckResult{Name: c.Name(), Status: domain.StatusWarn, Findings: []domain.Finding{{
		RuleID:       "note:missing",
		Severity:     domain.SeverityWarn,
		Category:     domain.CategoryPolicy,
		Message:      "source changes without a docs/notes decision record",
		Explanation:  "Non-doc source changes should carry a decision record (docs/notes/{lifecycle}/{class}/yyyy-mm-dd-topic-title.md). Advisory: flags the omission, never blocks.",
		SuggestedFix: "Run `kern note new --class <class> --lifecycle implemented \"<title>\"` when the change alters behavior, architecture, or a contract.",
		RuleVersion:  "1",
		Confidence:   1.0,
		Scope:        "repo",
		Evidence:     []domain.Evidence{{Kind: "change-set", Description: "source changes present, docs/notes/ entry absent from the changed set"}},
	}}}, nil
}
