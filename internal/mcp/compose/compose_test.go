package compose

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// runner is a scripted RunTool: it records calls and answers from results.
type runner struct {
	calls   []string
	args    []map[string]any
	results []string
	errs    []error
}

func (r *runner) run(_ context.Context, _, name string, args map[string]any) (string, error) {
	r.calls = append(r.calls, name)
	r.args = append(r.args, args)
	i := len(r.calls) - 1
	if i < len(r.errs) && r.errs[i] != nil {
		return "", r.errs[i]
	}
	if i < len(r.results) {
		return r.results[i], nil
	}
	return "ok", nil
}

func hooks(r *runner) Hooks { return Hooks{RunTool: r.run} }

// TestComposeRequiresPipeline pins the argument contract.
func TestComposeRequiresPipeline(t *testing.T) {
	if _, err := Compose(context.Background(), Hooks{}, map[string]any{}); err == nil {
		t.Fatal("missing pipeline must error")
	}
	if _, err := Compose(context.Background(), Hooks{}, map[string]any{"pipeline": "not json"}); err == nil {
		t.Fatal("malformed pipeline JSON must error")
	}
	if _, err := Compose(context.Background(), Hooks{}, map[string]any{"pipeline": []any{}}); err == nil {
		t.Fatal("empty pipeline must error")
	}
}

// TestComposeRejectsRecursion pins the bomb guard: a pipeline must not invoke
// kern_compose (unbounded nesting).
func TestComposeRejectsRecursion(t *testing.T) {
	_, err := Compose(context.Background(), Hooks{}, map[string]any{
		"pipeline": []any{map[string]any{"tool": "kern_compose"}},
	})
	if err == nil || !strings.Contains(err.Error(), "recursive") {
		t.Fatalf("expected recursion guard, got %v", err)
	}
}

// TestComposeStepValidation pins per-step validation and the missing-runner
// error, in the order they occur.
func TestComposeStepValidation(t *testing.T) {
	_, err := Compose(context.Background(), Hooks{}, map[string]any{
		"pipeline": []any{map[string]any{"args": map[string]any{}}},
	})
	if err == nil || !strings.Contains(err.Error(), "tool name is required") {
		t.Fatalf("expected tool-name error, got %v", err)
	}
	_, err = Compose(context.Background(), Hooks{}, map[string]any{
		"pipeline": []any{map[string]any{"tool": "kern_search"}},
	})
	if err == nil || !strings.Contains(err.Error(), "runner hook not configured") {
		t.Fatalf("expected runner error, got %v", err)
	}
}

// TestComposeBindsStepOutput pins the feature that makes composition useful:
// a bound step's output is interpolated into later steps, in both the "$name"
// and bare "name" spellings, and the header records the binding.
func TestComposeBindsStepOutput(t *testing.T) {
	r := &runner{results: []string{"dispatch\n", "second"}}
	out, err := Compose(context.Background(), hooks(r), map[string]any{
		"pipeline": []any{
			map[string]any{"tool": "kern_search", "args": map[string]any{"query": "dispatch"}, "bind": "$sym"},
			map[string]any{"tool": "kern_explore", "args": map[string]any{"symbol": "$sym", "note": "about sym"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("expected 2 calls, got %v", r.calls)
	}
	if got := r.args[1]["symbol"]; got != "dispatch" {
		t.Fatalf("$sym not interpolated: %v", got)
	}
	// Only $-prefixed references interpolate: a bare word inside prose is
	// left alone (substituting it would corrupt ordinary text).
	if got := r.args[1]["note"]; got != "about sym" {
		t.Fatalf("bare word must not be substituted: %v", got)
	}
	if !strings.Contains(out, "[bound to $sym]") || !strings.Contains(out, "[kern_compose: 2/2 steps completed") {
		t.Fatalf("output missing binding/summary:\n%s", out)
	}
}

// TestComposeErrorPolicy pins all three on_error policies: default aborts with
// the partial transcript AND an error, "skip"/"continue" record the failure
// and keep going.
func TestComposeErrorPolicy(t *testing.T) {
	pipeline := func(policy string) []any {
		return []any{
			map[string]any{"tool": "kern_fail", "on_error": policy},
			map[string]any{"tool": "kern_ok"},
		}
	}

	// Default (stop): error, and the transcript names the failure.
	r := &runner{errs: []error{errors.New("boom")}}
	out, err := Compose(context.Background(), hooks(r), map[string]any{"pipeline": pipeline("")})
	if err == nil {
		t.Fatal("default policy must abort the pipeline")
	}
	if !strings.Contains(out, "FAILED") || !strings.Contains(out, "Pipeline aborted at step 1") {
		t.Fatalf("abort transcript:\n%s", out)
	}
	if len(r.calls) != 1 {
		t.Fatalf("aborted pipeline must not run later steps: %v", r.calls)
	}

	for _, policy := range []string{"skip", "continue"} {
		r := &runner{errs: []error{errors.New("boom")}}
		out, err := Compose(context.Background(), hooks(r), map[string]any{"pipeline": pipeline(policy)})
		if err != nil {
			t.Fatalf("%s: unexpected error %v\n%s", policy, err, out)
		}
		if len(r.calls) != 2 {
			t.Fatalf("%s: later steps must still run: %v", policy, r.calls)
		}
		if !strings.Contains(out, "FAILED") {
			t.Fatalf("%s: failure notice missing:\n%s", policy, out)
		}
	}
}

// TestComposeAcceptsJSONStringPipeline pins the wire form an agent sends when
// it passes the pipeline as a JSON-encoded string.
func TestComposeAcceptsJSONStringPipeline(t *testing.T) {
	r := &runner{}
	out, err := Compose(context.Background(), hooks(r), map[string]any{
		"pipeline": `[{"tool":"kern_search","args":{"query":"x"}}]`,
		"timeout":  "5",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 || r.calls[0] != "kern_search" {
		t.Fatalf("string pipeline not executed: %v", r.calls)
	}
	if !strings.Contains(out, "1/1 steps completed") {
		t.Fatalf("summary missing:\n%s", out)
	}
}

// TestInterpolate pins the interpolation helpers directly: exact "$var",
// substring substitution, nested maps/slices, and pass-through of non-strings.
func TestInterpolate(t *testing.T) {
	b := map[string]string{"$x": "X", "y": "Y"}

	if got := InterpolateValue("$x", b); got != "X" {
		t.Fatalf("exact $var: %v", got)
	}
	if got := InterpolateValue("pre-$x-post", b); got != "pre-X-post" {
		t.Fatalf("substring: %v", got)
	}
	// A binding stored without the sigil still requires the "$" at the use
	// site: "y" alone is prose, "$y" is a reference.
	if got := InterpolateValue("y and $y", b); got != "y and Y" {
		t.Fatalf("sigil-required: %v", got)
	}
	if got := InterpolateValue(42, b); got != 42 {
		t.Fatalf("non-string must pass through: %v", got)
	}
	nested := map[string]any{
		"s": []any{"$x", map[string]any{"inner": "$x"}},
	}
	out := InterpolateArgs(nested, b)
	list, _ := out["s"].([]any)
	if len(list) != 2 || list[0] != "X" {
		t.Fatalf("slice interpolation failed: %+v", out)
	}
	inner, _ := list[1].(map[string]any)
	if inner["inner"] != "X" {
		t.Fatalf("nested map interpolation failed: %+v", out)
	}
	if len(InterpolateArgs(nil, b)) != 0 {
		t.Fatal("nil args must interpolate to an empty map")
	}
}
