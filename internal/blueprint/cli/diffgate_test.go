package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/checks/diffgate"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
)

// TestDiffGateServiceVerdicts builds the diff-gate check set on a tiny
// t.TempDir() repo and asserts structured verdicts plus the advisory exit
// semantics: WARN findings must NOT change the exit code (0), and
// --blocking elevation must flip the aggregate to BLOCK (exit 1).
func TestDiffGateServiceVerdicts(t *testing.T) {
	// internal/blueprint/cli cannot import internal/mcp (import cycle), so
	// the mcp-provided catalog is not registered in this package's tests.
	// Register a fake catalog so the schema:drift check runs (and reports the
	// missing baseline instead of skipping).
	diffgate.SetCatalogProvider(func() []diffgate.ToolInfo {
		return []diffgate.ToolInfo{{Name: "kern_fake", Phase: "meta", RiskLevel: "low", InputSchema: map[string]any{"type": "object"}}}
	})
	dir := t.TempDir()

	// Write a fresh catalog doc so catalog:doc (G36) passes; without it the
	// check BLOCKs on the missing docs/tool-catalog.md and the advisory-exit
	// assertion below would fail.
	if _, err := diffgate.WriteCatalogDoc(dir); err != nil {
		t.Fatalf("write catalog doc: %v", err)
	}

	// Unformatted Go source → format:gofmt WARN finding.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){\n}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	// os/exec usage in a non-test file → exec:unsafe WARN finding.
	if err := os.WriteFile(filepath.Join(dir, "exec.go"), []byte("package main\nimport \"os/exec\"\nfunc f() { exec.Command(\"sh\", \"-c\", \"ls\") }\n"), 0o644); err != nil {
		t.Fatalf("write exec.go: %v", err)
	}

	req := domain.ChangeRequest{
		RepositoryRoot: dir,
		Source:         domain.SourceHuman,
		Operation:      domain.OpCommit,
		Files: []domain.FileChange{
			{Path: "main.go", Op: domain.OpWrite},
			{Path: "exec.go", Op: domain.OpWrite},
		},
	}

	// noTests=true keeps the run fast and deterministic (no sandbox build);
	// the secret check is added only when a kern binary resolves.
	checks := buildDiffGateCheckList(dir, nil, false, true, false)
	svc := service.New(checks)
	result := svc.Validate(context.Background(), req)

	// Advisory exit semantics: WARN must not fail the gate.
	if result.ExitCode != 0 {
		t.Fatalf("advisory exit = %d, want 0 (WARN must not fail); status=%s", result.ExitCode, result.Status)
	}
	if result.Status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN", result.Status)
	}

	// Structured verdicts: every diff-gate check produced a per-check result
	// and the expected advisory findings are present.
	byRule := map[string]bool{}
	for _, f := range result.Findings {
		byRule[f.RuleID] = true
	}
	for _, want := range []string{"format:gofmt", "exec:unsafe", "changelog:missing", "schema:drift"} {
		if !byRule[want] {
			t.Errorf("missing finding rule %s (findings: %v)", want, result.Findings)
		}
	}
	if len(result.Checks) == 0 {
		t.Errorf("no per-check results emitted")
	}

	// --blocking elevation: WARN → BLOCK, exit 1.
	blocked := applyBlocking(result, true)
	if blocked.Status != domain.StatusBlock || blocked.ExitCode != 1 {
		t.Errorf("blocking elevation = %s/%d, want BLOCK/1", blocked.Status, blocked.ExitCode)
	}

	// Non-blocking leaves the advisory verdict untouched.
	advisory := applyBlocking(result, false)
	if advisory.Status != domain.StatusWarn || advisory.ExitCode != 0 {
		t.Errorf("advisory unchanged = %s/%d, want WARN/0", advisory.Status, advisory.ExitCode)
	}
}

// TestDiffGateParseFlags pins the diff-gate flag surface.
func TestDiffGateParseFlags(t *testing.T) {
	fl, code := parseDiffGateFlags([]string{"--root", "/tmp/x", "--timeout", "30", "--blocking", "--json", "--no-tests", "--init-baseline"})
	if code != 0 {
		t.Fatalf("parse code = %d, want 0", code)
	}
	if fl.root != "/tmp/x" || fl.timeoutSec != 30 || !fl.blocking || !fl.jsonOut || !fl.noTests || !fl.initBaseline {
		t.Errorf("parsed flags = %+v, want all set", fl)
	}
	// Defaults.
	fl2, code := parseDiffGateFlags(nil)
	if code != 0 {
		t.Fatalf("parse code = %d, want 0", code)
	}
	if fl2.root != "." || fl2.timeoutSec != 0 || fl2.blocking || fl2.jsonOut || fl2.noTests || fl2.initBaseline {
		t.Errorf("default flags = %+v, want root=. timeout=0 (auto: config execution.timeout_seconds, else 120) advisory text-only", fl2)
	}
	// Unknown flag → usage error (2).
	if _, code := parseDiffGateFlags([]string{"--bogus"}); code != 2 {
		t.Errorf("unknown flag code = %d, want 2", code)
	}
}
