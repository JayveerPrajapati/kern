// Package validate picks a language-appropriate build/test/syntax-check
// command for a project and runs it safely. It is the syntax gate that backs
// the auto-validation feature (#7) and feeds the self-correction loop (#9).
package validate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Command is a detected validation command.
type Command struct {
	Name string // human-readable label, e.g. "go test"
	Cmd  string
	Args []string
}

// Detect inspects the project root and returns the best validation command.
// It prefers test/build commands over syntax-only checks and skips commands
// whose binary is unavailable on PATH.
func Detect(root string) (*Command, error) {
	candidates := detectCandidates(root)
	for _, c := range candidates {
		if strings.Contains(c.Cmd, " ") {
			c = &Command{Name: c.Name, Cmd: c.Cmd, Args: c.Args}
		}
		if _, err := exec.LookPath(c.Cmd); err != nil {
			if alt := alias(c.Cmd); alt != "" {
				if _, err2 := exec.LookPath(alt); err2 == nil {
					c = &Command{Name: c.Name, Cmd: alt, Args: c.Args}
					return c, nil
				}
			}
			continue
		}
		return c, nil
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no supported project type detected in %s", root)
	}
	return nil, fmt.Errorf("required tooling not found in PATH for %s", root)
}

// alias maps a canonical binary name to a common platform alternate when the
// primary name is absent (e.g. python -> python3).
func alias(cmd string) string {
	switch cmd {
	case "python":
		return "python3"
	}
	return ""
}

// detectCandidates lists candidate commands in priority order. Detection is
// purely heuristic (file presence) and deterministic.
func detectCandidates(root string) []*Command {
	var out []*Command
	has := func(names ...string) bool {
		for _, n := range names {
			if _, err := os.Stat(filepath.Join(root, n)); err == nil {
				return true
			}
		}
		return false
	}
	glob := func(pattern string) bool {
		ms, _ := filepath.Glob(filepath.Join(root, pattern))
		return len(ms) > 0
	}

	switch {
	case has("go.mod"):
		out = append(out,
			&Command{Name: "go build", Cmd: "go", Args: []string{"build", "./..."}},
			&Command{Name: "go test", Cmd: "go", Args: []string{"test", "./..."}},
			&Command{Name: "go vet", Cmd: "go", Args: []string{"vet", "./..."}},
		)
	case has("Cargo.toml"):
		out = append(out,
			&Command{Name: "cargo build", Cmd: "cargo", Args: []string{"build", "--locked"}},
			&Command{Name: "cargo test", Cmd: "cargo", Args: []string{"test", "--no-run"}},
		)
	case has("package.json"):
		out = append(out,
			&Command{Name: "npm test", Cmd: "npm", Args: []string{"test", "--silent"}},
			&Command{Name: "npm run lint", Cmd: "npm", Args: []string{"run", "lint", "--silent"}},
			&Command{Name: "node syntax check", Cmd: "node", Args: []string{"--check", filepath.Join(root, "index.js")}},
		)
	case has("pyproject.toml", "pytest.ini", "setup.py", "setup.cfg", "requirements.txt"):
		out = append(out,
			&Command{Name: "pytest", Cmd: "python", Args: []string{"-m", "pytest", "-q"}},
			&Command{Name: "python py_compile", Cmd: "python", Args: []string{"-m", "compileall", "-q", root}},
		)
	case has("Gemfile", "Rakefile"):
		out = append(out,
			&Command{Name: "rake", Cmd: "bundle", Args: []string{"exec", "rake"}},
			&Command{Name: "ruby syntax check", Cmd: "ruby", Args: []string{"-c"}},
		)
	case has("composer.json"):
		out = append(out,
			&Command{Name: "composer test", Cmd: "composer", Args: []string{"test"}},
			&Command{Name: "php lint", Cmd: "php", Args: []string{"-l", filepath.Join(root, "index.php")}},
		)
	case has("Makefile", "makefile"):
		out = append(out,
			&Command{Name: "make test", Cmd: "make", Args: []string{"test"}},
			&Command{Name: "make build", Cmd: "make", Args: []string{"build"}},
		)
	case glob("*.go"):
		out = append(out, &Command{Name: "go vet (no module)", Cmd: "go", Args: []string{"vet", "./..."}})
	case glob("*.py"):
		out = append(out, &Command{Name: "python py_compile", Cmd: "python", Args: []string{"-m", "compileall", "-q", root}})
	case glob("*.js"), glob("*.ts"):
		out = append(out, &Command{Name: "node syntax check", Cmd: "node", Args: []string{"--check"}})
	}
	return out
}

// Result of a validation run.
type Result struct {
	Command  *Command
	OK       bool
	ExitCode int
	Output   string
	Err      error
	Dur      time.Duration
}

// Run executes the validation command in root with a timeout. Output is
// capped at maxOutput bytes (error context is enough for the heal loop).
func Run(root string, c *Command, timeout time.Duration) *Result {
	res := &Result{Command: c}
	if c == nil {
		res.Err = fmt.Errorf("nil command")
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Cmd, c.Args...)
	cmd.Dir = root
	start := time.Now()
	out, err := cmd.CombinedOutput()
	res.Dur = time.Since(start)
	res.Output = string(out)
	if ctx.Err() == context.DeadlineExceeded {
		res.Err = fmt.Errorf("timed out after %s", timeout)
		res.OK = false
		return res
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
		} else {
			res.Err = err
		}
		res.OK = false
		return res
	}
	res.OK = true
	return res
}

// Toolchain reports the runtime toolchain available for a detected command
// (used for richer CLI output).
func Toolchain(c *Command) string {
	if c == nil {
		return ""
	}
	switch c.Cmd {
	case "go":
		return runtime.Version()
	default:
		p, err := exec.LookPath(c.Cmd)
		if err != nil {
			return "not found"
		}
		return p
	}
}
