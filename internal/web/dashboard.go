package web

import (
	"embed"
	"html/template"
)

//go:embed dashboard.html
var dashboardTemplate embed.FS

// parseDashboardTemplate parses the single embedded dashboard template. The
// embedded file provides the template name ("dashboard.html"), so ParseFS is
// handed a matching New name. The returned template is what handleIndex uses
// to serve the read-only HTML dashboard at "/".
func parseDashboardTemplate() (*template.Template, error) {
	return template.New("dashboard.html").ParseFS(dashboardTemplate, "dashboard.html")
}
