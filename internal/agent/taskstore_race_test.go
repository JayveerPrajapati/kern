package agent

import (
	"fmt"
	"sync"
	"testing"
)

// TestTaskStoreConcurrentInstancesNoLostUpdate reproduces the lost-update race
// where two TaskStore instances pointing at the same backing file (the MCP
// server, web app, and CLI each build their own store) read-modify-write it
// concurrently. Before the process-wide path lock (cache.PathLock), interleaved
// Save calls from separate instances dropped each other's records. Each goroutine
// uses its OWN store instance (its own sync.Mutex), which is exactly the
// production shape — the per-instance mutex cannot serialize across instances.
func TestTaskStoreConcurrentInstancesNoLostUpdate(t *testing.T) {
	root := t.TempDir()

	const workers = 8
	const perWorker = 25

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			// A fresh store instance per worker, like each TaskService builds.
			s := NewTaskStore(root)
			for i := 0; i < perWorker; i++ {
				tk := NewTask(fmt.Sprintf("intent-%d-%d", worker, i), "x")
				if err := tk.Start(fmt.Sprintf("worker-%d", worker)); err != nil {
					t.Errorf("Start: %v", err)
					return
				}
				if _, err := s.Save(*tk); err != nil {
					t.Errorf("Save: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// Every task ever saved must be present — none lost to a race.
	want := workers * perWorker
	got, err := NewTaskStore(root).List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != want {
		t.Fatalf("persisted tasks = %d, want %d (lost-update race lost %d records)", len(got), want, want-len(got))
	}
}
