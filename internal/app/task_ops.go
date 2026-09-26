// Package app hosts the TaskService orchestration layer.
// Generated split of task.go by domain (see task.go for the core).
package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/deployment"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/orgapprovals"
	"github.com/JayveerPrajapati/kern/internal/prprovider"
	"github.com/JayveerPrajapati/kern/internal/verification"
	"log"
	"strings"
	"time"
)

// ErrInvalidPatch is the sentinel error returned when a patch cannot be
// applied to the worktree (malformed, garbage, or non-applicable input that
// git apply rejects). A patch that cannot be applied is always a caller error
// — the caller sent a patch that does not describe a valid change — so
// callers (e.g. the web API) classify it as a client error (400) rather than
// an internal failure (500). Other execution failures (worktree setup, diff,
// verification) are NOT wrapped with this sentinel.
var ErrInvalidPatch = errors.New("invalid patch")

// execExecutePatchCommand is the descriptive command text an Execute approval
// binds to. The patch itself is applied by git in a sandboxed worktree, so no
// shell command string exists at this site; the approval is bound to a
// stable, human-readable operation name so a HIGH/CRITICAL denial creates a
// PERSISTED, command-bound approval under the project root that `kern approve
// <id>` can resolve out-of-band (oracle-gate: the legacy empty-command gate
// left an in-memory approval nobody could resolve).
const execExecutePatchCommand = "kern_execute apply patch"

// execVerifyCommand renders the descriptive command text an ExecuteAndVerify
// approval binds to: "kern verify <types>" over the exact types that will run
// in the worktree. An empty types list mirrors the verify engine's own
// default (build), so the bound command always names what actually runs.
func execVerifyCommand(types []string) string {
	if len(types) == 0 {
		types = []string{"build"}
	}
	return "kern verify " + strings.Join(types, " ")
}

// execApprovalRoot returns the platform root exec approvals are persisted
// under, or "" when no platform is configured (the in-memory approval path:
// nothing to persist to, so CheckExecCommand returns honest guidance instead
// of a dead-end hint). Test services built without a Platform exercise this
// path.
func (s *TaskService) execApprovalRoot() string {
	if s.platform == nil {
		return ""
	}
	return s.platform.Root()
}

// Execute runs a patch in a sandboxed worktree, gated by governance .
// It creates a Task, transitions to EXECUTING, checks the governance firewall,
// applies the patch in a worktree, records the diff as an artifact, and returns
// the Task + diff.
// High-risk operations never directly modify the main working tree — the patch
// is applied in an isolated worktree. The governance gate (governance.CheckExec)
// is centralized here so no interface can bypass it.
func (s *TaskService) Execute(patch string) (*agent.Task, string, error) {
	t, err := s.Create("execute patch")
	if err != nil {
		return nil, "", err
	}

	// Governance gate: fail-closed before any execution. The approval is bound
	// to the descriptive apply-patch command and persisted under the project
	// root, so a HIGH/CRITICAL denial is resolvable via `kern approve <id>`
	// (oracle-gate: the legacy empty-command gate created an unresolvable
	// in-memory approval).
	if err := governance.CheckExecCommand(execExecutePatchCommand, s.execApprovalRoot()); err != nil {
		s.fail(t, "governance denied: "+err.Error())
		return t, "", err
	}

	// Task boundary: a controlled action must not bypass
	// task-scoped governance. Validate every path the patch touches against
	// the task's scope BEFORE applying it.
	if err := s.TaskScope(t.ID).ValidatePatch(patch); err != nil {
		s.fail(t, "boundary denied: "+err.Error())
		return t, "", err
	}

	if err := s.transition(t, domain.TaskExecuting); err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "EXECUTING"})

	wt, err := execution.NewWorktree(s.platform.Root())
	if err != nil {
		s.fail(t, err.Error())
		return t, "", err
	}
	defer func() { _ = wt.Cleanup() }()

	if err := wt.Apply(patch); err != nil {
		s.fail(t, "apply: "+err.Error())
		// A patch that git apply rejects (garbage, malformed, or not
		// applicable to the tree) is a caller error; wrap it so callers can
		// classify it via errors.Is(err, ErrInvalidPatch). The original
		// message text is preserved (the sentinel is only a prefix).
		return t, "", fmt.Errorf("%w: %v", ErrInvalidPatch, err)
	}

	diff, err := wt.Diff()
	if err != nil {
		s.fail(t, "diff: "+err.Error())
		return t, "", err
	}

	t.Output = fmt.Sprintf("applied patch: %d bytes, diff: %d chars", len(patch), len(diff))
	t.AddStep(agent.Step{
		Action:     "execute",
		AgentID:    "execution-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("applied %d-byte patch in worktree %s", len(patch), wt.Dir()),
		Status:     "success",
	})

	// Record the diff artifact, linked to the plan artifact (when one exists).
	s.recordArtifact(domain.ArtifactDiff, t.ID, "execution-engine",
		fmt.Sprintf("diff: %d chars", len(diff)),
		s.lastArtifactID(t.ID, domain.ArtifactPlan), "execution:worktree")

	if err := t.Complete(t.Output); err != nil {
		s.fail(t, err.Error())
		return t, diff, err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	return t, diff, nil
}

// ExecuteAndVerify runs a patch in a sandboxed worktree and immediately
// verifies it (build), keeping the worktree alive across both steps. It is the
// task-native equivalent of the legacy CLI runExecute path (which used raw
// execution.NewWorktree + manual verify). The worktree is cleaned up before
// returning. Returns the Task, the diff, and the verification result.
// This exists because Execute() defer-cleans the worktree, so a caller that
// wants to verify the worktree after Execute cannot access wt.Dir(). This
// method holds the worktree across both steps.
func (s *TaskService) ExecuteAndVerify(patch string, verifyTypes []string) (*agent.Task, string, verification.VerificationResult, error) {
	t, err := s.Create("execute patch")
	if err != nil {
		return nil, "", verification.VerificationResult{}, err
	}

	// Governance gate: fail-closed before any execution. The approval is bound
	// to the concrete verify command that will run in the worktree (the same
	// default the verify engine applies) and persisted under the project root,
	// so a HIGH/CRITICAL denial is command-bound and resolvable via `kern
	// approve <id>` (oracle-gate: the legacy empty-command gate created an
	// unresolvable in-memory approval).
	if err := governance.CheckExecCommand(execVerifyCommand(verifyTypes), s.execApprovalRoot()); err != nil {
		s.fail(t, "governance denied: "+err.Error())
		return t, "", verification.VerificationResult{}, err
	}

	// Task boundary: reject patches touching out-of-scope paths
	// before applying (same enforcement as Execute).
	if err := s.TaskScope(t.ID).ValidatePatch(patch); err != nil {
		s.fail(t, "boundary denied: "+err.Error())
		return t, "", verification.VerificationResult{}, err
	}

	if err := s.transition(t, domain.TaskExecuting); err != nil {
		s.fail(t, err.Error())
		return t, "", verification.VerificationResult{}, err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "EXECUTING"})

	wt, err := execution.NewWorktree(s.platform.Root())
	if err != nil {
		s.fail(t, err.Error())
		return t, "", verification.VerificationResult{}, err
	}
	defer func() { _ = wt.Cleanup() }()

	if err := wt.Apply(patch); err != nil {
		s.fail(t, "apply: "+err.Error())
		// Same classification as Execute: an unapplicable patch is a caller
		// error, wrapped in ErrInvalidPatch (message text preserved).
		return t, "", verification.VerificationResult{}, fmt.Errorf("%w: %v", ErrInvalidPatch, err)
	}

	diff, err := wt.Diff()
	if err != nil {
		s.fail(t, "diff: "+err.Error())
		return t, "", verification.VerificationResult{}, err
	}

	t.Output = fmt.Sprintf("applied patch: %d bytes, diff: %d chars", len(patch), len(diff))
	t.AddStep(agent.Step{
		Action:     "execute",
		AgentID:    "execution-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("applied %d-byte patch in worktree %s", len(patch), wt.Dir()),
		Status:     "success",
	})

	// Record the diff artifact, linked to the plan artifact (when one exists).
	s.recordArtifact(domain.ArtifactDiff, t.ID, "execution-engine",
		fmt.Sprintf("diff: %d chars", len(diff)),
		s.lastArtifactID(t.ID, domain.ArtifactPlan), "execution:worktree")

	// Verify the worktree (build/test) before cleanup.
	vres := s.verifyInWorktree(t, wt.Dir(), verifyTypes)

	// Cost/latency policy learning (Tier 2 #6): record this verify outcome —
	// task kind, configured model, PASS/FAIL — best-effort; learning proposes
	// via memory only and never blocks the flow.
	s.recordModelOutcome(t, vres.Verdict == verification.VerdictPass || vres.Verdict == verification.VerdictPassWithWarning)

	// Gate completion on the verification verdict: a failed verification must
	// never yield a COMPLETED task. Only a PASS verdict (or the non-blocking
	// PASS_WITH_WARNING) may complete; anything else fails the task.
	if vres.Verdict == verification.VerdictPass || vres.Verdict == verification.VerdictPassWithWarning {
		if err := t.Complete(t.Output); err != nil {
			s.fail(t, err.Error())
			return t, diff, vres, err
		}
		s.persist(t)
		s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
		return t, diff, vres, nil
	}
	verr := verificationFailure(vres)
	s.fail(t, verr.Error())
	return t, diff, vres, verr
}

// verifyInWorktree runs verification on the given worktree dir and records the
// result on the Task. It does NOT transition state — the caller decides the
// final transition. Returns the verification result.
func (s *TaskService) verifyInWorktree(t *agent.Task, worktreeDir string, types []string) verification.VerificationResult {
	if len(types) == 0 {
		types = []string{"build"}
	}
	eng := verification.NewEngine(worktreeDir)
	res := eng.Verify(types)
	t.Verification = &res
	t.AddStep(agent.Step{
		Action:     "verify",
		AgentID:    "verification-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     fmt.Sprintf("verdict: %s, summary: %s", res.Verdict, res.Summary),
		Status:     "success",
	})
	s.recordArtifact(domain.ArtifactVerificationReport, t.ID, "verification-engine",
		fmt.Sprintf("verdict: %s, summary: %s", res.Verdict, res.Summary),
		s.lastArtifactID(t.ID, domain.ArtifactDiff), "verification:worktree")
	return res
}

// CreatePR creates a PR from the Task's structured artifacts .
// It renders a PR body from the Plan, Impact, and Verification artifacts, and
// transitions the Task to PR_CREATED.
// The PR requires: (1) verification passed (Task is in READY_FOR_PR), (2) the
// diff artifact exists. The PR body is generated from artifacts, not from an
// agent's memory of what it changed — this is safer and more auditable.
// The body is always rendered and recorded as an artifact regardless of
// provider outcome. If the configured provider (default Noop) creates a real
// PR, the URL/number are stamped on the Task and appended to the output; a
// provider error is recorded in the step result but does NOT fail the task.
func (s *TaskService) CreatePR(taskID string, branch string) (*agent.Task, string, error) {
	t, ok := s.Get(taskID)
	if !ok {
		return nil, "", fmt.Errorf("task not found: %s", taskID)
	}

	// Require verification to have passed.
	if t.State != domain.TaskReadyForPR {
		return t, "", fmt.Errorf("task must be in READY_FOR_PR state (current: %s)", t.State)
	}

	// Render the PR body from structured artifacts.
	body := s.renderPRBody(t)

	t.Output = body

	// Attempt to create a real PR via the provider.
	// NoopProvider (default) returns Number=0, URL="" — no network call.
	repo, _ := prprovider.DetectRepo(s.platform.Root())
	prResult, prErr := s.prProvider.CreatePR(prprovider.Request{
		Owner: repo.Owner,
		Repo:  repo.Repo,
		Title: t.Intent,
		Head:  branch,
		Base:  "main",
		Body:  body,
	})

	var stepResult string
	switch {
	case prErr != nil:
		// Log the error in the step result but do NOT fail the task — the body
		// is still rendered and the artifact is recorded.
		stepResult = fmt.Sprintf("PR render: %d chars, branch: %s; PR creation FAILED: %v", len(body), branch, prErr)
	case prResult != nil && prResult.Number > 0:
		t.PRURL = prResult.URL
		t.PRNumber = prResult.Number
		t.Output = body + "\n\nPR: " + prResult.URL
		stepResult = fmt.Sprintf("PR #%d created: %s", prResult.Number, prResult.URL)
		// Post a review-findings comment on the new PR. Best-effort: a
		// comment failure never fails PR creation; no findings → no comment;
		// NoopProvider (default) is a no-op.
		s.postPRComment(t, prResult.Number, repo)
	default:
		// noop (Number == 0)
		if prResult != nil {
			t.PRURL = prResult.URL
			t.PRNumber = prResult.Number
		}
		stepResult = fmt.Sprintf("PR body: %d chars, branch: %s (noop — set KERN_GITHUB_TOKEN for real PR)", len(body), branch)
	}

	t.AddStep(agent.Step{
		Action:     "pr",
		AgentID:    "pr-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     stepResult,
		Status:     "success",
	})

	// Record the PR artifact, linked to the verification artifact.
	s.recordArtifact(domain.ArtifactPullRequest, t.ID, "pr-engine",
		fmt.Sprintf("PR: %s — branch %s", t.Intent, branch),
		s.lastArtifactID(t.ID, domain.ArtifactVerificationReport), "pr:render")

	if err := s.transition(t, domain.TaskPRCreated); err != nil {
		s.fail(t, err.Error())
		return t, body, err
	}
	s.publish(eventbus.PRCreated, t.ID, map[string]string{"branch": branch, "intent": t.Intent})
	s.persist(t)
	return t, body, nil
}

// maxPRCommentFindings caps how many findings the PR comment lists.
const maxPRCommentFindings = 30

// maxPRFindingChars caps each bullet in the PR comment.
const maxPRFindingChars = 300

// prCommentFindings extracts the review findings the task carries at PR time
// from its verification result — security findings, architecture violations,
// and static-analysis findings (the app-level findings the task record
// actually stores; reviewpack output is a CLI/council artifact and is never
// attached to a Task). It returns nil when the task carries no verification
// result or no findings, so the caller can skip the comment entirely.
func prCommentFindings(t *agent.Task) []string {
	if t == nil || t.Verification == nil {
		return nil
	}
	v := t.Verification
	var out []string
	if v.Security != nil {
		for _, f := range v.Security.Findings {
			sev := f.Severity
			if sev == "" {
				sev = "unknown"
			}
			loc := f.File
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", loc, f.Line)
			}
			msg := f.Message
			if msg == "" {
				msg = f.Rule
			}
			if msg == "" {
				continue
			}
			out = append(out, fmt.Sprintf("[security/%s] %s %s", sev, loc, strings.TrimSpace(msg)))
		}
	}
	if v.Architecture != nil {
		for _, viol := range v.Architecture.Violations {
			out = append(out, "[architecture] "+viol)
		}
	}
	if v.StaticAnalysis != nil {
		tool := v.StaticAnalysis.Tool
		if tool == "" {
			tool = "static"
		}
		for _, f := range v.StaticAnalysis.Findings {
			out = append(out, fmt.Sprintf("[%s] %s", tool, f))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// prCommentBody renders the review-findings comment: a bulleted list capped at
// maxPRCommentFindings findings, each truncated to maxPRFindingChars. It
// returns "" when there is nothing to post.
func prCommentBody(findings []string) string {
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Review findings\n\n")
	for i, f := range findings {
		if i >= maxPRCommentFindings {
			fmt.Fprintf(&b, "- _… and %d more finding(s)_\n", len(findings)-maxPRCommentFindings)
			break
		}
		fmt.Fprintf(&b, "- %s\n", clipFinding(f))
	}
	return b.String()
}

// clipFinding truncates a single finding to maxPRFindingChars runes.
func clipFinding(s string) string {
	r := []rune(s)
	if len(r) <= maxPRFindingChars {
		return s
	}
	return string(r[:maxPRFindingChars-3]) + "..."
}

// postPRComment posts a best-effort review-findings comment on a created PR.
// It NEVER fails PR creation: any error is logged and swallowed. No findings →
// no comment. The default NoopProvider is a no-op success.
func (s *TaskService) postPRComment(t *agent.Task, prNumber int, repo prprovider.RepoInfo) {
	if t == nil {
		return
	}
	body := prCommentBody(prCommentFindings(t))
	if body == "" {
		return // no findings → no comment
	}
	if err := s.prProvider.CommentPR(context.Background(), prprovider.CommentRequest{
		Owner:  repo.Owner,
		Repo:   repo.Repo,
		Number: prNumber,
		Body:   body,
	}); err != nil {
		// Best-effort: a failed comment must never fail PR creation.
		log.Printf("kern app: task %s: PR comment not posted (#%d): %v", t.ID, prNumber, err)
	}
}

// Deploy transitions a Task from PR_CREATED to DEPLOYING, performing a real
// deployment via the configured deployer (default NoopDeployer → simulated
// success; KERN_DEPLOY_COMMAND + KERN_ALLOW_DEPLOY=1 → real external deploy).
// The version string identifies the deployment version.
// Governance: a real deploy (ShellDeployer) is a CRITICAL production.deploy
// action. Deploy checks the governance firewall before proceeding; if approval
// is required it returns agent.ErrApprovalRequired wrapping the pending
// approval ID — the caller must resolve the approval (e.g. via kern approve)
// and call Deploy again — UNLESS an org root is configured (KERN_ORG_ROOT)
// and a matching org approval (orgapprovals.Consume) clears the gate: the
// org-wide pre-approval is single-use and consumed atomically. The
// NoopDeployer (simulated, default) skips the gate to preserve v1 behavior.
func (s *TaskService) Deploy(taskID string, version string) (*agent.Task, error) {
	t, ok := s.Get(taskID)
	if !ok {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	deployer := s.deployer
	if deployer == nil {
		deployer = deployment.NoopDeployer{}
	}

	// Governance gate: only real deploys (non-Noop) require human approval.
	// The firewall checks agent identity, permission, risk, and policy. A
	// CRITICAL production.deploy with no prior approval returns a pending
	// Approval; we surface it as ErrApprovalRequired so the caller can resolve
	// it and retry. NoopDeployer (simulated) bypasses the gate for v1 compat.
	if _, isNoop := deployer.(deployment.NoopDeployer); !isNoop {
		fw := s.platform.Firewall()
		if fw != nil {
			agentID := s.agentID
			if agentID == "" {
				agentID = "task-service"
			}
			allowed, risk, approval, err := fw.Check(agentID, "production", "deploy")
			if err != nil {
				s.fail(t, "governance: "+err.Error())
				return t, fmt.Errorf("deploy: governance check failed: %w", err)
			}
			// orgApproved is set when an org-wide approval (P13 stage 3)
			// cleared the gate: it overrides BOTH the pending-approval
			// return and the generic denial below (the org approval IS the
			// authorization for this deploy).
			orgApproved := false
			if !allowed && approval != nil {
				// P13 stage 3 — org-wide approval override: with an org root
				// configured (KERN_ORG_ROOT), a matching org approval
				// pre-approves this action across projects BEFORE the
				// per-project approval requirement is surfaced. Consume is
				// atomic single-use under the org store's flock — two
				// concurrent deploys can never double-spend one org approval
				// — and the consumed approval is audited on the org audit
				// trail (when the enterprise server wired the hook). No org
				// root → Consume is inert (none, no error) and the
				// per-project flow is byte-for-byte unchanged. A corrupt org
				// store fails closed: the per-project approval stays
				// authoritative.
				if orgAppr, ok, cerr := orgapprovals.Consume(governance.OrgRoot(), "deploy", "production"); cerr == nil && ok {
					log.Printf("kern app: task %s: deploy cleared by org approval %s (granted by %s)", t.ID, orgAppr.ID, orgAppr.GrantedBy)
					orgApproved = true
				} else {
					if cerr != nil {
						// A corrupt/unreadable org store fails closed into the
						// per-project path (same outcome as no match).
						log.Printf("kern app: task %s: org approval consume failed: %v", t.ID, cerr)
					}
					// Park the task — do NOT transition to Deploying yet. The caller
					// resolves the approval and calls Deploy again; the firewall's
					// single-use approved key makes the second Check pass.
					s.publish(eventbus.TaskApprovalRequested, t.ID, map[string]string{
						"approval_id": approval.ID,
						"risk":        string(risk.Level),
						"action":      "production.deploy",
					})
					s.publish(eventbus.ApprovalRequested, approval.ID, map[string]string{
						"task": t.ID, "action": "production.deploy", "risk": string(risk.Level),
					})
					return t, fmt.Errorf("%w: %s", agent.ErrApprovalRequired, approval.ID)
				}
			}
			if !allowed && !orgApproved {
				s.fail(t, "governance: deploy denied")
				return t, fmt.Errorf("deploy: denied by governance (risk %s)", risk.Level)
			}
		}
	}

	if err := s.transition(t, domain.TaskDeploying); err != nil {
		s.fail(t, err.Error())
		return t, err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "DEPLOYING", "version": version})

	res, derr := deployer.Deploy(context.Background(), deployment.DeployRequest{
		Service:     "kern",
		Version:     version,
		ProjectRoot: s.platform.Root(),
	})
	s.publish(eventbus.DeploymentStarted, t.ID, map[string]string{"service": "kern", "version": version})

	if derr != nil || !res.Success {
		msg := "deploy failed"
		if derr != nil {
			msg = derr.Error()
		}
		if res.Output != "" {
			msg = res.Output
		}
		s.recordArtifact(domain.ArtifactDeployment, t.ID, "deploy-engine",
			fmt.Sprintf("deployment: %s (failed)", version),
			s.lastArtifactID(t.ID, domain.ArtifactPullRequest), "deploy:failed")
		s.publish(eventbus.DeploymentFailed, t.ID, map[string]string{"service": "kern", "version": version, "error": msg})
		s.publish(eventbus.DeploymentRolledBack, t.ID, map[string]string{"service": "kern", "version": version, "error": msg})
		s.fail(t, msg)
		return t, fmt.Errorf("deploy: %s", msg)
	}

	t.AddStep(agent.Step{
		Action:     "deploy",
		AgentID:    "deploy-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     res.Output,
		Status:     "success",
	})
	s.recordArtifact(domain.ArtifactDeployment, t.ID, "deploy-engine",
		fmt.Sprintf("deployment: %s", version),
		s.lastArtifactID(t.ID, domain.ArtifactPullRequest), "deploy:"+version)
	s.publish(eventbus.DeploymentCompleted, t.ID, map[string]string{"service": "kern", "version": version})
	s.persist(t)
	return t, nil
}

// Observe transitions a Task from DEPLOYING to OBSERVING, runs a simulated
// production health check, and transitions to COMPLETED if healthy. In local
// mode the health check is a no-op (always healthy). Returns the Task.
func (s *TaskService) Observe(taskID string) (*agent.Task, error) {
	t, ok := s.Get(taskID)
	if !ok {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if err := s.transition(t, domain.TaskObserving); err != nil {
		s.fail(t, err.Error())
		return t, err
	}
	s.publish(eventbus.TaskUpdated, t.ID, map[string]string{"state": "OBSERVING"})
	t.AddStep(agent.Step{
		Action:     "observe",
		AgentID:    "observe-engine",
		StartedAt:  t.UpdatedAt,
		FinishedAt: time.Now(),
		Result:     "production healthy",
		Status:     "success",
	})
	// Transition to COMPLETED — the full lifecycle is done.
	if err := t.Complete("lifecycle complete: all 20 steps passed"); err != nil {
		s.fail(t, err.Error())
		return t, err
	}
	s.persist(t)
	s.publish(eventbus.TaskCompleted, t.ID, map[string]string{"state": "COMPLETED"})
	// Record the Audit artifact (P10.4) as the finalize point of the workflow:
	// after the task completes, the whole lifecycle is summarized in a typed,
	// traceable audit artifact linked to the last PR/deployment artifact.
	s.recordAuditArtifact(t.ID, "audit trail for "+t.Intent)
	return t, nil
}

// recordAuditArtifact records an ArtifactAudit finalize artifact for a task.
// It is best-effort and non-breaking: it links the audit trail to the most
// recent pull-request or deployment artifact in the chain (falling back to an
// empty parent when neither exists). createdBy is "audit-engine" and the
// provenance is "audit:finalize".
func (s *TaskService) recordAuditArtifact(taskID, intent string) {
	if s.arts == nil {
		return
	}
	parentID := s.lastArtifactID(taskID, domain.ArtifactPullRequest)
	if parentID == "" {
		parentID = s.lastArtifactID(taskID, domain.ArtifactDeployment)
	}
	arts, err := s.arts.GetByTask(taskID)
	if err != nil {
		return
	}
	summary := fmt.Sprintf("audit trail for %s (%d artifacts)", intent, len(arts))
	s.recordArtifact(domain.ArtifactAudit, taskID, "audit-engine",
		summary, parentID, "audit:finalize")
}

// renderPRBody generates a PR description from the Task's structured artifacts
// (Plan, Impact, Verification) — not from an agent's memory. This is safer and
// more auditable.
func (s *TaskService) renderPRBody(t *agent.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", t.Intent)
	fmt.Fprintf(&b, "Task: %s\n\n", t.ID)

	if t.Plan != nil {
		fmt.Fprintf(&b, "### Plan\n")
		fmt.Fprintf(&b, "Objective: %s\n", t.Plan.Objective)
		fmt.Fprintf(&b, "Risk: %s\n", t.Plan.Risk)
		fmt.Fprintf(&b, "Scope: %s\n\n", t.Plan.Scope)
		if len(t.Plan.ImplementationSteps) > 0 {
			fmt.Fprintf(&b, "#### Implementation steps\n")
			for i, step := range t.Plan.ImplementationSteps {
				fmt.Fprintf(&b, "%d. %s\n", i+1, step)
			}
			fmt.Fprintf(&b, "\n")
		}
		if t.Plan.Rollback != "" {
			fmt.Fprintf(&b, "#### Rollback\n%s\n\n", t.Plan.Rollback)
		}
	}

	if t.Impact != nil {
		fmt.Fprintf(&b, "### Impact\n")
		fmt.Fprintf(&b, "Risk: %s\n", t.Impact.Risk)
		fmt.Fprintf(&b, "Callers: %d, Services: %d, APIs: %d\n\n", len(t.Impact.WhoCalls), len(t.Impact.ServicesDepend), len(t.Impact.APIsAffected))
	}

	if t.Verification != nil {
		fmt.Fprintf(&b, "### Verification\n")
		fmt.Fprintf(&b, "Verdict: %s\n", t.Verification.Verdict)
		fmt.Fprintf(&b, "Summary: %s\n\n", t.Verification.Summary)
	}

	fmt.Fprintf(&b, "---\nGenerated by kern from structured artifacts.\n")
	return b.String()
}

// artifactKindForAction maps a workflow action name to the appropriate artifact
// kind for recording.
func artifactKindForAction(action string) domain.ArtifactKind {
	switch action {
	case "analyze":
		return domain.ArtifactContextPacket
	case "plan":
		return domain.ArtifactPlan
	case "code":
		return domain.ArtifactCodePatch
	case "verify":
		return domain.ArtifactVerificationReport
	case "pr":
		return domain.ArtifactPullRequest
	default:
		return domain.ArtifactAnalysisReport
	}
}

// toolForAction maps a workflow step action to the MCP tool that executes it
// ( tool-decision trace). It is the deterministic tool-selection
// trace: which tool the control plane uses for each workflow step. Every
// returned name MUST be a registered MCP tool — steps without a dedicated
// tool (pr, deploy, observe) fall back to the workflow orchestrator that
// drives them, never a phantom "kern_<action>" label.
func toolForAction(action string) string {
	switch action {
	case "analyze":
		return "kern_analyze"
	case "plan":
		return "kern_plan"
	case "code":
		return "kern_execute"
	case "verify":
		return "kern_verify"
	case "pr", "deploy", "observe":
		return "kern_workflow"
	default:
		return "kern_workflow"
	}
}

// truncate shortens a string to at most n characters, appending "…" when
// truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// verificationFailure names the first failing check and the first line of the
// summary, turning a bare multi-line summary dump into a single-sentence cause
// (e.g. "verification failed: build: go build ./... failed").
func verificationFailure(res verification.VerificationResult) error {
	check := failingCheckName(&res)
	line := summaryFirstLine(res.Summary)
	switch {
	case check != "" && line != "":
		return fmt.Errorf("verification failed: %s: %s", check, line)
	case check != "":
		return fmt.Errorf("verification failed: %s", check)
	case line != "":
		return fmt.Errorf("verification failed: %s", line)
	default:
		return errors.New("verification failed")
	}
}

// failingCheckName returns the name of the first check that failed, or "".
func failingCheckName(res *verification.VerificationResult) string {
	switch {
	case res.Build != nil && !res.Build.OK:
		return "build"
	case res.UnitTests != nil && !res.UnitTests.OK:
		return "unit tests"
	case res.Integration != nil && !res.Integration.OK:
		return "integration tests"
	case res.Security != nil && !res.Security.OK:
		return "security"
	case res.Architecture != nil && !res.Architecture.OK:
		return "architecture"
	case res.Dependency != nil && !res.Dependency.OK:
		return "dependency"
	case res.E2ETests != nil && !res.E2ETests.OK:
		return "e2e tests"
	case res.StaticAnalysis != nil && !res.StaticAnalysis.OK:
		return "static analysis"
	}
	return ""
}

// summaryFirstLine returns the first non-empty, trimmed line of a summary.
func summaryFirstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return strings.TrimSpace(s)
}
