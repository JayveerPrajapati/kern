package sandbox

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// Check implements the sandbox validation as a Blueprint service.Check (spec
// Phase 8). It runs build and test commands in an isolated git worktree and
// maps the result to a ValidationResult.
//
// If the build or tests fail, the check produces a BLOCK finding with the
// truncated output as evidence. If the sandbox itself fails (timeout, process
// error), it produces an ERROR.
type Check struct {
	config Config
}

// NewCheck constructs a SandboxCheck with the given config.
func NewCheck(cfg Config) Check {
	return Check{config: cfg}
}

// ConfigOption configures a Config when constructing a Check.
type ConfigOption func(*Config)

// WithNetworkIsolation returns a ConfigOption that isolates the sandboxed
// process from the host network (Linux: new network namespace via
// CLONE_NEWNET). On platforms without isolation support the sandbox fails
// closed unless WithAllowUnisolated is also given.
func WithNetworkIsolation() ConfigOption {
	return func(c *Config) { c.NetworkIsolated = true }
}

// WithAllowUnisolated returns a ConfigOption that explicitly permits running
// WITHOUT network isolation on platforms that cannot provide it. It only has
// effect alongside WithNetworkIsolation: without it, requesting isolation on
// an unsupported platform fails closed instead of running unisolated. The
// overridden run still emits a visible warning to stderr.
func WithAllowUnisolated() ConfigOption {
	return func(c *Config) { c.AllowUnisolated = true }
}

// NewDefaultCheck constructs a SandboxCheck with default config, optionally
// customized by the given ConfigOptions.
func NewDefaultCheck(opts ...ConfigOption) Check {
	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return Check{config: cfg}
}

// Name returns the stable check identifier for policy routing. The policy
// engine derives the enforcement category from the check-name prefix
// (categoryFromCheck), so the prefix MUST be the `tests` category (whose
// policy defaults to block) — a "sandbox" prefix would route to no rule and
// silently downgrade build/test failures to WARN.
func (Check) Name() string { return "tests:build-test" }

// Run executes the build and test commands in an isolated worktree.
// It runs `go build ./...` first, then `go test ./...` if the build passes.
func (c Check) Run(ctx context.Context, req domain.ChangeRequest) (result domain.CheckResult, err error) {
	if req.RepositoryRoot == "" {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "repository root required"}, nil
	}

	// Network isolation was requested and the caller explicitly accepted
	// running without it (--allow-unisolated). Surface that as a finding so
	// the artifact records that egress was NOT contained — never a silent
	// unisolated run. When the override was NOT granted, Run() fails closed
	// before any command executes, so no finding is needed there. Strict mode
	// (BLUEPRINT_REQUIRE_NETISO=1) escalates to BLOCK even over an override.
	var isolationFinding *domain.Finding
	if c.config.NetworkIsolated && !networkIsolationAvailable() && c.config.AllowUnisolated {
		sev := domain.SeverityWarn
		strict := os.Getenv("BLUEPRINT_REQUIRE_NETISO") == "1"
		if strict {
			sev = domain.SeverityBlock
		}
		isolationFinding = &domain.Finding{
			RuleID:       "sandbox:network-isolation-unavailable",
			Severity:     sev,
			Category:     domain.CategoryTests,
			Message:      "network isolation requested but unavailable on this platform; sandbox ran without egress containment",
			Explanation:  "Network isolation requires Linux CLONE_NEWNET or macOS sandbox-exec. On this platform the sandboxed build/test can reach the network, so a failing or malicious test could make external calls. The sandbox result is still valid for build/test correctness, but egress was not contained.",
			SuggestedFix: "Run CI on a Linux runner for full network isolation, or set BLUEPRINT_REQUIRE_NETISO=1 to hard-fail when isolation is unavailable.",
			Scope:        "repo",
		}
	}

	// sandboxRan tracks whether we actually executed the sandbox commands,
	// so the isolation finding is only attached to real sandbox runs (not the
	// repo-root guard or other pre-run errors).
	sandboxRan := false
	defer func() {
		if isolationFinding != nil && sandboxRan {
			result.Findings = append(result.Findings, *isolationFinding)
			// Strict mode escalates a non-ERROR result to BLOCK.
			if isolationFinding.Severity == domain.SeverityBlock && result.Status != domain.StatusError {
				result.Status = domain.StatusBlock
			}
		}
	}()

	sandboxRan = true
	worktreePath, cleanup, err := createWorktree(req.RepositoryRoot)
	if err != nil {
		return domain.CheckResult{
			Name:   c.Name(),
			Status: domain.StatusError,
			Error:  fmt.Sprintf("create worktree: %v", err),
		}, nil
	}
	defer cleanup()
	// The check validates the CHANGE, not just the committed tree:
	// createWorktree checks out HEAD only, so without applying the staged
	// diff a staged change that breaks tests would pass whenever HEAD is
	// green, and a HEAD-only regression would block an innocent staged
	// change under a misleading "staged changes cause test failures"
	// message. Applying the staged diff (HEAD -> index) makes the worktree
	// match the tree the commit would produce; nothing staged is a no-op
	// (plain HEAD validation, the old behavior). Fail-closed: an apply
	// error is an ERROR, never a partial application.
	if serr := applyStagedDiff(req.RepositoryRoot, worktreePath); serr != nil {
		return domain.CheckResult{
			Name:   c.Name(),
			Status: domain.StatusError,
			Error:  fmt.Sprintf("apply staged changes: %v", serr),
		}, nil
	}

	targets := c.config.Matrix
	if len(targets) == 0 {
		buildCmd := []string{"go", "build", "./..."}
		if env := os.Getenv("BLUEPRINT_SANDBOX_BUILD"); env != "" {
			buildCmd = SplitCommand(env)
		}
		testCmd := []string{"go", "test", "-short", "./..."}
		if env := os.Getenv("BLUEPRINT_SANDBOX_TEST"); env != "" {
			testCmd = SplitCommand(env)
		}
		targets = []MatrixTarget{{
			Name:  "go",
			Dir:   ".",
			Build: buildCmd,
			Test:  testCmd,
		}}
	}

	for _, target := range targets {
		// Run build command if configured
		if len(target.Build) > 0 {
			buildResult := RunInWorktree(ctx, worktreePath, target.Dir, target.Build, c.config)
			if buildResult.Error != "" {
				return domain.CheckResult{
					Name:   c.Name(),
					Status: domain.StatusError,
					Error:  fmt.Sprintf("sandbox build [%s] failed: %s", target.Name, buildResult.Error),
				}, nil
			}
			if buildResult.TimedOut {
				return domain.CheckResult{
					Name:   c.Name(),
					Status: domain.StatusError,
					Error:  fmt.Sprintf("sandbox build [%s] timed out after %s", target.Name, c.config.Timeout),
				}, nil
			}
			if !buildResult.Ok {
				evidenceLoc := fmt.Sprintf("sandbox:%s", strings.Join(target.Build, " "))
				if target.Dir != "" && target.Dir != "." {
					evidenceLoc = fmt.Sprintf("sandbox:%s (%s)", strings.Join(target.Build, " "), target.Dir)
				}
				finding := domain.Finding{
					RuleID:       "sandbox:build-failure",
					Severity:     domain.SeverityBlock,
					Category:     domain.CategoryTests,
					File:         target.Dir,
					Line:         0,
					Message:      fmt.Sprintf("Build failed for %s in isolated sandbox", target.Name),
					Explanation:  fmt.Sprintf("The staged changes cause a build failure in %s (exit code %d). The target must compile before changes can be committed.", target.Name, buildResult.ExitCode),
					SuggestedFix: "Fix the compilation errors shown in the evidence, then re-run blueprint check.",
					Evidence: []domain.Evidence{{
						Kind:        "build-output",
						Description: "truncated build stderr",
						Location:    evidenceLoc,
					}},
				}
				if buildResult.Stderr != "" {
					finding.Evidence[0].Description = failureDigest(buildResult.Stderr, 2000)
				} else if buildResult.Stdout != "" {
					finding.Evidence[0].Description = failureDigest(buildResult.Stdout, 2000)
				}
				return domain.CheckResult{
					Name:     c.Name(),
					Status:   domain.StatusBlock,
					Findings: []domain.Finding{finding},
				}, nil
			}
		}

		// Run test command if configured
		if len(target.Test) > 0 {
			testResult := RunInWorktree(ctx, worktreePath, target.Dir, target.Test, c.config)
			if testResult.Error != "" {
				return domain.CheckResult{
					Name:   c.Name(),
					Status: domain.StatusError,
					Error:  fmt.Sprintf("sandbox test [%s] failed: %s", target.Name, testResult.Error),
				}, nil
			}
			if testResult.TimedOut {
				return domain.CheckResult{
					Name:   c.Name(),
					Status: domain.StatusError,
					Error:  fmt.Sprintf("sandbox test [%s] timed out after %s", target.Name, c.config.Timeout),
				}, nil
			}
			if !testResult.Ok {
				evidenceLoc := fmt.Sprintf("sandbox:%s", strings.Join(target.Test, " "))
				if target.Dir != "" && target.Dir != "." {
					evidenceLoc = fmt.Sprintf("sandbox:%s (%s)", strings.Join(target.Test, " "), target.Dir)
				}
				finding := domain.Finding{
					RuleID:       "sandbox:test-failure",
					Severity:     domain.SeverityBlock,
					Category:     domain.CategoryTests,
					File:         target.Dir,
					Line:         0,
					Message:      fmt.Sprintf("Tests failed for %s in isolated sandbox", target.Name),
					Explanation:  fmt.Sprintf("The staged changes cause test failures in %s (exit code %d). All tests must pass before changes can be committed.", target.Name, testResult.ExitCode),
					SuggestedFix: "Fix the failing tests shown in the evidence, then re-run blueprint check.",
					Evidence: []domain.Evidence{{
						Kind:        "test-output",
						Description: "truncated test output",
						Location:    evidenceLoc,
					}},
				}
				if testResult.Stderr != "" {
					finding.Evidence[0].Description = failureDigest(testResult.Stderr, 2000)
				} else if testResult.Stdout != "" {
					finding.Evidence[0].Description = failureDigest(testResult.Stdout, 2000)
				}
				return domain.CheckResult{
					Name:     c.Name(),
					Status:   domain.StatusBlock,
					Findings: []domain.Finding{finding},
				}, nil
			}
		}

		// Run combined command if configured
		if len(target.Command) > 0 {
			cmdResult := RunInWorktree(ctx, worktreePath, target.Dir, target.Command, c.config)
			if cmdResult.Error != "" {
				return domain.CheckResult{
					Name:   c.Name(),
					Status: domain.StatusError,
					Error:  fmt.Sprintf("sandbox command [%s] failed: %s", target.Name, cmdResult.Error),
				}, nil
			}
			if cmdResult.TimedOut {
				return domain.CheckResult{
					Name:   c.Name(),
					Status: domain.StatusError,
					Error:  fmt.Sprintf("sandbox command [%s] timed out after %s", target.Name, c.config.Timeout),
				}, nil
			}
			if !cmdResult.Ok {
				evidenceLoc := fmt.Sprintf("sandbox:%s", strings.Join(target.Command, " "))
				if target.Dir != "" && target.Dir != "." {
					evidenceLoc = fmt.Sprintf("sandbox:%s (%s)", strings.Join(target.Command, " "), target.Dir)
				}
				finding := domain.Finding{
					RuleID:       "sandbox:test-failure",
					Severity:     domain.SeverityBlock,
					Category:     domain.CategoryTests,
					File:         target.Dir,
					Line:         0,
					Message:      fmt.Sprintf("Command failed for %s in isolated sandbox", target.Name),
					Explanation:  fmt.Sprintf("The staged changes cause command failure in %s (exit code %d).", target.Name, cmdResult.ExitCode),
					SuggestedFix: "Fix the failures shown in the evidence, then re-run blueprint check.",
					Evidence: []domain.Evidence{{
						Kind:        "command-output",
						Description: "truncated output",
						Location:    evidenceLoc,
					}},
				}
				if cmdResult.Stderr != "" {
					finding.Evidence[0].Description = failureDigest(cmdResult.Stderr, 2000)
				} else if cmdResult.Stdout != "" {
					finding.Evidence[0].Description = failureDigest(cmdResult.Stdout, 2000)
				}
				return domain.CheckResult{
					Name:     c.Name(),
					Status:   domain.StatusBlock,
					Findings: []domain.Finding{finding},
				}, nil
			}
		}
	}

	return domain.CheckResult{
		Name:   c.Name(),
		Status: domain.StatusPass,
	}, nil
}

// SplitCommand splits a command line string by whitespace into an argument slice.
func SplitCommand(cmd string) []string {
	return strings.Fields(cmd)
}

// truncateForEvidence keeps the HEAD and TAIL of s (roughly maxChars total)
// for inclusion in finding evidence. Build/test tools stream failure output
// at unpredictable positions: go test emits a failing package's detail at
// the moment that package completes (fast packages land near the head, slow
// ones near the tail) and the final FAIL verdict last; go build emits the
// compile error mid-output then the final FAIL. Head-only hid the verdict
// and late failures; tail-only hid early ones (both observed live while
// diagnosing a BLOCK whose 500-char head-truncated evidence contained only
// "flowchart LR..." chatter from a passing test).
func truncateForEvidence(s string, maxChars int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxChars {
		return s
	}
	head := maxChars / 4
	tail := maxChars * 3 / 4
	elided := len(s) - head - tail
	if elided < 0 {
		elided = 0
	}
	return s[:head] + fmt.Sprintf("\n... (%d bytes elided) ...\n", elided) + s[len(s)-tail:]
}

// failureDigest builds finding evidence from the failure-significant lines
// of build/test output: go test's "--- FAIL: Test" markers, its "FAIL" and
// "FAIL\t<package>" summary lines, "panic:" lines, and go's diagnostic
// "<file>.go:<line>: message" lines (which also carry t.Error/t.Fatal
// detail, prefixed with the _test.go location). Output position is
// unpredictable in multi-package runs (packages stream as they complete),
// so plain truncation cannot reliably capture them; when no
// failure-significant line is found, it falls back to truncateForEvidence.
func failureDigest(s string, maxChars int) string {
	var picked []string
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if ln == "" || strings.HasPrefix(ln, "ok  ") {
			continue
		}
		if strings.HasPrefix(ln, "--- FAIL") || strings.HasPrefix(ln, "FAIL") ||
			strings.HasPrefix(ln, "panic:") ||
			(strings.Contains(ln, ".go:") && strings.Contains(ln, ": ")) {
			picked = append(picked, ln)
		}
	}
	if len(picked) == 0 {
		return truncateForEvidence(s, maxChars)
	}
	digest := strings.Join(picked, "\n")
	if len(digest) > maxChars {
		return digest[:maxChars] + fmt.Sprintf("\n... (%d more failure lines elided)", len(picked))
	}
	return digest
}
