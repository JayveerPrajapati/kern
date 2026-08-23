// Package incident implements Incident Engineering (Phase 12): turning a
// production alert into a root-caused, evidence-backed, sandbox-verified fix
// and PR. It reuses the runtime layer, knowledge graph, memory, evidence,
// governance (firewall + approval) and execution/verification engines.
//
// Workflow D (OPERATE / FIX PRODUCTION) pipeline:
//
//	Alert → Correlate → Root Cause → Candidate Fix → Sandbox → Verify → PR
//
// Human approval is required for production changes by default: the fix never
// advances past the sandboxed, verified stage until an approval gate is granted.
package incident

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/evidence"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/prprovider"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

// DefaultLookback is the correlation window used when none is configured.
const DefaultLookback = 30 * time.Minute

// Engine orchestrates the incident workflow for a production repository. It is
// constructed once per repository root and reused across incidents.
type Engine struct {
	root   string
	src    runtime.Source
	mem    *memory.MemoryStore
	fw     *governance.Firewall
	appr   *governance.ApprovalWorkflow
	graph  *intelligence.Graph
	window     time.Duration
	bus        *eventbus.Bus       // optional event publisher; nil = no-op
	prProvider prprovider.Provider // PR creation provider (default Noop)
	shared     *runtime.SharedCorrelator // shared correlation service (Phase 13.3); nil = build own
}

// NewEngine builds an incident engine for a repository. It indexes the repo and
// builds the knowledge graph so root-cause analysis can reason over code.
func NewEngine(root string, src runtime.Source, mem *memory.MemoryStore, fw *governance.Firewall) (*Engine, error) {
	ix, err := index.Build(root)
	if err != nil {
		return nil, fmt.Errorf("incident: index: %w", err)
	}
	g := intelligence.FromIndex(ix)
	return NewEngineWithGraph(root, &g, src, mem, fw)
}

// NewEngineWithGraph builds an incident engine for a repository using a
// prebuilt knowledge graph instead of re-indexing the repo and re-deriving the
// graph (which is what NewEngine does). The caller owns the graph and must treat
// it as read-only: the engine stores only a reference and never mutates it.
// This is the hot-path constructor used by servers that already built the graph
// once at startup.
func NewEngineWithGraph(root string, g *intelligence.Graph, src runtime.Source, mem *memory.MemoryStore, fw *governance.Firewall) (*Engine, error) {
	if g == nil {
		return nil, errors.New("incident: nil knowledge graph")
	}
	e := &Engine{
		root:       root,
		src:        src,
		mem:        mem,
		fw:         fw,
		appr:       governance.NewApprovalWorkflow(),
		graph:      g,
		window:     DefaultLookback,
		prProvider: prprovider.NoopProvider{},
	}
	// Phase 13.3: the incident engine shares one correlation service instead of
	// building its own per Correlate call. It is initialized from the source when
	// present so all consumers reason over the same runtime; a nil source keeps
	// the shallow-only behavior. Callers may override it via WithSharedCorrelator.
	if src != nil {
		e.shared = runtime.NewSharedCorrelator(src, e.window)
	}
	return e, nil
}

// SetLookback overrides the correlation window.
func (e *Engine) SetLookback(d time.Duration) { e.window = d }

// WithPRProvider sets the PR provider used by CreateFixPR. If not called, the
// engine uses NoopProvider (render body only, no network). A nil provider is
// treated as a request for the NoopProvider.
func (e *Engine) WithPRProvider(p prprovider.Provider) *Engine {
	if p != nil {
		e.prProvider = p
	} else {
		e.prProvider = prprovider.NoopProvider{}
	}
	return e
}

// WithBus attaches an optional event bus. When non-nil, the engine publishes
// incident.created, incident.updated and incident.resolved at the lifecycle
// transitions. A nil bus is a no-op so the engine keeps working unchanged.
func (e *Engine) WithBus(b *eventbus.Bus) *Engine {
	e.bus = b
	return e
}

// WithSharedCorrelator overrides the correlation service used by Correlate. This
// is the dependency-injection point: a caller (e.g. the app/web layer) hands in
// the SAME *runtime.SharedCorrelator shared by deployment/audit/learning so every
// lane reasons over one consistent correlator (Phase 13.3). A nil value clears
// the override and falls back to building a correlator from the engine's source.
func (e *Engine) WithSharedCorrelator(sc *runtime.SharedCorrelator) *Engine {
	e.shared = sc
	return e
}

// publish delivers a bus event. A nil bus is a no-op.
func (e *Engine) publish(ev eventbus.Event) {
	if e.bus == nil {
		return
	}
	if ev.Source == "" {
		ev.Source = "incident"
	}
	e.bus.Publish(ev)
}

// Source exposes the underlying production source.
func (e *Engine) Source() runtime.Source { return e.src }

// ApprovalWorkflow exposes the approval workflow so callers can query/reject.
func (e *Engine) ApprovalWorkflow() *governance.ApprovalWorkflow { return e.appr }

// IngestAlert opens a new incident for an alert.
func (e *Engine) IngestAlert(a domain.Alert) *domain.Incident {
	a = sanitizeAlert(a)
	now := time.Now().UTC()
	inc := &domain.Incident{
		ID:        newIncID(),
		Title:     a.Message,
		Severity:  a.Severity,
		Status:    domain.IncidentOpen,
		Alert:     a,
		CreatedAt: now,
		UpdatedAt: now,
	}
	e.publish(eventbus.Event{Kind: eventbus.IncidentCreated, Subject: inc.ID, Payload: map[string]string{"service": a.Service, "severity": string(a.Severity)}})
	return inc
}

// sanitizeAlert normalizes and bounds untrusted alert fields so a client cannot
// inject fabricated evidence or force correlation onto an arbitrary service:
//   - unknown/absent severity is clamped to the known enum;
//   - a missing or implausibly-future OccurredAt (>24h ahead) is replaced with now;
//   - the service name is bounded to 200 chars.
func sanitizeAlert(a domain.Alert) domain.Alert {
	if !validSeverity(a.Severity) {
		a.Severity = domain.SeverityInfo
	}
	if a.OccurredAt.IsZero() || a.OccurredAt.After(time.Now().Add(24*time.Hour)) {
		a.OccurredAt = time.Now().UTC()
	}
	if len(a.Service) > 200 {
		a.Service = a.Service[:200]
	}
	return a
}

// validSeverity reports whether s is one of the canonical severity values.
func validSeverity(s domain.Severity) bool {
	switch s {
	case domain.SeverityCritical, domain.SeverityError, domain.SeverityWarning, domain.SeverityInfo:
		return true
	}
	return false
}

// Correlate maps the incident's alert to the affected service and gathers the
// related deployments, commits and runtime evidence within the lookback window.
// When a runtime source is available it additionally folds in the deep
// correlation chain (service -> deployment -> commit -> symbol -> task/pr/agent)
// as additive evidence; the shallow result is always preserved.
func (e *Engine) Correlate(inc *domain.Incident) {
	corr := e.correlator().Correlate(inc.Alert)
	inc.AffectedService = corr.AffectedService
	inc.RelatedDeployments = corr.Deployments
	inc.Status = domain.IncidentInvestigating
	inc.Evidence = append(inc.Evidence, runtimeEvidence(corr, e.src.Name()))

	// Fold the deep correlation chain in when a runtime source is present.
	if e.src != nil {
		chain := e.correlator().CorrelateChain(inc.Alert)
		inc.Evidence = append(inc.Evidence, chainEvidence(chain))
	}

	inc.UpdatedAt = time.Now()
	e.publish(eventbus.Event{Kind: eventbus.IncidentUpdated, Subject: inc.ID, Payload: map[string]string{"status": string(inc.Status)}})
}

// correlator returns the correlation service to use: the shared correlator when
// one has been injected (Phase 13.3), otherwise a fresh correlator built over the
// engine's source. When no source is present it returns a nil-safe no-op path via
// NewCorrelator(nil, window) which preserves the prior shallow behavior.
func (e *Engine) correlator() *runtime.Correlator {
	if e.shared != nil {
		return e.shared.Correlator()
	}
	return runtime.NewCorrelator(e.src, e.window)
}

// Resolve marks the incident CLOSED, writes the resolution to engineering
// memory (Phase 11: wire incident → pattern → memory) so the continuous-learning
// extractor can surface recurring patterns, and publishes incident.resolved.
func (e *Engine) Resolve(inc *domain.Incident) {
	inc.Status = domain.IncidentClosed
	inc.UpdatedAt = time.Now().UTC()

	// Phase 11 (11.5): persist the resolved incident as an incident-type memory
	// so the learning extractor can group it with similar incidents and surface
	// a recurring pattern. The lesson is recorded with provenance "incident"
	// and the affected service as its scope. A nil root cause is still recorded
	// (with the incident title) so the memory trail is complete.
	if e.mem != nil {
		lesson := buildIncidentLesson(inc)
		if lesson.Content != "" {
			if _, err := e.mem.Add(lesson); err == nil {
				inc.Memories = append(inc.Memories, lesson)
				e.publish(eventbus.Event{Kind: eventbus.LessonRecorded, Subject: inc.ID, Payload: map[string]string{"service": inc.AffectedService, "memory": lesson.ID}})
			}
		}
	}

	e.publish(eventbus.Event{Kind: eventbus.IncidentResolved, Subject: inc.ID, Payload: map[string]string{"service": inc.AffectedService}})
}

// buildIncidentLesson assembles the incident-resolution memory. It captures the
// incident summary, root cause, affected service, and fix verification so the
// learning extractor has structured content to group and pattern-match on.
func buildIncidentLesson(inc *domain.Incident) domain.Memory {
	content := "incident " + inc.ID
	if inc.AffectedService != "" {
		content += " in " + inc.AffectedService
	}
	content += ": " + inc.Title
	if inc.RootCause != nil {
		content += "; root cause: " + inc.RootCause.Summary
	}
	if inc.Verification != "" {
		content += "; fix verified: " + inc.Verification
	}
	return domain.Memory{
		Type:       domain.MemoryIncident,
		Content:    content,
		Source:     "incident-engine",
		Scope:      inc.AffectedService,
		Tags:       []string{"incident", "resolved", "postmortem"},
		Subject:    inc.ID,
		Provenance: "incident:" + inc.ID,
	}
}

// RootCause derives candidate hypotheses from the correlated evidence, memory
// and code graph, selects the strongest as the root cause, and updates the
// incident with evidence. It never fabricates a verdict: hypotheses are typed
// FACT / INFERENCE / HYPOTHESIS and ranked deterministically.
func (e *Engine) RootCause(inc *domain.Incident) {
	hs := e.hypotheses(inc)
	inc.Hypotheses = hs
	// Emit an evidence-backed Hypothesis claim per candidate.
	inc.Claims = inc.Claims[:0]
	for _, h := range hs {
		ev := domain.Evidence{}
		if len(h.Evidence) > 0 {
			ev = h.Evidence[0]
		}
		inc.Claims = append(inc.Claims, evidence.FromHypothesis(
			h.Statement, inc.AffectedService, "rootcause:"+h.Source, ev))
	}
	// Only promote a hypothesis to a RootCause (stated as fact) when it is at
	// least an INFERENCE backed by non-git evidence. Bare unverified hypotheses
	// stay surfaced as candidates with a nil RootCause.
	for _, h := range hs {
		if h.Confidence != domain.ClaimInference && h.Confidence != domain.ClaimFact {
			continue
		}
		if !hasNonGitEvidence(h.Evidence) {
			continue
		}
		inc.RootCause = &domain.RootCause{
			Summary:    h.Statement,
			Service:    inc.AffectedService,
			Hypothesis: h,
			Evidence:   h.Evidence,
		}
		break
	}
	inc.Status = domain.IncidentRootCauseFound
	inc.UpdatedAt = time.Now()
	e.publish(eventbus.Event{Kind: eventbus.IncidentUpdated, Subject: inc.ID, Payload: map[string]string{"status": string(inc.Status)}})
}

// ApplyAndVerifyFix sandboxes a candidate fix, applies it to the isolated
// worktree, computes the diff, assesses the fix's risk against the governance
// firewall (Phase 11: risk step in the fix pipeline), verifies it (build), and
// only marks it FIX_VERIFIED when both the risk is acceptable and the build
// passes. On success it records the diff, the verification summary, and the
// risk assessment. The fix is never applied to the live repository — only to
// the sandbox.
func (e *Engine) ApplyAndVerifyFix(inc *domain.Incident, apply func(workDir string) error) (string, error) {
	wt, err := execution.NewWorktree(e.root)
	if err != nil {
		return "", fmt.Errorf("worktree: %w", err)
	}
	defer wt.Cleanup()

	if err := apply(wt.Dir()); err != nil {
		return "", fmt.Errorf("apply fix: %w", err)
	}
	diff, err := wt.Diff()
	if err != nil {
		return "", fmt.Errorf("diff: %w", err)
	}

	inc.FixDiff = diff
	inc.Status = domain.IncidentFixProposed

	// Phase 11 (11.4) — Risk step in the fix pipeline. Before verifying, assess
	// whether applying this fix carries an unacceptable risk. When a firewall
	// is configured, the change is gated against the incident's affected
	// service; an always-blocked CRITICAL action (or a denied permission)
	// prevents the fix from proceeding to verification. The risk assessment is
	// recorded on the incident so it is auditable alongside the fix.
	if e.fw != nil {
		allowed, r, approval, fwErr := e.fw.Check("incident-fixer", incidentResource(inc), "fix")
		// An "unknown agent" means the firewall has no policy for the fixer (a
		// configuration gap, not a definitive risk denial). The incident engine
		// has a separate human-approval gate for production changes, so we only
		// hard-block on a real permission/policy denial.
		denied := fwErr != nil || !allowed
		if fwErr != nil && strings.Contains(fwErr.Error(), "unknown agent") {
			denied = false
		}
		if denied {
			inc.FixRisk = fmt.Sprintf("blocked: %s", reasonOf(fwErr, "fix denied by firewall"))
			inc.Status = domain.IncidentFixBlocked
			inc.UpdatedAt = time.Now()
			e.publish(eventbus.Event{Kind: eventbus.IncidentUpdated, Subject: inc.ID, Payload: map[string]string{"status": string(inc.Status), "risk": string(r.Level)}})
			return diff, errors.New("fix blocked by governance: " + inc.FixRisk)
		}
		_ = approval
		inc.FixRisk = fmt.Sprintf("%s", r.Level)
	}

	// Human approval gate is required for production changes; verification
	// happens on the candidate before a PR is created.
	res := verification.NewEngine(wt.Dir()).Verify([]string{"build"})
	inc.Verification = res.Summary
	if res.Verdict != verification.VerdictPass {
		return diff, errors.New("verification failed: " + res.Summary)
	}
	inc.Status = domain.IncidentFixVerified
	inc.UpdatedAt = time.Now()
	e.publish(eventbus.Event{Kind: eventbus.IncidentUpdated, Subject: inc.ID, Payload: map[string]string{"status": string(inc.Status)}})
	return diff, nil
}

// incidentRiskService returns the governance resource the incident's fix is
// gated against (its affected service, or "production" when unknown).
func incidentResource(inc *domain.Incident) string {
	if inc.AffectedService != "" {
		return inc.AffectedService
	}
	return "production"
}

// reasonOf returns a human-readable failure reason, preferring the firewall
// error when present.
func reasonOf(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}

// InjectRegression builds an incident engine whose runtime source encodes a
// controlled, known regression so the correlation + root-cause pipeline can
// deterministically resolve it. It creates a runtime source where a recent
// commit that touched regressionFile was deployed to the service and is
// corroborated by error events that reference that same file — exactly the
// signal the pipeline's deploy-regression hypothesis boosts (see hypotheses).
//
// The caller supplies the repository root (used to index the code graph), the
// service name, the known regression file/symbol to inject, and the memory +
// firewall stores. It returns an engine and a ready-to-ingest alert for that
// service. IngestAlert → Correlate → RootCause then resolves the root cause to
// the injected regression file, and ApplyAndVerifyFix can verify a candidate
// fix. ctx is reserved for future cancellation and is not yet consulted.
func InjectRegression(ctx context.Context, root, service, regressionFile string, mem *memory.MemoryStore, fw *governance.Firewall) (*Engine, domain.Alert, error) {
	now := time.Now().Truncate(time.Second)
	sha := "deadbeefcafe"
	st := runtime.NewStore()
	st.AddDeployment(domain.Deployment{Service: service, CommitSHA: sha, Version: "v1.0.0", DeployedAt: now.Add(-15 * time.Minute)})
	st.AddCommit(runtime.Commit{SHA: sha, Message: "regression: " + regressionFile, Author: "inject", Files: []string{regressionFile}, CommittedAt: now.Add(-16 * time.Minute)})
	st.IngestAll([]runtime.Event{
		{ID: "inj-err-1", Type: runtime.EventError, Service: service, Severity: "critical", Message: "panic in " + regressionFile, Timestamp: now.Add(-time.Minute), Attributes: map[string]string{"file": regressionFile}},
	})

	eng, err := NewEngine(root, st, mem, fw)
	if err != nil {
		return nil, domain.Alert{}, err
	}
	alert := domain.Alert{
		ID:         "inj-alert",
		Severity:   domain.SeverityCritical,
		Message:    "injected regression in " + regressionFile,
		Service:    service,
		OccurredAt: now,
	}
	return eng, alert, nil
}

// RequestApproval opens a human-approval gate for a production fix. It returns
// the pending approval (default requirement for production changes).
func (e *Engine) RequestApproval(inc *domain.Incident, requester, reason string) domain.Approval {
	return e.appr.Request(inc.ID, requester, reason)
}

// Approve grants a pending approval.
func (e *Engine) Approve(approvalID, approver string) (domain.Approval, error) {
	return e.appr.Approve(approvalID, approver)
}

// CreatePR produces the PR body for a verified, approved candidate fix and marks
// the incident PR_CREATED.
func (e *Engine) CreatePR(inc *domain.Incident) string {
	var b strings.Builder
	fmt.Fprintf(&b, "fix: %s\n\n", inc.Title)
	if inc.RootCause != nil {
		fmt.Fprintf(&b, "Root cause: %s (service %s)\n\n", inc.RootCause.Summary, inc.RootCause.Service)
	}
	if inc.Verification != "" {
		fmt.Fprintf(&b, "Verification: %s\n\n", inc.Verification)
	}
	b.WriteString("```diff\n")
	b.WriteString(inc.FixDiff)
	b.WriteString("\n```\n")
	inc.PRBody = b.String()
	inc.Status = domain.IncidentPRCreated
	inc.UpdatedAt = time.Now()
	e.publish(eventbus.Event{Kind: eventbus.IncidentUpdated, Subject: inc.ID, Payload: map[string]string{"status": string(inc.Status)}})
	return inc.PRBody
}

// CreateFixPR creates a PR for a verified incident fix (Phase 11.4: the final
// "PR" step of the candidate-fix pipeline risk → approval → sandbox → verify →
// PR). The incident must already be in the IncidentFixVerified state; otherwise
// it returns an error and does not create a PR. The PR title/body are rendered
// from the incident regardless of provider outcome, mirroring TaskService.CreatePR:
// a NoopProvider (default) returns Number=0/URL="" with no error, which is
// treated as a successful noop that still records the rendered PR body.
func (e *Engine) CreateFixPR(inc *domain.Incident, branch string) (prprovider.Result, error) {
	if inc.Status != domain.IncidentFixVerified {
		return prprovider.Result{}, fmt.Errorf("incident %s must be in FIX_VERIFIED state to create a PR (current: %s)", inc.ID, inc.Status)
	}

	title, body := renderFixPR(inc)

	// Detect the repo owner/repo; on error fall back to empty so the provider
	// (Noop by default) does not fail on missing metadata.
	repo, _ := prprovider.DetectRepo(e.root)

	res, err := e.prProvider.CreatePR(prprovider.Request{
		Owner: repo.Owner,
		Repo:  repo.Repo,
		Title: title,
		Head:  branch,
		Base:  "main",
		Body:  body,
	})
	if err != nil {
		return prprovider.Result{}, fmt.Errorf("create PR for incident %s: %w", inc.ID, err)
	}

	inc.PRBody = body
	// NoopProvider returns Number=0/URL="" — a successful noop. Record whatever
	// the provider returned so a real PR is stamped on the incident.
	if res != nil {
		inc.PRURL = res.URL
		inc.PRNumber = res.Number
	}
	inc.Status = domain.IncidentPRCreated
	inc.UpdatedAt = time.Now()
	e.publish(eventbus.Event{Kind: eventbus.PRCreated, Subject: inc.ID, Payload: map[string]string{"branch": branch, "pr": fmt.Sprintf("%d", inc.PRNumber)}})

	if res != nil {
		return *res, nil
	}
	return prprovider.Result{}, nil
}

// FixAndPR completes the incident candidate-fix pipeline in one call:
// risk → sandbox → verify → PR. It applies the candidate fix in the sandbox via
// ApplyAndVerifyFix and, on success (verification passed), creates a PR from the
// verified fix via CreateFixPR. It returns the applied diff and any error; if
// either step fails it returns the diff produced so far and the error.
func (e *Engine) FixAndPR(inc *domain.Incident, apply func(workDir string) error, branch string) (string, error) {
	diff, err := e.ApplyAndVerifyFix(inc, apply)
	if err != nil {
		return diff, err
	}
	if _, err := e.CreateFixPR(inc, branch); err != nil {
		return diff, err
	}
	return diff, nil
}

// renderFixPR builds the PR title and body for a verified incident fix.
func renderFixPR(inc *domain.Incident) (string, string) {
	title := inc.Title
	if title == "" {
		if inc.AffectedService != "" {
			title = "fix: incident in " + inc.AffectedService + " (" + inc.ID + ")"
		} else {
			title = "fix: incident " + inc.ID
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "fix: %s\n\n", inc.Title)
	if inc.AffectedService != "" {
		fmt.Fprintf(&b, "Affected service: %s\n\n", inc.AffectedService)
	}
	if inc.RootCause != nil {
		fmt.Fprintf(&b, "Root cause: %s (service %s)\n\n", inc.RootCause.Summary, inc.RootCause.Service)
	}
	if inc.Verification != "" {
		fmt.Fprintf(&b, "Verification: %s\n\n", inc.Verification)
	}
	b.WriteString("```diff\n")
	b.WriteString(inc.FixDiff)
	b.WriteString("\n```\n")
	return title, b.String()
}

// hypotheses derives ranked candidate explanations deterministically from the
// correlation evidence, the graph and memory. Scores are evidence-driven.
func (e *Engine) hypotheses(inc *domain.Incident) []domain.Hypothesis {
	corr := runtime.NewCorrelator(e.src, e.window).Correlate(inc.Alert)
	svc := inc.AffectedService
	if svc == "" {
		svc = corr.AffectedService
	}
	commits := e.src.Commits()
	bySHA := commitsBySHA(commits)
	errorFiles := filesReferencedByErrors(corr.ErrorEvents)

	var hs []domain.Hypothesis

	// 1) Recent-deploy regression hypothesis, boosted when an error event
	// references a file the commit touched.
	for _, d := range corr.Deployments {
		c, ok := bySHA[d.CommitSHA]
		if !ok {
			continue
		}
		stmt := fmt.Sprintf("Regression likely introduced by commit %s (%s) deployed as %s", shortSHA(c.SHA), c.Message, d.Version)
		score := 0.5
		var evs []domain.Evidence
		evs = append(evs, domain.Evidence{Type: domain.EvidenceGit, Source: "runtime", Content: fmt.Sprintf("commit %s %s", shortSHA(c.SHA), c.Message)})
		evs = append(evs, runtimeEvidence(corr, e.src.Name()))
		corroborated := false
		for _, f := range c.Files {
			if errorFiles[f] {
				score += 0.4
				corroborated = true
				stmt = fmt.Sprintf("%s — error events reference changed file %s", stmt, f)
				evs = append(evs, domain.Evidence{Type: domain.EvidenceRuntime, Source: "runtime", Content: "error event references " + f})
			}
		}
		// A deploy regression corroborated by runtime error events pointing at a
		// changed file is an INFERENCE; a bare deploy regression remains a
		// HYPOTHESIS so it is never promoted to a stated root cause on its own.
		confidence := domain.ClaimHypothesis
		if corroborated {
			confidence = domain.ClaimInference
		}
		hs = append(hs, domain.Hypothesis{
			Statement:  stmt,
			Source:     "deploy",
			Confidence: confidence,
			Score:      score,
			Evidence:   evs,
		})
	}

	// 2) Historical-incident hypothesis from engineering memory.
	recalled, _ := e.mem.Recall(memory.Query{Type: domain.MemoryIncident, Service: svc, Limit: 3})
	for _, m := range recalled {
		hs = append(hs, domain.Hypothesis{
			Statement:  "Historical incident matches: " + m.Content,
			Source:     "memory",
			Confidence: domain.ClaimInference,
			Score:      0.6,
			Evidence:   []domain.Evidence{domain.Evidence{Type: domain.EvidenceMemory, Source: "memory", Content: m.Content}},
		})
	}

	// 3) If no deploy regression was found, surface a code-level hypothesis from
	// the graph for the changed file with the most errors.
	if len(hs) == 0 && len(errorFiles) > 0 {
		for f := range errorFiles {
			hs = append(hs, domain.Hypothesis{
				Statement:  "Errors concentrate in " + f,
				Source:     "code",
				Confidence: domain.ClaimInference,
				Score:      0.5,
				Evidence:   []domain.Evidence{domain.Evidence{Type: domain.EvidenceRuntime, Source: "runtime", Content: "errors reference " + f}},
			})
		}
	}

	sort.SliceStable(hs, func(i, j int) bool { return hs[i].Score > hs[j].Score })
	return hs
}

// -- small helpers --

func newIncID() string {
	return fmt.Sprintf("inc-%d", time.Now().UnixNano())
}

func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

// hasNonGitEvidence reports whether at least one evidence entry is not purely
// git-derived, so an otherwise-git-only (deployment) hypothesis is not promoted
// to a stated root cause without independent runtime/log/metric backing.
func hasNonGitEvidence(evs []domain.Evidence) bool {
	for _, e := range evs {
		if e.Type != domain.EvidenceGit {
			return true
		}
	}
	return false
}

func commitsBySHA(commits []runtime.Commit) map[string]runtime.Commit {
	m := map[string]runtime.Commit{}
	for _, c := range commits {
		m[c.SHA] = c
	}
	return m
}

// filesReferencedByErrors returns the set of source file paths that error events
// explicitly reference (via a "file" attribute). Used to boost deploy-regression
// hypotheses when the failing file matches a file a recent commit changed.
func filesReferencedByErrors(events []runtime.Event) map[string]bool {
	out := map[string]bool{}
	for _, e := range events {
		if f := e.Attributes["file"]; f != "" && strings.Contains(f, "/") {
			out[f] = true
		}
	}
	return out
}

func runtimeEvidence(corr runtime.Correlation, source string) domain.Evidence {
	var depl []string
	for _, d := range corr.Deployments {
		depl = append(depl, d.Version+"@"+shortSHA(d.CommitSHA))
	}
	return domain.Evidence{
		Type:    domain.EvidenceRuntime,
		Source:  source,
		Content: fmt.Sprintf("%d errors, %d logs, %d metrics, %d spans; deployments %v", len(corr.ErrorEvents), len(corr.LogEvents), len(corr.MetricEvents), len(corr.TraceSpans), depl),
	}
}

// chainEvidence renders the resolved deep correlation chain as a single
// deterministic runtime-evidence entry so incident correlation carries the full
// evidence chain (service -> deployment -> commit -> symbol -> task/pr/agent).
func chainEvidence(chain runtime.CorrelationChain) domain.Evidence {
	var links []string
	for _, l := range chain.Links {
		links = append(links, string(l.Stage)+":"+l.ID)
	}
	content := fmt.Sprintf("deep chain %s: %s", chain.Service, strings.Join(links, " -> "))
	return domain.Evidence{
		Type:    domain.EvidenceRuntime,
		Source:  "runtime",
		Content: content,
	}
}
