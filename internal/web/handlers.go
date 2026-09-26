package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// handleIndex serves the HTML dashboard at "/" and a 404 JSON object for any
// other path.
func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	data, err := a.buildDashboard()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.dashboardT.Execute(w, data)
}

// handleTaskDetail serves an HTML detail page for a single task at /task/{id},
// showing all 13 lifecycle fields.
func (a *App) handleTaskDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/task/"))
	if err != nil || strings.TrimSpace(id) == "" {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}

	// Look up the task from the registry, falling back to the store.
	task, ok := a.tasks.GetTask(id)
	if !ok {
		if st := a.tasks.TaskStore(); st != nil {
			if t, serr := st.Get(id); serr == nil {
				task = &t
			}
		}
	}
	if task == nil {
		writeError(w, http.StatusNotFound, "task not found: "+id)
		return
	}

	// Build the template data from the task.
	data := a.buildTaskDetailData(task)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.taskDetailT.Execute(w, data)
}

// handleAgents serves the HTML agents page at /agents, listing the standard
// specialist team and their capabilities. It is read-only.
func (a *App) handleAgents(w http.ResponseWriter, r *http.Request) {
	data, err := a.buildAgents()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.agentsT.Execute(w, data)
}

// handleTasks serves the HTML tasks/efficiency page at /tasks, listing every
// submitted task with a compact per-task efficiency report and a link to its
// detail page. It is read-only.
func (a *App) handleTasks(w http.ResponseWriter, r *http.Request) {
	data, err := a.buildTasks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.tasksT.Execute(w, data)
}

// handleOverview serves the aggregate project overview.
func (a *App) handleOverview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.buildOverview())
}

// handleGraph serves the top hubs and communities.
func (a *App) handleGraph(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.buildGraph(10))
}

// handleMemory serves the typed engineering memories.
func (a *App) handleMemory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.buildMemory())
}

// handleIncidents serves a flattened summary of persisted incidents (GET) or
// records a new incident (POST).
func (a *App) handleIncidents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := a.buildIncidents()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		a.handleIncidentSave(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleArchitecture serves the architecture validation report.
func (a *App) handleArchitecture(w http.ResponseWriter, r *http.Request) {
	rep, err := a.buildArchitecture()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// handleGovernance serves policies, pending approvals and the audit log.
func (a *App) handleGovernance(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.buildGovernance())
}

// handleHealth serves a trivial liveness probe. GET/HEAD only — net/http
// serves HEAD through the GET handler, so a GET check is sufficient. Any
// other method (POST/PUT/DELETE) returns 405, matching the method guards on
// the other API routes.
func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// Beyond "ok", the probe reports the index status (whether a background
	// graph rebuild is in flight) so the console pages can poll it for their
	// pending-state line; see indexStatus. Additive field — "ok" is unchanged.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "index": a.indexStatus()})
}

// handleApprovalsPending serves the current pending approvals.
func (a *App) handleApprovalsPending(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.buildApprovals())
}

// approvalDecision is the JSON body for approve/reject actions.
type approvalDecision struct {
	ID       string `json:"id"`
	Approver string `json:"approver"`
}

// requireApproverRole enforces the org RBAC layer (Feature Batch G) on the
// approvals endpoints: when a user registry is wired (a.userRole != nil), the
// approver's role must allow the action; an unknown approver is denied. When
// no registry is wired the check passes (the historical single-user flow).
// A denied decision is itself recorded in the governance audit trail so
// failed multi-user attempts are visible in the console's audit surface.
func (a *App) requireApproverRole(approver, action, approvalID string) error {
	if a.userRole == nil {
		return nil
	}
	role, ok := a.userRole(approver)
	if !ok {
		a.recordApprovalAudit(approver, action, "denied", approvalID, "approver not a registered org user")
		return fmt.Errorf("approver %q is not a registered org user", approver)
	}
	if err := governance.RequireOrgRole(role, action); err != nil {
		a.recordApprovalAudit(approver, action, "denied", approvalID, err.Error())
		return err
	}
	return nil
}

// recordApprovalAudit writes an approval decision into the governance audit
// trail (approver identity + approval ID + decision). Invariant 4/6: the
// console's /audit and /api/audit surfaces read this log. The audit entry
// ALSO records the AUTHENTICATED principal (authPrincipal) separately from
// the approver identity the request body declared, so impersonation is
// detectable in the tamper-evident trail: any token holder can declare any
// approver name, but the principal column shows who actually authenticated.
func (a *App) recordApprovalAudit(approver, action, result, approvalID, reason string) {
	if a.firewall == nil {
		return
	}
	a.firewall.AuditLog().Record(governance.AuditEntry{
		AgentID:   approver,
		Action:    action,
		Resource:  approvalID,
		Result:    result,
		TaskID:    "",
		Reason:    reason,
		Principal: authPrincipal(),
	})
}

// authPrincipal identifies WHO authenticated the request, as distinct from the
// approver name the request body declares. There is no per-user identity with
// the shared KERN_AUTH_TOKEN — every token holder is the same principal — so
// the audit records the literal "shared-token"; with no token the console
// trusts the loopback client and records "loopback". Server-side state, never
// client-supplied, so it cannot be spoofed by the request body.
func authPrincipal() string {
	if os.Getenv(authTokenEnv) != "" {
		return "shared-token"
	}
	return "loopback"
}

// recordPolicySignals runs the best-effort policy-signal learning pass after
// a human approval decision (Self-Improvement use-cases Tier 3 #7): it learns
// from the approval log which actions always get approved vs which are risky
// and writes typed-claim memories (RECOMMENDATION / INFERENCE). Learning
// proposes, policy change approves — nothing here touches the firewall or
// policy store. Best-effort: a failure is logged and never blocks the
// decision that already happened; a nil store/memory (unwired App) is a
// no-op.
func (a *App) recordPolicySignals() {
	if _, err := app.RecordPolicySignals(a.fileApprovals, a.memories, app.DefaultPolicySignalThreshold); err != nil {
		log.Printf("web: policy signal learning skipped: %v", err)
	}
}

// handleApprovalApprove marks a pending approval as approved. It only accepts
// POST; any other method returns 405.
// In addition to marking the approval workflow's record as approved,
// this now also calls firewall.ApproveAction so the governance gate's
// approvedKeys map is populated — without this, a web approval would never
// unblock the firewall Check that originally requested it.
func (a *App) handleApprovalApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req approvalDecision
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" || req.Approver == "" {
		writeError(w, http.StatusBadRequest, "id and approver are required")
		return
	}
	// Multi-user RBAC (Feature Batch G): the approver's role must allow
	// approve. Denied -> 403 with the governance denial message.
	if err := a.requireApproverRole(req.Approver, governance.OrgActionApprove, req.ID); err != nil {
		writeError(w, http.StatusForbidden, "approve denied: "+err.Error())
		return
	}
	updated, err := a.approvals.Approve(req.ID, req.Approver)
	if err != nil {
		// The approval may be a workflow-engine gate persisted only in the
		// file store (not this in-memory workflow): fall back to the
		// persistent store so a UI approve still resolves it. Routing through
		// the app-layer TaskService both persists the decision AND advances a
		// gated task parked at WAITING_FOR_APPROVAL (mirroring
		// `kern approve`).
		if a.taskSvc != nil {
			if _, ferr := a.taskSvc.ResolveApprovalForTask(req.ID, req.Approver, true, ""); ferr != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("approval %q not found in the in-memory workflow (%v) and the decision could not be recorded: %v", req.ID, err, ferr))
				return
			}
			if a.firewall != nil {
				if aerr := a.firewall.ApproveAction(req.ID, req.Approver); aerr != nil {
					// The durable decision was recorded in the file store above;
					// this failure only means the in-process firewall gate was
					// not notified (e.g. the approval predates this process, so
					// its in-memory workflow does not know it). Surface it so the
					// operator knows the gate may stay blocked.
					log.Printf("web approve %s: decision recorded in the file store, but propagating to the in-process firewall failed: %v", req.ID, aerr)
				}
			}
			a.recordPolicySignals()
			a.bus.Publish(eventbus.Event{Kind: eventbus.ApprovalGranted, Source: "web", Subject: req.ID})
			writeJSON(w, http.StatusOK, map[string]string{"id": req.ID, "status": "approved"})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Also record the decision in the persistent store so the workflow engine
	// (which reads the file store for its gates) observes it on resume, and
	// advance a gated task parked at WAITING_FOR_APPROVAL. Routing
	// through the app-layer TaskService makes the web approve behave like
	// `kern approve` / kern_approve. Approve above already persisted the
	// decision through the workflow's own store; a failure of this
	// belt-and-braces write does not invalidate it, but it must not be silent
	// either.
	if a.taskSvc != nil {
		if _, derr := a.taskSvc.ResolveApprovalForTask(req.ID, req.Approver, true, ""); derr != nil {
			log.Printf("web approve %s: decision persisted by the workflow, but the persistent-store confirmation write failed: %v", req.ID, derr)
		}
	}
	// Propagate the approval to the firewall so the governance gate's
	// approvedKeys map is populated and a subsequent Check passes.
	if a.firewall != nil {
		if aerr := a.firewall.ApproveAction(req.ID, req.Approver); aerr != nil {
			// The decision is durably recorded; this only means the in-process
			// gate was not notified. Surface it so the operator knows a
			// subsequent Check may stay blocked.
			log.Printf("web approve %s: decision recorded, but propagating to the in-process firewall failed (the governance gate may stay blocked): %v", req.ID, aerr)
		}
		// Invariant 4/6: record the approval with the approver's identity and
		// the task ID so the audit trail is queryable by task. The
		// authenticated principal is recorded separately (authPrincipal) so a
		// token holder cannot impersonate a registered user in the trail.
		a.firewall.AuditLog().Record(governance.AuditEntry{
			AgentID:   req.Approver,
			Action:    "approve",
			Resource:  req.ID,
			Result:    "approved",
			TaskID:    updated.TaskID,
			Principal: authPrincipal(),
		})
	}
	a.recordPolicySignals()
	a.bus.Publish(eventbus.Event{Kind: eventbus.ApprovalGranted, Source: "web", Subject: req.ID})
	writeJSON(w, http.StatusOK, updated)
}

// handleApprovalReject marks a pending approval as rejected. It only accepts
// POST; any other method returns 405.
func (a *App) handleApprovalReject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req approvalDecision
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" || req.Approver == "" {
		writeError(w, http.StatusBadRequest, "id and approver are required")
		return
	}
	// Multi-user RBAC (Feature Batch G): the approver's role must allow
	// reject. Denied -> 403 with the governance denial message.
	if err := a.requireApproverRole(req.Approver, governance.OrgActionReject, req.ID); err != nil {
		writeError(w, http.StatusForbidden, "reject denied: "+err.Error())
		return
	}
	updated, err := a.approvals.Reject(req.ID, req.Approver, "rejected via console")
	if err != nil {
		// Fall back to the persistent store (workflow-engine gates live there).
		// Routing through the app-layer TaskService both persists the decision
		// AND advances a gated task parked at WAITING_FOR_APPROVAL to REJECTED
		// (mirroring `kern approve --reject`).
		if a.taskSvc != nil {
			if _, ferr := a.taskSvc.ResolveApprovalForTask(req.ID, req.Approver, false, "rejected via console"); ferr != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("approval %q not found in the in-memory workflow (%v) and the decision could not be recorded: %v", req.ID, err, ferr))
				return
			}
			a.recordPolicySignals()
			a.bus.Publish(eventbus.Event{Kind: eventbus.ApprovalRejected, Source: "web", Subject: req.ID})
			writeJSON(w, http.StatusOK, map[string]string{"id": req.ID, "status": "rejected"})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Record the rejection in the persistent store too, so any gate reading
	// the file store observes it, and advance a gated task parked at
	// WAITING_FOR_APPROVAL to REJECTED. Routing through the
	// app-layer TaskService makes the web reject behave like
	// `kern approve --reject` / kern_approve reject=true. Reject above
	// already persisted the decision through the workflow's own store; a
	// failure here must not be silent.
	if a.taskSvc != nil {
		if _, derr := a.taskSvc.ResolveApprovalForTask(req.ID, req.Approver, false, "rejected via console"); derr != nil {
			log.Printf("web reject %s: decision persisted by the workflow, but the persistent-store confirmation write failed: %v", req.ID, derr)
		}
	}
	// Invariant 4/6: record the rejection with the approver's identity. The
	// authenticated principal is recorded separately (authPrincipal) so a
	// token holder cannot impersonate a registered user in the trail.
	if a.firewall != nil {
		a.firewall.AuditLog().Record(governance.AuditEntry{
			AgentID:   req.Approver,
			Action:    "reject",
			Resource:  req.ID,
			Result:    "denied",
			TaskID:    updated.TaskID,
			Principal: authPrincipal(),
		})
	}
	a.recordPolicySignals()
	a.bus.Publish(eventbus.Event{Kind: eventbus.ApprovalRejected, Source: "web", Subject: req.ID})
	writeJSON(w, http.StatusOK, updated)
}

// handleIncidentSave records a new incident. It only accepts POST; any other
// method returns 405.
func (a *App) handleIncidentSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var inc domain.Incident
	if err := json.NewDecoder(r.Body).Decode(&inc); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The ID is generated inside the store's Save under its mutex (using
	// crypto/rand) so concurrent requests can never collide on a timestamp.
	if inc.CreatedAt.IsZero() {
		inc.CreatedAt = time.Now().UTC()
	}
	if inc.UpdatedAt.IsZero() {
		inc.UpdatedAt = time.Now().UTC()
	}
	saved, err := a.inter.Save(&inc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.bus.Publish(eventbus.Event{
		Kind:    eventbus.IncidentCreated,
		Source:  "web",
		Subject: saved.ID,
		Service: saved.AffectedService,
	})
	writeJSON(w, http.StatusOK, saved)
}
