package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
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
