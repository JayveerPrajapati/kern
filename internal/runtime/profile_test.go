package runtime

import (
	"math"
	"strings"
	"testing"
	"time"
)

// fixtureStore builds a Store from a small snapshot: two services with
// known event/error counts and one untagged event.
func fixtureStore(t *testing.T) *Store {
	t.Helper()
	st, err := ParseSnapshot([]byte(`{
		"events": [
			{"id":"e1","type":"metric","service":"app","severity":"info","message":"rps=10","timestamp":"2026-09-09T10:00:00Z"},
			{"id":"e2","type":"error","service":"app","severity":"error","message":"boom","timestamp":"2026-09-09T10:00:01Z"},
			{"id":"e3","type":"metric","service":"worker","severity":"info","message":"rps=5","timestamp":"2026-09-09T10:00:02Z"},
			{"id":"e4","type":"metric","service":"app","severity":"info","message":"rps=11","timestamp":"2026-09-09T10:00:03Z"},
			{"id":"e5","type":"log","service":"","severity":"info","message":"untagged","timestamp":"2026-09-09T10:00:04Z"}
		],
		"deployments": [{"service":"app","version":"v1","timestamp":"2026-09-09T09:00:00Z"}],
		"commits": [{"sha":"abc","message":"x","author":"me","files":["a.go"],"committed_at":"2026-09-09T08:00:00Z"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// TestServiceProfilesAggregates pins per-service event/error/recency
// aggregation with the untagged group and deterministic ordering.
func TestServiceProfilesAggregates(t *testing.T) {
	profiles := ServiceProfiles(fixtureStore(t))
	if len(profiles) != 3 {
		t.Fatalf("profiles = %d, want 3 (app, worker, untagged)", len(profiles))
	}
	if profiles[0].Name != "" || profiles[1].Name != "app" || profiles[2].Name != "worker" {
		t.Fatalf("profile order = %q,%q,%q, want sorted by name", profiles[0].Name, profiles[1].Name, profiles[2].Name)
	}
	app := ProfileFor(profiles, "app")
	if app == nil {
		t.Fatal("no app profile")
	}
	if app.Events != 3 || app.Errors != 1 {
		t.Fatalf("app profile = %+v, want 3 events / 1 error", app)
	}
	if math.Abs(app.ErrorRate-100.0/3.0) > 1e-9 {
		t.Fatalf("app error rate = %.6f, want %.6f", app.ErrorRate, 100.0/3.0)
	}
	if !app.First.Equal(time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("app first = %v, want 10:00:00Z", app.First)
	}
}

// TestServiceProfilesNilSafe pins the nil-source and no-telemetry contracts.
func TestServiceProfilesNilSafe(t *testing.T) {
	if got := ServiceProfiles(nil); got != nil {
		t.Fatalf("ServiceProfiles(nil) = %v, want nil", got)
	}
	st, err := ParseSnapshot([]byte(`{"events":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := ServiceProfiles(st); got != nil {
		t.Fatalf("ServiceProfiles(empty) = %v, want nil", got)
	}
	if got := ProfileFor(nil, "app"); got != nil {
		t.Fatalf("ProfileFor(nil) = %v, want nil", got)
	}
}

// TestOverlayRendersProfileAndFlags pins the review overlay renderer: the
// matched service line with its profile, the high-error FLAG, and the
// explicit no-telemetry line for unmatched package directories.
func TestOverlayRendersProfileAndFlags(t *testing.T) {
	st := fixtureStore(t)
	overlay := Overlay(st)
	if overlay == nil {
		t.Fatal("Overlay(fixture) = nil, want a renderer")
	}
	// app has 33.3% errors -> flagged.
	line := overlay("internal/app/handler.go")
	for _, want := range []string{`svc "app"`, "3 events", "33.3% errors", "FLAG: error rate above 5%"} {
		if !strings.Contains(line, want) {
			t.Fatalf("overlay line = %q, missing %q", line, want)
		}
	}
	// worker exists, low error rate -> no FLAG.
	if w := overlay("internal/worker/main.go"); strings.Contains(w, "FLAG") {
		t.Fatalf("worker overlay = %q, want no FLAG", w)
	}
	// Unmatched directory -> explicit no-telemetry line.
	if no := overlay("internal/unknown/x.go"); !strings.Contains(no, `no telemetry for package dir "unknown"`) {
		t.Fatalf("unknown overlay = %q, want explicit no-telemetry line", no)
	}
	// Nil source -> nil renderer (no overlay at all).
	if got := Overlay(nil); got != nil {
		t.Fatalf("Overlay(nil) returned a renderer, want nil")
	}
}
