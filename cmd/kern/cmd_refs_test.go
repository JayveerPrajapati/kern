package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func refsModeFixture(t *testing.T) string {
	t.Helper()
	t.Setenv("KERN_DAEMON", "0")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module refsfixture\n\ngo 1.20\n",
		"main.go": `package main

func Target() int { return 1 }

func Caller() int { return Target() }

func main() { _ = Caller() }
`,
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRefsConciseModeSuppressesEvidence(t *testing.T) {
	root := refsModeFixture(t)
	concise := captureStdout(t, func() { runRefs([]string{"Target", "--root", root, "--mode", "concise"}) })
	for _, want := range []string{"# func Target", "references: 1 in 1 file(s) (ast)"} {
		if !strings.Contains(concise, want) {
			t.Fatalf("concise refs missing %q:\n%s", want, concise)
		}
	}
	if strings.Contains(concise, "main.go:5:") {
		t.Fatalf("concise refs leaked the evidence row:\n%s", concise)
	}
}

func TestRefsEvidenceModePreservesReferenceList(t *testing.T) {
	root := refsModeFixture(t)
	evidence := captureStdout(t, func() { runRefs([]string{"Target", "--root", root, "--mode", "evidence"}) })
	if !strings.Contains(evidence, "main.go:5:28  call") {
		t.Fatalf("evidence refs missing the AST reference row:\n%s", evidence)
	}
}

func TestRefsConciseModeDoesNotChangeJSON(t *testing.T) {
	root := refsModeFixture(t)
	defaultJSON := captureStdout(t, func() { runRefs([]string{"Target", "--root", root, "--json"}) })
	conciseJSON := captureStdout(t, func() { runRefs([]string{"Target", "--root", root, "--json", "--mode", "concise"}) })
	if defaultJSON != conciseJSON {
		t.Fatalf("concise mode changed JSON output:\ndefault:\n%s\nconcise:\n%s", defaultJSON, conciseJSON)
	}
}

func TestRefsInvalidModeIsUsageError(t *testing.T) {
	expectExit(t, 2, func() { runRefs([]string{"Target", "--mode", "bogus"}) })
}
