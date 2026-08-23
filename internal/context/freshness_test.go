package context

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestEvidenceFreshness(t *testing.T) {
	now := time.Now()
	bound := 24 * time.Hour
	fresh := domain.Evidence{Timestamp: now.Add(-time.Hour)}
	aging := domain.Evidence{Timestamp: now.Add(-13 * time.Hour)}
	stale := domain.Evidence{Timestamp: now.Add(-48 * time.Hour)}

	if cls, s := EvidenceFreshness(fresh, now, bound); cls != FreshnessFresh || s != 1.0 {
		t.Errorf("fresh = %q %.1f, want FRESH 1.0", cls, s)
	}
	if cls, s := EvidenceFreshness(aging, now, bound); cls != FreshnessAging || s != 0.5 {
		t.Errorf("aging = %q %.1f, want AGING 0.5", cls, s)
	}
	if cls, s := EvidenceFreshness(stale, now, bound); cls != FreshnessStale || s != 0.1 {
		t.Errorf("stale = %q %.1f, want STALE 0.1", cls, s)
	}
}

func TestRiskFreshnessMultiplier(t *testing.T) {
	now := time.Now()
	bound := 24 * time.Hour
	// No evidence: no penalty.
	if m, cls := RiskFreshnessMultiplier(nil, now, bound); m != 1.0 || cls != FreshnessFresh {
		t.Errorf("no evidence = %v %v, want 1.0 FRESH", m, cls)
	}
	// Stale evidence: heavy penalty.
	if m, cls := RiskFreshnessMultiplier([]domain.Evidence{{Timestamp: now.Add(-48 * time.Hour)}}, now, bound); m != 0.5 || cls != FreshnessStale {
		t.Errorf("stale = %v %v, want 0.5 STALE", m, cls)
	}
	// Aging evidence: mild penalty.
	if m, _ := RiskFreshnessMultiplier([]domain.Evidence{{Timestamp: now.Add(-13 * time.Hour)}}, now, bound); m != 0.8 {
		t.Errorf("aging multiplier = %v, want 0.8", m)
	}
}

func TestInvalidationMarker(t *testing.T) {
	at := time.Now()
	m := NewInvalidationMarker("sym.UserService", "signature changed", "file-watch", at)
	if m.Entity != "sym.UserService" || m.Reason != "signature changed" || m.Source != "file-watch" {
		t.Errorf("marker = %+v", m)
	}
	if m.At.IsZero() {
		t.Error("marker timestamp not set")
	}
	markers := InvalidateContext([]string{"a", "b"}, "x", "user", at)
	if len(markers) != 2 {
		t.Errorf("markers = %d, want 2", len(markers))
	}
}
