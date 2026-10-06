// Command agentic is the kern agentic benchmark harness: it runs the same
// coding tasks through a headless agent twice — once with external plugins
// (and therefore kern's tools) and once without them — then scores the
// results deterministically. Raw artifacts (the agent's event stream, the
// final workspace) are always kept so metrics can be recomputed later with
// `rescore` at zero API cost.
//
// Usage:
//
//	go run ./evaluate/agentic run     [-tasks dir] [-arms file] [-out dir] [-timeout 10m] [-task sub] [-arm sub]
//	go run ./evaluate/agentic rescore [-tasks dir] <run-dir>
//	go run ./evaluate/agentic list   [-tasks dir] [-arms file]
//
// This is a dev script, deliberately outside the product surface: stdlib
// only, no kern internal imports, not listed in the architecture ledger.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func main() {
	if err := Main(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "agentic:", err)
		os.Exit(1)
	}
}

// Main dispatches subcommands; it is the testable entry point.
func Main(args []string) error {
	if len(args) == 0 {
		return cmdRun(nil)
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "rescore":
		return cmdRescore(args[1:])
	case "list":
		return cmdList(args[1:])
	default:
		if strings.HasPrefix(args[0], "-") {
			// Flags-first invocation: default to the run subcommand.
			return cmdRun(args)
		}
		return fmt.Errorf("unknown subcommand %q (want run, rescore, or list)", args[0])
	}
}

// toolDir resolves the directory containing this tool's static assets
// (arms.json, tasks/). With `go run` the compiled binary lives in a temp
// dir, so runtime.Caller — which records the source file path — is the
// reliable mechanism; the executable dir is the fallback for built binaries
// shipped next to their assets.
func toolDir() (string, error) {
	if _, file, _, ok := runtime.Caller(0); ok {
		return filepath.Dir(file), nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// checkTimeout bounds every deterministic check script.
const checkTimeout = 60 * time.Second

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var (
		tasksDir = fs.String("tasks", "", "tasks directory (default <tool>/tasks)")
		armsFile = fs.String("arms", "", "arms JSON file (default <tool>/arms.json)")
		outDir   = fs.String("out", "", "output directory (default <tool>/results)")
		timeout  = fs.Duration("timeout", 10*time.Minute, "per-task agent timeout")
		taskSub  = fs.String("task", "", "substring filter on task name")
		armSub   = fs.String("arm", "", "substring filter on arm name")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	td, err := toolDir()
	if err != nil {
		return err
	}
	if *tasksDir == "" {
		*tasksDir = filepath.Join(td, "tasks")
	}
	if *armsFile == "" {
		*armsFile = filepath.Join(td, "arms.json")
	}
	if *outDir == "" {
		*outDir = filepath.Join(td, "results")
	}

	arms, err := loadArms(*armsFile)
	if err != nil {
		return err
	}
	tasks, err := loadTasks(*tasksDir)
	if err != nil {
		return err
	}
	arms = filterArms(arms, *armSub)
	tasks = filterTasks(tasks, *taskSub)
	if len(arms) == 0 {
		return fmt.Errorf("no arms match %q", *armSub)
	}
	if len(tasks) == 0 {
		return fmt.Errorf("no tasks match %q", *taskSub)
	}

	runDir := filepath.Join(*outDir, time.Now().UTC().Format("20060102T150405Z"))
	results, err := runAll(context.Background(), arms, tasks, runDir, *timeout)
	if err != nil {
		return err
	}
	res := newResults(results)
	if err := writeJSON(filepath.Join(runDir, "results.json"), res); err != nil {
		return err
	}
	printSummary(os.Stdout, res)
	fmt.Fprintf(os.Stderr, "run dir: %s\n", runDir)
	return nil
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	var (
		tasksDir = fs.String("tasks", "", "tasks directory (default <tool>/tasks)")
		armsFile = fs.String("arms", "", "arms JSON file (default <tool>/arms.json)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	td, err := toolDir()
	if err != nil {
		return err
	}
	if *tasksDir == "" {
		*tasksDir = filepath.Join(td, "tasks")
	}
	if *armsFile == "" {
		*armsFile = filepath.Join(td, "arms.json")
	}
	arms, err := loadArms(*armsFile)
	if err != nil {
		return err
	}
	tasks, err := loadTasks(*tasksDir)
	if err != nil {
		return err
	}
	fmt.Println("arms:")
	for _, a := range arms {
		fmt.Printf("  %-12s %s\n", a.Name, strings.Join(a.Command, " "))
	}
	fmt.Println("tasks:")
	for _, t := range tasks {
		fmt.Printf("  %-16s %s\n", t.Name, t.Title)
	}
	return nil
}

func filterArms(arms []Arm, sub string) []Arm {
	if sub == "" {
		return arms
	}
	var out []Arm
	for _, a := range arms {
		if strings.Contains(a.Name, sub) {
			out = append(out, a)
		}
	}
	return out
}

func filterTasks(tasks []Task, sub string) []Task {
	if sub == "" {
		return tasks
	}
	var out []Task
	for _, t := range tasks {
		if strings.Contains(t.Name, sub) {
			out = append(out, t)
		}
	}
	return out
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
