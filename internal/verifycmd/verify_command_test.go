package verifycmd

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var allToolchains = map[string]bool{"go": true, "maven": true, "gradle": true, "node": true, "rust": true, "python": true, "make": true, "kern": true}

func TestClassifyCommandAllowed(t *testing.T) {
	for _, c := range []string{
		"go test ./...",
		"go vet ./... && go test ./internal/mcp/",
		"go test -run TestX -v ./pkg 2>&1",
		"mvn -q test",
		"./mvnw verify",
		"gradle build",
		"./gradlew test --info",
		"npm test",
		"npm run lint",
		"cargo test",
		"python -m pytest -q",
		"pytest",
		"make lint",
		"kern version",
		"kern doctor",
		"git status",
		"git log --oneline",
		"git diff --stat",
		"git diff --name-only",
	} {
		if err := classifyCommand(c, allToolchains); err != nil {
			t.Errorf("%q should be allowed: %v", c, err)
		}
	}
}

func TestClassifyCommandRefused(t *testing.T) {
	for _, c := range []string{
		"",
		"cat main.go",
		"sed -n 1,5p main.go",
		"grep -rn foo .",
		"go run main.go",
		"go generate ./...",
		"npx cowsay",
		"npm exec foo",
		"python -c 'print(open(\"x\").read())'",
		"python script.py",
		"cargo run",
		"mvn exec:java",
		"kern run",
		"kern setup",
		"kern mutate",
		"git show HEAD",
		"git blame main.go",
		"git diff",
		"git log -p",
		"git commit -m x",
		"go test ./... | tail",
		"go test ./... > out.txt",
		"go test ./... ; cat main.go",
		"go test ./... && cat main.go",
		"go test $(cat x)",
		"go test `cat x`",
		"go test ./... &",
		"go test ./...\ncat main.go",
	} {
		if err := classifyCommand(c, allToolchains); err == nil {
			t.Errorf("%q should be refused", c)
		}
	}
}

func TestClassifyCommandNeedsMatchingToolchain(t *testing.T) {
	goOnly := map[string]bool{"go": true}
	if err := classifyCommand("go test ./...", goOnly); err != nil {
		t.Fatalf("go in a go repo: %v", err)
	}
	if err := classifyCommand("mvn test", goOnly); err == nil {
		t.Fatal("mvn must be refused in a repo with no Maven marker")
	}
	if err := classifyCommand("git status", map[string]bool{}); err != nil {
		t.Fatalf("read-only git is allowed in any repo: %v", err)
	}
}

func TestDetectToolchains(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"go.mod", "pom.xml", "package.json", ".kern"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := detectToolchains(dir)
	for _, want := range []string{"go", "maven", "node", "kern"} {
		if !got[want] {
			t.Errorf("missing toolchain %q in %v", want, got)
		}
	}
}

const goTestLog = `=== RUN   TestOk
--- PASS: TestOk (0.00s)
=== RUN   TestBroken
    broken_test.go:12: expected 1, got 2
--- FAIL: TestBroken (0.00s)
FAIL
FAIL	example.com/app/pkg	0.012s
ok  	example.com/app/other	0.004s
?   	example.com/app/empty	[no test files]
`

func TestShapeSummaryNamesFailures(t *testing.T) {
	out, err := shapeOutput(goTestLog, 1, 0, "anchor-abc", "summary")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"exit=1", "anchor=anchor-abc", "go test: 1 ok, 1 failed, 1 without tests", "failed tests: TestBroken", "broken_test.go:12"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q in:\n%s", want, out)
		}
	}
}

func TestShapeSummaryPassIsTiny(t *testing.T) {
	log := strings.Repeat("ok  \texample.com/app/p\t0.01s\n", 200)
	out, err := shapeOutput(log, 0, 0, "anchor-abc", "summary")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "\n"); n > 5 {
		t.Fatalf("passing summary should be a few lines, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "go test: 200 ok, 0 failed") {
		t.Fatalf("summary missing counts:\n%s", out)
	}
}

func TestShapeWindows(t *testing.T) {
	log := "a\nb\nc\nd\ne\n"
	tail, err := shapeOutput(log, 0, 0, "x", "tail:2")
	if err != nil || !strings.HasSuffix(tail, "4: d\n5: e") {
		t.Fatalf("tail:2 = %q, %v", tail, err)
	}
	win, err := shapeOutput(log, 0, 0, "x", "lines:2-3")
	if err != nil || !strings.HasSuffix(win, "2: b\n3: c") || strings.Contains(win, "4: d") {
		t.Fatalf("lines:2-3 = %q, %v", win, err)
	}
	if _, err := shapeOutput(log, 0, 0, "x", "lines:99-100"); err == nil {
		t.Fatal("window past the end must error")
	}
	full, err := shapeOutput(log, 0, 0, "x", "full")
	if err != nil || !strings.Contains(full, "e\n") {
		t.Fatalf("full = %q, %v", full, err)
	}
	if _, err := shapeOutput(log, 0, 0, "x", "bogus"); err == nil {
		t.Fatal("unknown mode must error")
	}
}

var anchorRe = regexp.MustCompile(`anchor=(anchor-[0-9a-f]+)`)

func TestVerifyCommandRunsAndReslices(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, exit, err := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "go version"})
	if err != nil {
		t.Fatalf("verify command failed: %v", err)
	}
	if exit != 0 {
		t.Fatalf("go version exit = %d, want 0", exit)
	}
	m := anchorRe.FindStringSubmatch(first)
	if m == nil || !strings.Contains(first, "exit=0") {
		t.Fatalf("expected exit=0 and an anchor, got:\n%s", first)
	}
	again, _, err := VerifyCommand(context.Background(), map[string]any{"anchor": m[1], "output": "lines:1-1"})
	if err != nil {
		t.Fatalf("anchor reslice failed: %v", err)
	}
	if !strings.Contains(again, "1: go version") {
		t.Fatalf("anchor slice should return the first output line, got:\n%s", again)
	}
}

func TestVerifyCommandRefusesContentReaders(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "cat go.mod"})
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("cat must be refused, got %v", err)
	}
}

// TestVerifyCommandDangerousClassGated pins audit-table-2 A across the
// verifycmd↔governance boundary: a command that PASSES verifycmd's own shape
// gate (npm install is in the node allow set) must then be refused by
// governance.CheckExecCommand's dangerous-class escalation — kern_verify and
// kern exec agree on what needs human approval. The exec secret is redirected
// out of the real user config dir so the approval stamp stays in temp space.
func TestVerifyCommandDangerousClassGated(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KERN_TOOLS", "kern_verify")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "")
	t.Setenv("HOME", t.TempDir())

	_, _, err := VerifyCommand(context.Background(), map[string]any{"root": dir, "command": "npm install"})
	if err == nil {
		t.Fatal("npm install must be refused by the dangerous-class gate")
	}
	if !strings.Contains(err.Error(), "dangerous class") {
		t.Fatalf("refusal should name the dangerous class: %v", err)
	}
	if !strings.Contains(err.Error(), "kern approve") {
		t.Fatalf("refusal should carry the kern approve hint: %v", err)
	}
}
