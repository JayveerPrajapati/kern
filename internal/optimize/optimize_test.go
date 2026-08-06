package optimize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

func TestFewShotInjectsBaselines(t *testing.T) {
	// Isolate memory + cache under a temp XDG dir.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	_ = memory.Add(root, "the fastapi session signs the bearer token into a cookie")

	// Put a go.mod so memory is keyed on this exact root (no discovery needed).
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module t\n"), 0o644)

	res, err := Prompt("how does the fastapi session store the bearer token?", "", Options{
		FewShot: true,
		Root:    root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "baseline") {
		t.Fatalf("expected baseline markers in output, got %q", res.Output)
	}

	// Without FewShot no baseline section is added.
	res2, err := Prompt("how does the fastapi session store the bearer token?", "", Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res2.Output, "baseline") {
		t.Fatalf("did not expect baseline without FewShot, got %q", res2.Output)
	}
}

func TestFewShotNoMemory(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	res, err := Prompt("some unrelated fresh topic here", "", Options{FewShot: true, Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, "baseline") {
		t.Fatalf("no lessons to recall; expected no baseline, got %q", res.Output)
	}
}
