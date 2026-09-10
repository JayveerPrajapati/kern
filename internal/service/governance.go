package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	bpcli "github.com/JayveerPrajapati/kern/internal/blueprint/cli"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// GovernanceService centralizes change-governance operations: approving or
// rejecting pending approval gates, reading the tamper-evident audit trail,
// and running the change-governance check pipeline (boundaries, secrets,
// duplication, tests).
type GovernanceService interface {
	// PendingApprovals lists the approvals awaiting a human decision for
	// root, read from the persistent approval store.
	PendingApprovals(ctx context.Context, root string) ([]domain.Approval, error)
	// Approve records a human decision on a pending approval. approve=true
	// approves, false rejects. The decision is persisted to the shared store.
	Approve(ctx context.Context, root, id, approver string, approve bool, reason string) (domain.Approval, error)
	// Audit returns the governance audit trail for root, optionally filtered
	// to one task (taskID "" = all entries).
	Audit(ctx context.Context, root, taskID string) ([]governance.AuditEntry, error)
	// Check runs the change-governance validation pipeline over root's
	// staged changes and returns the pipeline's exit code plus captured
	// output. Exit code 0 = clean, 1 = findings, 2 = usage/config error.
	Check(ctx context.Context, root string, opts CheckOptions) (*CheckReport, error)
}

// CheckOptions configures GovernanceService.Check.
type CheckOptions struct {
	Staged     bool   // check staged changes (default behavior; kept for clarity)
	Source     string // change source: agent|ide|human
	Resilience bool   // also run resilience (fault-injection) scenarios (slow, WARN-only)
	Tests      bool   // also run sandbox build/test in an isolated worktree (slow, blocks on failure)
	JSON       bool   // emit the pipeline result as JSON
}

// CheckReport is the result of GovernanceService.Check.
type CheckReport struct {
	Root     string `json:"root"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
}

// governanceService is the default GovernanceService implementation. It reads
// approvals from the same persistent file store the workflow/deploy gates
// write, the audit trail from the .kern/audit log, and delegates Check to the
// canonical blueprint pipeline so the service verdict always matches
// `kern check`.
type governanceService struct{}

func newGovernanceService() *governanceService { return &governanceService{} }

func (s *governanceService) PendingApprovals(ctx context.Context, root string) ([]domain.Approval, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return governance.NewFileStore(resolveRoot(root)).Pending()
}

func (s *governanceService) Approve(ctx context.Context, root, id, approver string, approve bool, reason string) (domain.Approval, error) {
	if err := ctx.Err(); err != nil {
		return domain.Approval{}, err
	}
	if approver == "" {
		approver = "service-user"
	}
	return governance.NewFileStore(resolveRoot(root)).Decide(id, approver, approve, reason)
}

func (s *governanceService) Audit(ctx context.Context, root, taskID string) ([]governance.AuditEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root = resolveRoot(root)
	// A missing root (e.g. a CI worktree deleted after the run) is an error,
	// not an empty trail: blueprint verify-receipt relies on the subprocess
	// exit code to distinguish "chain unreadable" (soft skip, warn) from
	// "chain readable but hash absent" (hard failure). Without this check
	// kern audit --root <missing> exits 0 with "no audit entries" and a
	// receipt whose worktree was cleaned up is wrongly invalidated.
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("audit: root directory does not exist: %s", root)
	}
	store := storage.NewLog(filepath.Join(root, ".kern", "audit"))
	entries, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []governance.AuditEntry
	for _, e := range entries {
		var entry governance.AuditEntry
		if err := storage.UnmarshalValue(e.Value, &entry); err != nil {
			continue // malformed entry — skip, never fail the whole trail
		}
		if taskID != "" && entry.TaskID != taskID {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

func (s *governanceService) Check(ctx context.Context, root string, opts CheckOptions) (*CheckReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root = resolveRoot(root)
	args := []string{"--repo", root}
	if opts.Source != "" {
		args = append(args, "--source", opts.Source)
	}
	if opts.Resilience {
		args = append(args, "--resilience")
	}
	if opts.Tests {
		args = append(args, "--tests")
	}
	if opts.JSON {
		args = append(args, "--json")
	}

	// The blueprint pipeline emits to os.Stdout/os.Stderr directly; capture
	// both so the service returns a self-contained report. Reads run in
	// goroutines so a large report can never deadlock on the pipe buffer.
	stdout, stderr, err := captureOutput(func() {
		bpcli.RunCheck(args)
	})
	if err != nil {
		return nil, err
	}
	return &CheckReport{Root: root, Stdout: stdout, Stderr: stderr}, nil
}

// captureOutput runs fn with os.Stdout/os.Stderr redirected to pipes and
// returns everything written to each. It restores the originals afterwards.
func captureOutput(fn func()) (stdout, stderr string, err error) {
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		return "", "", err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		return "", "", err
	}
	os.Stdout, os.Stderr = outW, errW

	var outBuf, errBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(&outBuf, outR) }()
	go func() { defer wg.Done(); _, _ = io.Copy(&errBuf, errR) }()

	func() {
		defer func() {
			os.Stdout, os.Stderr = oldOut, oldErr
			_ = outW.Close()
			_ = errW.Close()
		}()
		fn()
	}()

	wg.Wait()
	return outBuf.String(), errBuf.String(), nil
}
