package web

import (
	"net/http"
	"strings"
	"testing"
)

// phase18Routes are the HTML page routes added in. Each must render
// with 200, set a text/html Content-Type, carry the shared top navigation, and
// contain its expected section heading. They mirror the assertions in
// new_pages_test.go for the earlier pages.
var phase18Routes = []struct {
	path   string
	title  string
	marker string
}{
	{"/graph", "<title>Graph", "Graph at a glance"},
	{"/memory", "<title>Memory", "Memory at a glance"},
	{"/architecture", "<title>Architecture", "Architecture at a glance"},
	{"/eval", "<title>Evaluation", "Evaluation views"},
}

// TestPhase18PagesServe200 asserts every new HTML route returns 200,
// serves text/html, and renders its title plus an expected heading marker.
func TestPhase18PagesServe200(t *testing.T) {
	app := newTestApp(t)
	for _, tt := range phase18Routes {
		rec := get(t, app, tt.path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200: %s", tt.path, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Fatalf("%s Content-Type = %q, want text/html", tt.path, ct)
		}
		body := rec.Body.String()
		if !strings.Contains(body, tt.title) {
			t.Fatalf("%s missing title %q in body", tt.path, tt.title)
		}
		if !strings.Contains(body, tt.marker) {
			t.Fatalf("%s missing marker %q in body", tt.path, tt.marker)
		}
		if !strings.Contains(body, "topnav") {
			t.Fatalf("%s missing shared topnav", tt.path)
		}
	}
}

// TestPhase18EvalSections asserts the /eval page renders all three inspector
// sections (agent comparison, task replay, context inspection) with anchor
// targets so the in-page sub-navigation resolves.
func TestPhase18EvalSections(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/eval")
	if rec.Code != http.StatusOK {
		t.Fatalf("/eval status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, marker := range []string{
		`id="agents"`,
		`id="replay"`,
		`id="context"`,
		"Agent comparison",
		"Task replay",
		"Context inspection",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("/eval missing marker %q", marker)
		}
	}
}

// TestPhase18GraphReusesBuilder asserts /graph links to the /api/graph JSON
// endpoint and renders the hubs/communities sections backed by buildGraph.
func TestPhase18GraphReusesBuilder(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/graph")
	if rec.Code != http.StatusOK {
		t.Fatalf("/graph status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "/api/graph") {
		t.Fatal("/graph missing link to /api/graph")
	}
	if !strings.Contains(body, "Hubs (nodes)") {
		t.Fatal("/graph missing Hubs (nodes) section")
	}
	if !strings.Contains(body, "Communities (clusters)") {
		t.Fatal("/graph missing Communities (clusters) section")
	}
}

// TestPhase18ArchitectureReusesBuilder asserts /architecture links to the
// /api/architecture JSON endpoint and renders the violations section backed by
// buildArchitecture.
func TestPhase18ArchitectureReusesBuilder(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/architecture")
	if rec.Code != http.StatusOK {
		t.Fatalf("/architecture status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "/api/architecture") {
		t.Fatal("/architecture missing link to /api/architecture")
	}
	if !strings.Contains(body, "Violations") {
		t.Fatal("/architecture missing Violations section")
	}
}

// TestPhase18MemoryRendersStatus asserts /memory links to /api/memory, renders
// the freshness Status column, and surfaces the memory seeded by the test
// fixture (proving buildMemory data flows through to the page).
func TestPhase18MemoryRendersStatus(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/memory")
	if rec.Code != http.StatusOK {
		t.Fatalf("/memory status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "/api/memory") {
		t.Fatal("/memory missing link to /api/memory")
	}
	if !strings.Contains(body, "<th>Status</th>") {
		t.Fatal("/memory missing Status column header")
	}
	if !strings.Contains(body, "checkout incident") {
		t.Fatal("/memory missing seeded memory content from buildMemory")
	}
}
