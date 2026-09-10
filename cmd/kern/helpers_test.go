package main

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/optimize"
)

// TestWireRecorder pins wireRecorder's contract: it must not panic and must be
// safe to call repeatedly. XDG_CACHE_HOME is isolated so the stats recorder
// (rooted at <cache>/kern/stats) never touches the real user cache.
func TestWireRecorder(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	wireRecorder()
	wireRecorder() // repeated calls must be safe

	if optimize.Recorder == nil {
		t.Fatal("wireRecorder: optimize.Recorder not wired")
	}
}
