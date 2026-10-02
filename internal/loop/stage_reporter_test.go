package loop

import (
	"context"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

func TestStageReporterContextRoundTrip(t *testing.T) {
	if fn := StageReporterFromContext(context.Background()); fn != nil {
		t.Fatal("empty context must yield nil reporter")
	}
	var got []string
	ctx := WithStageReporter(context.Background(), func(stage string, pct int) {
		got = append(got, stage)
	})
	fn := StageReporterFromContext(ctx)
	if fn == nil {
		t.Fatal("reporter must round-trip through the context")
	}
	fn("verify", 55)
	if len(got) != 1 || got[0] != "verify" {
		t.Fatalf("reporter captured %v, want [verify]", got)
	}
	if ctx2 := WithStageReporter(ctx, nil); ctx2 != ctx {
		t.Fatal("nil reporter must return ctx unchanged")
	}
}

// TestLoopReportsStages pins the stage-reporter contract end-to-end: an L0
// run reports exactly the stages that pass the autonomy gate (intent,
// remember, verify, observe — plan/code/protect/deploy/learn are gated above
// L0 and must not be reported), with strictly increasing cumulative pct.
func TestLoopReportsStages(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full loop execution in -short mode")
	}
	lp, err := NewLoop(LoopConfig{Root: loopFixture(t), Level: L0, Mem: memory.NewMemoryStore(t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	var stages []string
	var pcts []int
	ctx := WithStageReporter(context.Background(), func(stage string, pct int) {
		stages = append(stages, stage)
		pcts = append(pcts, pct)
	})
	if _, err := lp.RunContext(ctx, "stage reporter smoke", nil); err != nil {
		t.Fatalf("L0 run: %v", err)
	}
	want := []string{stageIntent, stageRemember, stageVerify, stageObserve}
	if len(stages) != len(want) {
		t.Fatalf("reported stages = %v, want %v", stages, want)
	}
	for i := range want {
		if stages[i] != want[i] {
			t.Fatalf("reported stage %d = %q, want %q (all: %v)", i, stages[i], want[i], stages)
		}
	}
	for i := 1; i < len(pcts); i++ {
		if pcts[i] <= pcts[i-1] {
			t.Fatalf("cumulative pct must strictly increase across stages: %v", pcts)
		}
	}
}
