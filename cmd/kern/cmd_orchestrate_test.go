package main

import (
	"strings"
	"testing"

	kernctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

func TestBoundOrchestrateJSONTruncatesSerializedOutput(t *testing.T) {
	res := &kernctx.OrchestrateResult{
		TaskType: kernctx.TaskFixBug,
		Plan: &kernctx.Plan{
			TaskType: kernctx.TaskFixBug,
			Selections: []kernctx.Selection{
				{Content: strings.Repeat("selection ", 10_000)},
			},
		},
		FittedText: strings.Repeat("context ", 20_000),
	}

	out, err := boundOrchestrateJSON(res, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := tokenize.Count(string(out)); got > 100 {
		t.Fatalf("bounded output is %d tokens, want <= 100", got)
	}
	if !strings.Contains(string(out), `"truncated":true`) {
		t.Fatalf("bounded output must contain an explicit truncation marker:\n%s", out)
	}
	if strings.Contains(string(out), "selection selection") {
		t.Fatal("bounded output leaked the full selection payload")
	}
}

func TestBoundOrchestrateJSONDefaultBudgetReturnsSmallResult(t *testing.T) {
	res := &kernctx.OrchestrateResult{TaskType: kernctx.TaskFixBug}

	out, err := boundOrchestrateJSON(res, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := tokenize.Count(string(out)); got > orchestrateDefaultMaxTokens {
		t.Fatalf("default-bounded output is %d tokens, want <= %d", got, orchestrateDefaultMaxTokens)
	}
	if strings.Contains(string(out), `"truncated":true`) {
		t.Fatalf("small result must not be truncated:\n%s", out)
	}
}

func TestBoundOrchestrateJSONBoundedResultFitsBudget(t *testing.T) {
	res := &kernctx.OrchestrateResult{
		TaskType: kernctx.TaskFixBug,
		Plan: &kernctx.Plan{
			TaskType: kernctx.TaskFixBug,
			Selections: []kernctx.Selection{
				{Content: strings.Repeat("selection ", 10_000)},
			},
		},
	}

	out, err := boundOrchestrateJSON(res, 120)
	if err != nil {
		t.Fatal(err)
	}
	if got := tokenize.Count(string(out)); got > 120 {
		t.Fatalf("bounded output is %d tokens, want <= 120", got)
	}
	if !strings.Contains(string(out), `"plan.selections"`) {
		t.Fatalf("bounded output must report the dropped plan selection payload:\n%s", out)
	}
}

func TestBoundOrchestrateJSONTruncatesEvents(t *testing.T) {
	events := make([]kernctx.OrchestrateStage, 33)
	for i := range events {
		events[i] = kernctx.StageTaskReceived
	}
	res := &kernctx.OrchestrateResult{TaskType: kernctx.TaskFixBug, Events: events}

	out, err := boundOrchestrateJSON(res, 80)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"events"`) {
		t.Fatalf("event truncation must be reported:\n%s", out)
	}
}

func TestBoundOrchestrateJSONRejectsImpossibleBudget(t *testing.T) {
	_, err := boundOrchestrateJSON(&kernctx.OrchestrateResult{}, 1)
	if err == nil {
		t.Fatal("a one-token budget must fail rather than return invalid JSON")
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("abc", -1); got != "" {
		t.Fatalf("negative budget returned %q, want empty", got)
	}
	if got := truncateRunes("abc", 3); got != "abc" {
		t.Fatalf("in-budget string returned %q, want abc", got)
	}
}
