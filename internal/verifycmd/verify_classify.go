package verifycmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// toolchainMarkers maps a repo marker file to the toolchain it implies.
var toolchainMarkers = map[string]string{
	"go.mod":           "go",
	"pom.xml":          "maven",
	"mvnw":             "maven",
	"build.gradle":     "gradle",
	"build.gradle.kts": "gradle",
	"settings.gradle":  "gradle",
	"gradlew":          "gradle",
	"package.json":     "node",
	"Cargo.toml":       "rust",
	"pyproject.toml":   "python",
	"setup.py":         "python",
	"requirements.txt": "python",
	"pytest.ini":       "python",
	"Makefile":         "make",
	".kern":            "kern",
}

// detectToolchains reports the build toolchains a repo root uses.
func detectToolchains(root string) map[string]bool {
	found := map[string]bool{}
	for marker, tc := range toolchainMarkers {
		if _, err := os.Stat(filepath.Join(root, marker)); err == nil {
			found[tc] = true
		}
	}
	return found
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// subcommandRule describes which subcommands of a tool may run. An empty
// allow set means any subcommand is fine; deny always wins.
type subcommandRule struct {
	allow map[string]bool
	deny  []string // substrings refused anywhere in the arguments
	// denyFlags are option names (without dashes) that make the tool run a
	// caller-chosen program; matched per token in "-x", "--x" and "-x=v"
	// forms, so test names and quoted values never false-positive.
	denyFlags []string
}

// flagName returns the option name of a "-x", "--x" or "-x=value" token, or ""
// for a non-flag token.
func flagName(arg string) string {
	if !strings.HasPrefix(arg, "-") {
		return ""
	}
	name := strings.TrimLeft(arg, "-")
	if i := strings.IndexByte(name, '='); i >= 0 {
		name = name[:i]
	}
	return name
}

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[i] = true
	}
	return m
}

// toolRules are the commands that build, test or lint without printing
// source files. Commands that run arbitrary code (go run, npx, python -c) or
// print file contents (cat, git show) are deliberately absent.
var toolRules = map[string]map[string]subcommandRule{
	"go": {
		// -exec, -toolexec and -vettool make go run a caller-chosen program.
		"go":            {allow: set("build", "test", "vet", "fmt", "mod", "list", "version"), denyFlags: []string{"exec", "toolexec", "vettool"}},
		"gofmt":         {allow: set("-l", "-d")},
		"golangci-lint": {},
		"staticcheck":   {},
		"govulncheck":   {},
	},
	"maven": {
		"mvn":  {deny: []string{"exec:"}},
		"mvnw": {deny: []string{"exec:"}},
	},
	"gradle": {
		// An init script is arbitrary Groovy/Kotlin loaded from a caller-chosen path.
		"gradle":  {denyFlags: []string{"init-script", "I"}},
		"gradlew": {denyFlags: []string{"init-script", "I"}},
	},
	"node": {
		"npm":  {allow: set("test", "run", "ci", "install", "ls", "audit", "outdated")},
		"yarn": {allow: set("test", "run", "install", "build", "lint", "audit")},
		"pnpm": {allow: set("test", "run", "install", "build", "lint", "audit")},
	},
	"rust": {
		"cargo": {allow: set("build", "test", "check", "clippy", "fmt", "doc", "tree", "audit")},
	},
	"python": {
		"pytest": {},
		"ruff":   {},
		"mypy":   {},
		"flake8": {},
		"python": {allow: set("-m")},
		"python3": {
			allow: set("-m"),
		},
	},
	"make": {
		"make": {},
	},
	"kern": {
		"kern": {allow: set("version", "doctor")},
	},
}

// pythonModules are the only modules `python -m` may start.
var pythonModules = set("pytest", "unittest", "mypy", "ruff", "pip")

// gitReadOnly lists git subcommands that never print file contents.
var gitReadOnly = set("status", "log", "branch", "rev-parse", "ls-files", "describe", "diff")

// gitContentFlags make log/diff print patches or raw file content.
var gitContentFlags = []string{"-p", "--patch", "-u", "--raw", "--cc", "--color-words"}

// gitDiffSummaryFlags are the only forms of `git diff` allowed.
var gitDiffSummaryFlags = []string{"--stat", "--name-only", "--name-status", "--shortstat", "--numstat"}

// classifyCommand decides whether a single command line may run through
// kern_verify. The line may join simple commands with && but may not use
// pipes, redirection, substitution or multiple lines: kern shapes the output,
// so there is nothing to pipe into.
func classifyCommand(command string, toolchains map[string]bool) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("command is empty")
	}
	if strings.ContainsAny(command, "\n\r\x00") {
		return fmt.Errorf("command must be a single line")
	}
	segs, ok := splitCommand(strings.ReplaceAll(command, "2>&1", ""))
	if !ok {
		return fmt.Errorf("command may only join simple commands with && (no pipes, redirection, substitution or background jobs outside quotes): kern shapes the output for you")
	}
	for _, seg := range segs {
		if err := classifySegment(strings.Fields(seg), toolchains); err != nil {
			return err
		}
	}
	return nil
}

// splitCommand splits on top-level && and reports false when a shell operator
// (pipe, ;, redirection, background, backtick, $( or ${) appears outside
// single quotes, or a substitution appears inside double quotes, or a quote
// is left open. Operators inside quotes are plain text to sh, so a test
// filter such as -run 'A|B' is fine.
func splitCommand(command string) ([]string, bool) {
	var segs []string
	var cur strings.Builder
	var quote byte
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case quote == '"':
			switch {
			case c == '"':
				quote = 0
			case c == '\\' && i+1 < len(command):
				cur.WriteByte(c)
				i++
				c = command[i]
			case c == '`', c == '$' && i+1 < len(command) && (command[i+1] == '(' || command[i+1] == '{'):
				return nil, false
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '&' && i+1 < len(command) && command[i+1] == '&':
			segs = append(segs, cur.String())
			cur.Reset()
			i++
			continue
		case strings.IndexByte("|;&<>`", c) >= 0,
			c == '$' && i+1 < len(command) && (command[i+1] == '(' || command[i+1] == '{'):
			return nil, false
		}
		cur.WriteByte(c)
	}
	if quote != 0 {
		return nil, false
	}
	return append(segs, cur.String()), true
}

func classifySegment(tokens []string, toolchains map[string]bool) error {
	if len(tokens) == 0 {
		return fmt.Errorf("empty command between &&")
	}
	name := strings.TrimPrefix(filepath.Base(tokens[0]), "./")
	args := tokens[1:]

	if name == "git" {
		return classifyGit(args)
	}
	for tc := range toolchains {
		rule, ok := toolRules[tc][name]
		if !ok {
			continue
		}
		return checkRule(name, rule, args)
	}
	return fmt.Errorf("%q is not an allowed build/test command for this repo (detected: %s); allowed git is read-only status/log/diff --stat. Use kern_explore to read code",
		name, detectedList(toolchains))
}

func detectedList(toolchains map[string]bool) string {
	if len(toolchains) == 0 {
		return "no known toolchain"
	}
	return strings.Join(sortedKeys(toolchains), ", ")
}

func checkRule(name string, rule subcommandRule, args []string) error {
	joined := strings.Join(args, " ")
	for _, d := range rule.deny {
		if strings.Contains(joined, d) {
			return fmt.Errorf("%s with %q is not allowed", name, d)
		}
	}
	for _, a := range args {
		fn := flagName(a)
		if fn == "" {
			continue
		}
		for _, d := range rule.denyFlags {
			if fn == d {
				return fmt.Errorf("%s with -%s is not allowed: it runs a caller-chosen program", name, d)
			}
		}
	}
	if len(rule.allow) == 0 {
		return nil
	}
	first := firstNonFlag(args)
	if name == "python" || name == "python3" {
		if len(args) < 2 || args[0] != "-m" || !pythonModules[args[1]] {
			return fmt.Errorf("%s is only allowed as `%s -m <%s>`", name, name, strings.Join(sortedKeys(pythonModules), "|"))
		}
		return nil
	}
	if name == "gofmt" {
		for _, a := range args {
			if a != "-l" && a != "-d" && !strings.HasPrefix(a, "./") && !strings.HasPrefix(a, ".") && !strings.HasSuffix(a, ".go") {
				return fmt.Errorf("gofmt is only allowed with -l or -d")
			}
		}
		return nil
	}
	if !rule.allow[first] {
		return fmt.Errorf("%s %s is not allowed (allowed: %s)", name, first, strings.Join(sortedKeys(rule.allow), ", "))
	}
	return nil
}

func firstNonFlag(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

func classifyGit(args []string) error {
	sub := firstNonFlag(args)
	if !gitReadOnly[sub] {
		return fmt.Errorf("git %s is not allowed through kern_verify (read-only status/log/branch/rev-parse/ls-files/describe and diff --stat only)", sub)
	}
	for _, a := range args {
		for _, f := range gitContentFlags {
			if a == f {
				return fmt.Errorf("git %s %s prints file content; use kern_explore instead", sub, f)
			}
		}
	}
	if sub == "diff" {
		for _, a := range args {
			for _, f := range gitDiffSummaryFlags {
				if a == f {
					return nil
				}
			}
		}
		return fmt.Errorf("git diff is only allowed with %s (full diffs print file content; use kern_explore)", strings.Join(gitDiffSummaryFlags, "|"))
	}
	return nil
}
