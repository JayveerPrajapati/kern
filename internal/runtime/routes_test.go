package runtime

import (
	"strings"
	"testing"
)

// TestParsePrometheusLabelsPinsAttributes pins the label->attributes
// extraction: every label is recorded verbatim (route/method/status), and
// unquoted bool labels map to "1".
func TestParsePrometheusLabelsPinsAttributes(t *testing.T) {
	data := []byte(`http_requests_total{route="/api/users",method="GET",status="200"} 42 1750000000
http_errors_total{route="/api/users",service="app",code="500"} 3 1750000000
bool_metric{flag} 1 1750000000
plain_metric 7 1750000000`)
	events, err := ParsePrometheus(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4", len(events))
	}
	e := events[0]
	if e.Attributes["route"] != "/api/users" || e.Attributes["method"] != "GET" || e.Attributes["status"] != "200" {
		t.Fatalf("attrs = %v, want route/method/status extracted", e.Attributes)
	}
	if e.Service != "" {
		t.Fatalf("service = %q, want empty (no service label)", e.Service)
	}
	if events[1].Service != "app" {
		t.Fatalf("service = %q, want app", events[1].Service)
	}
	if events[2].Attributes["flag"] != "1" {
		t.Fatalf("bool label attrs = %v, want flag=1", events[2].Attributes)
	}
	if len(events[3].Attributes) != 0 {
		t.Fatalf("plain metric attrs = %v, want none", events[3].Attributes)
	}
}

// TestRoutesAggregatesByRoute pins route-level aggregation: events group by
// route+service with error rates, sorted deterministically.
func TestRoutesAggregatesByRoute(t *testing.T) {
	st, err := ParseSnapshot([]byte(`{"events":[
		{"id":"a","type":"metric","service":"app","severity":"info","message":"rps=1","attributes":{"route":"/api/users"},"timestamp":"2026-09-09T10:00:00Z"},
		{"id":"b","type":"error","service":"app","severity":"error","message":"e","attributes":{"route":"/api/users"},"timestamp":"2026-09-09T10:00:01Z"},
		{"id":"c","type":"metric","service":"app","severity":"info","message":"rps=2","attributes":{"path":"/health"},"timestamp":"2026-09-09T10:00:02Z"},
		{"id":"d","type":"metric","service":"worker","severity":"info","message":"rps=1","attributes":{"route":"/api/users"},"timestamp":"2026-09-09T10:00:03Z"},
		{"id":"e","type":"metric","service":"app","severity":"info","message":"rps=1","timestamp":"2026-09-09T10:00:04Z"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	routes := Routes(st)
	if len(routes) != 3 {
		t.Fatalf("routes = %d, want 3 (unlabeled event excluded)", len(routes))
	}
	if routes[0].Route != "/api/users" || routes[0].Service != "app" {
		t.Fatalf("routes[0] = %+v, want /api/users + app", routes[0])
	}
	if routes[0].Events != 2 || routes[0].Errors != 1 || routes[0].ErrorRate != 50 {
		t.Fatalf("routes[0] = %+v, want 2 events / 1 error / 50%%", routes[0])
	}
	if Routes(nil) != nil {
		t.Fatal("Routes(nil) must be nil")
	}
}

// TestDriftTemplateAware pins the drift comparison: parameterized code
// routes match concrete runtime routes segment-wise (both the ":id" and
// "{uid}" template forms); normalization handles trailing slashes and
// queries; both directions are reported.
func TestDriftTemplateAware(t *testing.T) {
	code := []string{"/api/users/:id", "/api/users/{uid}", "/health/", "/api/users", "/legacy"}
	runtimeRoutes := []RouteInfo{
		{Route: "/api/users/42", Service: "app", Events: 10, Errors: 1},
		{Route: "/health", Service: "app", Events: 5},
		{Route: "/api/users?page=2", Service: "app", Events: 3},
		{Route: "/ghost", Service: "app", Events: 100, Errors: 99},
	}
	rep := Drift(runtimeRoutes, code)
	// /api/users/42 matches BOTH template forms, plus /health and the exact
	// /api/users route -> 4 distinct code routes have traffic.
	if rep.Matched != 4 {
		t.Fatalf("matched = %d, want 4 (both templates + /health + /api/users)", rep.Matched)
	}
	if len(rep.CodeOnly) != 1 || rep.CodeOnly[0] != "/legacy" {
		t.Fatalf("code-only = %v, want [/legacy]", rep.CodeOnly)
	}
	if len(rep.ProdOnly) != 1 || rep.ProdOnly[0].Route != "/ghost" {
		t.Fatalf("prod-only = %+v, want [/ghost]", rep.ProdOnly)
	}
	if rep.ProdOnly[0].Events != 100 || rep.ProdOnly[0].Errors != 99 {
		t.Fatalf("prod-only detail = %+v, want 100 events / 99 errors", rep.ProdOnly[0])
	}
	// Nil/empty inputs are valid.
	if Drift(nil, nil).Matched != 0 {
		t.Fatal("Drift(nil, nil) must be empty")
	}
}

// TestNormalizeRoute pins canonicalization.
func TestNormalizeRoute(t *testing.T) {
	cases := map[string]string{
		"/health/":    "/health",
		"/":           "/",
		"*api/x":      "api/x",
		"/x?a=1&b=2":  "/x",
		"/api/users/": "/api/users",
	}
	for in, want := range cases {
		if got := normalizeRoute(in); got != want {
			t.Fatalf("normalizeRoute(%q) = %q, want %q", in, got, want)
		}
	}
	if !strings.Contains(normalizeRoute(" /spaced "), "spaced") {
		t.Fatal("normalizeRoute must trim spaces")
	}
}
