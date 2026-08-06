package validate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDetectGo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if c.Cmd != "go" || len(c.Args) == 0 || c.Args[0] != "build" {
		t.Fatalf("expected go build, got %v %v", c.Cmd, c.Args)
	}
}

func TestDetectPython(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "requirements.txt"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if c.Cmd != "python" && c.Cmd != "python3" {
		t.Fatalf("expected python, got %v", c.Cmd)
	}
}

func TestDetectUnknown(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Detect(root); err == nil {
		t.Fatal("expected error for unknown project")
	}
}

func TestDetectMissingToolchain(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// go binary is present on dev machines; assert Detect returns a command
	// with Cmd "go" or skips cleanly if unavailable.
	c, err := Detect(root)
	if err != nil {
		t.Logf("go not on PATH, skipping: %v", err)
		return
	}
	if c.Cmd != "go" {
		t.Fatalf("expected go, got %v", c.Cmd)
	}
}

func TestRunTrueCommand(t *testing.T) {
	c := &Command{Name: "true", Cmd: "true", Args: nil}
	res := Run(t.TempDir(), c, 10*time.Second)
	if !res.OK {
		t.Fatalf("true should pass: %+v", res)
	}
}

func TestRunFailingCommand(t *testing.T) {
	c := &Command{Name: "false", Cmd: "false", Args: nil}
	res := Run(t.TempDir(), c, 10*time.Second)
	if res.OK {
		t.Fatal("false should fail")
	}
	if res.ExitCode == 0 {
		t.Fatal("expected non-zero exit code")
	}
}

func TestRunTimeout(t *testing.T) {
	c := &Command{Name: "sleep", Cmd: "sleep", Args: []string{"10"}}
	res := Run(t.TempDir(), c, 200*time.Millisecond)
	if res.OK {
		t.Fatal("sleep should time out")
	}
	if res.Err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestDetectGoProjectRuns(t *testing.T) {
	// Exercise a real go vet run from this package directory (no network,
	// deps are local/cached).
	c := &Command{Name: "go vet (meta)", Cmd: "go", Args: []string{"vet", "./..."}}
	res := Run(".", c, 60*time.Second)
	if !res.OK {
		t.Fatalf("go vet failed: %s", res.Output)
	}
}
