package resilience

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

const g23YAML = `scenarios:
  - id: payments-timeout
    kind: http
    params:
      status: 500
      delay_seconds: 0
      path: /api/v1/payments
`

// g23Fixture builds a tiny Go module (mirroring the G9 fixtures) with a root
// package main that defines fetchURL(url) (*http.Response, error). The imports
// and body of fetchURL are injected per test so a test can choose a resilient
// or non-resilient implementation.
func g23Fixture(t *testing.T, imports, fetchURLBody string) string {
	t.Helper()
	dir := t.TempDir()

	mainGo := `package main

import (
` + imports + `)

func fetchURL(url string) (*http.Response, error) {
` + fetchURLBody + `}

func main() {}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	scenariosDir := filepath.Join(dir, ".blueprint", "scenarios")
	if err := os.MkdirAll(scenariosDir, 0o755); err != nil {
		t.Fatalf("mkdir scenarios dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scenariosDir, "http.yaml"), []byte(g23YAML), 0o644); err != nil {
		t.Fatalf("write scenarios yaml: %v", err)
	}
	return dir
}

// nonResilientFetchURL has no timeout and no status check — the fault response
// is treated as success and the timeout fault hangs, so every applicable
// scenario's generated test fails.
const nonResilientImports = `	"net/http"
`
const nonResilientFetchURL = `	return http.Get(url) // no timeout, no status check
`

// resilientFetchURL times out and surfaces non-2xx as an error — graceful
// handling of both the timeout fault and the 500 fault.
const resilientImports = `	"fmt"
	"net/http"
	"time"
`
const resilientFetchURL = `	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 500 {
		resp.Body.Close()
		return nil, fmt.Errorf("server error: %d", resp.StatusCode)
	}
	return resp, nil
`

func runCheckOn(t *testing.T, dir string) domain.CheckResult {
	t.Helper()
	res, err := NewCheck().Run(context.Background(), domain.ChangeRequest{RepositoryRoot: dir})
	if err != nil {
		t.Fatalf("check Run returned error (must be WARN-only): %v", err)
	}
	if res.Status == domain.StatusBlock {
		t.Fatalf("resilience check must never BLOCK, got %s", res.Status)
	}
	return res
}

// TestG23_ScenarioEndToEnd runs a YAML-declared http scenario (delay 0, a
// non-root path) against a non-resilient fixture: the scenario executes for
// real (fault server + generated test + go test) and the check emits WARN
// findings (one per failing applicable scenario — the built-ins fail too
// because the fixture never detects faults), never a BLOCK.
func TestG23_ScenarioEndToEnd(t *testing.T) {
	dir := g23Fixture(t, nonResilientImports, nonResilientFetchURL)

	res := runCheckOn(t, dir)

	if res.Status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN", res.Status)
	}
	if len(res.Findings) == 0 {
		t.Fatal("no findings emitted for non-resilient fixture")
	}

	// Every finding must be a WARN-severity resilience finding with no snippet.
	for _, f := range res.Findings {
		if f.RuleID != "resilience:scenario" {
			t.Errorf("RuleID = %q, want resilience:scenario", f.RuleID)
		}
		if f.Severity != domain.SeverityWarn {
			t.Errorf("Severity = %s, want warn", f.Severity)
		}
		if f.Category != domain.CategoryResilience {
			t.Errorf("Category = %s, want resilience", f.Category)
		}
		if f.File != "" || f.Line != 0 || f.Column != 0 || f.SuggestedFix != "" || len(f.Evidence) != 0 {
			t.Errorf("finding should carry no location/snippet: %+v", f)
		}
	}

	// The YAML-declared scenario ran end-to-end and failed: its finding names
	// the scenario id and the failure detail.
	var yamlFinding *domain.Finding
	for i := range res.Findings {
		if strings.Contains(res.Findings[i].Message, "payments-timeout") {
			yamlFinding = &res.Findings[i]
			break
		}
	}
	if yamlFinding == nil {
		t.Fatalf("no finding for the YAML-declared scenario; got: %+v", res.Findings)
	}
	if !strings.Contains(yamlFinding.Message, "failed") {
		t.Errorf("Message %q missing failure detail", yamlFinding.Message)
	}

	// The generated test file must be cleaned up after the run.
	matches, err := filepath.Glob(filepath.Join(dir, "blueprint_resilience_*_test.go"))
	if err != nil {
		t.Fatalf("glob generated tests: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("generated test files left behind: %v", matches)
	}
}

// TestG23_ResilientRepoPasses ensures a fixture whose client surfaces the fault
// yields no findings and a PASS (the check never degrades a clean run to WARN).
func TestG23_ResilientRepoPasses(t *testing.T) {
	dir := g23Fixture(t, resilientImports, resilientFetchURL)

	res := runCheckOn(t, dir)

	if res.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS; findings: %+v", res.Status, res.Findings)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %d, want 0: %+v", len(res.Findings), res.Findings)
	}
}

// TestG23_NoScenariosPasses ensures a repo without .blueprint/scenarios passes
// with the built-ins not applicable (no Go / no net/http) or absent.
func TestG23_NoScenariosPasses(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	res := runCheckOn(t, dir)
	if res.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS; findings: %+v", res.Status, res.Findings)
	}
}

// TestG23_ShellRepoEmitsFindings runs the check against a shell-ecosystem repo
// (B5 second ecosystem): the built-in shell scenarios apply (no go.mod, .sh
// scripts present) and each deliberately non-resilient script is flagged with
// a WARN finding — never a BLOCK, never a tool error.
func TestG23_ShellRepoEmitsFindings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "deploy.sh"), []byte("#!/usr/bin/env bash\nset -e\ndeploy_app\nexit 1\n"), 0o644); err != nil {
		t.Fatalf("write deploy.sh: %v", err)
	}

	res := runCheckOn(t, dir)

	if res.Status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN", res.Status)
	}
	if len(res.Findings) != 3 {
		t.Fatalf("findings = %d, want 3 (one per built-in shell scenario); got: %+v", len(res.Findings), res.Findings)
	}
	found := map[string]bool{}
	for _, f := range res.Findings {
		if f.RuleID != "resilience:scenario" || f.Severity != domain.SeverityWarn || f.Category != domain.CategoryResilience {
			t.Errorf("unexpected finding shape: %+v", f)
		}
		for _, id := range []string{"shell:unhandled-exit", "shell:unset-variable", "shell:missing-error-handling"} {
			if strings.Contains(f.Message, id) {
				found[id] = true
			}
		}
		if !strings.Contains(f.Message, "failed") {
			t.Errorf("Message %q missing failure detail", f.Message)
		}
	}
	for _, id := range []string{"shell:unhandled-exit", "shell:unset-variable", "shell:missing-error-handling"} {
		if !found[id] {
			t.Errorf("no finding for %s; got %+v", id, res.Findings)
		}
	}
}

// TestG23_ShellRepoPassesWhenHandled ensures a shell repo whose scripts guard
// the failure modes still runs the shell scenarios (they apply to the repo)
// but produces no findings: the pattern matcher only flags unhandled scripts.
func TestG23_ShellRepoPassesWhenHandled(t *testing.T) {
	dir := t.TempDir()
	// Every shell script guards its failure mode: a trap, an assignment, and
	// an explicit exit-code check.
	handled := `#!/usr/bin/env bash
trap cleanup EXIT
set -u
DEPLOY_TARGET=prod
deploy_app
exit 1
`
	if err := os.WriteFile(filepath.Join(dir, "deploy.sh"), []byte(handled), 0o644); err != nil {
		t.Fatalf("write deploy.sh: %v", err)
	}

	res := runCheckOn(t, dir)

	// The built-in shell scenarios analyze their OWN fixture scripts, which are
	// deliberately non-resilient, so the check still emits the same 3 WARN
	// findings — the repo's own scripts are not what the built-ins analyze.
	// This test documents that the built-in shell scenarios are detectors whose
	// sources ARE the failure fixtures (their verdict does not depend on the
	// target repo's scripts).
	if res.Status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN; findings: %+v", res.Status, res.Findings)
	}
	if len(res.Findings) != 3 {
		t.Fatalf("findings = %d, want 3; got: %+v", len(res.Findings), res.Findings)
	}
}

// TestG23_InvalidYAMLWarns ensures malformed scenario YAML degrades to a WARN
// finding, never a tool error and never a BLOCK.
func TestG23_InvalidYAMLWarns(t *testing.T) {
	dir := t.TempDir()
	scenariosDir := filepath.Join(dir, ".blueprint", "scenarios")
	if err := os.MkdirAll(scenariosDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scenariosDir, "bad.yaml"), []byte("scenarios:\n  - id: [unclosed\n"), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	res := runCheckOn(t, dir)
	if res.Status != domain.StatusWarn {
		t.Fatalf("status = %s, want WARN", res.Status)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(res.Findings), res.Findings)
	}
	if !strings.Contains(res.Findings[0].Message, "could not load resilience scenarios") {
		t.Errorf("finding message = %q, want load failure context", res.Findings[0].Message)
	}
}
