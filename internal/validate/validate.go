// Package validate picks a language-appropriate build/test/syntax-check
// command for a project and runs it safely.
package validate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/processgroup"
)

// ErrNoProjectType is returned by Detect and DetectKind when the project
// root has NO supported project type at all (zero detection candidates) —
// distinct from "a candidate exists but its tooling is missing", which stays
// an actionable error. Callers use errors.Is to degrade a verification check
// to a clean skip instead of a false FAIL (F1).
var ErrNoProjectType = errors.New("no supported project type detected")

// ErrNoKindCommand is returned by DetectKind when a project type IS detected
// but it declares no command of the requested kind — e.g. a package.json
// with no "build" script, or a Makefile with no build target. Absence of a
// step is not a failure; callers degrade the check to an honest skip, never
// a vacuous PASS or a false FAIL (6.1).
var ErrNoKindCommand = errors.New("no command of that kind declared")

// Command is a detected validation command.
type Command struct {
	Name string // human-readable label, e.g. "go test"
	Cmd  string
	Args []string
	// Kind categorizes the command: "build" (compile/assemble), "test"
	// (run the project's test suite), or "lint" (static analysis). Used by
	// DetectKind so verification can pick, e.g., the test command while
	// VerifyBuild keeps the build command.
	Kind string
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
					c = &Command{Name: c.Name, Cmd: alt, Args: c.Args, Kind: kindOf(c)}
					return c, nil
				}
			}
			continue
		}
		c.Kind = kindOf(c)
		return c, nil
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w in %s", ErrNoProjectType, root)
	}
	return nil, fmt.Errorf("required tooling not found in PATH for %s", root)
}

// DetectKind inspects the project root and returns the best command of the
// given kind ("build", "test", or "lint"). It shares Detect's candidate list
// and PATH filtering, so a Go module yields "go test" for kind "test" while
// an npm project yields "npm test" — verification sub-checks no longer have
// to hard-code Go commands. Returns the same errors as Detect when nothing
// is detected.
func DetectKind(root, kind string) (*Command, error) {
	candidates := detectCandidates(root)
	for _, c := range candidates {
		if kindOf(c) != kind {
			continue
		}
		if _, err := exec.LookPath(c.Cmd); err != nil {
			if alt := alias(c.Cmd); alt != "" {
				if _, err2 := exec.LookPath(alt); err2 == nil {
					return &Command{Name: c.Name, Cmd: alt, Args: c.Args, Kind: kind}, nil
				}
			}
			continue
		}
		c.Kind = kind
		return c, nil
	}
	// Distinguish the outcomes so callers can degrade an ABSENT step to a
	// skip while a PRESENT step with missing tooling stays an actionable
	// error:
	//   - zero candidates at all                  -> ErrNoProjectType
	//   - a project type exists but declares no
	//     <kind> step (e.g. no build script in
	//     package.json, no build target in a
	//     Makefile)                               -> ErrNoKindCommand
	//   - a <kind> step exists but its tooling is
	//     missing on PATH                         -> actionable error
	kindSeen := false
	for _, c := range candidates {
		if kindOf(c) == kind {
			kindSeen = true
			break
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w in %s", ErrNoProjectType, root)
	}
	if !kindSeen {
		return nil, fmt.Errorf("%w (%s) in %s", ErrNoKindCommand, kind, root)
	}
	return nil, fmt.Errorf("required tooling not found in PATH for %s", root)
}

// kindOf classifies a candidate by its name label: test-suite runners are
// "test", static analyzers (vet/lint) are "lint", everything else compiles
// or checks syntax and is "build".
func kindOf(c *Command) string {
	n := strings.ToLower(c.Name)
	switch {
	case strings.Contains(n, "test"), strings.Contains(n, "pytest"), strings.Contains(n, "rake"):
		return "test"
	case strings.Contains(n, "vet"), strings.Contains(n, "lint"):
		return "lint"
	default:
		return "build"
	}
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
		)
		// The build step is the package.json "build" script. Without one
		// there is nothing to build — the old `node --check index.js`
		// fallback was a one-file syntax check that vacuously passed
		// ("build: OK") for projects with no build step (6.1).
		if pkgHasScript(root, "build") {
			out = append(out, &Command{Name: "npm run build", Cmd: "npm", Args: []string{"run", "build", "--silent"}})
		}
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
		)
		// A "build" target is optional: `make build` on a Makefile without
		// one fails ("No rule to make target") — a false FAIL for an absent
		// step (6.1).
		if makefileHasTarget(root, "build") {
			out = append(out, &Command{Name: "make build", Cmd: "make", Args: []string{"build"}})
		}
	case has("Chart.yaml") || glob("*/Chart.yaml"):
		// Helm chart repo: lint each chart directory containing Chart.yaml.
		chartDirs := helmChartDirs(root)
		for _, d := range chartDirs {
			out = append(out, &Command{Name: "helm lint " + d, Cmd: "helm", Args: []string{"lint", d}})
		}
	case glob("*.go"):
		// No go.mod: `go vet ./...` is invalid outside a module ("pattern
		// ./...: directory prefix . does not contain main module"), so the
		// no-module vet form targets the explicit root-level files instead —
		// `go vet <file>...` runs without a module (F1).
		vetArgs := []string{"vet"}
		if fs, _ := filepath.Glob(filepath.Join(root, "*.go")); len(fs) > 0 {
			for _, f := range fs {
				vetArgs = append(vetArgs, filepath.Base(f))
			}
		} else {
			vetArgs = append(vetArgs, "./...")
		}
		out = append(out, &Command{Name: "go vet (no module)", Cmd: "go", Args: vetArgs})
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

// DeclaresSteps reports whether root's own manifest declares discrete steps
// (package.json scripts or Makefile targets). 6.1: when such a manifest
// exists but declares no build step, verification reports an honest skip
// instead of degrading to an unrelated check; for projects WITHOUT a step
// manifest (e.g. stray *.go files) the Detect fallback stays the D1
// degradation (go vet on the files present).
func DeclaresSteps(root string) bool {
	for _, n := range []string{"package.json", "Makefile", "makefile"} {
		if _, err := os.Stat(filepath.Join(root, n)); err == nil {
			return true
		}
	}
	return false
}

// pkgHasScript reports whether the package.json at root declares the named
// script. Deterministic; a missing or unparseable package.json reports false
// — npm run <name> would fail non-zero, and detection prefers the honest
// skip to a false FAIL (6.1).
func pkgHasScript(root, name string) bool {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	return pkg.Scripts[name] != ""
}

// makefileHasTarget reports whether the Makefile (or makefile) at root
// defines the named target: a non-comment, non-recipe line whose target list
// (before the first ':') contains the name. Deterministic.
func makefileHasTarget(root, name string) bool {
	var data []byte
	var err error
	for _, mf := range []string{"Makefile", "makefile"} {
		if data, err = os.ReadFile(filepath.Join(root, mf)); err == nil {
			break
		}
	}
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		// Skip blank lines, comments, recipes (tab-indented), directive
		// lines (.PHONY, include, ...) and variable assignments.
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ".") || strings.HasPrefix(line, "\t") {
			continue
		}
		before, _, ok := strings.Cut(line, ":")
		if !ok || strings.ContainsAny(before, "=$") {
			continue
		}
		for _, t := range strings.Fields(before) {
			if t == name {
				return true
			}
		}
	}
	return false
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
	// Checks holds one outcome per per-extension check when the result was
	// produced by RunChecks; it is empty for single-command Run results.
	Checks []CheckResult
}

// FailedChecks returns the names of per-extension checks that ran and failed.
// A project whose validation output is all failure is what the heal loop
// repairs against.
func (r *Result) FailedChecks() []string {
	var out []string
	for _, c := range r.Checks {
		if !c.Skipped && !c.OK {
			out = append(out, c.Name)
		}
	}
	return out
}

// SkippedChecks returns the names of per-extension checks that could not run
// (missing toolchain or no syntax parser). Skipped is NOT a pass: "unable to
// validate" must never read as "OK".
func (r *Result) SkippedChecks() []string {
	var out []string
	for _, c := range r.Checks {
		if !c.Skipped {
			continue
		}
		if c.Reason != "" {
			out = append(out, c.Name+" ("+c.Reason+")")
		} else {
			out = append(out, c.Name)
		}
	}
	return out
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
