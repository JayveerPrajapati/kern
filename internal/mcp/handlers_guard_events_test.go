package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGuardCheckPublishesEventsViaMCP mirrors the CLI test: the MCP guard
// check persists ArchitectureViolation events to .kern/events.jsonl (and
// emits them live when a relay owns the socket — covered by relay tests).
func TestGuardCheckPublishesEventsViaMCP(t *testing.T) {
	root := guardProject(t)
	_ = mcpAssertOK(t, "kern_guard_check", map[string]any{"root": root, "file": "client/client.go", "threshold": "10"})
	data, err := os.ReadFile(filepath.Join(root, ".kern", "events.jsonl"))
	if err != nil {
		t.Fatalf("read events file: %v", err)
	}
	if s := string(data); !strings.Contains(s, `"Kind":"architecture.violation"`) || !strings.Contains(s, `"Source":"guard"`) {
		t.Fatalf("events file missing guard violation event, got: %s", s)
	}
}

// TestGuardCheckPublishesWarningWhenUnconfiguredViaMCP: with no boundaries
// file the MCP guard publishes the ArchitectureWarning event, matching the
// CLI guard's behavior.
func TestGuardCheckPublishesWarningWhenUnconfiguredViaMCP(t *testing.T) {
	root := mcpProject(t) // no .kern/boundaries.json
	_ = mcpAssertOK(t, "kern_guard_check", map[string]any{"root": root, "file": "app.go"})
	data, err := os.ReadFile(filepath.Join(root, ".kern", "events.jsonl"))
	if err != nil {
		t.Fatalf("read events file: %v", err)
	}
	if s := string(data); !strings.Contains(s, `"Kind":"architecture.warning"`) {
		t.Fatalf("events file missing unconfigured warning, got: %s", s)
	}
}
