package web

import (
	"embed"
	"html/template"
)

//go:embed dashboard.html task_detail.html
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
