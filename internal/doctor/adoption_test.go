package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/metrics"
)

// writeAdoptionLog writes n events, routed of which are kern-routed.
func writeAdoptionLog(t *testing.T, root string, n, routed int) {
	t.Helper()
	path := filepath.Join(root, ".kern", "adoption.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf []byte
	for i := 0; i < n; i++ {
		line, err := json.Marshal(metrics.AdoptionEvent{Ts: int64(i), Tool: "read", Routed: i < routed})
		if err != nil {
			t.Fatal(err)
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCheckAdoptionThreshold pins the measurement contract: a routing share
// under 60% warns once the log has at least 20 shadow calls; a small log and a
// healthy share stay ok.
func TestCheckAdoptionThreshold(t *testing.T) {
	root := t.TempDir()
	if f := checkAdoption(root); f.Level != "ok" {
		t.Fatalf("missing log must be ok, got %+v", f)
	}
	writeAdoptionLog(t, root, 30, 10) // 33% of 30
	if f := checkAdoption(root); f.Level != "warn" {
		t.Fatalf("33%% over 30 calls must warn, got %+v", f)
	}
	writeAdoptionLog(t, root, 30, 30)
	if f := checkAdoption(root); f.Level != "ok" {
		t.Fatalf("100%% must be ok, got %+v", f)
	}
	writeAdoptionLog(t, root, 10, 0) // below the noise floor
	if f := checkAdoption(root); f.Level != "ok" {
		t.Fatalf("10 calls must not warn, got %+v", f)
	}
}
