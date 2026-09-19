package agentctl

import (
	"context"
	"strings"
	"testing"
)

func TestAgentMessageEmptyTo(t *testing.T) {
	ctx := context.Background()
	_, err := AgentMessage(ctx, Hooks{}, map[string]any{
		"to_agent": "",
		"notes":    "hello",
	})
	if err == nil || !strings.Contains(err.Error(), "to_agent is required") {
		t.Fatalf("expected error on empty to_agent, got: %v", err)
	}
}

func TestAgentInterruptEmptyTaskID(t *testing.T) {
	ctx := context.Background()
	_, err := AgentInterrupt(ctx, Hooks{}, map[string]any{
		"task_id": "",
	})
	if err == nil || !strings.Contains(err.Error(), "task_id is required") {
		t.Fatalf("expected error on empty task_id, got: %v", err)
	}
}

func TestLLMProvidersReport(t *testing.T) {
	ctx := context.Background()
	res, err := LLMProviders(ctx, map[string]any{
		"probe": false,
	})
	if err != nil {
		t.Fatalf("LLMProviders failed: %v", err)
	}
	if !strings.Contains(res, "provider:") {
		t.Errorf("expected provider header, got: %s", res)
	}
}
