package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

func TestHandleAgentFingerprint(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	s.audit = governance.NewAuditLog()

	agentID := "test-agent-42"

	// Record sequence of normal tool calls
	for _, action := range []string{"kern_search", "kern_context", "kern_pre_edit", "kern_plan"} {
		s.audit.Record(governance.AuditEntry{
			AgentID:   agentID,
			Action:    action,
			Timestamp: time.Now(),
			Risk:      domain.Risk{Level: domain.RiskLow},
			Result:    "allowed",
		})
	}

	// Test 1: Normal behavior pattern
	res, err := s.handleAgentFingerprint(context.Background(), map[string]any{
		"agent_id": agentID,
	})
	if err != nil {
		t.Fatalf("handleAgentFingerprint error: %v", err)
	}

	if !strings.Contains(res, "AGENT BEHAVIOR FINGERPRINT") {
		t.Errorf("missing header in response: %s", res)
	}
	if !strings.Contains(res, "NORMAL") {
		t.Errorf("expected NORMAL status in response: %s", res)
	}

	// Test 2: Detect repetitive loop
	for i := 0; i < 4; i++ {
		s.audit.Record(governance.AuditEntry{
			AgentID:   agentID,
			Action:    "kern_search",
			Timestamp: time.Now(),
			Risk:      domain.Risk{Level: domain.RiskLow},
			Result:    "allowed",
		})
	}

	resLoop, err := s.handleAgentFingerprint(context.Background(), map[string]any{
		"agent_id": agentID,
		"format":   "json",
	})
	if err != nil {
		t.Fatalf("handleAgentFingerprint loop test error: %v", err)
	}

	var report AgentFingerprintReport
	if err := json.Unmarshal([]byte(resLoop), &report); err != nil {
		t.Fatalf("failed to parse JSON fingerprint report: %v", err)
	}
	if !report.LoopDetected {
		t.Errorf("expected LoopDetected=true for consecutive identical tool calls")
	}
	if report.HealthStatus != "LOOPING" {
		t.Errorf("expected health LOOPING, got %s", report.HealthStatus)
	}
}
