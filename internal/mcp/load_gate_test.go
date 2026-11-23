package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type loadGateThresholds struct {
	MaxP95MS int64 `json:"max_p95_ms"`
	MaxP99MS int64 `json:"max_p99_ms"`
}

func readLoadGateThresholds(t *testing.T) loadGateThresholds {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "cmd", "kern", "load_thresholds.json"))
	if err != nil {
		t.Fatalf("read load_thresholds.json: %v", err)
	}
	var th loadGateThresholds
	if err := json.Unmarshal(data, &th); err != nil {
		t.Fatalf("decode load_thresholds.json: %v", err)
	}
	if th.MaxP95MS <= 0 || th.MaxP99MS <= 0 {
		t.Fatalf("invalid load thresholds: %+v", th)
	}
	return th
}

func TestLoadGate(t *testing.T) {
	dir := os.Getenv("KERN_LOAD_REPORT_DIR")
	if dir == "" {
		t.Skip("KERN_LOAD_REPORT_DIR is not set")
	}
	data, err := os.ReadFile(filepath.Join(dir, "load.json"))
	if err != nil {
		t.Fatalf("read load report: %v", err)
	}
	var results []loadResult
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatalf("decode load report: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("load report contains no results")
	}
	thresholds := readLoadGateThresholds(t)
	for _, r := range results {
		if r.ToolErrors != 0 || r.TransportErr != 0 {
			t.Errorf("%s/%s: tool errors=%d transport errors=%d", r.Tool, r.Profile, r.ToolErrors, r.TransportErr)
		}
		if r.Successes != r.Requests {
			t.Errorf("%s/%s: successes=%d want=%d", r.Tool, r.Profile, r.Successes, r.Requests)
		}
		if r.P95 > time.Duration(thresholds.MaxP95MS)*time.Millisecond {
			t.Errorf("%s/%s: P95 %s exceeds %s budget", r.Tool, r.Profile, r.P95,
				time.Duration(thresholds.MaxP95MS)*time.Millisecond)
		}
		if r.P99 > time.Duration(thresholds.MaxP99MS)*time.Millisecond {
			t.Errorf("%s/%s: P99 %s exceeds %s budget", r.Tool, r.Profile, r.P99,
				time.Duration(thresholds.MaxP99MS)*time.Millisecond)
		}
		if r.ServedBy == "" {
			t.Errorf("%s/%s: served_by is missing", r.Tool, r.Profile)
		}
	}
}
