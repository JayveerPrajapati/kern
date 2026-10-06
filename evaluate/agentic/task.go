package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Task is one benchmark problem: a prompt, a workspace template copied per
// run, and a deterministic check script that encodes correctness, safety,
// and reuse expectations (exit 0 = pass).
type Task struct {
	Name    string
	Title   string
	Prompt  string
	Dir     string
	Fixture string // workspace template, copied to <run>/<arm>/<task>/ws
	Check   string // check.sh, run with cwd = the workspace copy
	Timeout time.Duration
}

type taskMeta struct {
	Timeout string `json:"timeout"`
}

func loadTasks(dir string) ([]Task, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read tasks dir: %w", err)
	}
	var tasks []Task
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		t, err := loadTask(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("%s: no tasks found (each task needs task.md, fixture/, check.sh)", dir)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Name < tasks[j].Name })
	return tasks, nil
}

func loadTask(dir string) (Task, error) {
	name := filepath.Base(dir)
	t := Task{Name: name, Dir: dir}

	md, err := os.ReadFile(filepath.Join(dir, "task.md"))
	if err != nil {
		return t, fmt.Errorf("task %s: %w", name, err)
	}
	t.Title, t.Prompt = parseTaskMD(string(md), name)

	t.Fixture = filepath.Join(dir, "fixture")
	if st, err := os.Stat(t.Fixture); err != nil || !st.IsDir() {
		return t, fmt.Errorf("task %s: missing fixture/ directory", name)
	}
	t.Check = filepath.Join(dir, "check.sh")
	if _, err := os.Stat(t.Check); err != nil {
		return t, fmt.Errorf("task %s: missing check.sh (must be executable)", name)
	}

	if meta, err := os.ReadFile(filepath.Join(dir, "task.json")); err == nil {
		var m taskMeta
		if err := json.Unmarshal(meta, &m); err != nil {
			return t, fmt.Errorf("task %s: parse task.json: %w", name, err)
		}
		if m.Timeout != "" {
			d, err := time.ParseDuration(m.Timeout)
			if err != nil || d <= 0 {
				return t, fmt.Errorf("task %s: invalid task.json timeout %q", name, m.Timeout)
			}
			t.Timeout = d
		}
	}
	return t, nil
}

// parseTaskMD splits the optional "# Title" first line from the prompt body.
func parseTaskMD(md, fallbackTitle string) (title, prompt string) {
	lines := strings.SplitN(md, "\n", 2)
	if len(lines) == 2 && strings.HasPrefix(lines[0], "# ") {
		return strings.TrimSpace(lines[0][2:]), strings.TrimLeft(lines[1], "\n")
	}
	return fallbackTitle, md
}
