// Org approvals view (P13 stage 3): a single web console page at
// /org-approvals that lists the org-wide approval store and records
// approve/reject decisions, consistent with the console's existing chrome.
// It reads the org root through governance.OrgRoot() (KERN_ORG_ROOT); with
// no org root the page renders the inert empty state and every POST is
// refused — the per-project approval flow is untouched.
package web

import (
	"net/http"
	"os"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/orgapprovals"
)

// orgApprovalsPageData is the template data for /org-approvals.
type orgApprovalsPageData struct {
	Root      string // project root (console chrome)
	OrgRoot   string // configured org root ("" = org scope inactive)
	Approvals []orgapprovals.Approval
	Count     int
}

// handleOrgApprovals serves the org approvals view.
//   - GET  /org-approvals renders the approval list with approve/reject
//     forms on pending approvals (empty/inert when no org root).
//   - POST /org-approvals records an approve (action=approve, id,
//     granted_by) or reject (action=reject, id, granted_by, reason)
//     decision, then redirects back to the list.
func (a *App) handleOrgApprovals(w http.ResponseWriter, r *http.Request) {
	orgRoot := governance.OrgRoot()
	switch r.Method {
	case http.MethodGet:
		approvals := orgapprovals.List(orgRoot)
		data := orgApprovalsPageData{Root: a.root, OrgRoot: orgRoot, Approvals: approvals, Count: len(approvals)}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = a.orgApprovalsT.Execute(w, data)
	case http.MethodPost:
		if orgRoot == "" {
			writeError(w, http.StatusBadRequest, "no org root configured (set KERN_ORG_ROOT)")
			return
		}
		// Org approvals are org-wide governance decisions: when org scope is
		// active they must never be approvable from an unauthenticated
		// loopback console (finding 7). With KERN_AUTH_TOKEN set, the global
		// bearer gate (ServeHTTP → authorized) already enforced it on every
		// request; the gap this closes is the unset-token case, which the
		// gate historically let through. GET (list) stays open on loopback
		// like the other console pages.
		if os.Getenv(authTokenEnv) == "" {
			writeError(w, http.StatusForbidden, "org approval decisions require KERN_AUTH_TOKEN to be set (org-wide approvals must not be approvable from an unauthenticated console); set KERN_AUTH_TOKEN and retry with 'Authorization: Bearer <token>', or use the enterprise console")
			return
		}
		if err := r.ParseForm(); err != nil {
			writeError(w, http.StatusBadRequest, "invalid form: "+err.Error())
			return
		}
		id, by := r.FormValue("id"), r.FormValue("granted_by")
		if id == "" || by == "" {
			writeError(w, http.StatusBadRequest, "id and granted_by are required")
			return
		}
		switch r.FormValue("action") {
		case "approve":
			if _, err := orgapprovals.Approve(orgRoot, id, by); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		case "reject":
			if _, err := orgapprovals.Reject(orgRoot, id, by, r.FormValue("reason")); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		default:
			writeError(w, http.StatusBadRequest, "action must be approve or reject")
			return
		}
		http.Redirect(w, r, "/org-approvals", http.StatusSeeOther)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
