package tasklife

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/metrics"
)

func TestSnapshotPersistenceFailureIsLoggedAndCounted(t *testing.T) {
	root := t.TempDir()
	cacheRoot := t.TempDir()
	t.Setenv("KERN_CACHE_DIR", cacheRoot)
	// A regular file where the snapshots directory must go makes the first
	// lock/create fail without depending on OS permissions.
	if err := os.WriteFile(filepath.Join(cacheRoot, "snapshots"), []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := metrics.New()
	svc := &TaskService{
		snapshots: agent.NewSnapshotStore(root),
		metrics:   rec,
	}
	svc.persist(&agent.Task{ID: "t-persist-failure"})

	if got := rec.Snapshot().TaskPersistenceFailureCount; got != 1 {
		t.Fatalf("TaskPersistenceFailureCount = %d, want 1", got)
	}
}

func TestTaskPersistenceFailureIsLoggedAndCounted(t *testing.T) {
	root := t.TempDir()
	cacheRoot := t.TempDir()
	t.Setenv("KERN_CACHE_DIR", cacheRoot)
	if err := os.WriteFile(filepath.Join(cacheRoot, "tasks"), []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := metrics.New()
	svc := &TaskService{
		store:   agent.NewTaskStore(root),
		metrics: rec,
	}
	svc.persist(&agent.Task{ID: "t-store-failure"})

	if got := rec.Snapshot().TaskPersistenceFailureCount; got != 1 {
		t.Fatalf("TaskPersistenceFailureCount = %d, want 1", got)
	}
}
