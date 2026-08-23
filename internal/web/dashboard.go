package web

import (
	"embed"
	"html/template"
)

//go:embed dashboard.html task_detail.html agents.html tasks.html approvals.html risks.html artifacts.html audit.html system_map.html incidents.html efficiency.html graph.html memory.html architecture.html eval.html
var dashboardTemplate embed.FS

// parseDashboardTemplate parses the single embedded dashboard template. The
// embedded file provides the template name ("dashboard.html"), so ParseFS is
// handed a matching New name. The returned template is what handleIndex uses
// to serve the read-only HTML dashboard at "/".
func parseDashboardTemplate() (*template.Template, error) {
	return template.New("dashboard.html").ParseFS(dashboardTemplate, "dashboard.html")
}

// parseTaskDetailTemplate parses the single embedded task detail template. The
// embedded file provides the template name ("task_detail.html"), so ParseFS is
// handed a matching New name. The returned template is what handleTaskDetail
// uses to serve the read-only HTML detail page at "/task/{id}".
func parseTaskDetailTemplate() (*template.Template, error) {
	return template.New("task_detail.html").ParseFS(dashboardTemplate, "task_detail.html")
}

// parseAgentsTemplate parses the embedded agents template. The returned
// template is what handleAgents uses to serve the specialist roster at
// "/agents".
func parseAgentsTemplate() (*template.Template, error) {
	return template.New("agents.html").ParseFS(dashboardTemplate, "agents.html")
}

// parseTasksTemplate parses the embedded tasks/efficiency template. The
// returned template is what handleTasks uses to serve the per-task efficiency
// roster at "/tasks".
func parseTasksTemplate() (*template.Template, error) {
	return template.New("tasks.html").ParseFS(dashboardTemplate, "tasks.html")
}

// parseApprovalsTemplate parses the embedded approvals template.
func parseApprovalsTemplate() (*template.Template, error) {
	return template.New("approvals.html").ParseFS(dashboardTemplate, "approvals.html")
}

// parseRisksTemplate parses the embedded risks template.
func parseRisksTemplate() (*template.Template, error) {
	return template.New("risks.html").ParseFS(dashboardTemplate, "risks.html")
}

// parseArtifactsTemplate parses the embedded artifacts template.
func parseArtifactsTemplate() (*template.Template, error) {
	return template.New("artifacts.html").ParseFS(dashboardTemplate, "artifacts.html")
}

// parseAuditTemplate parses the embedded audit template.
func parseAuditTemplate() (*template.Template, error) {
	return template.New("audit.html").ParseFS(dashboardTemplate, "audit.html")
}

// parseSystemMapTemplate parses the embedded system-map template.
func parseSystemMapTemplate() (*template.Template, error) {
	return template.New("system_map.html").ParseFS(dashboardTemplate, "system_map.html")
}

// parseIncidentsTemplate parses the embedded incidents template.
func parseIncidentsTemplate() (*template.Template, error) {
	return template.New("incidents.html").ParseFS(dashboardTemplate, "incidents.html")
}

// parseEfficiencyTemplate parses the embedded efficiency template.
func parseEfficiencyTemplate() (*template.Template, error) {
	return template.New("efficiency.html").ParseFS(dashboardTemplate, "efficiency.html")
}

// parseGraphTemplate parses the embedded graph template. The returned template
// is what handleGraph uses to serve the hub/community inspector at "/graph".
func parseGraphTemplate() (*template.Template, error) {
	return template.New("graph.html").ParseFS(dashboardTemplate, "graph.html")
}

// parseMemoryTemplate parses the embedded memory template. The returned
// template is what handleMemory uses to serve the typed-memory roster at
// "/memory".
func parseMemoryTemplate() (*template.Template, error) {
	return template.New("memory.html").ParseFS(dashboardTemplate, "memory.html")
}

// parseArchitectureTemplate parses the embedded architecture template. The
// returned template is what handleArchitecture uses to serve the layered
// architecture report at "/architecture".
func parseArchitectureTemplate() (*template.Template, error) {
	return template.New("architecture.html").ParseFS(dashboardTemplate, "architecture.html")
}

// parseEvalTemplate parses the embedded evaluation template. The returned
// template is what handleEval uses to serve the agent-comparison, task-replay
// and context-inspection views at "/eval".
func parseEvalTemplate() (*template.Template, error) {
	return template.New("eval.html").ParseFS(dashboardTemplate, "eval.html")
}
