package sec

import (
	"regexp"
	"strings"
)

// Python sink rules (G-4). Detection is line-oriented: each rule's trigger
// regex is matched against a single source line, and the rule's condition may
// require (or forbid) additional tokens on the same line — e.g. subprocess's
// shell=True flag, or yaml.load's explicit Loader=. Comment lines (trimmed
// lines starting with '#') are skipped entirely, so documented examples are
// not reported as findings.
var (
	rePyEval     = regexp.MustCompile(`\beval\s*\(`)
	rePyExec     = regexp.MustCompile(`\bexec\s*\(`)
	rePyOsSystem = regexp.MustCompile(`\bos\.(?:system|popen)\s*\(`)
	rePySubproc  = regexp.MustCompile(`\bsubprocess\.(?:run|call|Popen|check_output|check_call)\s*\(`)
	rePyPickle   = regexp.MustCompile(`\bpickle\.loads\s*\(`)
	rePyYamlLoad = regexp.MustCompile(`\byaml\.load\s*\(`)
	rePySQLExec  = regexp.MustCompile(`\b(?:execute|executemany)\s*\(`)
	// rePyPercentF recognizes classic %-formatting placeholders (%s, %d, %(name)s…)
	// while skipping plain modulo on integers.
	rePyPercentF = regexp.MustCompile(`%[\(sdrfoxegc]`)
)

// ScanPythonFile detects Python sinks in one source file, line by line. rel
// is a root-relative path used only for reporting; findings reference
// rel:line (1-based). At most one finding is emitted per rule per line; the
// subprocess rule upgrades to the High shell=True variant when shell=True is
// on the same line, else emits the Medium py-subprocess variant.
func ScanPythonFile(rel string, src []byte) []Finding {
	var findings []Finding
	for i, raw := range strings.Split(string(src), "\n") {
		lineNo := i + 1
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		findings = append(findings, scanPyLine(rel, lineNo, raw)...)
	}
	return findings
}

// scanPyLine applies every Python rule to a single non-comment source line.
func scanPyLine(rel string, lineNo int, line string) []Finding {
	var out []Finding
	add := func(rule string, sev Severity, msg string) {
		out = append(out, Finding{
			File:     rel,
			Line:     lineNo,
			Rule:     rule,
			Severity: string(sev),
			Message:  msg,
			Snippet:  pySnippet(line),
		})
	}
	if rePySubproc.MatchString(line) {
		if strings.Contains(line, "shell=True") {
			add("py-subprocess-shell", SeverityError, "subprocess launched with shell=True (shell injection risk)")
		} else {
			add("py-subprocess", SeverityWarning, "subprocess execution without an explicit shell=False guard")
		}
	}
	if rePyYamlLoad.MatchString(line) && !strings.Contains(line, "Loader=") {
		add("py-yaml-load", SeverityError, "yaml.load without an explicit Loader= (unsafe by default)")
	}
	if rePySQLExec.MatchString(line) && (strings.Contains(line, `f"`) || strings.Contains(line, `f'`) || rePyPercentF.MatchString(line) || strings.Contains(line, ".format(")) {
		add("py-sql-format", SeverityError, "SQL built with f-string, %-formatting or .format() from a query call")
	}
	if rePyEval.MatchString(line) {
		add("py-eval", SeverityError, "dynamic code evaluation via eval()")
	}
	if rePyExec.MatchString(line) {
		add("py-exec", SeverityError, "dynamic code execution via exec()")
	}
	if rePyOsSystem.MatchString(line) {
		add("py-os-system", SeverityError, "shell command execution via os.system()/os.popen()")
	}
	if rePyPickle.MatchString(line) {
		add("py-pickle-load", SeverityError, "unsafe deserialization via pickle.loads()")
	}
	return out
}

// pySnippet returns the trimmed source line, capped at the same 120-char
// limit the Go scanner uses for snippets.
func pySnippet(line string) string {
	s := strings.TrimSpace(line)
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return s
}
