package sec

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestTaintLitePythonFinding(t *testing.T) {
	root := t.TempDir()
	// The file contains a source expression (req.Body) and a Python sink
	// (os.system); the finding must pass through and be marked tainted by the
	// source-file heuristic — no Go call-graph symbols are required (G-4).
	writeTaintFile(t, root, "app.py", "import os\n\ndef run(cmd):\n    cmd = req.Body[\"cmd\"]\n    os.system(cmd)\n")
	ix := &index.Index{Root: root, Symbols: nil, Callers: map[string][]string{}}
	findings := []Finding{{
		File:     "app.py",
		Line:     5,
		Rule:     "py-os-system",
		Severity: "error",
		Message:  "shell command execution via os.system()/os.popen()",
	}}
	got := TaintLite(ix, findings)
	if len(got) != 1 {
		t.Fatalf("TaintLite returned %d findings, want 1", len(got))
	}
	tf := got[0]
	if !tf.Tainted {
		t.Error("expected tainted via source-file heuristic")
	}
	if tf.Func != "os.system" {
		t.Errorf("Func = %q, want os.system", tf.Func)
	}
	// The original finding passes through untouched.
	if tf.File != "app.py" || tf.Line != 5 || tf.Rule != "py-os-system" {
		t.Errorf("finding not preserved: %+v", tf)
	}
}

func TestTaintLitePythonFindingNotTainted(t *testing.T) {
	root := t.TempDir()
	// No source expression anywhere in the file -> not tainted, no panic.
	writeTaintFile(t, root, "app.py", "import os\n\ndef run(cmd):\n    os.system(cmd)\n")
	ix := &index.Index{Root: root, Symbols: nil, Callers: map[string][]string{}}
	findings := []Finding{{
		File: "app.py", Line: 4, Rule: "py-os-system", Severity: "error",
		Message: "shell command execution via os.system()/os.popen()",
	}}
	got := TaintLite(ix, findings)
	if len(got) != 1 {
		t.Fatalf("TaintLite returned %d findings, want 1", len(got))
	}
	if got[0].Tainted {
		t.Error("expected not tainted without a source expression")
	}
	if got[0].Func != "os.system" {
		t.Errorf("Func = %q, want os.system", got[0].Func)
	}
}

func TestTaintLitePythonFindingNilIndex(t *testing.T) {
	// A nil index (e.g. index build failure) must not panic; the finding
	// passes through with the callee symbol but stays untainted.
	findings := []Finding{{
		File: "app.py", Line: 3, Rule: "py-eval", Severity: "error",
		Message: "dynamic code evaluation via eval()",
	}}
	got := TaintLite(nil, findings)
	if len(got) != 1 {
		t.Fatalf("TaintLite returned %d findings, want 1", len(got))
	}
	if got[0].Tainted {
		t.Error("expected not tainted with a nil index")
	}
	if got[0].Func != "eval" {
		t.Errorf("Func = %q, want eval", got[0].Func)
	}
	if got[0].File != "app.py" || got[0].Line != 3 || got[0].Rule != "py-eval" {
		t.Errorf("finding not preserved: %+v", got[0])
	}
}

func TestTaintLitePythonFindingSinkSymbols(t *testing.T) {
	// Every Python rule maps to the matched callee as the sink symbol.
	cases := map[string]string{
		"py-eval":             "eval",
		"py-exec":             "exec",
		"py-os-system":        "os.system",
		"py-subprocess-shell": "subprocess",
		"py-subprocess":       "subprocess",
		"py-pickle-load":      "pickle.loads",
		"py-yaml-load":        "yaml.load",
		"py-sql-format":       "execute",
	}
	for rule, want := range cases {
		got := pythonSinkSymbol(Finding{Rule: rule})
		if got != want {
			t.Errorf("pythonSinkSymbol(%s) = %q, want %q", rule, got, want)
		}
	}
	if got := pythonSinkSymbol(Finding{Rule: "unknown"}); got != "<python>" {
		t.Errorf("pythonSinkSymbol(unknown) = %q, want <python>", got)
	}
}
