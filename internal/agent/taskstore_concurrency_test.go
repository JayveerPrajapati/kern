package agent

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestTaskStoreConcurrentInstancesNoLostUpdates proves the store is safe when
// multiple store instances write the SAME backing file concurrently — the
// production shape where kern-mcp, kern-server and the CLI all work on one
// project root, and the shape under `go test ./...` where parallel test
// binaries share the per-root task store.
// Before the cross-process file lock, each instance held only an in-process
// mutex (PathLock), so separate processes interleaved their load->modify->save
// critical sections and lost each other's updates. Two goroutines here each
// open their own fd (like two processes), so flock contention is real.
func TestTaskStoreConcurrentInstancesNoLostUpdates(t *testing.T) {
	root := t.TempDir()
	const writers = 4
	const perWriter = 8

	// Each writer gets its OWN store instance (own fd) pointing at the same
	// root -> same backing file, exactly like separate kern processes.
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			st := NewTaskStore(root)
			for i := 0; i < perWriter; i++ {
				tk := NewTask("analyze", fmt.Sprintf("writer-%d task-%d", w, i))
				tk.ID = "" // let the store assign the ID under the file lock
				if _, err := st.Save(*tk); err != nil {
					t.Errorf("save: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// All writes must have survived: every task is present and IDs are unique.
	st := NewTaskStore(root)
	all, err := st.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != writers*perWriter {
		t.Fatalf("stored %d tasks, want %d (lost updates under concurrency)", len(all), writers*perWriter)
	}
	seen := map[string]bool{}
	for _, tk := range all {
		if tk.ID == "" {
			t.Fatalf("task %q has empty ID; store must assign one", tk.Input)
		}
		if seen[tk.ID] {
			t.Fatalf("duplicate task ID %q (process-local counters collided)", tk.ID)
		}
		seen[tk.ID] = true
	}
}

// TestTaskStoreConcurrentSameIDNoLostUpdates covers the other half of the
// race: concurrent instances updating the SAME task ID (e.g. two processes
// driving the same lifecycle) must not drop one of the updates — the final
// record reflects one complete write, and both writes were serialized rather
// than one reading the other's pre-write state.
func TestTaskStoreConcurrentSameIDNoLostUpdates(t *testing.T) {
	root := t.TempDir()
	st := NewTaskStore(root)
	tk := NewTask("code", "shared task")
	tk.ID = "" // store-assigned, so both writers target the same ID
	saved, err := st.Save(*tk)
	if err != nil {
		t.Fatalf("initial save: %v", err)
	}

	const writers = 8
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			other := NewTaskStore(root)
			upd := *tk
			upd.ID = saved.ID
			upd.Output = fmt.Sprintf("write-%d", w)
			upd.UpdatedAt = time.Now()
			if _, err := other.Save(upd); err != nil {
				t.Errorf("concurrent save: %v", err)
			}
		}(w)
	}
	wg.Wait()

	// The file must remain valid JSON with exactly one record for the ID.
	all, err := st.List()
	if err != nil {
		t.Fatalf("List after concurrent updates: %v", err)
	}
	n := 0
	for _, it := range all {
		if it.ID == saved.ID {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("found %d records for %q, want exactly 1", n, saved.ID)
	}
}
