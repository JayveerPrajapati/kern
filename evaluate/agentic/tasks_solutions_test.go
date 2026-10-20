package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestTasksAreSolvableAndNotVacuous guards every task that ships a reference
// solution/: its check.sh must FAIL on the pristine fixture (otherwise the
// task measures nothing) and PASS once the solution overlay is applied
// (otherwise the task is unsolvable or the check is wrong).
func TestTasksAreSolvableAndNotVacuous(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not found")
	}
	tasks, err := filepath.Glob(filepath.Join("tasks", "*", "solution"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) == 0 {
		t.Fatal("no task ships a solution/ — the realistic tasks must be self-validating")
	}
	for _, sol := range tasks {
		taskDir, err := filepath.Abs(filepath.Dir(sol))
		if err != nil {
			t.Fatal(err)
		}
		sol := filepath.Join(taskDir, "solution")
		t.Run(filepath.Base(taskDir), func(t *testing.T) {
			ws := t.TempDir()
			if err := copyDir(filepath.Join(taskDir, "fixture"), ws); err != nil {
				t.Fatal(err)
			}
			check := func() error {
				cmd := exec.Command("sh", filepath.Join(taskDir, "check.sh"))
				cmd.Dir = ws
				cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Logf("check output:\n%s", out)
				}
				return err
			}
			if err := check(); err == nil {
				t.Fatal("check.sh passes on the pristine fixture; the task is vacuous")
			}
			if err := copyDir(sol, ws); err != nil {
				t.Fatal(err)
			}
			if err := check(); err != nil {
				t.Fatalf("check.sh fails with the reference solution applied: %v", err)
			}
		})
	}
}
