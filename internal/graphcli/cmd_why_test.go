package graphcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func whyModeFixture(t *testing.T) string {
	t.Helper()
	t.Setenv("KERN_DAEMON", "0")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module whyfixture\n\ngo 1.20\n",
		"main.go": `package main

// Target is the documented fixture root.
func Target() int { return 1 }

// Caller depends on Target for its result.
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

func TestWhyConciseModeSuppressesCallerEvidence(t *testing.T) {
	root := whyModeFixture(t)
	out := captureStdout(t, func() { RunWhy([]string{"Target", "--root", root, "--mode", "concise"}) })
	for _, want := range []string{
		"func Target",
		"doc: Target is the documented fixture root.",
		"incoming edges: 1, outgoing calls: 0",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("concise why missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "who depends on it and why") || strings.Contains(out, "Caller depends") {
		t.Fatalf("concise why leaked caller evidence:\n%s", out)
	}
}

func TestWhyEvidenceModePreservesCallerRationale(t *testing.T) {
	root := whyModeFixture(t)
	out := captureStdout(t, func() { RunWhy([]string{"Target", "--root", root, "--mode", "evidence"}) })
	if !strings.Contains(out, "who depends on it and why") || !strings.Contains(out, "Caller depends on Target") {
		t.Fatalf("evidence why missing caller rationale:\n%s", out)
	}
}

func TestWhyConciseModeDoesNotChangeJSON(t *testing.T) {
	root := whyModeFixture(t)
	defaultJSON := captureStdout(t, func() { RunWhy([]string{"Target", "--root", root, "--json"}) })
	conciseJSON := captureStdout(t, func() { RunWhy([]string{"Target", "--root", root, "--json", "--mode", "concise"}) })
	if defaultJSON != conciseJSON {
		t.Fatalf("concise mode changed JSON output:\ndefault:\n%s\nconcise:\n%s", defaultJSON, conciseJSON)
	}
}

func TestWhyInvalidModeIsUsageError(t *testing.T) {
	if code := RunWhy([]string{"Target", "--mode", "bogus"}); code != 2 {
		t.Fatalf("RunWhy(invalid mode) exit = %d, want 2", code)
	}
}
