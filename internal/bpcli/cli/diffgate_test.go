package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// the mcp-provided catalog is not injected in this package's tests.
	// Inject a fake catalog explicitly (the same SetToolInfos path
	// catalog.WithDiffgateTools uses at server construction) so the
	// schema:drift check runs (and reports the missing baseline instead of
	// erroring on an empty injection).
	fake := []diffgate.ToolInfo{{Name: "kern_fake", Phase: "meta", RiskLevel: "low", InputSchema: map[string]any{"type": "object"}}}
	diffgate.SetToolInfos(fake)
	dir := t.TempDir()

	// Write a fresh catalog doc so catalog:doc (G36) passes; without it the
	// check BLOCKs on the missing docs/tool-catalog.md and the advisory-exit
	// assertion below would fail.
	if _, err := diffgate.WriteCatalogDoc(dir, diffgate.ToolInfos()); err != nil {
		t.Fatalf("write catalog doc: %v", err)
	}
	// Write the contracts doc too: contracts:doc BLOCKs on a missing
	// docs/mcp/tool-contracts.md. This test previously only wrote the
	// catalog doc, so it failed on every machine once contracts:doc joined
	// the diff-gate check list (pre-existing CI red).
	contractsDir := filepath.Join(dir, "docs", "mcp")
	if err := os.MkdirAll(contractsDir, 0o755); err != nil {
		t.Fatalf("mkdir contracts doc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contractsDir, "tool-contracts.md"), diffgate.GenerateContractsDoc(fake), 0o644); err != nil {
		t.Fatalf("write contracts doc: %v", err)
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

// TestDiffGateDocGatesScopedToExistingDocs pins the doc-gate scoping: the
// catalog:doc (G36) / contracts:doc checks are drift guards for repos that
// COMMIT the generated docs. A repo without docs/tool-catalog.md (any
// non-kern project) must not get the checks at all — no BLOCK for their
// absence; a repo carrying the docs keeps the checks.
func TestDiffGateDocGatesScopedToExistingDocs(t *testing.T) {
	// Doc-less repo: the two doc gates are omitted entirely.
	noDocs := t.TempDir()
	checks := buildDiffGateCheckList(noDocs, nil, false, true, false)
	for _, c := range checks {
		if c.Name() == "catalog:doc" || c.Name() == "contracts:doc" {
			t.Errorf("doc gate %s present in a repo without generated docs (root %s)", c.Name(), noDocs)
		}
	}

	// Repo carrying the committed docs: both gates are included.
	withDocs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(withDocs, "docs", "mcp"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(withDocs, "docs", "tool-catalog.md"), []byte("# catalog\n"), 0o644); err != nil {
		t.Fatalf("write catalog doc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(withDocs, "docs", "mcp", "tool-contracts.md"), []byte("# contracts\n"), 0o644); err != nil {
		t.Fatalf("write contracts doc: %v", err)
	}
	checks = buildDiffGateCheckList(withDocs, nil, false, true, false)
	names := map[string]bool{}
	for _, c := range checks {
		names[c.Name()] = true
	}
	if !names["catalog:doc"] || !names["contracts:doc"] {
		t.Errorf("doc gates missing in a repo WITH generated docs (names: %v)", names)
	}
}

// TestEmitTextShowsCheckError pins the per-check text printer used by
// diff-gate: a check that errored (BLOCK/ERROR with an Error field but no
// findings — e.g. catalog:doc on a stale docs/tool-catalog.md) must print the
// error message instead of a misleading "0 findings".
func TestEmitTextShowsCheckError(t *testing.T) {
	res := domain.ValidationResult{
		Status:   domain.StatusBlock,
		ExitCode: 1,
		Checks: []domain.CheckResult{
			{Name: "catalog:doc", Status: domain.StatusBlock, Error: "docs/tool-catalog.md is stale — run `kern gen-catalog`"},
			{Name: "format:gofmt", Status: domain.StatusWarn, Findings: []domain.Finding{{RuleID: "format:gofmt"}}},
		},
	}

	stdout, _ := captureRun(t, func() { emitText(res) })
	if strings.Contains(stdout, "0 findings") {
		t.Errorf("emitText still claims 0 findings:\n%s", stdout)
	}
	if !strings.Contains(stdout, "is stale") {
		t.Errorf("emitText missing the check error message:\n%s", stdout)
	}
	// The findings-carrying check keeps its normal count line.
	if !strings.Contains(stdout, "(0ms, 1 findings)") {
		t.Errorf("emitText missing the normal findings count line:\n%s", stdout)
	}

	// emitFixText shares the printer: same behavior.
	stdout, _ = captureRun(t, func() { emitFixText(res, 1, nil, false) })
	if strings.Contains(stdout, "0 findings") {
		t.Errorf("emitFixText still claims 0 findings:\n%s", stdout)
	}
	if !strings.Contains(stdout, "is stale") {
		t.Errorf("emitFixText missing the check error message:\n%s", stdout)
	}
}

// TestDiffGateReporterEmitsProgress pins the per-check progress reporter
// wired into runDiffGate: a running line before each check (1-based index)
// and a verdict line with duration after, both on stderr (stdout stays
// reserved for the final verdict / --json document).
func TestDiffGateReporterEmitsProgress(t *testing.T) {
	rep := diffGateReporter{}
	_, stderr := captureRun(t, func() {
		rep.CheckStarted(0, 3, "format:gofmt")
		rep.CheckFinished(0, 3, "format:gofmt", domain.StatusWarn, 12*time.Millisecond)
		rep.CheckStarted(2, 3, "tests:build-test")
		rep.CheckFinished(2, 3, "tests:build-test", domain.StatusSkip, 0)
	})
	for _, want := range []string{
		"diff-gate: running check 1/3: format:gofmt...",
		"diff-gate: check format:gofmt: WARN (12ms)",
		"diff-gate: running check 3/3: tests:build-test...",
		"diff-gate: check tests:build-test: SKIP (0s)",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// TestDiffGateWiringEmitsProgressPerCheck: the diff-gate wiring — exactly the
// construction runDiffGate uses (buildDiffGateCheckList + service.New with the
// reporter) — emits one stderr progress line per check while validating a
// non-empty change. This is the anti-silence guard for the QA-reproduced bug:
// the sandboxed tests:build-test check could consume the whole gate budget
// with zero output until the single final verdict.
func TestDiffGateWiringEmitsProgressPerCheck(t *testing.T) {
	dir := t.TempDir()
	// Unformatted Go source → format:gofmt WARN finding (same fixture shape
	// as TestDiffGateServiceVerdicts); noTests=true keeps the run fast and
	// deterministic (no sandbox build).
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){\n}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	checks := buildDiffGateCheckList(dir, nil, false, true, false)
	req := domain.ChangeRequest{
		RepositoryRoot: dir,
		Source:         domain.SourceHuman,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: "main.go", Op: domain.OpWrite}},
	}
	svc := service.New(checks, service.WithCheckReporter(diffGateReporter{}))

	_, stderr := captureRun(t, func() {
		svc.Validate(context.Background(), req)
	})

	if !strings.Contains(stderr, "diff-gate: running check 1/") {
		t.Errorf("stderr missing the running-check progress line:\n%s", stderr)
	}
	if !strings.Contains(stderr, "diff-gate: check ") {
		t.Errorf("stderr missing the completed-check line:\n%s", stderr)
	}
	// One running line per registered check, in order.
	if got := strings.Count(stderr, "diff-gate: running check "); got != len(checks) {
		t.Errorf("running-check lines = %d, want %d (one per check)\n%s", got, len(checks), stderr)
	}
}

// stubCheck is a minimal service.Check test double for wiring tests that need
// a fast, deterministic check.
type stubCheck struct {
	name   string
	status domain.Status
}

func (s stubCheck) Name() string { return s.name }

func (s stubCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	return domain.CheckResult{Name: s.name, Status: s.status}, nil
}

// TestBudgetAwareCheckSkipsBelowFloor pins the budget-awareness wrapper: when
// the remaining ctx budget is below the floor, the slow sandbox check is
// skipped with an explicit SKIP verdict and rerun guidance (never burning the
// whole budget); with ample budget the wrapped check runs unchanged.
func TestBudgetAwareCheckSkipsBelowFloor(t *testing.T) {
	inner := stubCheck{name: "tests:build-test", status: domain.StatusPass}
	wrapped := &budgetAwareCheck{inner: inner, floor: 30 * time.Second}
	req := domain.ChangeRequest{RepositoryRoot: "/tmp/repo", Files: []domain.FileChange{{Path: "main.go", Op: domain.OpEdit}}}

	// Exhausted budget (2s < 30s floor) → SKIP with guidance.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Second))
	defer cancel()
	cr, err := wrapped.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run err = %v, want nil", err)
	}
	if cr.Status != domain.StatusSkip || !cr.Skipped {
		t.Errorf("result = %+v, want StatusSkip + Skipped", cr)
	}
	if !strings.Contains(cr.Error, "insufficient remaining budget") {
		t.Errorf("skip message = %q, want it to carry rerun guidance", cr.Error)
	}
	if !strings.Contains(cr.Error, "--no-tests") {
		t.Errorf("skip message = %q, want --no-tests rerun hint", cr.Error)
	}

	// Ample budget (10m > 30s floor) → inner check runs.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel2()
	cr2, err2 := wrapped.Run(ctx2, req)
	if err2 != nil {
		t.Fatalf("Run err = %v, want nil", err2)
	}
	if cr2.Status != domain.StatusPass {
		t.Errorf("result with ample budget = %+v, want the inner PASS", cr2)
	}
}

// TestRunDiffGateEmitsProgressLines is the end-to-end anti-silence guard for
// the QA-reproduced bug: `kern diff-gate` on a non-empty diff showed ZERO
// output for up to the full budget because the service rendered one final
// verdict. runDiffGate must now emit per-check progress lines to stderr while
// it runs. Uses a real (tiny) git repo with a staged change — the exact shape
// the gate operates on; an unborn HEAD (no commit) is fine because
// discoverWorkingTreeChanges falls back to the staged set.
func TestRunDiffGateEmitsProgressLines(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){\n}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	git("add", "main.go")

	// Force degraded mode: a bogus KERN_BINARY makes newKernClientOrDegraded
	// resolve nothing and return a nil client WITHOUT attempting the
	// auto-install (which could hit the network), so the secret check is
	// omitted and the run stays fast, offline, and deterministic.
	t.Setenv("KERN_BINARY", filepath.Join(dir, "no-such-kern"))

	code := 0
	stdout, stderr := captureRun(t, func() {
		code = runDiffGate([]string{"--root", dir, "--no-tests", "--timeout", "30"})
	})

	if code != 0 && code != 1 && code != 2 {
		t.Fatalf("runDiffGate exit = %d, want a completed run (0/1/2)", code)
	}
	if !strings.Contains(stderr, "diff-gate: running check ") {
		t.Errorf("stderr missing running-check progress (the core anti-silence fix):\n%s", stderr)
	}
	if !strings.Contains(stderr, "diff-gate: check ") {
		t.Errorf("stderr missing completed-check lines:\n%s", stderr)
	}
	// Text verdict still lands on stdout (the gate's normal output).
	if !strings.Contains(stdout, "blueprint: ") {
		t.Errorf("stdout missing the final verdict:\n%s", stdout)
	}
}
