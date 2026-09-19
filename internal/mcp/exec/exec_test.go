package exec

import (
	"context"
	"strings"
	"testing"
)

func TestSandboxEmptyCommand(t *testing.T) {
	ctx := context.Background()
	_, err := Sandbox(ctx, "test", map[string]any{
		"command": "",
	})
	if err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("expected error on empty command, got: %v", err)
	}
}

func TestRunBuildEmptyCommand(t *testing.T) {
	ctx := context.Background()
	_, err := RunBuild(ctx, "test", map[string]any{
		"command": "",
	})
	if err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("expected error on empty command, got: %v", err)
	}
}

func TestExecList(t *testing.T) {
	ctx := context.Background()
	res, err := Exec(ctx, map[string]any{
		"list": "true",
	})
	if err != nil {
		t.Fatalf("Exec list failed: %v", err)
	}
	if !strings.Contains(res, "installed runtimes:") {
		t.Errorf("expected installed runtimes in list output, got: %s", res)
	}
}
