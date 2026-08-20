// Package validate picks a language-appropriate build/test/syntax-check
// command for a project and runs it safely.
package validate

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/processgroup"
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
	case has("pom.xml"):
		out = append(out,
			&Command{Name: "maven test", Cmd: "mvn", Args: []string{"test", "-q"}},
			&Command{Name: "maven compile", Cmd: "mvn", Args: []string{"compile", "-q"}},
		)
	case has("build.gradle", "build.gradle.kts"):
		out = append(out,
			&Command{Name: "gradle build", Cmd: "gradle", Args: []string{"build", "-q"}},
			&Command{Name: "gradle test", Cmd: "gradle", Args: []string{"test", "-q"}},
		)
	case nestedPom(root) != "" || nestedGradle(root) != "":
		// Single-module Maven/Gradle project in a subdirectory (no root
		// pom.xml/build.gradle). Use -f so mvn/gradle finds the build file
		// without a reactor.
		if p := nestedPom(root); p != "" {
			out = append(out,
				&Command{Name: "maven test", Cmd: "mvn", Args: []string{"-f", p, "test", "-q"}},
				&Command{Name: "maven compile", Cmd: "mvn", Args: []string{"-f", p, "compile", "-q"}},
			)
		} else if g := nestedGradle(root); g != "" {
			out = append(out,
				&Command{Name: "gradle build", Cmd: "gradle", Args: []string{"-p", filepath.Dir(g), "build", "-q"}},
			)
		}
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
			&Command{Name: "python py_compile", Cmd: "python", Args: []string{"-m", "compileall", "-q", root}},
			&Command{Name: "pytest", Cmd: "python", Args: []string{"-m", "pytest", "-q"}},
		)
	case has("Gemfile", "Rakefile"):
		// `ruby -c` with no file argument reads from stdin and blocks forever,
		// so only offer the syntax check when a .rb file is present.
		var rubyArgs []string
		if rbs, _ := filepath.Glob(filepath.Join(root, "*.rb")); len(rbs) > 0 {
			rubyArgs = []string{"-c", rbs[0]}
		}
		out = append(out,
			&Command{Name: "rake", Cmd: "bundle", Args: []string{"exec", "rake"}},
		)
		if len(rubyArgs) > 0 {
			out = append(out, &Command{Name: "ruby syntax check", Cmd: "ruby", Args: rubyArgs})
		}
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
	case has("Chart.yaml") || glob("*/Chart.yaml"):
		// Helm chart repo: lint each chart directory containing Chart.yaml.
		chartDirs := helmChartDirs(root)
		for _, d := range chartDirs {
			out = append(out, &Command{Name: "helm lint " + d, Cmd: "helm", Args: []string{"lint", d}})
		}
	case glob("*.go"):
		out = append(out, &Command{Name: "go vet (no module)", Cmd: "go", Args: []string{"vet", "./..."}})
	case glob("*.py"):
		out = append(out, &Command{Name: "python py_compile", Cmd: "python", Args: []string{"-m", "compileall", "-q", root}})
	case glob("*.js"), glob("*.ts"):
		out = append(out, &Command{Name: "node syntax check", Cmd: "node", Args: []string{"--check"}})
	case hasFilesRecursive(root, ".py"):
		out = append(out, &Command{Name: "python py_compile", Cmd: "python", Args: []string{"-m", "compileall", "-q", root}})
	case hasFilesRecursive(root, ".js"):
		out = append(out, &Command{Name: "node syntax check", Cmd: "node", Args: []string{"--check"}})
	}
	return out
}

// hasFilesRecursive reports whether any file with the given extension exists
// under root, including subdirectories (shallow walk, stops at the first
// match). Ignored directories (index.IgnoredDir) are skipped. This catches
// projects whose source lives only in subdirectories, where root-level globs
// miss every file.
func hasFilesRecursive(root, ext string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && index.IgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ext) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// nestedPom returns the root-relative path of the first pom.xml in a
// subdirectory (one level deep), or "" when none exists. This handles
// single-module Maven projects that live in a subdirectory without a root
// aggregator pom (e.g. lcm-app/pom.xml).
func nestedPom(root string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(root, e.Name(), "pom.xml")
		if _, err := os.Stat(p); err == nil {
			return filepath.Join(e.Name(), "pom.xml")
		}
	}
	return ""
}

// nestedGradle returns the root-relative path of the first build.gradle or
// build.gradle.kts in a subdirectory (one level deep), or "" when none exists.
func nestedGradle(root string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		for _, name := range []string{"build.gradle", "build.gradle.kts"} {
			p := filepath.Join(root, e.Name(), name)
			if _, err := os.Stat(p); err == nil {
				return filepath.Join(e.Name(), name)
			}
		}
	}
	return ""
}

// helmChartDirs returns root-relative chart directories (one level deep)
// that contain a Chart.yaml, plus "." when root itself has a Chart.yaml.
func helmChartDirs(root string) []string {
	var dirs []string
	if _, err := os.Stat(filepath.Join(root, "Chart.yaml")); err == nil {
		dirs = append(dirs, ".")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return dirs
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), "Chart.yaml")); err == nil {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs
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

// maxOutput caps the amount of combined output captured from a validation
// command. A noisy build or test cannot exhaust memory — only this much is
// buffered as error context for the heal loop.
const maxOutput = 1 << 20 // 1 MiB

// Run executes the validation command in root with a timeout. Output is
// capped at maxOutput bytes (error context is enough for the heal loop). The
// parent context cancels the command when aborted, killing the whole process
// group (including grandchildren such as compiler daemons or npm
// postinstall hooks) rather than just the direct child.
func Run(parent context.Context, root string, c *Command, timeout time.Duration) *Result {
	if parent == nil {
		parent = context.Background()
	}
	res := &Result{Command: c}
	if c == nil {
		res.Err = fmt.Errorf("nil command")
		return res
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Cmd, c.Args...)
	cmd.Dir = root
	out := &cappedBuffer{limit: maxOutput}
	cmd.Stdout = out
	cmd.Stderr = out
	processgroup.Set(cmd)
	start := time.Now()
	err := cmd.Run()
	res.Dur = time.Since(start)
	res.Output = out.String()
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		// The context kill only reaches the direct child; kill the process
		// group so any grandchildren the command spawned also die.
		processgroup.Kill(cmd)
		res.ExitCode = -1 // signal-killed, not a clean exit
		res.Err = fmt.Errorf("timed out after %s", timeout)
		res.OK = false
		return res
	case ctx.Err() == context.Canceled:
		processgroup.Kill(cmd)
		res.ExitCode = -1
		res.Err = fmt.Errorf("cancelled")
		res.OK = false
		return res
	case err != nil:
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

// cappedBuffer buffers up to limit bytes of combined output and discards (but
// still counts, so writes never block) everything beyond it. It records a
// single extra byte so the caller can distinguish "exactly limit" from
// "truncated" without buffering the whole stream. This bounds memory for
// commands that spew unbounded output.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	overLimit bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	room := c.limit + 1 - c.buf.Len()
	if room <= 0 {
		c.overLimit = true
		return n, nil
	}
	if n > room {
		p = p[:room]
		c.overLimit = true
	}
	c.buf.Write(p)
	// Always report the full input length so the exec copy loop does not see a
	// "short write" (it only errors when n < len(p) with a nil error).
	return n, nil
}

func (c *cappedBuffer) String() string { return c.buf.String() }

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
