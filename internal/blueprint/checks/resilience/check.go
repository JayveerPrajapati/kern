// Package resilience implements the opt-in Phase 9 resilience check (spec
// Section 18). It loads the built-in plus declarative fault-injection
// scenarios (.blueprint/scenarios/*.yaml) and runs every scenario that applies
// to the repository.
//
// The check is ALWAYS WARN-only: findings carry WARN severity and every
// failure path — invalid YAML, failed Prepare, failed scenario run — degrades
// to a WARN finding rather than a tool error, so the resilience check can
// never block a change (spec line 1084-style invariant). It is opt-in behind
// `blueprint check --resilience` because fault injection is slow.
package resilience

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	scenarios "github.com/JayveerPrajapati/kern/internal/blueprint/resilience"
)

// Check implements service.Check for resilience fault-injection scenarios.
type Check struct {
	sandboxTimeout time.Duration
}

// NewCheck constructs a resilience check. Scenarios run with a per-command
// sandbox timeout so a hung `go test` cannot run forever.
func NewCheck() *Check {
	return &Check{sandboxTimeout: 60 * time.Second}
}

// Name is the stable check identifier used in CheckResult.Name and policy
// routing. Shared with the service via resilience.CheckName so the service can
// recognize (and record the absence of) this check (P2-2).
func (Check) Name() string { return scenarios.CheckName }

// Run loads all scenarios for the repository and executes each applicable one
// (Prepare → Run → Cleanup), converting every failure into a WARN finding.
// It never returns a non-nil error and never emits a BLOCK.
func (c *Check) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	if req.RepositoryRoot == "" {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "repository root required"}, nil
	}

	scen, err := scenarios.LoadAll(req.RepositoryRoot)
	if err != nil {
		// Invalid YAML must never hard-fail the check (WARN-only invariant).
		return c.warnResult([]domain.Finding{
			c.warnFinding("could not load resilience scenarios: " + err.Error()),
		}), nil
	}

	info := scenarios.DetectRepoInfo(req.RepositoryRoot)
	var applicable []scenarios.Scenario
	for _, s := range scen {
		if s.Applicable(info) {
			applicable = append(applicable, s)
		}
	}
	if len(applicable) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}

	// Scenarios write their generated test file relative to the working
	// directory and the sandbox resolves "." to the repo root, so run them
	// with the repository root as the current directory (restored on return).
	oldWD, err := os.Getwd()
	if err != nil {
		return c.warnResult([]domain.Finding{c.warnFinding(fmt.Sprintf("could not determine working directory: %v", err))}), nil
	}
	if err := os.Chdir(req.RepositoryRoot); err != nil {
		return c.warnResult([]domain.Finding{c.warnFinding(fmt.Sprintf("scenario could not run: %v", err))}), nil
	}
	defer func() {
		if cdErr := os.Chdir(oldWD); cdErr != nil {
			fmt.Fprintf(os.Stderr, "resilience check: could not restore working directory %q: %v\n", oldWD, cdErr)
		}
	}()

	sb := repoSandbox{root: req.RepositoryRoot, timeout: c.sandboxTimeout}
	var findings []domain.Finding
	for _, s := range applicable {
		if err := s.Prepare(ctx); err != nil {
			// Infra failure (server could not start): WARN, never a tool error.
			findings = append(findings, c.warnFinding(fmt.Sprintf("scenario %s could not run: %v", s.ID(), err)))
			continue
		}
		res := s.Run(ctx, sb)
		_ = s.Cleanup(ctx) // best-effort; idempotent and safe after failed runs
		if !res.Passed {
			findings = append(findings, c.warnFinding(fmt.Sprintf("scenario %s failed: %s", s.ID(), res.Detail)))
		}
	}

	if len(findings) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}
	return c.warnResult(findings), nil
}

// warnFinding builds a WARN-severity resilience finding (RuleID
// "resilience:scenario", Category resilience) with no snippet.
func (c *Check) warnFinding(msg string) domain.Finding {
	return domain.Finding{
		RuleID:      "resilience:scenario",
		Severity:    domain.SeverityWarn,
		Category:    domain.CategoryResilience,
		Message:     msg,
		RuleVersion: "1",
		Confidence:  0.7,
		Scope:       "repo",
	}
}

// warnResult wraps findings in a WARN-status CheckResult.
func (c *Check) warnResult(findings []domain.Finding) domain.CheckResult {
	return domain.CheckResult{Name: c.Name(), Status: domain.StatusWarn, Findings: findings}
}

// repoSandbox implements scenarios.Sandbox by running commands directly in the
// repository root (mirroring the G9 test harness's testSandbox), with a
// bounded per-command timeout. The repoRoot argument passed by scenarios is
// "." — it is resolved to the bound repository root.
type repoSandbox struct {
	root    string
	timeout time.Duration
}

func (s repoSandbox) Run(ctx context.Context, repoRoot string, command []string) scenarios.SandboxResult {
	if len(command) == 0 {
		return scenarios.SandboxResult{Output: "no command specified", ExitCode: -1}
	}
	dir := repoRoot
	if dir == "." || dir == "" {
		dir = s.root
	}

	cmdCtx := ctx
	var cancel context.CancelFunc
	if s.timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(cmdCtx, command[0], command[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	res := scenarios.SandboxResult{
		Stdout:   string(out),
		Stderr:   "",
		Output:   string(out),
		ExitCode: 0,
		Ok:       err == nil,
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = -1
		}
		if cmdCtx.Err() == context.DeadlineExceeded {
			res.TimedOut = true
		}
	}
	return res
}
