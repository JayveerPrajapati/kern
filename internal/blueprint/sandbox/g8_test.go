package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// g8Repo creates a git repo with a buildable Go module, returns its path.
func g8Repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	g8Git(t, dir, "init", "-q")
	g8Git(t, dir, "config", "user.email", "t@t")
	g8Git(t, dir, "config", "user.name", "t")
	g8Write(t, dir, "go.mod", "module example.com/test\n\ngo 1.23\n")
	g8Write(t, dir, "main.go", "package main\n\nfunc main() { println(\"hello\") }\n")
	g8Git(t, dir, "add", "-A")
	g8Git(t, dir, "commit", "-qm", "init")
	return dir
}

func g8Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func g8Write(t *testing.T, dir, relpath, content string) {
	t.Helper()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relpath, err)
	}
}

func g8WriteCommit(t *testing.T, dir, relpath, content string) {
	t.Helper()
	g8Write(t, dir, relpath, content)
	g8Git(t, dir, "add", relpath)
	g8Git(t, dir, "commit", "-qm", "add "+relpath)
}

// G8-1: successful build
func TestG8_SuccessfulBuild(t *testing.T) {
	dir := g8Repo(t)
	res := Run(context.Background(), dir, []string{"go", "build", "./..."}, DefaultConfig())
	if res.Error != "" {
		t.Fatalf("sandbox error: %s", res.Error)
	}
	if !res.Ok {
		t.Fatalf("expected Ok=true, got exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
}

// G8-2: failing build
func TestG8_FailingBuild(t *testing.T) {
	dir := g8Repo(t)
	g8WriteCommit(t, dir, "bad.go", "package main\n\nfunc broken() { this is invalid }\n")

	res := Run(context.Background(), dir, []string{"go", "build", "./..."}, DefaultConfig())
	if res.Ok {
		t.Fatal("expected Ok=false for failing build")
	}
	if res.ExitCode == 0 {
		t.Error("expected non-zero exit code")
	}
	if res.Stderr == "" {
		t.Error("expected stderr output for build failure")
	}
}

// G8-3: failing test
func TestG8_FailingTest(t *testing.T) {
	dir := g8Repo(t)
	g8WriteCommit(t, dir, "main_test.go", `package main

import "testing"

func TestFail(t *testing.T) {
	t.Fatal("intentional failure")
}
`)

	res := Run(context.Background(), dir, []string{"go", "test", "./..."}, DefaultConfig())
	if res.Ok {
		t.Fatal("expected Ok=false for failing test")
	}
	if !strings.Contains(res.Stderr, "FAIL") && !strings.Contains(res.Stdout, "FAIL") {
		t.Errorf("expected FAIL in output; stdout=%s stderr=%s", res.Stdout, res.Stderr)
	}
}

// G8-4: timeout
func TestG8_Timeout(t *testing.T) {
	dir := g8Repo(t)
	g8WriteCommit(t, dir, "main.go", `package main

import (
	"fmt"
	"time"
)

func main() {
	time.Sleep(30 * time.Second)
	fmt.Println("done")
}
`)

	cfg := Config{Timeout: 2 * time.Second, MaxOutputBytes: 1 << 20}
	res := Run(context.Background(), dir, []string{"go", "run", "main.go"}, cfg)
	if !res.TimedOut {
		t.Fatal("expected TimedOut=true")
	}
	if res.Ok {
		t.Error("expected Ok=false on timeout")
	}
}

// G8-5: process tree cleanup
// Verifies that after a timeout, no child processes are left running.
func TestG8_ProcessTreeCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group test is Unix-only")
	}
	dir := g8Repo(t)
	// Create a program that spawns a child that outlives the parent.
	g8WriteCommit(t, dir, "main.go", `package main

import (
	"os/exec"
	"time"
)

func main() {
	cmd := exec.Command("sleep", "60")
	cmd.Start()
	time.Sleep(30 * time.Second)
}
`)

	cfg := Config{Timeout: 2 * time.Second, MaxOutputBytes: 1 << 20}
	res := Run(context.Background(), dir, []string{"go", "run", "main.go"}, cfg)
	if !res.TimedOut {
		t.Fatal("expected timeout")
	}

	// Give the OS a moment to reap the process group, then poll until it is
	// gone. A single fixed 500ms sleep is flaky on loaded machines (reaping
	// can take longer under load); polling for up to 5s still catches a real
	// orphan, it just tolerates reaping latency.
	deadline := time.Now().Add(5 * time.Second)
	for {
		checkCmd := exec.Command("pgrep", "-f", "sleep 60")
		out, err := checkCmd.Output()
		orphans := err == nil && len(strings.TrimSpace(string(out))) > 0
		if !orphans {
			break // pgrep exits 1 on no match — group fully reaped.
		}
		if time.Now().After(deadline) {
			t.Errorf("orphaned child process still running after timeout:\n%s", out)
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// G8-6: large stdout
func TestG8_LargeStdout(t *testing.T) {
	dir := g8Repo(t)
	// Create a program that prints a lot to stdout.
	g8WriteCommit(t, dir, "main.go", `package main

import (
	"fmt"
	"strings"
)

func main() {
	// Print ~2 MiB to stdout.
	chunk := strings.Repeat("x", 4096)
	for i := 0; i < 512; i++ {
		fmt.Println(chunk)
	}
}
`)

	cfg := Config{Timeout: 30 * time.Second, MaxOutputBytes: 100 * 1024} // 100 KiB cap
	res := Run(context.Background(), dir, []string{"go", "run", "main.go"}, cfg)
	if !res.Truncated {
		t.Error("expected Truncated=true for large stdout")
	}
	if len(res.Stdout) > 100*1024 {
		t.Errorf("stdout not capped: %d bytes (cap 100KiB)", len(res.Stdout))
	}
}

// G8-7: large stderr
func TestG8_LargeStderr(t *testing.T) {
	dir := g8Repo(t)
	g8WriteCommit(t, dir, "main.go", `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	chunk := strings.Repeat("e", 4096)
	for i := 0; i < 512; i++ {
		fmt.Fprintln(os.Stderr, chunk)
	}
}
`)

	cfg := Config{Timeout: 30 * time.Second, MaxOutputBytes: 100 * 1024}
	res := Run(context.Background(), dir, []string{"go", "run", "main.go"}, cfg)
	if !res.Truncated {
		t.Error("expected Truncated=true for large stderr")
	}
	if len(res.Stderr) > 100*1024 {
		t.Errorf("stderr not capped: %d bytes (cap 100KiB)", len(res.Stderr))
	}
}

// G8-8: concurrent sandbox runs
func TestG8_ConcurrentRuns(t *testing.T) {
	dir := g8Repo(t)

	const N = 5
	var wg sync.WaitGroup
	errs := make(chan error, N)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := Run(context.Background(), dir, []string{"go", "build", "./..."}, DefaultConfig())
			if !res.Ok {
				errs <- fmt.Errorf("concurrent run failed: exit=%d err=%s", res.ExitCode, res.Error)
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

// G8-9: cancellation
func TestG8_Cancellation(t *testing.T) {
	dir := g8Repo(t)
	g8WriteCommit(t, dir, "main.go", `package main

import (
	"time"
)

func main() {
	time.Sleep(30 * time.Second)
}
`)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(1 * time.Second)
		cancel()
	}()

	cfg := Config{Timeout: 30 * time.Second, MaxOutputBytes: 1 << 20}
	res := Run(ctx, dir, []string{"go", "run", "main.go"}, cfg)
	if !res.Cancelled {
		t.Fatal("expected Cancelled=true")
	}
}

// G8-10: cleanup after failure
func TestG8_CleanupAfterFailure(t *testing.T) {
	dir := g8Repo(t)
	g8WriteCommit(t, dir, "bad.go", "package main\n\nfunc broken() { invalid }")

	res := Run(context.Background(), dir, []string{"go", "build", "./..."}, DefaultConfig())
	if res.Ok {
		t.Fatal("expected build failure")
	}

	// The worktree temp dir should be cleaned up.
	if res.Worktree != "" {
		if _, err := os.Stat(res.Worktree); !os.IsNotExist(err) {
			t.Errorf("worktree not cleaned up: %s still exists", res.Worktree)
		}
	}

	// Verify no stale worktree registrations remain.
	listCmd := exec.Command("git", "worktree", "list")
	listCmd.Dir = dir
	out, _ := listCmd.Output()
	worktreeLines := strings.Split(strings.TrimSpace(string(out)), "\n")
	// Should be exactly 1 (the main repo), not 2+.
	if len(worktreeLines) > 1 {
		t.Errorf("stale worktree registrations remain:\n%s", out)
	}
}

// G8-11: repository remains untouched after sandbox execution
func TestG8_RepoUntouched(t *testing.T) {
	dir := g8Repo(t)

	// Record the repo state before sandbox execution.
	preCmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	preOut, _ := preCmd.Output()
	preStatus := strings.TrimSpace(string(preOut))

	// Run a sandbox build.
	res := Run(context.Background(), dir, []string{"go", "build", "./..."}, DefaultConfig())
	if !res.Ok {
		t.Fatalf("sandbox build failed: %s", res.Error)
	}

	// Verify the repo state is unchanged.
	postCmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	postOut, _ := postCmd.Output()
	postStatus := strings.TrimSpace(string(postOut))

	if preStatus != postStatus {
		t.Errorf("repo state changed after sandbox execution:\nbefore: %q\nafter:  %q", preStatus, postStatus)
	}

	// Verify no build artifacts were left in the main repo.
	if _, err := os.Stat(filepath.Join(dir, "main.test")); err == nil {
		t.Error("test binary left in main repo")
	}
}

// G8-bonus: SandboxCheck as a service.Check
func TestG8_SandboxCheckPass(t *testing.T) {
	dir := g8Repo(t)
	check := NewDefaultCheck()
	res, err := check.Run(context.Background(), changeReq(dir))
	if err != nil {
		t.Fatalf("Check.Run error: %v", err)
	}
	if res.Status != "PASS" {
		t.Errorf("status = %s, want PASS; error=%s findings=%+v", res.Status, res.Error, res.Findings)
	}
}

// G8-bonus: SandboxCheck detects failing build
func TestG8_SandboxCheckFailingBuild(t *testing.T) {
	dir := g8Repo(t)
	g8WriteCommit(t, dir, "bad.go", "package main\n\nfunc broken() { invalid }")

	check := NewDefaultCheck()
	res, _ := check.Run(context.Background(), changeReq(dir))
	if res.Status != "BLOCK" {
		t.Errorf("status = %s, want BLOCK", res.Status)
	}
	if len(res.Findings) == 0 {
		t.Error("expected findings for failing build")
	}
}

// G8-bonus: SandboxCheck detects failing test
func TestG8_SandboxCheckFailingTest(t *testing.T) {
	dir := g8Repo(t)
	g8WriteCommit(t, dir, "main_test.go", `package main

import "testing"

func TestFail(t *testing.T) { t.Fatal("fail") }
`)

	check := NewDefaultCheck()
	res, _ := check.Run(context.Background(), changeReq(dir))
	if res.Status != "BLOCK" {
		t.Errorf("status = %s, want BLOCK", res.Status)
	}
}

// changeReq builds a minimal ChangeRequest for a repo.
func changeReq(repoRoot string) domain.ChangeRequest {
	return domain.ChangeRequest{
		RepositoryRoot: repoRoot,
		Source:         domain.SourceCI,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: ".", Op: domain.OpWrite}},
	}
}
