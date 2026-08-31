package agent

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// TestTaskStoreCrossProcessNoLostUpdates is the true cross-process regression
// test: it spawns several child test processes, each holding its OWN TaskStore
// instance (its own fd and its own in-process PathLock — the two never meet
// across processes) and writing to the SAME backing file. This is the exact
// shape that corrupted the task store under `go test ./...` before the
// cross-process file lock: each process's load->modify->save interleaved with
// the others' and every Save (replace by ID) silently dropped the other
// processes' writes.
func TestTaskStoreCrossProcessNoLostUpdates(t *testing.T) {
	if os.Getenv("TASKSTORE_HELPER") == "1" {
		// Child: write a few store-assigned tasks, then exit. The store
		// instance here is this process's own (own fd, own PathLock).
		root := os.Getenv("TASKSTORE_ROOT")
		st := NewTaskStore(root)
		for i := 0; i < 5; i++ {
			tk := NewTask("analyze", fmt.Sprintf("child-%d-%d", os.Getpid(), i))
			tk.ID = "" // store assigns under the file lock
			if _, err := st.Save(*tk); err != nil {
				fmt.Fprintf(os.Stderr, "child save: %v\n", err)
				os.Exit(1)
			}
		}
		os.Exit(0)
	}

	root := t.TempDir()
	const children = 4
	const perChild = 5

	// Spawn children concurrently; each writes to the shared per-root file.
	done := make(chan error, children)
	for i := 0; i < children; i++ {
		go func() {
			cmd := exec.Command(os.Args[0], "-test.run=TestTaskStoreCrossProcessNoLostUpdates")
			cmd.Env = append(os.Environ(), "TASKSTORE_HELPER=1", "TASKSTORE_ROOT="+root)
			out, err := cmd.CombinedOutput()
			if err != nil {
				done <- fmt.Errorf("child failed: %v: %s", err, out)
				return
			}
			done <- nil
		}()
	}
	for i := 0; i < children; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}

	// Every child's every task must have survived: 4x5 = 20 records with
	// unique IDs. Any lost update fails here.
	all, err := NewTaskStore(root).List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != children*perChild {
		t.Fatalf("stored %d tasks, want %d (cross-process lost updates)", len(all), children*perChild)
	}
	seen := map[string]bool{}
	for _, tk := range all {
		if seen[tk.ID] {
			t.Fatalf("duplicate task ID %q across processes", tk.ID)
		}
		seen[tk.ID] = true
	}
}
