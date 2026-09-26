// Org approvals REST surface. The enterprise org API mounts this at
// /org/approvals (thin delegating stubs in internal/enterprise); the whole
// handler lives here so the enterprise package stays at its LOC cap.
package orgapprovals

import (
	"encoding/json"
	"net/http"
	"strings"
)

// ServeApprovals serves the org approvals REST surface mounted at
// /org/approvals on the enterprise org API:
//
//   - GET  /org/approvals          → list all org approvals (snake_case)
//   - POST /org/approvals          → create {action, resource, granted_by,
//     reason} — 201 with the created approval; strict body (unknown fields
//     rejected by name)
//   - POST /org/approvals/approve  → approve {id, granted_by} — 200 with the
//     approved approval
//   - POST /org/approvals/reject   → reject {id, granted_by, reason} — 200
//     with the rejected approval
//
// With no org root the list is empty and every write is refused (400) — org
// scope is strictly opt-in. Create/approve/reject events are audited on the
// org audit trail via the process-wide hook (SetAuditHook), which the
// enterprise server wires to its shared org audit log.
func ServeApprovals(w http.ResponseWriter, r *http.Request, root string) {
	switch strings.Trim(strings.TrimPrefix(r.URL.Path, "/org/approvals"), "/") {
	case "":
		serveApprovalsRoot(w, r, root)
	case "approve":
		serveApprovalApprove(w, r, root)
	case "reject":
		serveApprovalReject(w, r, root)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// serveApprovalsRoot implements GET (list) and POST (create) on /org/approvals.
func serveApprovalsRoot(w http.ResponseWriter, r *http.Request, root string) {
	switch r.Method {
	case http.MethodGet:
		approvals := List(root)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"approvals": approvals,
			"count":     len(approvals),
		})
	case http.MethodPost:
		// The write body is strict and server-owned: unknown fields are
		// rejected by name so a client can never believe it created an org
		// approval the server did not record.
		var body struct {
			Action    string `json:"action"`
			Resource  string `json:"resource"`
			GrantedBy string `json:"granted_by"`
			Reason    string `json:"reason"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			http.Error(w, "enterprise: invalid approval body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if body.Action == "" || body.GrantedBy == "" {
			http.Error(w, "enterprise: action and granted_by are required", http.StatusBadRequest)
			return
		}
		a, err := Create(root, body.Action, body.Resource, body.GrantedBy, body.Reason)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(a)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// serveApprovalApprove implements POST /org/approvals/approve.
func serveApprovalApprove(w http.ResponseWriter, r *http.Request, root string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID        string `json:"id"`
		GrantedBy string `json:"granted_by"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		http.Error(w, "enterprise: invalid approval body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.ID == "" || body.GrantedBy == "" {
		http.Error(w, "enterprise: id and granted_by are required", http.StatusBadRequest)
		return
	}
	a, err := Approve(root, body.ID, body.GrantedBy)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a)
}

// serveApprovalReject implements POST /org/approvals/reject.
func serveApprovalReject(w http.ResponseWriter, r *http.Request, root string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID        string `json:"id"`
		GrantedBy string `json:"granted_by"`
		Reason    string `json:"reason"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		http.Error(w, "enterprise: invalid approval body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.ID == "" || body.GrantedBy == "" {
		http.Error(w, "enterprise: id and granted_by are required", http.StatusBadRequest)
		return
	}
	a, err := Reject(root, body.ID, body.GrantedBy, body.Reason)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a)
}
