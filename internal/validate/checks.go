package validate

// Per-extension validation checks.
//
// The original validator auto-detected ONE command for a whole project
// (Detect/detectCandidates): a polyglot repo whose first PATH-available
// command covered only one language let broken files in every other language
// pass as "validated OK". Checks/RunChecks replace that single-language
// auto-detect with a deterministic, per-extension baseline:
//
//   - .go is always syntax-checked in-process with the same go/parser AST
//     parse the index uses — no toolchain required — plus `go vet` (module
//     repos) or per-file `gofmt -e` when the Go toolchain is on PATH.
//   - .py keeps the existing compileall path.
//   - every other code language is checked per-file with a cheap syntax-only
//     toolchain command when one exists; otherwise the group is reported as
//     "unable to validate" (Skipped) — never as OK.

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Check is one per-extension validation check covering a language group of a
// project. A check either runs a toolchain command (Cmd != "") or runs
// in-process (Cmd == "", e.g. the deterministic Go syntax parse). Checks whose
// toolchain or parser is unavailable are marked Skipped at build time — the
// caller must surface "unable to validate" rather than treating them as OK.
type Check struct {
	Name string
	Cmd  string
	Args []string
	Kind string // "syntax" | "build" | "test" | "lint"
	Lang string
	// Files are the root-relative source files this check covers ("" =
	// root-wide, e.g. `go vet ./...` or compileall on the root).
	Files []string
	// Skipped marks a check that cannot run (toolchain/parser missing).
	Skipped bool
	Reason  string
}

// CheckResult is the outcome of one check within a RunChecks aggregation.
type CheckResult struct {
	Name     string
	OK       bool
	Skipped  bool // could not run (toolchain/parser missing): NOT validated
	Reason   string
	ExitCode int
	Output   string
	Dur      time.Duration
}

// extLang maps source extensions to the language groups validation covers.
// Markup/data formats (css, html, markdown, json, yaml) are deliberately
// excluded: they have no meaningful syntax checker, and silently skipping
// them would reintroduce the "validated OK without checking anything" bug.
var extLang = map[string]string{
	".go": "go", ".py": "python",
	".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "javascript",
	".ts": "typescript", ".tsx": "typescript",
	".rs": "rust", ".c": "c", ".h": "c", ".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".hpp": "cpp", ".hxx": "cpp",
	".cs": "csharp", ".java": "java", ".rb": "ruby", ".php": "php",
	".sh": "shell", ".bash": "shell", ".dart": "dart",
}

// langSyntaxCmd maps language groups to a cheap per-file syntax-only toolchain
// command (flags appended with the file). Languages without an entry have no
// syntax-only checker available and validate as "unable to validate" unless a
// full build command exists for the project.
var langSyntaxCmd = map[string][]string{
	"javascript": {"node", "--check"},
	"typescript": {"node", "--check"},
	"ruby":       {"ruby", "-c"},
	"php":        {"php", "-l"},
	"shell":      {"bash", "-n"},
}

// Checks returns the per-extension validation checks covering the source
// files under root. When file is non-empty the checks are constrained to that
// single root-relative file (heal --file). A language group whose files
// cannot be validated — no toolchain command and no in-process parser — is
// still returned as a check marked Skipped so the aggregate verdict can
// report "unable to validate" instead of "OK". Result order is deterministic
// (language groups sorted).
func Checks(root, file string) []*Check {
	groups := map[string][]string{}
	if file != "" {
		if l := langFor(file); l != "" {
			groups[l] = []string{file}
		}
	} else {
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
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if l := langFor(rel); l != "" {
				groups[l] = append(groups[l], rel)
			}
			return nil
		})
	}
	langs := make([]string, 0, len(groups))
	for l := range groups {
		langs = append(langs, l)
	}
	slices.Sort(langs)
	var out []*Check
	for _, l := range langs {
		out = append(out, checksForLang(root, l, groups[l], file)...)
	}
	return out
}

// langFor returns the validation language group for a root-relative path, or
// "" for unsupported files.
func langFor(rel string) string {
	return extLang[strings.ToLower(filepath.Ext(rel))]
}

// checksForLang builds the checks for one language group.
func checksForLang(root, lang string, files []string, file string) []*Check {
	switch lang {
	case "go":
		// Deterministic baseline: the same go/parser AST parse the index
		// uses. Always runs, no toolchain needed.
		out := []*Check{{Name: "go syntax", Kind: "syntax", Lang: "go", Files: files}}
		if _, err := exec.LookPath("go"); err != nil {
			return out
		}
		switch {
		case file != "":
			out = append(out, &Check{Name: "gofmt", Cmd: "gofmt", Args: []string{"-e", file}, Kind: "syntax", Lang: "go"})
		case hasFile(root, "go.mod"):
			// go build, not go vet: newer go vet prefixes errors with "vet: "
			// (e.g. "vet: ./hub.go:3:25: undefined: x"), which heal's
			// failingFiles cannot parse into repair candidates. The compiler
			// prints the stable "./hub.go:3:25: ..." format.
			out = append(out, &Check{Name: "go build", Cmd: "go", Args: []string{"build", "./..."}, Kind: "build", Lang: "go"})
		case len(files) <= 8:
			// No module: `go vet ./...` cannot run; gofmt is the syntax extra.
			// Per-file (gofmt rejects mixed directories), bounded to small
			// script-style repos; larger no-module repos rely on the
			// deterministic syntax baseline above.
			for _, f := range files {
				out = append(out, &Check{Name: "gofmt", Cmd: "gofmt", Args: []string{"-e", f}, Kind: "syntax", Lang: "go"})
			}
		}
		return out
	case "python":
		py := "python"
		if _, err := exec.LookPath(py); err != nil {
			py = "python3"
		}
		if _, err := exec.LookPath(py); err != nil {
			return []*Check{{Name: "python py_compile", Kind: "build", Lang: "python", Skipped: true, Reason: "python not on PATH"}}
		}
		args := []string{"-m", "compileall", "-q"}
		if file != "" {
			args = append(args, file)
		} else {
			args = append(args, root)
		}
		return []*Check{{Name: "python py_compile", Cmd: py, Args: args, Kind: "build", Lang: "python"}}
	default:
		cmd, ok := langSyntaxCmd[lang]
		if !ok {
			return []*Check{{Name: lang + " syntax", Kind: "syntax", Lang: lang, Skipped: true,
				Reason: fmt.Sprintf("no syntax-only checker available for %s", lang)}}
		}
		if _, err := exec.LookPath(cmd[0]); err != nil {
			return []*Check{{Name: lang + " syntax", Kind: "syntax", Lang: lang, Skipped: true,
				Reason: fmt.Sprintf("%s not on PATH", cmd[0])}}
		}
		if file != "" {
			return []*Check{{Name: lang + " syntax", Cmd: cmd[0], Args: append(slices.Clone(cmd[1:]), file), Kind: "syntax", Lang: lang}}
		}
		if len(files) > 32 {
			return []*Check{{Name: lang + " syntax", Kind: "syntax", Lang: lang, Skipped: true,
				Reason: fmt.Sprintf("too many %s files for a per-file syntax check", lang)}}
		}
		var out []*Check
		for _, f := range files {
			out = append(out, &Check{Name: lang + " syntax", Cmd: cmd[0], Args: append(slices.Clone(cmd[1:]), f), Kind: "syntax", Lang: lang})
		}
		return out
	}
}

// hasFile reports whether name exists under root.
func hasFile(root, name string) bool {
	_, err := os.Stat(filepath.Join(root, name))
	return err == nil
}

// RunChecks runs every per-extension check for root (optionally scoped to a
// single root-relative file) and aggregates the results. OK is true only when
// every check ran and passed; a check that could not run (missing toolchain
// or parser) is recorded as Skipped and makes OK false — "unable to validate"
// must never read as "OK". FailedChecks and SkippedChecks on the result let
// callers distinguish a real failure from an unvalidatable project.
func RunChecks(ctx context.Context, root, file string, timeout time.Duration) *Result {
	start := time.Now()
	if file != "" {
		if _, err := os.Stat(filepath.Join(root, file)); err != nil {
			return &Result{Err: fmt.Errorf("file not found: %s", file), Dur: time.Since(start)}
		}
	}
	checks := Checks(root, file)
	if len(checks) == 0 {
		if file != "" {
			return &Result{Err: fmt.Errorf("no supported source type for file %s", file), Dur: time.Since(start)}
		}
		return &Result{Err: fmt.Errorf("no supported source files detected in %s", root), Dur: time.Since(start)}
	}
	res := &Result{Command: &Command{Name: checks[0].Name, Cmd: checks[0].Cmd, Args: checks[0].Args, Kind: checks[0].Kind}}
	for _, chk := range checks {
		cr := runCheck(ctx, root, chk, timeout)
		if res.Output != "" {
			res.Output += "\n"
		}
		res.Output += "== " + cr.Name + " ==\n" + cr.Output
		res.Checks = append(res.Checks, cr)
		res.Dur += cr.Dur
	}
	anyRan, allOK := false, true
	for _, cr := range res.Checks {
		if cr.Skipped {
			allOK = false
			continue
		}
		anyRan = true
		if !cr.OK {
			allOK = false
		}
	}
	res.OK = anyRan && allOK
	if !res.OK {
		res.ExitCode = 1
	}
	res.Dur += time.Since(start)
	return res
}

// runCheck executes one check. In-process checks (Cmd == "") run the
// deterministic syntax parsers; toolchain checks shell out via Run.
func runCheck(ctx context.Context, root string, chk *Check, timeout time.Duration) CheckResult {
	cr := CheckResult{Name: chk.Name}
	if chk.Skipped {
		cr.Skipped = true
		cr.Reason = chk.Reason
		return cr
	}
	if chk.Lang == "go" && chk.Cmd == "" {
		return runGoSyntax(root, chk)
	}
	res := Run(ctx, root, &Command{Name: chk.Name, Cmd: chk.Cmd, Args: chk.Args, Kind: chk.Kind}, timeout)
	cr.OK, cr.ExitCode, cr.Dur = res.OK, res.ExitCode, res.Dur
	cr.Output = res.Output
	if res.Err != nil {
		cr.Reason = res.Err.Error()
	}
	if chk.Lang == "python" {
		cr.Output = normalizePythonOutput(cr.Output)
	}
	return cr
}

// runGoSyntax runs the deterministic in-process syntax check for every Go
// file in the check: the same go/parser AST parse the index uses, with no
// toolchain required. Errors are emitted as path:line:col: message lines so
// heal's failingFiles can extract repair candidates.
func runGoSyntax(root string, chk *Check) CheckResult {
	cr := CheckResult{Name: chk.Name}
	fset := token.NewFileSet()
	var msgs []string
	for _, rel := range chk.Files {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		if _, perr := parser.ParseFile(fset, rel, b, 0); perr != nil {
			msgs = append(msgs, perr.Error())
		}
	}
	if len(msgs) > 0 {
		cr.Output = strings.Join(msgs, "\n") + "\n"
		cr.ExitCode = 1
		return cr
	}
	cr.OK = true
	return cr
}

// pyErrLineRe matches compileall/py_compile error shapes:
//
//	File "./broken.py", line 3                (py_compile)
//	*** Error compiling './broken.py'...      (compileall -q)
var pyErrLineRe = regexp.MustCompile(`(?m)(?:^File "([^"]+)", line (\d+)|Error compiling '([^']+)')`)

// normalizePythonOutput rewrites compileall's multi-line traceback-style
// errors into heal's path:line:col format so failingFiles can extract the
// broken file as a repair candidate. The original output is kept; the
// normalized lines are appended.
func normalizePythonOutput(out string) string {
	var b strings.Builder
	b.WriteString(out)
	if !strings.HasSuffix(out, "\n") {
		b.WriteString("\n")
	}
	seen := map[string]bool{}
	for _, m := range pyErrLineRe.FindAllStringSubmatch(out, -1) {
		path, line := m[1], m[2]
		if path == "" {
			path = m[3]
			line = "1"
		}
		path = strings.TrimPrefix(path, "./")
		if seen[path] {
			continue
		}
		seen[path] = true
		fmt.Fprintf(&b, "%s:%s:1: python syntax error\n", path, line)
	}
	return b.String()
}
