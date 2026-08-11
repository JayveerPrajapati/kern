package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func build(t *testing.T, files map[string]string) (*index.Index, string) {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		dir := filepath.Dir(filepath.Join(root, name))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	return ix, root
}

func TestVerifyConfirmsRealReferences(t *testing.T) {
	ix, root := build(t, map[string]string{
		"app.py": "def greet(name):\n    return 'hi ' + name\n\n@app.route('/users')\ndef list_users():\n    return []\n",
	})
	text := "the handler is list_users in app.py:6 and it serves /users"
	rep := Sorted(Verify(ix, root, text))
	if !rep.OK {
		t.Fatalf("expected all confirmed, got %+v", rep.Missing)
	}
	if !anyFound(rep, FileRef, "app.py:6") {
		t.Errorf("file:line check missing: %+v", rep.Checks)
	}
	if !anyFound(rep, Route, "/users") {
		t.Errorf("route check missing: %+v", rep.Checks)
	}
	if !anyFound(rep, Sym, "list_users") {
		t.Errorf("symbol check missing: %+v", rep.Checks)
	}
}

func TestVerifyFlagsMissingReferences(t *testing.T) {
	ix, root := build(t, map[string]string{
		"app.py": "def real():\n    pass\n",
	})
	text := "see nonexistent_fn and the route /api/v1/nope also app.py:99"
	rep := Sorted(Verify(ix, root, text))
	if rep.OK {
		t.Fatal("expected some missing references")
	}
	if !anyFound(rep, FileRef, "app.py:99") {
		t.Errorf("expected app.py:99 flagged missing: %+v", rep.Checks)
	}
	if !anyFound(rep, Route, "/api/v1/nope") {
		t.Errorf("expected route flagged missing: %+v", rep.Checks)
	}
}

func TestVerifyNilIndexFallsBackToSource(t *testing.T) {
	root := t.TempDir()
	content := "line one\nline two\n"
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	rep := Verify(nil, root, "see notes.md:2 for details")
	rep = Sorted(rep)
	if !anyFound(rep, FileRef, "notes.md:2") {
		t.Errorf("file:line should be confirmed from raw source: %+v", rep.Checks)
	}
	repBad := Verify(nil, root, "see notes.md:99 for details")
	if c := findCheck(repBad, FileRef, "notes.md:99"); c == nil || c.Found {
		t.Errorf("out-of-range line should be flagged missing: %+v", c)
	}
}

func TestRenderContainsVerdicts(t *testing.T) {
	rep := Report{
		Checks:  []Check{{Type: FileRef, Ref: "a.go:1", Found: true, Detail: "ok"}, {Type: Route, Ref: "/nope", Found: false, Detail: "missing"}},
		Missing: []string{"/nope"},
		OK:      false,
	}
	out := Render(rep)
	if !strings.Contains(out, "a.go:1") || !strings.Contains(out, "MISS") {
		t.Errorf("render missing verdicts: %q", out)
	}
}

func anyFound(rep Report, t Type, ref string) bool {
	for _, c := range rep.Checks {
		if c.Type == t && c.Ref == ref {
			return true
		}
	}
	return false
}

func findCheck(rep Report, t Type, ref string) *Check {
	for i := range rep.Checks {
		if rep.Checks[i].Type == t && rep.Checks[i].Ref == ref {
			return &rep.Checks[i]
		}
	}
	return nil
}

// TestVerifyIgnoresNonRoutePaths verifies absolute filesystem paths and
// date-like strings are not flagged as unregistered routes (W2-30).
func TestVerifyIgnoresNonRoutePaths(t *testing.T) {
	ix, root := build(t, map[string]string{
		"app.py": "@app.route('/users')\ndef list_users():\n    pass\n",
	})
	text := "path /usr/local/bin/foo and date /2024/01/01 and route /users"
	rep := Sorted(Verify(ix, root, text))
	if !rep.OK {
		t.Fatalf("expected no missing refs, got %+v", rep.Missing)
	}
	if !anyFound(rep, Route, "/users") {
		t.Errorf("registered route missing: %+v", rep.Checks)
	}
	for _, c := range rep.Checks {
		if c.Type == Route && c.Ref != "/users" {
			t.Errorf("non-route %q must not be reported: %+v", c.Ref, rep.Checks)
		}
	}
}

// findFileCheck returns the FileRef check whose Ref contains the given file
// name, if any.
func findFileCheck(rep Report, name string) *Check {
	for i := range rep.Checks {
		if rep.Checks[i].Type == FileRef && strings.Contains(rep.Checks[i].Ref, name) {
			return &rep.Checks[i]
		}
	}
	return nil
}

// TestVerifyAbsFileRefConfinedToRoot verifies a file:line ref that resolves to
// an absolute path outside root is never read (no filesystem oracle): it is
// reported Found=false (unverifiable) rather than probing the real file
// (which would flip Found=true for a valid line) (W2-32).
func TestVerifyAbsFileRefConfinedToRoot(t *testing.T) {
	ix, root := build(t, map[string]string{"a.go": "package a\n"})
	outside := filepath.Join(t.TempDir(), "secret.go")
	_ = os.WriteFile(outside, []byte("line one\nline two\n"), 0o644)
	rep := Sorted(Verify(ix, root, outside+":1"))
	c := findFileCheck(rep, "secret.go")
	if c == nil {
		t.Fatal("outside file ref not reported at all")
	}
	if c.Found {
		t.Fatalf("outside abs ref must be unverifiable (never read), got Found=true: %+v", c)
	}
}
