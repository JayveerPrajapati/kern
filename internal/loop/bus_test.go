package loop

import (
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// TestLoopPublishesEvents verifies the closed loop publishes deployment,
// observe and lesson events when a bus is attached.
func TestLoopPublishesEvents(t *testing.T) {
	root := loopFixture(t)

	src := runtime.NewStore()
	now := time.Now().Truncate(time.Second)
	src.Ingest(runtime.Event{ID: "e1", Type: runtime.EventLog, Service: "checkout", Severity: "info", Message: "ok", Timestamp: now})

	bus := eventbus.New()
	var mu sync.Mutex
	kinds := map[eventbus.Kind]int{}
	bus.Subscribe("", func(ev eventbus.Event) {
		mu.Lock()
		defer mu.Unlock()
		kinds[ev.Kind]++
	})

	lp, err := NewLoop(LoopConfig{
		Root:      root,
		Level:     L4,
		Service:   "checkout",
		Since:     now.Add(-time.Minute),
		Source:    src,
		Mem:       memory.NewMemoryStore(t.TempDir()),
		Incidents: incident.NewStore(t.TempDir()),
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	lp.WithBus(bus)

	if _, err := lp.Run("add a helper", func(stage, intent string, wt *execution.Worktree, r *Result) (string, error) {
		return "ok", nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Delivery is asynchronous now; wait for all expected kinds to arrive.
	want := []eventbus.Kind{
		eventbus.DeploymentStarted,
		eventbus.DeploymentCompleted,
		eventbus.ObserveHealthy,
		eventbus.LessonRecorded,
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		all := true
		for _, k := range want {
			if kinds[k] == 0 {
				all = false
				break
			}
		}
		mu.Unlock()
		if all {
			break
		}
		time.Sleep(time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, k := range want {
		if kinds[k] == 0 {
			t.Errorf("no %s published", k)
		}
	}
}

// TestLoopWithBusNilIsNoOp verifies a loop with a nil bus still runs.
func TestLoopWithBusNilIsNoOp(t *testing.T) {
	root := loopFixture(t)
	lp, err := NewLoop(LoopConfig{Root: root, Level: L0, Service: "s", Source: runtime.NewStore()})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	if _, err := lp.Run(root, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
