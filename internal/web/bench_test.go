package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// benchFixtureRoot writes the minimal fixture the console benchmarks serve
// (same shape as fixtureRoot, benchmark-typed so b.TempDir is used).
func benchFixtureRoot(b *testing.B) string {
	b.Helper()
	root := b.TempDir()
	files := map[string]string{
		"go.mod": "module consolefixture\n\ngo 1.20\n",
		"main.go": `package main
func helper() string {
return "h"
}
func main() {
_ = helper()
}
`,
		"main_test.go": `package main
import "testing"
func TestHelper(t *testing.T) {
if helper() != "h" {
t.Fatal("helper() != h")
}
}
`,
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	return root
}

// benchApp builds the console App once for a benchmark. The index/graph/
// engines are prebuilt in New (the #1 startup bottleneck is deliberately
// excluded from the timed loop); ServeHTTP is what the benchmarks measure.
func benchApp(b *testing.B) *App {
	b.Helper()
	app, err := New(benchFixtureRoot(b))
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	return app
}

// benchGet runs one GET against the app and returns the recorder.
func benchGet(app *App, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

// BenchmarkServeHTTPReads measures the steady-state read path for the
// dashboard's hot endpoints: overview, graph, memory, and health. The
// prebuilt index/graph are shared (never rebuilt per request), so the
// benchmark pins handler+serialization cost — the part that dominates
// interactive dashboard latency.
func BenchmarkServeHTTPReads(b *testing.B) {
	app := benchApp(b)
	paths := []string{
		"/api/overview",
		"/api/graph",
		"/api/memory",
		"/api/health",
	}
	// Pre-warm: verify each endpoint serves 200 once, outside the timer.
	for _, p := range paths {
		if rec := benchGet(app, p); rec.Code != http.StatusOK {
			b.Fatalf("pre-warm %s: code %d", p, rec.Code)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range paths {
			if rec := benchGet(app, p); rec.Code != http.StatusOK {
				b.Fatalf("%s: code %d", p, rec.Code)
			}
		}
	}
}

// BenchmarkServeHTTPOverview isolates the flagship endpoint.
func BenchmarkServeHTTPOverview(b *testing.B) {
	app := benchApp(b)
	if rec := benchGet(app, "/api/overview"); rec.Code != http.StatusOK {
		b.Fatalf("pre-warm: code %d", rec.Code)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if rec := benchGet(app, "/api/overview"); rec.Code != http.StatusOK {
			b.Fatalf("code %d", rec.Code)
		}
	}
}

// BenchmarkServeHTTPArchitecture measures the TTL-cached architecture
// report (the archTTL path), which was historically the per-request
// re-index bottleneck.
func BenchmarkServeHTTPArchitecture(b *testing.B) {
	app := benchApp(b)
	if rec := benchGet(app, "/api/architecture"); rec.Code != http.StatusOK {
		b.Fatalf("pre-warm: code %d", rec.Code)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if rec := benchGet(app, "/api/architecture"); rec.Code != http.StatusOK {
			b.Fatalf("code %d", rec.Code)
		}
	}
}
