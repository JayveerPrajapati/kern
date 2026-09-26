package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestSetPoliciesPropagatesToGovernanceSurface pins the P13 propagation seam:
// SetPolicies swaps the firewall's assessor, so the /api/governance policy
// list and the /api/risks assessments reflect the applied set instead of the
// hardcoded defaults. Enterprise mode calls SetPolicies when building each
// project's App from the org policy.
func TestSetPoliciesPropagatesToGovernanceSurface(t *testing.T) {
	app := newTestApp(t)

	// Sanity: before SetPolicies the firewall enforces the defaults.
	before := app.buildRisks()
	if len(before) == 0 {
		t.Fatal("buildRisks returned no items")
	}

	custom := []domain.Policy{
		{
			ID:          "pol-org-1",
			Name:        "org_source_write",
			Description: "Org rule: source writes are high risk.",
			Rule:        "HIGH source.write",
			Scope:       "source",
			Enabled:     true,
		},
		{
			ID:          "pol-org-deploy",
			Name:        "org_production_deploy",
			Description: "Org rule: deploys require approval.",
			Rule:        "CRITICAL production.deploy",
			Scope:       "production",
			Enabled:     true,
		},
	}
	app.SetPolicies(custom)

	// /api/governance lists the applied (org) policies, not the defaults.
	rec := get(t, app, "/api/governance")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/governance = %d, want 200", rec.Code)
	}
	var gov struct {
		Policies []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"policies"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &gov); err != nil {
		t.Fatalf("decode governance: %v", err)
	}
	if len(gov.Policies) != 2 || gov.Policies[0].ID != "pol-org-1" {
		t.Fatalf("policies after SetPolicies = %+v, want the 2 org policies", gov.Policies)
	}

	// /api/risks is assessed from the applied policies: source.write is now
	// HIGH (approval required) instead of the default MEDIUM.
	rec = get(t, app, "/api/risks")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/risks = %d, want 200", rec.Code)
	}
	var risks struct {
		Items []struct {
			Resource         string  `json:"resource"`
			Action           string  `json:"action"`
			Level            string  `json:"level"`
			ApprovalRequired bool    `json:"approval_required"`
			Blocked          bool    `json:"blocked"`
			Score            float64 `json:"score"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &risks); err != nil {
		t.Fatalf("decode risks: %v", err)
	}
	var sourceWrite *struct {
		Resource         string  `json:"resource"`
		Action           string  `json:"action"`
		Level            string  `json:"level"`
		ApprovalRequired bool    `json:"approval_required"`
		Blocked          bool    `json:"blocked"`
		Score            float64 `json:"score"`
	}
	for i := range risks.Items {
		if risks.Items[i].Resource == "source" && risks.Items[i].Action == "write" {
			sourceWrite = &risks.Items[i]
			break
		}
	}
	if sourceWrite == nil {
		t.Fatal("risks items do not include source/write")
	}
	if sourceWrite.Level != "HIGH" || !sourceWrite.ApprovalRequired {
		t.Errorf("source.write after SetPolicies = level %s approval_required %v, want HIGH/true", sourceWrite.Level, sourceWrite.ApprovalRequired)
	}
}

// TestRisksDefaultAssessmentUnchanged pins the byte-for-byte default: without
// SetPolicies the risks page assesses source.write as MEDIUM with no approval
// requirement — exactly what the default policy set produced before the
// propagation seam existed.
func TestRisksDefaultAssessmentUnchanged(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/api/risks")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/risks = %d, want 200", rec.Code)
	}
	var risks struct {
		Items []struct {
			Resource         string `json:"resource"`
			Action           string `json:"action"`
			Level            string `json:"level"`
			ApprovalRequired bool   `json:"approval_required"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &risks); err != nil {
		t.Fatalf("decode risks: %v", err)
	}
	for _, item := range risks.Items {
		if item.Resource == "source" && item.Action == "write" {
			if item.Level != "MEDIUM" || item.ApprovalRequired {
				t.Errorf("default source.write = level %s approval_required %v, want MEDIUM/false", item.Level, item.ApprovalRequired)
			}
			return
		}
	}
	t.Fatal("risks items do not include source/write")
}
