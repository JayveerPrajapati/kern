package main

import (
	"encoding/json"
	"strings"
)

// Usage is the token accounting extracted from a raw agent event stream.
// The raw file is always retained alongside it, so this heuristic can be
// improved and old runs rescored without any API spend.
//
// Both a per-event sum and the single-event maximum are reported because
// agents differ: some emit cumulative per-message counters (where the max is
// the honest number) and some emit per-part deltas (where the sum is).
type Usage struct {
	InputSum  *int64 `json:"input_tokens_sum,omitempty"`
	OutputSum *int64 `json:"output_tokens_sum,omitempty"`
	InputMax  *int64 `json:"input_tokens_max,omitempty"`
	OutputMax *int64 `json:"output_tokens_max,omitempty"`
	Events    int    `json:"events_with_tokens"`
	Note      string `json:"note,omitempty"`
}

// extractUsage parses the agent's raw event stream (JSONL, or one
// concatenated JSON value) and extracts token counts. Recognized shapes,
// matched at any nesting depth up to 8:
//
//	{"tokens": {"input": N, "output": M}}
//	{"usage":  {"input": N, "output": M, ...}}
//	{"input_tokens": N, "output_tokens": M}
//
// Within one event the maximum across shapes wins (never summed) to avoid
// double-counting the same counter exposed under two keys.
func extractUsage(raw []byte) Usage {
	var events []any
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev any
		if json.Unmarshal([]byte(line), &ev) == nil {
			events = append(events, ev)
		}
	}
	if len(events) == 0 {
		var ev any
		if json.Unmarshal(raw, &ev) == nil {
			events = append(events, ev)
		}
	}

	var u Usage
	for _, ev := range events {
		in, out, ok := usageIn(ev, 0)
		if !ok {
			continue
		}
		u.Events++
		switch {
		case u.InputSum == nil:
			inS, outS := in, out // per-event sums
			inM, outM := in, out // single-event maxima
			u.InputSum, u.OutputSum, u.InputMax, u.OutputMax = &inS, &outS, &inM, &outM
		default:
			*u.InputSum += in
			*u.OutputSum += out
			if in > *u.InputMax {
				*u.InputMax = in
			}
			if out > *u.OutputMax {
				*u.OutputMax = out
			}
		}
	}
	if u.Events == 0 {
		u.Note = "no token fields found in event stream (raw events retained; extraction can improve and rescore)"
	}
	return u
}

// usageIn reports the largest input/output token counts found anywhere in
// the event value.
func usageIn(v any, depth int) (int64, int64, bool) {
	if depth > 8 {
		return 0, 0, false
	}
	switch t := v.(type) {
	case map[string]any:
		var bestIn, bestOut int64
		found := false
		if n, ok := numAt(t, "input_tokens"); ok {
			bestIn, found = max64(bestIn, n), true
		}
		if n, ok := numAt(t, "output_tokens"); ok {
			bestOut, found = max64(bestOut, n), true
		}
		if sub, ok := t["tokens"].(map[string]any); ok {
			if n, ok := numAt(sub, "input"); ok {
				bestIn, found = max64(bestIn, n), true
			}
			if n, ok := numAt(sub, "output"); ok {
				bestOut, found = max64(bestOut, n), true
			}
		}
		if sub, ok := t["usage"].(map[string]any); ok {
			if n, ok := numAt(sub, "input"); ok {
				bestIn, found = max64(bestIn, n), true
			}
			if n, ok := numAt(sub, "output"); ok {
				bestOut, found = max64(bestOut, n), true
			}
		}
		for _, child := range t {
			in, out, ok := usageIn(child, depth+1)
			if ok {
				found = true
				bestIn, bestOut = max64(bestIn, in), max64(bestOut, out)
			}
		}
		return bestIn, bestOut, found
	case []any:
		var bestIn, bestOut int64
		found := false
		for _, e := range t {
			in, out, ok := usageIn(e, depth+1)
			if ok {
				found = true
				bestIn, bestOut = max64(bestIn, in), max64(bestOut, out)
			}
		}
		return bestIn, bestOut, found
	default:
		return 0, 0, false
	}
}

func numAt(m map[string]any, key string) (int64, bool) {
	f, ok := m[key].(float64)
	if !ok || f < 0 {
		return 0, false
	}
	return int64(f), true
}

func max64(a, b int64) int64 {
	if b > a {
		return b
	}
	return a
}
