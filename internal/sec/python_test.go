package sec

import (
	"reflect"
	"testing"
)

func TestScanPythonFileAllRules(t *testing.T) {
	src := []byte(`import os
import subprocess
import pickle
import yaml

def run(user_cmd):
    eval(user_cmd)
    exec(user_cmd)
    os.system(user_cmd)
    os.popen(user_cmd)
    subprocess.run(user_cmd, shell=True)
    subprocess.call(user_cmd)
    pickle.loads(data)
    yaml.load(data)
    cursor.execute(f"SELECT * FROM users WHERE name = {user_cmd}")
`)
	findings := ScanPythonFile("app.py", src)

	want := map[string][]int{
		"py-eval":             {7},
		"py-exec":             {8},
		"py-os-system":        {9, 10},
		"py-subprocess-shell": {11},
		"py-subprocess":       {12},
		"py-pickle-load":      {13},
		"py-yaml-load":        {14},
		"py-sql-format":       {15},
	}
	got := map[string][]int{}
	for _, f := range findings {
		if f.File != "app.py" {
			t.Errorf("finding file = %q, want app.py", f.File)
		}
		got[f.Rule] = append(got[f.Rule], f.Line)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScanPythonFile findings = %v, want %v", got, want)
	}

	// Every finding carries the finding type fields (severity + reason).
	for _, f := range findings {
		if f.Severity == "" || f.Message == "" {
			t.Errorf("finding %s:%d missing severity or message: %+v", f.Rule, f.Line, f)
		}
	}
}

func TestScanPythonFileSeverityMapping(t *testing.T) {
	// py-subprocess (Medium) is a warning; the High/Critical rules are errors.
	src := []byte("subprocess.run(cmd)\neval(x)\ncursor.execute(f\"SELECT {x}\")\n")
	findings := ScanPythonFile("app.py", src)
	byRule := map[string]string{}
	for _, f := range findings {
		byRule[f.Rule] = f.Severity
	}
	if byRule["py-subprocess"] != "warning" {
		t.Errorf("py-subprocess severity = %q, want warning", byRule["py-subprocess"])
	}
	for _, r := range []string{"py-eval", "py-sql-format"} {
		if byRule[r] != "error" {
			t.Errorf("%s severity = %q, want error", r, byRule[r])
		}
	}
}

func TestScanPythonFileNegatives(t *testing.T) {
	src := []byte(`import yaml
import subprocess

data = yaml.load(payload, Loader=yaml.SafeLoader)
subprocess.run(cmd)
# eval(suspicious)
# os.system("whoami")
`)
	findings := ScanPythonFile("app.py", src)
	for _, f := range findings {
		switch f.Rule {
		case "py-yaml-load":
			t.Errorf("yaml.load with Loader= must not fire, got %+v", f)
		case "py-subprocess-shell":
			t.Errorf("subprocess without shell=True must not fire shell variant, got %+v", f)
		case "py-eval", "py-os-system":
			t.Errorf("commented-out sink must not fire, got %+v", f)
		}
	}
	// The plain subprocess call (no shell=True) is a Medium py-subprocess finding.
	var found bool
	for _, f := range findings {
		if f.Rule == "py-subprocess" && f.Line == 5 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected py-subprocess on line 5, got %+v", findings)
	}
}

func TestScanPythonFileCommentLineSkipped(t *testing.T) {
	// A line whose trimmed form starts with '#' is skipped entirely.
	src := []byte("# eval(x)\n#   pickle.loads(data)\n  # subprocess.run(cmd, shell=True)\n")
	if findings := ScanPythonFile("app.py", src); len(findings) != 0 {
		t.Errorf("comment-only source produced findings: %+v", findings)
	}
}

func TestFilterByFiles(t *testing.T) {
	base := []Finding{
		{File: "a.go", Line: 1, Rule: "sql-injection"},
		{File: "b.py", Line: 2, Rule: "py-eval"},
		{File: "c.go", Line: 3, Rule: "command-injection"},
	}

	// Matched: only findings in the file set survive.
	got := FilterByFiles(base, []string{"b.py"})
	if len(got) != 1 || got[0].File != "b.py" || got[0].Rule != "py-eval" {
		t.Errorf("FilterByFiles matched = %+v, want only b.py", got)
	}

	// Unmatched: a fileset with no matching findings yields empty.
	if got := FilterByFiles(base, []string{"nope.go"}); len(got) != 0 {
		t.Errorf("FilterByFiles unmatched = %+v, want empty", got)
	}

	// Empty fileset matches nothing (nil means "disabled" at the call sites).
	if got := FilterByFiles(base, nil); got != nil {
		t.Errorf("FilterByFiles nil set = %+v, want nil", got)
	}
	if got := FilterByFiles(base, []string{}); len(got) != 0 {
		t.Errorf("FilterByFiles empty set = %+v, want empty", got)
	}

	// Original findings are not mutated.
	if len(base) != 3 {
		t.Errorf("FilterByFiles mutated input: %+v", base)
	}
}
