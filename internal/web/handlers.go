package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance/audit"
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
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
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

// handleHealth serves a trivial liveness probe.
func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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

// handleApprovalApprove marks a pending approval as approved. It only accepts
// POST; any other method returns 405.
//
// Phase 9: in addition to marking the approval workflow's record as approved,
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
	updated, err := a.approvals.Approve(req.ID, req.Approver)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Propagate the approval to the firewall so the governance gate's
	// approvedKeys map is populated and a subsequent Check passes.
	if a.firewall != nil {
		_ = a.firewall.ApproveAction(req.ID, req.Approver)
		// Invariant 4/6: record the approval with the approver's identity and
		// the task ID so the audit trail is queryable by task.
		a.firewall.AuditLog().Record(audit.AuditEntry{
			AgentID: req.Approver,
			Action:  "approve",
			Resource: req.ID,
			Result:  "approved",
			TaskID:  updated.TaskID,
		})
	}
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
	updated, err := a.approvals.Reject(req.ID, req.Approver, "rejected via console")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Invariant 4/6: record the rejection with the approver's identity.
	if a.firewall != nil {
		a.firewall.AuditLog().Record(audit.AuditEntry{
			AgentID:  req.Approver,
			Action:   "reject",
			Resource: req.ID,
			Result:   "denied",
			TaskID:   updated.TaskID,
		})
	}
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
