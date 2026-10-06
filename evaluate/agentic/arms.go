package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Arm is one side of a comparison: a named command template that runs a
// headless agent on a task prompt inside the copied workspace. Arms are pure
// data — any headless agent works; the shipped arms.json uses opencode with
// and without --pure (external plugins, i.e. kern's tools, disabled).
type Arm struct {
	Name    string            `json:"name"`
	Command []string          `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
}

type armsFile struct {
	Arms []Arm `json:"arms"`
}

func loadArms(path string) ([]Arm, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read arms: %w", err)
	}
	var f armsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(f.Arms) == 0 {
		return nil, fmt.Errorf("%s: no arms defined", path)
	}
	seen := map[string]bool{}
	for _, a := range f.Arms {
		if a.Name == "" {
			return nil, fmt.Errorf("%s: arm with empty name", path)
		}
		if seen[a.Name] {
			return nil, fmt.Errorf("%s: duplicate arm name %q", path, a.Name)
		}
		seen[a.Name] = true
		if len(a.Command) == 0 {
			return nil, fmt.Errorf("%s: arm %q has an empty command", path, a.Name)
		}
	}
	return f.Arms, nil
}

// argv expands the command template: a literal "{prompt}" element is
// replaced with the task prompt; if no placeholder is present the prompt is
// appended as the final argument.
func (a Arm) argv(prompt string) []string {
	out := make([]string, 0, len(a.Command)+1)
	replaced := false
	for _, arg := range a.Command {
		if arg == "{prompt}" {
			out = append(out, prompt)
			replaced = true
			continue
		}
		out = append(out, arg)
	}
	if !replaced {
		out = append(out, prompt)
	}
	return out
}

// envPairs renders the arm's env additions for exec.Cmd.Env.
func (a Arm) envPairs() []string {
	if len(a.Env) == 0 {
		return nil
	}
	out := make([]string, 0, len(a.Env))
	for k, v := range a.Env {
		out = append(out, k+"="+v)
	}
	return out
}
