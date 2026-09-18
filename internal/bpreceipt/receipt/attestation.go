package receipt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// InTotoSubject represents the subject artifact of an in-toto statement.
type InTotoSubject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// InTotoPredicate represents the supply chain validation metadata.
type InTotoPredicate struct {
	ReceiptID      string    `json:"receipt_id"`
	BuilderID      string    `json:"builder_id"`
	Status         string    `json:"status"`
	ExitCode       int       `json:"exit_code"`
	BaseRevision   string    `json:"base_revision"`
	HeadRevision   string    `json:"head_revision"`
	ValidationHash string    `json:"validation_hash"`
	AuditChainHash string    `json:"audit_chain_hash"`
	AuditRecordID  string    `json:"audit_record_id"`
	KernChainHash  string    `json:"kern_chain_hash,omitempty"`
	FindingsCount  int       `json:"findings_count"`
	GeneratedAt    time.Time `json:"generated_at"`
	GeneratedBy    string    `json:"generated_by"`
	Signature      string    `json:"signature"`
}

// InTotoStatement represents an in-toto v0.1/v0.2 supply-chain attestation statement.
type InTotoStatement struct {
	Type          string          `json:"_type"`
	Subject       []InTotoSubject `json:"subject"`
	PredicateType string          `json:"predicateType"`
	Predicate     InTotoPredicate `json:"predicate"`
}

// RenderInToto generates an in-toto v0.1/v0.2 supply-chain attestation statement for the receipt.
func RenderInToto(r *Receipt) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("cannot render in-toto statement for nil receipt")
	}

	repoName := filepath.Base(r.RepoRoot)
	if repoName == "" || repoName == "." {
		repoName = "repository"
	}

	stmt := InTotoStatement{
		Type: "https://in-toto.io/Statement/v0.1",
		Subject: []InTotoSubject{
			{
				Name: repoName,
				Digest: map[string]string{
					"sha256":  r.ValidationHash,
					"gitHead": r.HeadRevision,
				},
			},
		},
		PredicateType: "https://kernops.dev/attestation/v1",
		Predicate: InTotoPredicate{
			ReceiptID:      r.ReceiptID,
			BuilderID:      "kernops/v2.1.0",
			Status:         r.Status,
			ExitCode:       r.ExitCode,
			BaseRevision:   r.BaseRevision,
			HeadRevision:   r.HeadRevision,
			ValidationHash: r.ValidationHash,
			AuditChainHash: r.AuditChainHash,
			AuditRecordID:  r.AuditRecordID,
			KernChainHash:  r.KernChainHash,
			FindingsCount:  r.FindingsCount,
			GeneratedAt:    r.GeneratedAt,
			GeneratedBy:    r.GeneratedBy,
			Signature:      r.Signature,
		},
	}

	return json.MarshalIndent(stmt, "", "  ")
}

// RenderSARIF renders a Receipt and its findings into standard SARIF 2.1.0 format.
func RenderSARIF(r *Receipt, findings []domain.Finding) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("cannot render sarif for nil receipt")
	}

	type region struct {
		StartLine   int `json:"startLine,omitempty"`
		StartColumn int `json:"startColumn,omitempty"`
	}
	type physicalLocation struct {
		ArtifactLocation map[string]any `json:"artifactLocation"`
		Region           region         `json:"region,omitempty"`
	}
	type location struct {
		Physical physicalLocation `json:"physicalLocation"`
	}
	type rule struct {
		ID               string            `json:"id"`
		Name             string            `json:"name"`
		ShortDescription map[string]string `json:"shortDescription"`
	}
	type result struct {
		RuleID    string         `json:"ruleId"`
		Level     string         `json:"level"`
		Message   map[string]any `json:"message"`
		Locations []location     `json:"locations"`
	}

	ruleSeen := make(map[string]bool)
	var rules []rule
	results := make([]result, 0, len(findings))

	for _, f := range findings {
		ruleID := f.RuleID
		if ruleID == "" {
			ruleID = string(f.Category)
		}
		if ruleID == "" {
			ruleID = "GENERIC_FINDING"
		}

		if !ruleSeen[ruleID] {
			ruleSeen[ruleID] = true
			rules = append(rules, rule{
				ID:               ruleID,
				Name:             ruleID,
				ShortDescription: map[string]string{"text": f.Message},
			})
		}

		level := "error"
		switch f.Severity {
		case domain.SeverityWarn:
			level = "warning"
		case domain.SeverityInfo:
			level = "note"
		}

		loc := location{
			Physical: physicalLocation{
				ArtifactLocation: map[string]any{"uri": f.File},
				Region: region{
					StartLine:   f.Line,
					StartColumn: f.Column,
				},
			},
		}

		results = append(results, result{
			RuleID:    ruleID,
			Level:     level,
			Message:   map[string]any{"text": f.Message},
			Locations: []location{loc},
		})
	}

	doc := map[string]any{
		"$schema": "https://schemastore.azurewebsites.net/schemas/json/sarif-2.1.0-rtm.5.json",
		"version": "2.1.0",
		"runs": []any{
			map[string]any{
				"tool": map[string]any{
					"driver": map[string]any{
						"name":           "kernops",
						"version":        "2.1.0",
						"informationUri": "https://github.com/JayveerPrajapati/kern",
						"rules":          rules,
					},
				},
				"results": results,
				"invocations": []any{
					map[string]any{
						"executionSuccessful": (r.Status == "PASS" || r.Status == "WARN"),
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// VerifyDiffIntegrity checks that the git state matches the cryptographic fingerprint signed in the receipt.
func VerifyDiffIntegrity(r *Receipt, repoRoot string) error {
	if r == nil {
		return fmt.Errorf("nil receipt")
	}

	// 1. Verify receipt internal cryptographic signature
	if err := r.Verify(); err != nil {
		return fmt.Errorf("receipt signature invalid: %w", err)
	}

	// 2. If HeadRevision is specified, verify that repository HEAD matches
	if r.HeadRevision != "" && r.HeadRevision != "HEAD" && r.HeadRevision != "." {
		cmd := exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = repoRoot
		out, err := cmd.CombinedOutput()
		if err == nil {
			currentHead := strings.TrimSpace(string(out))
			attestedHead := r.HeadRevision
			revCmd := exec.Command("git", "rev-parse", r.HeadRevision)
			revCmd.Dir = repoRoot
			if revOut, revErr := revCmd.CombinedOutput(); revErr == nil {
				attestedHead = strings.TrimSpace(string(revOut))
			}

			if !strings.HasPrefix(currentHead, attestedHead) && !strings.HasPrefix(attestedHead, currentHead) {
				return fmt.Errorf("tamper detected: repository HEAD %q does not match attested head_revision %q", currentHead, r.HeadRevision)
			}
		}
	}

	// 3. Verify that there are no uncommitted changes on top of attested head
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = repoRoot
	out, err := statusCmd.CombinedOutput()
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		// Ignore untracked files or .blueprint/ receipts directory
		lines := strings.Split(string(out), "\n")
		var dirtyTracked []string
		for _, ln := range lines {
			trimmed := strings.TrimSpace(ln)
			if trimmed == "" || strings.HasPrefix(trimmed, "??") || strings.Contains(trimmed, ".blueprint") || strings.Contains(trimmed, ".kern") {
				continue
			}
			dirtyTracked = append(dirtyTracked, trimmed)
		}
		if len(dirtyTracked) > 0 {
			return fmt.Errorf("tamper detected: uncommitted mutations present since receipt was sealed (%s)", strings.Join(dirtyTracked, ", "))
		}
	}

	return nil
}
