package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// fix3Fixture creates a temp Go repo with a built index (symbols: FindUser,
// Pagination) so task listing and plan degradation run against a real index,
// mirroring how the production CLI resolves them.
func fix3Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, dir, "go.mod", "module fix3demo\n\ngo 1.23\n")
	writeFixtureFile(t, dir, "service.go", `package fix3demo

// FindUser fetches a user by id.
func FindUser(id int) string { return "user" }

// Pagination is a pagination helper.
func Pagination(page int) []int { return nil }
`)
	if _, err := index.Build(dir); err != nil {
		t.Fatalf("build index: %v", err)
	}
	return dir
}

// mustApp loads a Platform for dir, failing the test on error.
func mustApp(t *testing.T, dir string) *app.Platform {
	t.Helper()
	p, err := app.New(dir)
	if err != nil {
		t.Fatalf("app.New(%s): %v", dir, err)
	}
	return p
}

// recoverExitCode runs fn, recovering the exitError sentinel that
// fatal/fatalUsage panic with, and returns the exit code (0 when fn
// returned normally).
func recoverExitCode(fn func()) (code int) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitError); ok {
				code = e.code
				return
			}
			panic(r)
		}
	}()
	fn()
	return 0
}

func TestTasksListsTasks(t *testing.T) {
	dir := fix3Fixture(t)
	// Seed a task through the same TaskService the CLI uses.
	ts := app.NewTaskService(mustApp(t, dir), eventbus.New())
	task, err := ts.Create("add pagination to the service layer")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	// A fresh service (a new process) must still list the task via the
	// persisted store — the same store `kern task <id>` reads.
	out := captureStdout(t, func() {
		runTasks([]string{"--root", dir})
	})
	for _, col := range []string{"ID", "STATE", "INTENT", "UPDATED"} {
		if !strings.Contains(out, col) {
			t.Errorf("kern tasks output missing column %q:\n%s", col, out)
		}
	}
	if !strings.Contains(out, task.ID) {
		t.Errorf("kern tasks output missing task id %q:\n%s", task.ID, out)
	}
	if !strings.Contains(out, "add pagination to the service layer") {
		t.Errorf("kern tasks output missing task intent:\n%s", out)
	}
}

func TestTaskListSubcommand(t *testing.T) {
	dir := fix3Fixture(t)
	ts := app.NewTaskService(mustApp(t, dir), eventbus.New())
	task, err := ts.Create("fix the query layer")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	out := captureStdout(t, func() {
		runTask([]string{"list", "--root", dir})
	})
	if !strings.Contains(out, task.ID) {
		t.Errorf("kern task list output missing task id %q:\n%s", task.ID, out)
	}
	// `kern task <id>` detail still works for the same task (list and
	// detail read the same store).
	detail := captureStdout(t, func() {
		runTask([]string{task.ID, "--root", dir})
	})
	if !strings.Contains(detail, "task: "+task.ID) {
		t.Errorf("kern task <id> output missing task header:\n%s", detail)
	}
}

func TestTasksEmptyOutput(t *testing.T) {
	dir := fix3Fixture(t)
	out := captureStdout(t, func() {
		runTasks([]string{"--root", dir})
	})
	if !strings.Contains(out, "no tasks") {
		t.Errorf("kern tasks on empty store = %q, want 'no tasks'", out)
	}
}

func TestDispatchTasksRegistered(t *testing.T) {
	if e, ok := commandTable["tasks"]; !ok || e.help == "" {
		t.Errorf("commandTable[tasks] = %+v, want registered entry with help", e)
	}
}

func TestPlanSymbolDegradeHints(t *testing.T) {
	dir := fix3Fixture(t)
	// A real symbol-resolution miss (the error resolveSymbol produces for
	// free-text changes): must degrade to an actionable message with close
	// candidates and a kern search pointer, not a bare error.
	err := fmt.Errorf("no symbol named %q was found in this project's index (candidates: %s). Pass a concrete exported name.", "pagination", "pagination")
	stderr := captureStderr(t, func() {
		code := recoverExitCode(func() {
			symbolDegrade("plan", "Add pagination to the service layer", dir, err)
		})
		if code != 1 {
			t.Errorf("symbolDegrade exit code = %d, want 1", code)
		}
	})
	for _, want := range []string{
		"no matching symbol",
		"close candidates",
		"Pagination",
		"kern search",
		"kern plan <symbol>",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("symbolDegrade message missing %q:\n%s", want, stderr)
		}
	}
	if symbolDegrade("plan", "x", ".", fmt.Errorf("some unrelated failure")) {
		t.Fatal("symbolDegrade handled a non-symbol error, want passthrough")
	}
}

// storeRecordCount returns how many task records the persisted store for dir
// currently holds (the same file `kern task <id>` and a fresh TaskService
// read).
func storeRecordCount(t *testing.T, dir string) int {
	t.Helper()
	ts := app.NewTaskService(mustApp(t, dir), eventbus.New())
	if ts.Store() == nil {
		t.Fatal("task service has no persisted store")
	}
	list, err := ts.Store().List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	return len(list)
}

// taskIDFromOutput extracts the task ID from a CLI "[task: <id> — state: …]"
// line.
func taskIDFromOutput(t *testing.T, out string) string {
	t.Helper()
	const marker = "[task: "
	start := strings.Index(out, marker)
	if start < 0 {
		t.Fatalf("output has no %q line:\n%s", marker, out)
	}
	rest := out[start+len(marker):]
	end := strings.Index(rest, " — ")
	if end < 0 {
		t.Fatalf("cannot parse task line from output:\n%s", out)
	}
	return rest[:end]
}

// TestAnalyzeTaskPersistenceGatedOnTaskFlag locks the F9 regression fix: the
// CLI's documented `--task` flag must produce an authoritative persisted task
// record (store-assigned t-<n>, queryable via `kern task <id>` from a fresh
// service), while a run without --task must not touch the store. A --lens-only
// run (no --task) stays ephemeral: it uses the taskful machinery but makes no
// persistence promise.
func TestAnalyzeTaskPersistenceGatedOnTaskFlag(t *testing.T) {
	dir := fix3Fixture(t)

	// With --task: the [task: <id>] line names a persisted record.
	var out string
	if code := recoverExitCode(func() {
		out = captureStdout(t, func() { runAnalyze("analyze", []string{"FindUser", "--root", dir, "--task", "persist-me"}) })
	}); code != 0 {
		t.Fatalf("analyze --task exited %d, want 0 (stderr above)", code)
	}
	id := taskIDFromOutput(t, out)
	if !strings.HasPrefix(id, "t-") {
		t.Fatalf("task id = %q, want store-assigned t-<n> prefix with --task", id)
	}
	// A fresh service (a new process) must resolve the task via the store —
	// the same store `kern task <id>` reads.
	ts := app.NewTaskService(mustApp(t, dir), eventbus.New())
	if got, ok := ts.Get(id); !ok {
		t.Fatalf("task %q not queryable from a fresh service after --task analyze", id)
	} else if got.State == "" {
		t.Fatalf("task %q loaded from store has no state", id)
	}

	// Without --task: the stateless path prints no task line and the store
	// gains no records.
	before := storeRecordCount(t, dir)
	if code := recoverExitCode(func() {
		captureStdout(t, func() { runAnalyze("analyze", []string{"FindUser", "--root", dir}) })
	}); code != 0 {
		t.Fatalf("stateless analyze exited %d, want 0", code)
	}
	if got := storeRecordCount(t, dir); got != before {
		t.Fatalf("stateless analyze added %d store record(s): %d -> %d", got-before, before, got)
	}

	// --lens only (no --task): taskful machinery, but ephemeral — the printed
	// ID is a-<n> and is NOT queryable from a fresh service.
	if code := recoverExitCode(func() {
		out = captureStdout(t, func() { runAnalyze("analyze", []string{"FindUser", "--root", dir, "--lens", "security"}) })
	}); code != 0 {
		t.Fatalf("analyze --lens exited %d, want 0", code)
	}
	ephID := taskIDFromOutput(t, out)
	if !strings.HasPrefix(ephID, "a-") {
		t.Fatalf("lens-only task id = %q, want ephemeral a-<n> prefix", ephID)
	}
	ts2 := app.NewTaskService(mustApp(t, dir), eventbus.New())
	if _, ok := ts2.Get(ephID); ok {
		t.Fatalf("lens-only task %q must NOT be persisted (F9: no store pollution without --task)", ephID)
	}
	if got := storeRecordCount(t, dir); got != before {
		t.Fatalf("lens-only analyze added %d store record(s): %d -> %d", got-before, before, got)
	}
}

// TestWhatIfImpactTaskPersistenceGatedOnTaskFlag locks the same --task gate on
// `kern what-if` and `kern impact`: --task persists (fresh Get succeeds),
// absent --task the store is untouched. The task ID is read from the --json
// task_id field (the text renderers carry no task line).
func TestWhatIfImpactTaskPersistenceGatedOnTaskFlag(t *testing.T) {
	dir := fix3Fixture(t)

	// what-if --task persists.
	var out string
	if code := recoverExitCode(func() {
		out = captureStdout(t, func() { runWhatIf("what-if", []string{"FindUser", "--root", dir, "--task", "wi", "--json"}) })
	}); code != 0 {
		t.Fatalf("what-if --task exited %d, want 0", code)
	}
	wi := assertValidJSON(t, out)
	id, _ := wi["task_id"].(string)
	if !strings.HasPrefix(id, "t-") {
		t.Fatalf("what-if task_id = %q, want t-<n> with --task", id)
	}
	ts := app.NewTaskService(mustApp(t, dir), eventbus.New())
	if _, ok := ts.Get(id); !ok {
		t.Fatalf("what-if task %q not queryable from a fresh service", id)
	}

	// impact --task persists.
	if code := recoverExitCode(func() {
		out = captureStdout(t, func() { runImpact([]string{"FindUser", "--root", dir, "--task", "imp", "--json"}) })
	}); code != 0 {
		t.Fatalf("impact --task exited %d, want 0", code)
	}
	imp := assertValidJSON(t, out)
	id2, _ := imp["task_id"].(string)
	if !strings.HasPrefix(id2, "t-") {
		t.Fatalf("impact task_id = %q, want t-<n> with --task", id2)
	}
	ts = app.NewTaskService(mustApp(t, dir), eventbus.New())
	if _, ok := ts.Get(id2); !ok {
		t.Fatalf("impact task %q not queryable from a fresh service", id2)
	}

	// Absent --task: no store growth.
	before := storeRecordCount(t, dir)
	if code := recoverExitCode(func() {
		captureStdout(t, func() { runWhatIf("what-if", []string{"FindUser", "--root", dir, "--json"}) })
	}); code != 0 {
		t.Fatalf("what-if exited %d, want 0", code)
	}
	if code := recoverExitCode(func() {
		captureStdout(t, func() { runImpact([]string{"FindUser", "--root", dir, "--json"}) })
	}); code != 0 {
		t.Fatalf("impact exited %d, want 0", code)
	}
	if got := storeRecordCount(t, dir); got != before {
		t.Fatalf("stateless what-if/impact added %d store record(s): %d -> %d", got-before, before, got)
	}
}
