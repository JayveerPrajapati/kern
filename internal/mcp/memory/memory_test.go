package memory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	kernmemory "github.com/JayveerPrajapati/kern/internal/memory"
)

// TestToolRequiresAction pins the consolidated dispatcher's contract: action
// is mandatory and unknown actions are rejected with the menu, so a typo never
// silently does nothing.
func TestToolRequiresAction(t *testing.T) {
	ctx := context.Background()
	if _, err := Tool(ctx, Hooks{}, map[string]any{}); err == nil {
		t.Fatal("missing action must error")
	}
	_, err := Tool(ctx, Hooks{}, map[string]any{"action": "bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("expected unknown-action error, got %v", err)
	}
}

// TestToolDispatchesEveryAction pins that each documented action reaches its
// body (via hooks, so no store is touched).
func TestToolDispatchesEveryAction(t *testing.T) {
	ctx := context.Background()
	h := Hooks{
		Add: func(context.Context, string, string) (bool, error) { return true, nil },
		List: func(context.Context, string) ([]kernmemory.Entry, error) {
			return []kernmemory.Entry{{Text: "lesson"}}, nil
		},
		Recall: func(context.Context, string, string, int) ([]kernmemory.Entry, error) {
			return []kernmemory.Entry{{Text: "recalled"}}, nil
		},
		Remove: func(_ context.Context, _, id string) (kernmemory.Entry, error) {
			return kernmemory.Entry{Text: "removed-" + id}, nil
		},
		Clear: func(context.Context, string) error { return nil },
	}
	cases := []struct{ action, want string }{
		{"add", "remembered."},
		{"list", "lesson"},
		{"recall", "recalled"},
		{"remove", "removed-7"},
		{"clear", "project memory cleared."},
	}
	for _, c := range cases {
		args := map[string]any{"action": c.action, "lesson": "l", "prompt": "p", "id": "7"}
		out, err := Tool(ctx, h, args)
		if err != nil {
			t.Fatalf("%s: %v", c.action, err)
		}
		if !strings.Contains(out, c.want) {
			t.Fatalf("%s: output %q missing %q", c.action, out, c.want)
		}
	}
}

// TestRemoveRequiresID pins the per-action required argument the shared schema
// cannot express.
func TestRemoveRequiresID(t *testing.T) {
	_, err := Tool(context.Background(), Hooks{}, map[string]any{"action": "remove"})
	if err == nil || !strings.Contains(err.Error(), "'id' is required") {
		t.Fatalf("expected id-required error, got %v", err)
	}
}

// TestAddDuplicateAndFailure pins both hook outcomes: a duplicate lesson is
// reported (not silently re-stored) and a hook failure propagates.
func TestAddDuplicateAndFailure(t *testing.T) {
	ctx := context.Background()
	dup := Hooks{Add: func(context.Context, string, string) (bool, error) { return false, nil }}
	out, err := Add(ctx, dup, map[string]any{"lesson": "x"})
	if err != nil || !strings.Contains(out, "already remembered") {
		t.Fatalf("duplicate: %q %v", out, err)
	}
	boom := Hooks{Add: func(context.Context, string, string) (bool, error) {
		return false, errors.New("disk full")
	}}
	if _, err := Add(ctx, boom, map[string]any{"lesson": "x"}); err == nil {
		t.Fatal("hook failure must propagate")
	}
	if _, err := Add(ctx, Hooks{}, map[string]any{}); err == nil {
		t.Fatal("lesson is required")
	}
}

// TestRecallValidatesAndFallsBack pins input validation and the k alias.
func TestRecallValidatesAndFallsBack(t *testing.T) {
	ctx := context.Background()
	if _, err := Recall(ctx, Hooks{}, map[string]any{}); err == nil {
		t.Fatal("prompt is required")
	}
	if _, err := Recall(ctx, Hooks{}, map[string]any{"prompt": "p", "limit": "abc"}); err == nil {
		t.Fatal("non-numeric limit must error")
	}
	var seenK int
	h := Hooks{Recall: func(_ context.Context, _, _ string, k int) ([]kernmemory.Entry, error) {
		seenK = k
		return nil, nil
	}}
	out, err := Recall(ctx, h, map[string]any{"prompt": "p", "k": "3"})
	if err != nil {
		t.Fatal(err)
	}
	if seenK != 3 {
		t.Fatalf("k alias not honored: got %d", seenK)
	}
	if out != kernmemory.NoRecallMatch {
		t.Fatalf("empty recall should return the no-match message, got %q", out)
	}
}

// TestRankedOutputs pins the ranked action's two renderings, including the
// table escaping that keeps a lesson from breaking the markdown grid.
func TestRankedOutputs(t *testing.T) {
	ctx := context.Background()
	if _, err := Ranked(ctx, map[string]any{}); err == nil {
		t.Fatal("prompt is required")
	}
	root := t.TempDir()
	lesson := "pipes | and\nnewlines " + strings.Repeat("long ", 30)
	if _, err := kernmemory.AddExplicit(root, lesson); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	out, err := Ranked(ctx, map[string]any{"prompt": "pipes", "root": root, "format": "json"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Prompt  string `json:"prompt"`
		Count   int    `json:"count"`
		Lessons []struct {
			Text  string  `json:"text"`
			Score float64 `json:"score"`
		} `json:"lessons"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("ranked json is not JSON: %v\n%s", err, out)
	}
	if decoded.Prompt != "pipes" {
		t.Fatalf("prompt not echoed: %+v", decoded)
	}

	table, err := Ranked(ctx, map[string]any{"prompt": "pipes", "root": root})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(table, "Decay-Ranked Memory Retrieval") {
		t.Fatalf("table header missing:\n%s", table)
	}
	if strings.Contains(table, "| pipes | and") {
		t.Fatalf("raw pipe in a lesson must be escaped:\n%s", table)
	}
	if !strings.Contains(table, "\\|") {
		t.Fatalf("expected an escaped pipe:\n%s", table)
	}
}

// TestStoreBackedActions pins the no-hook fallbacks against a real (temp)
// project store: add → list → recall → remove → clear.
func TestStoreBackedActions(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	if out, err := Add(ctx, Hooks{}, map[string]any{"lesson": "use kern_explore first", "root": root}); err != nil || !strings.Contains(out, "remembered") {
		t.Fatalf("add: %q %v", out, err)
	}
	out, err := List(ctx, Hooks{}, map[string]any{"root": root})
	if err != nil || !strings.Contains(out, "kern_explore") {
		t.Fatalf("list: %q %v", out, err)
	}
	out, err = Tool(ctx, Hooks{}, map[string]any{"action": "recall", "prompt": "kern_explore", "root": root, "limit": "5"})
	if err != nil || !strings.Contains(out, "kern_explore") {
		t.Fatalf("recall: %q %v", out, err)
	}
	if _, err := Tool(ctx, Hooks{}, map[string]any{"action": "remove", "id": "1", "root": root}); err != nil {
		t.Fatalf("remove by index: %v", err)
	}
	if _, err := Tool(ctx, Hooks{}, map[string]any{"action": "clear", "root": root}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	out, err = List(ctx, Hooks{}, map[string]any{"root": root})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "kern_explore") {
		t.Fatalf("clear left lessons behind: %q", out)
	}
}

// TestListPropagatesHookError pins error transparency (a swallowed store error
// would read as "no lessons").
func TestListPropagatesHookError(t *testing.T) {
	h := Hooks{List: func(context.Context, string) ([]kernmemory.Entry, error) {
		return nil, errors.New("store corrupt")
	}}
	if _, err := List(context.Background(), h, map[string]any{}); err == nil {
		t.Fatal("hook error must propagate")
	}
}
