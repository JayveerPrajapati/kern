package main

import (
	"strings"
	"testing"
)

// --- P2-3 (G24): latency budget gate (end-to-end, real kern) ---

const latencyBudgetConfig = "version: 1\nmode: enforce\nexecution:\n  staged_latency_budget_ms: 1\n"

const cleanWebMain = "package web\nfunc Handle() {}\n"
const cleanWebExtra = "package web\nfunc Extra() {}\n"

// latencyBudgetRepo builds a clean repo whose .blueprint/config.yaml sets a
// 1ms staged latency budget (committed on main, so the ci worktree sees it).
func latencyBudgetRepo(t *testing.T) string {
	t.Helper()
	return g11Repo(t,
		map[string]string{
			"web/web.go":             cleanWebMain,
			".blueprint/config.yaml": latencyBudgetConfig,
		},
		map[string]string{
			"web/extra.go": cleanWebExtra,
		},
	)
}

// TestG24_CIArtifactCarriesLatencyFindingAndChecks verifies that `blueprint ci`
// on a repo with a 1ms budget exits 0 (WARN never blocks) and that the JSON
// artifact carries the performance:latency-budget finding, the per-check
// breakdown (name/status/duration_ms), and latency_budget_ms.
func TestG24_CIArtifactCarriesLatencyFindingAndChecks(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	dir := latencyBudgetRepo(t)

	_, _, code, artifact := runCICommand(t, binPath, dir, kernPath, "--json")
	if code != 0 {
		t.Fatalf("ci exit = %d, want 0 (WARN never blocks)", code)
	}

	found := false
	for _, f := range artifact.Findings {
		if f.RuleID == "performance:latency-budget" {
			found = true
		}
	}
	if !found {
		t.Fatalf("artifact missing performance:latency-budget finding: %+v", artifact.Findings)
	}
	if len(artifact.Checks) == 0 {
		t.Fatalf("artifact missing checks array: %+v", artifact)
	}
	for _, c := range artifact.Checks {
		if c.Name == "" {
			t.Errorf("check with empty name: %+v", c)
		}
		if c.Status == "" {
			t.Errorf("check %s with empty status", c.Name)
		}
		if c.DurationMs == 0 {
			t.Errorf("check %s duration_ms = 0, want > 0", c.Name)
		}
	}
	if artifact.LatencyBudgetMs != 1 {
		t.Errorf("artifact latency_budget_ms = %d, want 1", artifact.LatencyBudgetMs)
	}
}

// TestG24_StrictLatencyHardFails verifies that `blueprint ci --strict-latency`
// upgrades the WARN-only latency finding to a hard CI failure (exit 1) while
// the artifact still carries the finding and the per-check breakdown.
func TestG24_StrictLatencyHardFails(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	dir := latencyBudgetRepo(t)

	_, _, code, artifact := runCICommand(t, binPath, dir, kernPath, "--strict-latency", "--json")
	if code != 1 {
		t.Fatalf("ci --strict-latency exit = %d, want 1", code)
	}
	found := false
	for _, f := range artifact.Findings {
		if f.RuleID == "performance:latency-budget" {
			found = true
		}
	}
	if !found {
		t.Fatalf("strict artifact missing performance:latency-budget finding: %+v", artifact.Findings)
	}
	if len(artifact.Checks) == 0 {
		t.Fatalf("strict artifact missing checks array: %+v", artifact)
	}
}

// TestG24_CheckHookNeverBlocksOnLatency verifies the pre-commit hook path
// (`blueprint check --staged`): the latency finding renders as a WARN and the
// exit stays 0 — the hook must never block on latency.
func TestG24_CheckHookNeverBlocksOnLatency(t *testing.T) {
	_ = requireKernPath(t)
	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.23\n")
	g4WriteFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	g4WriteFile(t, dir, ".blueprint/config.yaml", latencyBudgetConfig)
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")
	g4WriteFile(t, dir, "main.go", "package main\n\nfunc main() {}\n\n// staged change\n")
	g4RunGit(t, dir, "add", "main.go")

	out, code := g4BlueprintCheck(t, bin, dir)
	if code != 0 {
		t.Fatalf("check exit = %d, want 0 (hook never blocks on latency); output:\n%s", code, out)
	}
	if !strings.Contains(out, "performance:latency-budget") {
		t.Fatalf("output missing latency finding:\n%s", out)
	}
	if !strings.Contains(out, "WARN") {
		t.Fatalf("output missing WARN status:\n%s", out)
	}
}

// TestG24_CINoBudgetNoLatencyFinding verifies that without a configured
// budget the ci artifact carries no performance finding and latency_budget_ms
// stays 0.
func TestG24_CINoBudgetNoLatencyFinding(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	dir := g11Repo(t,
		map[string]string{
			"web/web.go": cleanWebMain,
		},
		map[string]string{
			"web/extra.go": cleanWebExtra,
		},
	)

	_, _, code, artifact := runCICommand(t, binPath, dir, kernPath, "--json")
	if code != 0 {
		t.Fatalf("ci exit = %d, want 0", code)
	}
	for _, f := range artifact.Findings {
		if f.RuleID == "performance:latency-budget" {
			t.Fatalf("unexpected latency finding without a configured budget: %+v", f)
		}
	}
	if artifact.LatencyBudgetMs != 0 {
		t.Errorf("artifact latency_budget_ms = %d, want 0 (unset)", artifact.LatencyBudgetMs)
	}
}
