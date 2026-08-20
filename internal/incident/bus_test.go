package incident

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// testEngineLite builds an incident engine wired to a runtime source with one
// error event, a deployment and a commit, so both shallow correlation and the
// deep chain produce evidence.
func testEngineLite() (*Engine, *runtime.Store) {
	src := runtime.NewStore()
	now := time.Now().Truncate(time.Second)
	src.Ingest(runtime.Event{
		ID: "e1", Type: runtime.EventError, Service: "checkout", Severity: "error",
		Message: "checkout failed", Timestamp: now,
		Attributes: map[string]string{"file": "svc/checkout.go", "symbol": "CheckoutService"},
	})
	src.AddDeployment(domain.Deployment{Service: "checkout", Version: "v2", CommitSHA: "abc123", DeployedAt: now.Add(-time.Minute)})
	src.AddCommit(runtime.Commit{SHA: "abc123", Message: "fix checkout (#42)", Author: "sre", Files: []string{"svc/checkout.go"}, CommittedAt: now.Add(-2 * time.Minute)})
	return &Engine{src: src, window: DefaultLookback}, src
}

// TestIncidentPublishesLifecycle verifies incident.created / updated /
// resolved are published when a bus is attached.
func TestIncidentPublishesLifecycle(t *testing.T) {
	e, _ := testEngineLite()
	bus := eventbus.New()
	var mu sync.Mutex
	kinds := map[eventbus.Kind]int{}
	bus.Subscribe("", func(ev eventbus.Event) {
		mu.Lock()
		defer mu.Unlock()
		kinds[ev.Kind]++
	})
	e.WithBus(bus)

	inc := e.IngestAlert(domain.Alert{ID: "a1", Severity: domain.SeverityError, Message: "boom", Service: "checkout", OccurredAt: time.Now()})
	bus.Flush()
	if kinds[eventbus.IncidentCreated] == 0 {
		t.Error("no incident.created published")
	}

	e.Correlate(inc)
	bus.Flush()
	if kinds[eventbus.IncidentUpdated] == 0 {
		t.Error("no incident.updated published after correlate")
	}

	e.Resolve(inc)
	bus.Flush()
	if kinds[eventbus.IncidentResolved] == 0 {
		t.Error("no incident.resolved published")
	}
}

// TestCorrelateFoldsDeepChain proves the deep correlation chain is folded into
// the incident evidence (additive to the shallow correlation).
func TestCorrelateFoldsDeepChain(t *testing.T) {
	e, _ := testEngineLite()
	inc := e.IngestAlert(domain.Alert{ID: "a1", Severity: domain.SeverityError, Message: "boom", Service: "checkout", OccurredAt: time.Now()})
	e.Correlate(inc)

	found := false
	for _, ev := range inc.Evidence {
		if strings.Contains(ev.Content, "deep chain") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("incident evidence missing deep chain entry; got %+v", inc.Evidence)
	}
}
