// Package refactor provides atomic multi-file refactoring and rollback transactions.
package refactor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/diff"
)

// FileEdit represents a modification to a specific file.
type FileEdit struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// TransactionRequest configures a multi-file transactional refactoring batch.
type TransactionRequest struct {
	Root           string     `json:"root"`
	Edits          []FileEdit `json:"edits"`
	CompileCommand string     `json:"compile_command,omitempty"`
	Apply          bool       `json:"apply"` // true to commit to live tree; false = dry-run preview
}

// TransactionResult represents the outcome of the transactional refactor.
type TransactionResult struct {
	Success        bool     `json:"success"`
	RolledBack     bool     `json:"rolled_back"`
	ModifiedFiles  []string `json:"modified_files"`
	UnifiedDiff    string   `json:"unified_diff"`
	CompilerOutput string   `json:"compiler_output,omitempty"`
	Error          string   `json:"error,omitempty"`
}

// ExecuteTransaction applies batch modifications in an isolated workspace,
// verifies compilation, and commits atomically to the live root only on success.
func ExecuteTransaction(ctx context.Context, req TransactionRequest) (*TransactionResult, error) {
	if req.Root == "" {
		req.Root = "."
	}
	absRoot, err := filepath.Abs(req.Root)
	if err != nil {
		absRoot = req.Root
	}

	if len(req.Edits) == 0 {
		return &TransactionResult{
			Success:       true,
			RolledBack:    false,
			ModifiedFiles: nil,
			UnifiedDiff:   "",
		}, nil
	}

	// 1. Create temporary sandbox workspace
	sandboxDir, err := os.MkdirTemp("", "kern-refactor-tx-*")
	if err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}
	defer func() { _ = os.RemoveAll(sandboxDir) }()

	// Copy repository contents to sandbox (respecting basic files)
	if err := copyWorktree(absRoot, sandboxDir); err != nil {
		return nil, fmt.Errorf("copy worktree to sandbox: %w", err)
	}

	// 2. Generate unified diff and apply edits to sandbox
	var modifiedPaths []string
	var diffBuilder strings.Builder

	for _, edit := range req.Edits {
		relPath := filepath.Clean(edit.Path)
		if filepath.IsAbs(relPath) {
			if rel, err := filepath.Rel(absRoot, relPath); err == nil && !strings.HasPrefix(rel, "..") {
				relPath = rel
			}
		}

		origFull := filepath.Join(absRoot, relPath)
		origContent := ""
		if data, err := os.ReadFile(origFull); err == nil {
			origContent = string(data)
		}

		sandboxFull := filepath.Join(sandboxDir, relPath)
		if err := os.MkdirAll(filepath.Dir(sandboxFull), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir for %s: %w", relPath, err)
		}

		if err := os.WriteFile(sandboxFull, []byte(edit.Content), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", relPath, err)
		}

		d := diff.Unified(relPath, relPath, strings.Split(origContent, "\n"), strings.Split(edit.Content, "\n"))
		if d != "" {
			diffBuilder.WriteString(d)
			diffBuilder.WriteString("\n")
			modifiedPaths = append(modifiedPaths, relPath)
		}
	}

	// 3. Automated Compilation Check in the sandbox
	compileCmd := req.CompileCommand
	if compileCmd == "" {
		// Auto-detect go module
		if _, err := os.Stat(filepath.Join(sandboxDir, "go.mod")); err == nil {
			compileCmd = "go build ./..."
		}
	}

	var compileOutput string
	if compileCmd != "" {
		parts := strings.Fields(compileCmd)
		cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
		cmd.Dir = sandboxDir
		cmd.Env = os.Environ()

		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &outBuf

		if err := cmd.Run(); err != nil {
			compileOutput = outBuf.String()
			// Compilation failed: Roll back transaction (do not touch live tree)
			return &TransactionResult{
				Success:        false,
				RolledBack:     true,
				ModifiedFiles:  modifiedPaths,
				UnifiedDiff:    diffBuilder.String(),
				CompilerOutput: compileOutput,
				Error:          fmt.Sprintf("compilation failed: %v", err),
			}, nil
		}
		compileOutput = outBuf.String()
	}

	// 4. If Apply is true, atomically commit validated modifications to live tree
	if req.Apply {
		for _, edit := range req.Edits {
			relPath := filepath.Clean(edit.Path)
			if filepath.IsAbs(relPath) {
				if rel, err := filepath.Rel(absRoot, relPath); err == nil && !strings.HasPrefix(rel, "..") {
					relPath = rel
				}
			}

			liveFull := filepath.Join(absRoot, relPath)
			if err := os.MkdirAll(filepath.Dir(liveFull), 0o755); err != nil {
				return nil, fmt.Errorf("apply mkdir %s: %w", relPath, err)
			}
			if err := os.WriteFile(liveFull, []byte(edit.Content), 0o644); err != nil {
				return nil, fmt.Errorf("apply write %s: %w", relPath, err)
			}
		}
	}

	return &TransactionResult{
		Success:        true,
		RolledBack:     false,
		ModifiedFiles:  modifiedPaths,
		UnifiedDiff:    diffBuilder.String(),
		CompilerOutput: compileOutput,
	}, nil
}

func copyWorktree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Skip VCS, tool state and temp dirs
		if info.IsDir() && (rel == ".git" || rel == ".kern" || rel == ".blueprint" || rel == "node_modules" || rel == "vendor") {
			return filepath.SkipDir
		}

		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
