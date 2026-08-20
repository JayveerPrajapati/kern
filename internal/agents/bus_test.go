package agents

import (
	"sync"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// TestPipelinePublishesEvents verifies the pipeline publishes agent.tool_called
// and agent.handoff when a bus is attached.
func TestPipelinePublishesEvents(t *testing.T) {
	team, runtime, err := StandardTeam()
	if err != nil {
		t.Fatalf("StandardTeam: %v", err)
	}
	bus := eventbus.New()
	var mu sync.Mutex
	kinds := map[eventbus.Kind]int{}
	bus.Subscribe("", func(ev eventbus.Event) {
		mu.Lock()
		defer mu.Unlock()
		kinds[ev.Kind]++
	})

	p := NewPipeline(team, runtime, governance.NewApprovalWorkflow()).WithBus(bus)

	task := agent.NewTask("code", "implement X")
	if _, results, err := p.Run(task, fixedHandler); err != nil {
		t.Fatalf("Run: %v", err)
	} else if len(results) != 6 {
		t.Fatalf("results = %d, want 6 pipeline stages", len(results))
	}

	bus.Flush()
	if kinds[eventbus.AgentToolCalled] != 6 {
		t.Errorf("agent.tool_called = %d, want 6", kinds[eventbus.AgentToolCalled])
	}
	if kinds[eventbus.AgentHandoff] == 0 {
		t.Error("no agent.handoff published (stages should hand off between specialists)")
	}
}

// TestPipelineNilBusIsNoOp confirms the pipeline runs unchanged without a bus.
func TestPipelineNilBusIsNoOp(t *testing.T) {
	team, runtime, err := StandardTeam()
	if err != nil {
		t.Fatalf("StandardTeam: %v", err)
	}
	p := NewPipeline(team, runtime, governance.NewApprovalWorkflow())
	if _, _, err := p.Run(agent.NewTask("code", "x"), fixedHandler); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
