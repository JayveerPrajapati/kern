package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// cmdRescore recomputes metrics for a finished run from its kept artifacts
// (workspace copies + raw event streams + prior metrics.json). It NEVER
// executes arm commands — that is the no-API-spend contract — but it does
// re-run each task's deterministic check.sh, so check improvements propagate
// to old runs. Output: <run-dir>/results-rescored.json.
func cmdRescore(args []string) error {
	fs := flag.NewFlagSet("rescore", flag.ContinueOnError)
	tasksDir := fs.String("tasks", "", "tasks directory (default <tool>/tasks)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return fmt.Errorf("usage: agentic rescore [-tasks dir] <run-dir>")
	}
	runDir := rest[0]
	if st, err := os.Stat(runDir); err != nil || !st.IsDir() {
		return fmt.Errorf("run dir %s: not a directory", runDir)
	}
	td, err := toolDir()
	if err != nil {
		return err
	}
	if *tasksDir == "" {
		*tasksDir = filepath.Join(td, "tasks")
	}
	tasks, err := loadTasks(*tasksDir)
	if err != nil {
		return err
	}
	byName := map[string]Task{}
	for _, t := range tasks {
		byName[t.Name] = t
	}

	armEnts, err := os.ReadDir(runDir)
	if err != nil {
		return err
	}
	var runs []RunOutcome
	var warnings []string
	for _, ae := range armEnts {
		if !ae.IsDir() {
			continue
		}
		taskEnts, err := os.ReadDir(filepath.Join(runDir, ae.Name()))
		if err != nil {
			return err
		}
		for _, te := range taskEnts {
			if !te.IsDir() {
				continue
			}
			task, ok := byName[te.Name()]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("arm %s: task %q is no longer known under -tasks; skipped", ae.Name(), te.Name()))
				continue
			}
			oc, err := rescoreOne(runDir, ae.Name(), task)
			if err != nil {
				return err
			}
			runs = append(runs, oc)
		}
	}
	if len(runs) == 0 {
		return fmt.Errorf("%s: no (arm, task) directories found", runDir)
	}
	res := newResults(runs)
	for _, w := range warnings {
		res.Warnings = append(res.Warnings, w)
	}
	if err := writeJSON(filepath.Join(runDir, "results-rescored.json"), res); err != nil {
		return err
	}
	printSummary(os.Stdout, res)
	fmt.Fprintf(os.Stderr, "rescored: %s\n", filepath.Join(runDir, "results-rescored.json"))
	return nil
}

// rescoreOne recomputes pass/delta/usage for one run. Duration, exit code,
// and the timed-out flag are read back from the original metrics.json —
// they are properties of the agent execution, which must not re-run.
func rescoreOne(runDir, arm string, task Task) (RunOutcome, error) {
	base := filepath.Join(runDir, arm, task.Name)
	ws := filepath.Join(base, "ws")
	art := filepath.Join(base, "art")

	oc := RunOutcome{Arm: arm, Task: task.Name, DurationSec: -1}
	if data, err := os.ReadFile(filepath.Join(art, "metrics.json")); err == nil {
		var prior RunOutcome
		if json.Unmarshal(data, &prior) == nil {
			oc.DurationSec = prior.DurationSec
			oc.Exit = prior.Exit
			oc.TimedOut = prior.TimedOut
		}
	}

	oc.Usage = extractUsage(mustRead(filepath.Join(art, "events.jsonl")))
	if d := workspaceDelta(task.Fixture, ws); d != nil {
		oc.FilesChanged, oc.NonTestLOC, oc.TestLOC = d.FilesChanged, d.NonTestLOC, d.TestLOC
	}
	oc.Pass = runCheck(task, ws, art)
	return oc, nil
}
