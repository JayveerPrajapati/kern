package cockpit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePanicAndFrames(t *testing.T) {
	mockLog := `
2026-09-04T05:10:00Z ERROR worker crashed
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: code=0x1 addr=0x0 pc=0x5b3a12]

goroutine 42 [running]:
github.com/JayveerPrajapati/kern/internal/api.(*Service).Process(0x0, 0x1)
	/workspace/kern/internal/api/service.go:88 +0x2e
main.main()
	/workspace/kern/cmd/server/main.go:25 +0x10
`
	errMsg, frames := parsePanicAndFrames("/workspace/kern", mockLog)
	if !strings.Contains(errMsg, "nil pointer dereference") {
		t.Fatalf("expected nil pointer dereference in error message, got: %q", errMsg)
	}
	if len(frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(frames))
	}
	if frames[0].File != "internal/api/service.go" || frames[0].Line != 88 {
		t.Fatalf("frame 0 mismatch: %+v", frames[0])
	}
	if frames[1].File != "cmd/server/main.go" || frames[1].Line != 25 {
		t.Fatalf("frame 1 mismatch: %+v", frames[1])
	}
}

func TestRunTriageEndToEnd(t *testing.T) {
	tempRepo := t.TempDir()

	// Initialize mock go module inside tempRepo
	goMod := `module example.com/triage-test

go 1.22
`
	_ = os.WriteFile(filepath.Join(tempRepo, "go.mod"), []byte(goMod), 0o644)

	pkgDir := filepath.Join(tempRepo, "pkg", "calc")
	_ = os.MkdirAll(pkgDir, 0o755)

	buggySource := `package calc

import "errors"

func Divide(a, b int) (int, error) {
	if b == 0 {
		panic("divide by zero")
	}
	return a / b, nil
}
`
	_ = os.WriteFile(filepath.Join(pkgDir, "calc.go"), []byte(buggySource), 0o644)

	mockPanicLog := `
2026-09-04T05:12:00Z FATAL unhandled exception in calculator service
panic: divide by zero

goroutine 1 [running]:
example.com/triage-test/pkg/calc.Divide(0xa, 0x0)
	pkg/calc/calc.go:7 +0x35
`

	cfg := TriageConfig{
		RepoRoot:       tempRepo,
		RawLog:         mockPanicLog,
		NonInteractive: true,
		ReproWriter: func(workDir string, rep *TriageReport) (string, error) {
			reproPath := filepath.Join(workDir, "pkg", "calc", "triage_repro_test.go")
			content := `package calc

import "testing"

func TestTriageReproduction(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("caught panic: %v", r)
		}
	}()
	_, _ = Divide(10, 0)
}
`
			_ = os.WriteFile(reproPath, []byte(content), 0o644)
			return reproPath, nil
		},
		FixApplier: func(workDir string, rep *TriageReport) error {
			// Apply fix to calc.go: return error instead of panic
			fixedSource := `package calc

import "errors"

func Divide(a, b int) (int, error) {
	if b == 0 {
		return 0, errors.New("cannot divide by zero")
	}
	return a / b, nil
}
`
			return os.WriteFile(filepath.Join(workDir, "pkg", "calc", "calc.go"), []byte(fixedSource), 0o644)
		},
	}

	report, err := RunTriage(context.Background(), cfg)
	if err != nil {
		t.Fatalf("RunTriage failed: %v", err)
	}

	if !report.Success {
		t.Fatalf("expected report.Success to be true, got false (error: %s)", report.Error)
	}
	if !report.InitialReproFailed {
		t.Fatalf("expected initial reproduction test to fail")
	}
	if len(report.CorrelatedFiles) == 0 {
		t.Fatalf("expected correlated files from stack trace")
	}
	if report.ReceiptID == "" {
		t.Fatalf("expected compliance receipt to be generated")
	}
	if report.Diff == "" {
		t.Fatalf("expected non-empty diff from fix")
	}
}
