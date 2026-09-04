package mcp

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// G28 (T2.2 "harden repair loop into a first-class agent workflow"): the full
// agent repair cycle exercised through the REAL MCP surface — the registered
// handlers (ValidateStagedHandler, RepairGuidanceHandler) called exactly as
// the server's tools/call dispatch does, NOT the G7 shim:
//
//	stage bad change -> blueprint_validate_staged -> BLOCK
//	-> blueprint_repair_guidance -> structured, actionable contract
//	-> agent applies suggested_fix -> re-stage -> re-validate -> PASS
//
// The architecture check auto-rebuilds the kern index when stale (see
// ArchitectureCheck.Run), so the repaired import edges are visible to
// re-validation without an explicit rebuild.
func TestG28_RepairLoopEndToEnd(t *testing.T) {
	g5RequireKern(t)
	dir := g5Repo(t)

	// Iteration 1: stage a change that violates the web->db boundary.
	g5Stage(t, dir, "web/bad.go", "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n")

	args, _ := json.Marshal(map[string]string{"repo": dir, "source": "agent"})
	tr := g5CallValidateStaged(t, args)
	m := g5ParseResult(t, tr)
	status, _ := m["status"].(string)
	if !strings.EqualFold(status, "BLOCK") {
		t.Fatalf("iteration 1: status=%v, want BLOCK; full: %v", m["status"], m)
	}
	findings, _ := m["findings"].([]interface{})
	if len(findings) == 0 {
		t.Fatalf("iteration 1: expected a BLOCK finding")
	}
	finding := findings[0].(map[string]interface{})
	t.Logf("Iteration 1 BLOCK: rule=%v file=%v", finding["rule_id"], finding["file"])

	// Iteration 2: call blueprint_repair_guidance with the finding.
	guidanceArgs, _ := json.Marshal(map[string]interface{}{"finding": finding})
	gtr := g28CallRepairGuidance(t, guidanceArgs)
	contract := g28ParseContract(t, gtr)

	// The repair contract must be well-formed (the G7 assertFeedbackContract
	// fields) and actionable.
	if ruleID, _ := contract["rule_id"].(string); ruleID == "" {
		t.Error("repair contract: rule_id is empty")
	}
	if what, _ := contract["what_failed"].(string); what == "" {
		t.Error("repair contract: what_failed is empty")
	}
	if why, _ := contract["why"].(string); why == "" {
		t.Error("repair contract: why is empty")
	}
	if fix, _ := contract["suggested_fix"].(string); fix == "" {
		t.Error("repair contract: suggested_fix is empty")
	}
	location, _ := contract["location"].(map[string]interface{})
	if _, ok := location["file"]; !ok {
		t.Error("repair contract: location.file missing")
	}
	if _, ok := location["line"]; !ok {
		t.Error("repair contract: location.line missing")
	}
	evidence, _ := contract["evidence"].([]interface{})
	if len(evidence) == 0 {
		t.Error("repair contract: evidence is empty")
	}
	repairLoop, _ := contract["repair_loop"].(map[string]interface{})
	if step, _ := repairLoop["step"].(string); step != "repair" {
		t.Errorf("repair contract: repair_loop.step = %v, want \"repair\"", step)
	}
	if rvw, _ := repairLoop["re_validate_with"].(string); rvw != "blueprint_validate_staged" {
		t.Errorf("repair contract: repair_loop.re_validate_with = %v, want blueprint_validate_staged", rvw)
	}
	agentContract, _ := contract["agent_contract"].(map[string]interface{})
	if agentContract == nil {
		t.Fatalf("repair contract: agent_contract missing")
	}
	if actionable, _ := agentContract["is_actionable"].(bool); !actionable {
		t.Errorf("repair contract: agent_contract.is_actionable = false, want true (finding: %v)", finding)
	}
	if required, _ := agentContract["required_fields_present"].(bool); !required {
		t.Errorf("repair contract: agent_contract.required_fields_present = false, want true")
	}

	t.Logf("Repair guidance: rule=%s what=%q fix=%q", contract["rule_id"], contract["what_failed"], contract["suggested_fix"])

	// Iteration 3: agent repairs by routing through the api layer instead of
	// importing db directly (mirrors G7's repair), then re-validates -> PASS.
	g5Stage(t, dir, "api/api.go", "package api\nimport \"example.com/repo/db\"\nfunc Process() { db.Query() }\n")
	g5Stage(t, dir, "web/bad.go", "package web\nimport \"example.com/repo/api\"\nfunc Bad() { api.Process() }\n")

	tr2 := g5CallValidateStaged(t, args)
	m2 := g5ParseResult(t, tr2)
	status2, _ := m2["status"].(string)
	if strings.EqualFold(status2, "BLOCK") {
		t.Fatalf("iteration 3: status=BLOCK — repair did not resolve the finding; findings: %v", m2["findings"])
	}
	t.Logf("Repair loop: iter1=%s -> repair_guidance -> iter3=%s", status, status2)
}

// TestG28_ToolListIncludesRepairGuidance builds the real blueprint-mcp server
// binary and verifies blueprint_repair_guidance is advertised in the MCP
// tools/list response, then exercises the tool through the real JSON-RPC
// tools/call surface with a sample finding and asserts the structured repair
// contract.
func TestG28_ToolListIncludesRepairGuidance(t *testing.T) {
	// Build the blueprint-mcp binary.
	binPath := filepath.Join(t.TempDir(), "blueprint-mcp")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", binPath, "./cmd/blueprint-mcp")
	cmd.Dir = g5FindRepoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build blueprint-mcp: %v\n%s", err, out)
	}

	// A sample BLOCK finding (shape mirrors a real architecture finding).
	sample := map[string]interface{}{
		"rule_id":       "architecture:forbidden-import",
		"severity":      "block",
		"category":      "architecture",
		"file":          "web/bad.go",
		"line":          3,
		"message":       "web/bad.go imports forbidden package db (boundary web->db)",
		"explanation":   "Importing db from web violates the web->db boundary declared in .kern/boundaries.json.",
		"suggested_fix": "Remove the dependency from web on db, or use an allow rule.",
		"evidence": []map[string]interface{}{
			{"kind": "import-edge", "description": "web/bad.go -> db", "location": "web/bad.go:3"},
		},
	}
	sampleJSON, _ := json.Marshal(sample)

	// Pipe: initialize -> tools/list -> tools/call.
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"blueprint_repair_guidance","arguments":{"finding":` + string(sampleJSON) + `}}}`,
	}, "\n")

	srvCmd := exec.Command(binPath)
	srvCmd.Stdin = strings.NewReader(input)
	out, err := srvCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("blueprint-mcp failed: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected >=3 response lines, got %d:\n%s", len(lines), out)
	}

	// Line 2: tools/list must advertise blueprint_repair_guidance.
	var listResp struct {
		Result struct {
			Tools []struct {
				Name        string                 `json:"name"`
				Description string                 `json:"description"`
				InputSchema map[string]interface{} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listResp); err != nil {
		t.Fatalf("parse tools/list response: %v\nraw: %s", err, lines[1])
	}
	found := false
	for _, tool := range listResp.Result.Tools {
		if tool.Name != "blueprint_repair_guidance" {
			continue
		}
		found = true
		if tool.Description == "" {
			t.Error("blueprint_repair_guidance: empty description")
		}
		req, _ := tool.InputSchema["required"].([]interface{})
		if len(req) != 1 || req[0] != "finding" {
			t.Errorf("blueprint_repair_guidance: inputSchema.required = %v, want [finding]", req)
		}
	}
	if !found {
		t.Fatalf("blueprint_repair_guidance not found in tools/list; tools: %v", listResp.Result.Tools)
	}
	t.Logf("blueprint_repair_guidance advertised in tools/list")

	// Line 3: tools/call with the sample finding returns the repair contract.
	var callResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &callResp); err != nil {
		t.Fatalf("parse tools/call response: %v\nraw: %s", err, lines[2])
	}
	if callResp.Error != nil {
		t.Fatalf("tools/call returned error: %s", callResp.Error.Message)
	}
	if callResp.Result.IsError || len(callResp.Result.Content) == 0 {
		t.Fatalf("tools/call returned an error result: %+v", callResp.Result)
	}
	contract := g28ParseContract(t, ToolResult{Content: []ToolContent{{Type: "text", Text: callResp.Result.Content[0].Text}}})
	if ruleID, _ := contract["rule_id"].(string); ruleID != "architecture:forbidden-import" {
		t.Errorf("tools/call contract: rule_id = %v, want architecture:forbidden-import", ruleID)
	}
	if fix, _ := contract["suggested_fix"].(string); fix == "" {
		t.Error("tools/call contract: suggested_fix is empty")
	}
	agentContract, _ := contract["agent_contract"].(map[string]interface{})
	if actionable, _ := agentContract["is_actionable"].(bool); !actionable {
		t.Errorf("tools/call contract: agent_contract.is_actionable = false, want true")
	}
	if required, _ := agentContract["required_fields_present"].(bool); !required {
		t.Errorf("tools/call contract: agent_contract.required_fields_present = false, want true")
	}
	t.Logf("tools/call blueprint_repair_guidance result: %s", callResp.Result.Content[0].Text)
}

// g28CallRepairGuidance calls the real RepairGuidanceHandler with args,
// mirroring g5CallValidateStaged.
func g28CallRepairGuidance(t *testing.T, args json.RawMessage) ToolResult {
	t.Helper()
	h := RepairGuidanceHandler{}
	return h.Handle(context.Background(), args)
}

// g28ParseContract parses a repair-guidance ToolResult's text content as JSON.
// Fails the test if the result is an error result or not valid JSON.
func g28ParseContract(t *testing.T, tr ToolResult) map[string]interface{} {
	t.Helper()
	if tr.IsError {
		t.Fatalf("expected success result, got error: %s", tr.Content[0].Text)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &m); err != nil {
		t.Fatalf("parse repair contract JSON: %v\nraw: %s", err, tr.Content[0].Text)
	}
	return m
}
