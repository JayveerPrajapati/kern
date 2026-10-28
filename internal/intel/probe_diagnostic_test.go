package intel

import (
	"path/filepath"
	"testing"
)

// TestProbeAnchorsDiagnosticLocation pins incident triage: a compiler-style
// file:line:col in the task must anchor the enclosing declaration instead of
// falling through to fuzzy keyword matching.
func TestProbeAnchorsDiagnosticLocation(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"app/app.go": "package app\n\nfunc Target() {\n\t_ = 1\n}\n\nfunc Other() {}\n",
	})
	ix := buildIndex(t, dir)

	for _, task := range []string{
		"app/app.go:4:2: undefined: missing",
		filepath.Join(dir, "app/app.go") + ":4:2: undefined: missing",
	} {
		report := Probe(ix, task, 4000)
		found := false
		for _, a := range report.Anchors {
			if a.Resolved == "Target" {
				found = true
				if a.Fuzzy {
					t.Errorf("%q: diagnostic anchor Target marked fuzzy", task)
				}
			}
		}
		if !found {
			t.Errorf("%q: expected Target anchor from the diagnostic line, got %+v", task, report.Anchors)
		}
	}
}
