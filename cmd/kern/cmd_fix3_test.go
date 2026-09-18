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

func TestRunAgentHintsMCPSurfaces(t *testing.T) {
	for _, sub := range []string{"message", "interrupt"} {
		stderr := captureStderr(t, func() {
			code := recoverExitCode(func() {
				runAgent([]string{sub, "--to", "worker", "hello"})
			})
			if code != 1 {
				t.Errorf("runAgent(%q) exit code = %d, want 1 (runtime error, not usage dump)", sub, code)
			}
		})
		if !strings.Contains(stderr, "MCP-tool surface") {
			t.Errorf("runAgent(%q) hint missing 'MCP-tool surface':\n%s", sub, stderr)
		}
		if !strings.Contains(stderr, "kern_agent_"+sub) {
			t.Errorf("runAgent(%q) hint missing kern_agent_%s MCP tool name:\n%s", sub, sub, stderr)
		}
		if !strings.Contains(stderr, "kern agent-"+sub) {
			t.Errorf("runAgent(%q) hint missing CLI mirror 'kern agent-%s':\n%s", sub, sub, stderr)
		}
	}
}

func TestRunAgentUnknownSubcommand(t *testing.T) {
	stderr := captureStderr(t, func() {
		code := recoverExitCode(func() {
			runAgent([]string{"frobnicate"})
		})
		if code != 2 {
			t.Errorf("runAgent(unknown) exit code = %d, want 2 (usage error)", code)
		}
	})
	if !strings.Contains(stderr, "MCP-tool surface") {
		t.Errorf("runAgent(unknown) hint missing MCP-tool hint:\n%s", stderr)
	}
}

func TestDispatchAgentAndTasksRegistered(t *testing.T) {
	if e, ok := commandTable["tasks"]; !ok || e.help == "" {
		t.Errorf("commandTable[tasks] = %+v, want registered entry with help", e)
	}
	if e, ok := commandTable["agent"]; !ok || e.help == "" {
		t.Errorf("commandTable[agent] = %+v, want registered entry with help", e)
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
			planSymbolDegrade("Add pagination to the service layer", dir, err)
		})
		if code != 1 {
			t.Errorf("planSymbolDegrade exit code = %d, want 1", code)
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
			t.Errorf("planSymbolDegrade message missing %q:\n%s", want, stderr)
		}
	}
}

func TestPlanSymbolDegradePassesThroughOtherErrors(t *testing.T) {
	if planSymbolDegrade("x", ".", fmt.Errorf("some unrelated failure")) {
		t.Fatal("planSymbolDegrade handled a non-symbol error, want passthrough")
	}
}
