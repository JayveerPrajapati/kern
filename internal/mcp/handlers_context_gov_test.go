package mcp

import (
	"strings"
	"testing"
)

// TestHandleCompactGovernedByScope pins C6 (deep-dive 2026-10-03):
// kern_compact_file serves source, so it must respect the same authorized
// scope as the governed retrieval family. A task scope denying secret/
// refuses the read, the default (project-wide) scope keeps it working, and
// an allowed sibling passes under the denied scope.
func TestHandleCompactGovernedByScope(t *testing.T) {
	root := provenanceProject(t)

	// Default scope (no agent_id): the whole project root is in scope, so
	// the compact summary of the secret file renders normally.
	text := mcpAssertOK(t, "kern_compact_file", map[string]any{
		"root": root,
		"path": "secret/b.go",
		"tier": "summary",
	})
	if !strings.Contains(text, "SecretB") {
		t.Fatalf("default-scoped compact missing symbol summary: %q", text)
	}

	// Task scope denying secret/: the file's symbols are out of scope, so
	// the read is refused before any source is served.
	errText := mcpToolError(t, "kern_compact_file", map[string]any{
		"root":  root,
		"path":  "secret/b.go",
		"tier":  "summary",
		"scope": deniedScope(),
	})
	if !strings.Contains(errText, "authorized read scope") {
		t.Fatalf("denied compact error = %q, want scope refusal", errText)
	}
	if strings.Contains(errText, "SecretB") || strings.Contains(errText, "hidden") {
		t.Fatalf("denied compact leaked content: %q", errText)
	}

	// Allowed sibling under the same denied scope still passes.
	text = mcpAssertOK(t, "kern_compact_file", map[string]any{
		"root":  root,
		"path":  "public/a.go",
		"tier":  "summary",
		"scope": deniedScope(),
	})
	if !strings.Contains(text, "PublicA") {
		t.Fatalf("allowed compact missing symbol summary: %q", text)
	}
}

// TestHandleFitContextGovernedByScope pins the same contract for
// kern_fit_context: its files/symbols arguments are gated against the
// authorized scope before any source is fitted into the budget.
func TestHandleFitContextGovernedByScope(t *testing.T) {
	root := provenanceProject(t)

	// Default scope: fitting the secret file works.
	mcpAssertOK(t, "kern_fit_context", map[string]any{
		"root":       root,
		"files":      "secret/b.go",
		"max_tokens": "500",
	})

	// Denied scope on the file path: refused.
	errText := mcpToolError(t, "kern_fit_context", map[string]any{
		"root":       root,
		"files":      "secret/b.go",
		"max_tokens": "500",
		"scope":      deniedScope(),
	})
	if !strings.Contains(errText, "authorized read scope") {
		t.Fatalf("denied fit_context error = %q, want scope refusal", errText)
	}

	// Denied scope on the symbol list: refused before fitting.
	errText = mcpToolError(t, "kern_fit_context", map[string]any{
		"root":       root,
		"symbols":    "SecretB",
		"max_tokens": "500",
		"scope":      deniedScope(),
	})
	if !strings.Contains(errText, "authorized read scope") {
		t.Fatalf("denied fit_context symbol error = %q, want scope refusal", errText)
	}

	// Allowed symbol under the same denied scope passes.
	mcpAssertOK(t, "kern_fit_context", map[string]any{
		"root":       root,
		"symbols":    "PublicA",
		"max_tokens": "500",
		"scope":      deniedScope(),
	})
}

// TestCompactGatePermissivePassThrough verifies the escape hatch:
// KERN_MCP_PERMISSIVE=1 restores raw mode for the gated tools too.
func TestCompactGatePermissivePassThrough(t *testing.T) {
	t.Setenv("KERN_MCP_PERMISSIVE", "1")
	root := provenanceProject(t)
	mcpAssertOK(t, "kern_compact_file", map[string]any{
		"root":  root,
		"path":  "secret/b.go",
		"tier":  "summary",
		"scope": deniedScope(),
	})
}
