// Package cicd integrates kern with CI/CD pipelines (GitHub Actions, GitLab CI,
// Jenkins, etc.). It wraps TaskService operations with governance gates so
// CI/CD-triggered changes pass through the same firewall as interactive
// changes — no bypass path.
// The Pipeline runs: analyze → plan → execute → verify → PR, with each
// mutation gated by governance.CheckExec and the deploy stage requiring
// explicit KERN_ALLOW_DEPLOY=1 (same as the loop).
package cicd

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/prprovider"
)

// Trigger describes a CI/CD-triggered change request.
type Trigger struct {
	Source     string // "github-actions", "gitlab-ci", "jenkins", "manual"
	Repository string // "owner/repo" (for PR creation)
	Branch     string // source branch (e.g. "feature-branch")
	BaseBranch string // target branch (default "main")
	Intent     string // the change description / intent
	Patch      string // optional: a pre-built unified diff to apply
	AgentID    string // the CI/CD agent identity (default "cicd")
}

// Result is the outcome of a CI/CD pipeline run.
type Result struct {
	Trigger    Trigger
	Task       *domain.Task // the authoritative Task (nil on early failure)
	Phase      string       // last completed phase: "analyzed", "planned", "executed", "verified", "pr-created"
	Diff       string       // the applied diff (empty if no execution)
	PRURL      string       // PR URL (empty if no PR created)
	PRNumber   int          // PR number (0 if no PR)
	Verdict    string       // verification verdict
	Approved   bool         // whether governance approved the execution
	GateResult string       // governance gate result: "approved", "denied", "skipped"
	Error      string       // error message (empty on success)
}

// Pipeline runs governance-gated change workflows from CI/CD triggers.
type Pipeline struct {
	platform *app.Platform
	ts       *app.TaskService
	fw       *governance.Firewall
}

// New creates a Pipeline for the given root. It builds the Platform and
// TaskService once and reuses them across all runs.
func New(root string) (*Pipeline, error) {
	p, err := app.New(root)
	if err != nil {
		return nil, fmt.Errorf("cicd: platform: %w", err)
	}
	ts := app.NewTaskService(p, eventbus.New()).
		WithAgentID("cicd").
		WithPRProvider(app.AutoPRProvider())
	return &Pipeline{
		platform: p,
		ts:       ts,
		fw:       p.Firewall(),
	}, nil
}

// Run executes the CI/CD pipeline for the given trigger. It:
// 1. If a patch is provided, checks the governance gate (KERN_ALLOW_EXEC must
// be set for execution) then applies it via ExecuteAndVerify.
// 2. If no patch, runs Analyze + Plan (read-only, no governance gate needed).
// 3. Creates a PR when execution + verification succeed and a repo/branch is
// given.
// 4. Returns the result with all phase outputs.
// The pipeline NEVER deploys — deployment is a separate, human-approved step
// (use the loop with KERN_ALLOW_DEPLOY=1 for that). CI/CD stops at PR creation.
func (p *Pipeline) Run(trigger Trigger) *Result {
	res := &Result{Trigger: trigger}

	// If a patch is provided, we need execution governance clearance.
	if trigger.Patch != "" {
		// Gate 1: governance CheckExec (fail-closed).
		if err := governance.CheckExec(); err != nil {
			res.GateResult = "denied"
			res.Error = fmt.Sprintf("governance denied execution: %v", err)
			return res
		}
		res.GateResult = "approved"
		res.Approved = true

		// Execute + verify in sandbox.
		task, diff, v, err := p.ts.ExecuteAndVerify(trigger.Patch, []string{"build"})
		if err != nil {
			res.Phase = "executed"
			res.Error = fmt.Sprintf("execute failed: %v", err)
			res.Task = &task.Task
			res.Diff = diff
			return res
		}
		res.Task = &task.Task
		res.Diff = diff
		res.Verdict = string(v.Verdict)
		res.Phase = "verified"

		// Create a PR from the executed diff (requires repo + branch).
		if trigger.Repository != "" && trigger.Branch != "" {
			prURL, prNum := p.createPR(trigger, diff)
			res.PRURL = prURL
			res.PRNumber = prNum
			res.Phase = "pr-created"
		}
		return res
	}

	// No patch: read-only analyze + plan (no governance gate needed for reads).
	task, _, err := p.ts.Analyze(trigger.Intent)
	if err != nil {
		res.Error = fmt.Sprintf("analyze failed: %v", err)
		return res
	}
	res.Task = &task.Task
	res.Phase = "analyzed"

	planTask, _, _, err := p.ts.Plan(trigger.Intent)
	if err != nil {
		res.Error = fmt.Sprintf("plan failed: %v", err)
		return res
	}
	res.Task = &planTask.Task
	res.Phase = "planned"

	return res
}

// createPR creates a real PR via the prprovider when KERN_GITHUB_TOKEN is set.
// Returns the PR URL and number (empty/0 on failure or noop).
func (p *Pipeline) createPR(trigger Trigger, diff string) (string, int) {
	owner, repo := parseRepo(trigger.Repository)
	if owner == "" || repo == "" {
		return "", 0
	}

	base := trigger.BaseBranch
	if base == "" {
		base = "main"
	}

	provider := app.AutoPRProvider()
	result, err := provider.CreatePR(prprovider.Request{
		Owner: owner,
		Repo:  repo,
		Title: trigger.Intent,
		Head:  trigger.Branch,
		Base:  base,
		Body: fmt.Sprintf("## %s\n\nAutomated change from CI/CD pipeline (%s).\n\n```diff\n%s\n```\n",
			trigger.Intent, trigger.Source, diff),
	})
	if err != nil || result == nil {
		return "", 0
	}
	return result.URL, result.Number
}

// parseRepo splits "owner/repo" into owner and repo. It handles edge cases:
// empty input, input with no slash, and inputs with extra segments (only the
// first two segments are used).
func parseRepo(input string) (owner, repo string) {
	parts := strings.Split(input, "/")
	if len(parts) < 2 {
		return "", ""
	}
	owner = parts[0]
	repo = parts[1]
	if owner == "" || repo == "" {
		return "", ""
	}
	return owner, repo
}
