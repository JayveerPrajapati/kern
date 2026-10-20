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
	"github.com/JayveerPrajapati/kern/internal/fsutil"
)

// FileEdit represents a modification to a specific file.
type FileEdit struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// editSnapshot records what the transaction observed about one live file at
// snapshot time, so the apply phase can detect a concurrent edit before any
// write lands.
type editSnapshot struct {
	path     string // repo-relative path
	orig     string // live content observed at snapshot time ("" if the file did not exist)
	existed  bool   // whether the live file existed at snapshot time
	content  string // new content to write on apply
	resolved string // symlink-resolved live path, filled during apply preflight
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
	// VerificationSkipped is true when no compile command was configured and
	// no go.mod was present, so no compilation check actually ran.
	VerificationSkipped bool `json:"verification_skipped,omitempty"`
}

// ExecuteTransaction applies batch modifications in an isolated workspace,
// verifies compilation, and commits atomically to the live root only on
// success. Symlink containment is delegated to fsutil: ConfinePath validates
// a path stays inside the real (symlink-resolved) root; NearestExisting
// yields the canonical target so the apply write cannot be redirected by a
// swapped symlink — the defense-in-depth behind the lexical "../" checks.
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

	// Validate every edit before touching the sandbox: empty paths, relative
	// escapes ("../..") and absolute-outside-root paths are rejected;
	// absolute inside-root paths are normalized to repo-relative. The engine
	// fails closed even when a caller bypasses the MCP layer guards.
	for i := range req.Edits {
		if strings.TrimSpace(req.Edits[i].Path) == "" {
			return nil, fmt.Errorf("edits[%d].path is required (each edit is {path, content} with the new complete file content)", i)
		}
		rel := filepath.Clean(req.Edits[i].Path)
		if filepath.IsAbs(rel) {
			r, err := filepath.Rel(absRoot, rel)
			if err != nil || strings.HasPrefix(r, "..") {
				return nil, fmt.Errorf("edits[%d].path %q is outside the project root", i, req.Edits[i].Path)
			}
			rel = r
		} else if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("edits[%d].path %q escapes the project root", i, req.Edits[i].Path)
		}
		req.Edits[i].Path = rel
	}

	// 1. Create temporary sandbox workspace
	sandboxDir, err := os.MkdirTemp("", "kern-refactor-tx-*")
	if err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}
	defer func() { _ = os.RemoveAll(sandboxDir) }()

	if err := copyWorktree(absRoot, sandboxDir); err != nil {
		return nil, fmt.Errorf("copy worktree to sandbox: %w", err)
	}

	// 2. Generate unified diff and apply edits to sandbox
	var modifiedPaths []string
	var diffBuilder strings.Builder
	var snapshots []editSnapshot

	for _, edit := range req.Edits {
		relPath := filepath.Clean(edit.Path)
		if filepath.IsAbs(relPath) {
			if rel, err := filepath.Rel(absRoot, relPath); err == nil && !strings.HasPrefix(rel, "..") {
				relPath = rel
			}
		}

		// Snapshot the live file for the TOCTOU preflight (read errors tolerated:
		// treated as new file).
		origFull, err := fsutil.ConfinePath(absRoot, relPath, "edits")
		if err != nil {
			return nil, err
		}
		origContent := ""
		existed := false
		if data, err := os.ReadFile(origFull); err == nil {
			origContent = string(data)
			existed = true
		}
		snapshots = append(snapshots, editSnapshot{
			path:    relPath,
			orig:    origContent,
			existed: existed,
			content: edit.Content,
		})

		// The sandbox copy has no live symlinks; ConfinePath still fails closed
		// here (defense in depth) and yields the canonical path.
		sandboxFull, err := fsutil.ConfinePath(sandboxDir, relPath, "edits")
		if err != nil {
			return nil, err
		}
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
	verificationSkipped := false
	if compileCmd == "" {
		// Auto-detect go module
		if _, err := os.Stat(filepath.Join(sandboxDir, "go.mod")); err == nil {
			compileCmd = "go build ./..."
		} else {
			// No compile command and no go.mod: nothing can be verified.
			// Callers must surface this instead of claiming verification.
			verificationSkipped = true
		}
	}

	var compileOutput string
	if compileCmd != "" {
		parts := strings.Fields(compileCmd)
		cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
		cmd.Dir = sandboxDir
		// Security: never hand the spawned process the operator's full
		// environment (all secrets). Mirror the host-sampler allowlist
		// (PATH, HOME, TMPDIR) plus the Go toolchain vars the compile step
		// genuinely needs when the operator set them.
		cmd.Env = fsutil.MinimalExecEnv()

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
		// TOCTOU preflight: re-verify every live file against the snapshot
		// BEFORE the first write lands (all-or-nothing commit). ConfinePath
		// re-validates containment; NearestExisting gives the canonical
		// write target so a swapped symlink cannot redirect the write.
		for i := range snapshots {
			snap := &snapshots[i]
			full, err := fsutil.ConfinePath(absRoot, snap.path, "edits")
			if err != nil {
				return nil, err
			}
			snap.resolved, err = fsutil.NearestExisting(full, "edits")
			if err != nil {
				return nil, err
			}
			if snap.existed {
				data, err := os.ReadFile(full)
				if err != nil || !bytes.Equal(data, []byte(snap.orig)) {
					return nil, fmt.Errorf("apply aborted: %s changed on disk since the transaction snapshot (concurrent edit); re-run the refactor", snap.path)
				}
			} else {
				if _, err := os.Stat(full); err == nil {
					return nil, fmt.Errorf("apply aborted: %s changed on disk since the transaction snapshot (concurrent edit); re-run the refactor", snap.path)
				}
			}
		}

		// Write phase: use the preflight-resolved paths (no re-resolution, no
		// re-verification — the checks above already ran for every edit).
		for _, snap := range snapshots {
			if err := os.MkdirAll(filepath.Dir(snap.resolved), 0o755); err != nil {
				return nil, fmt.Errorf("apply mkdir %s: %w", snap.path, err)
			}
			if err := os.WriteFile(snap.resolved, []byte(snap.content), 0o644); err != nil {
				return nil, fmt.Errorf("apply write %s: %w", snap.path, err)
			}
		}
	}

	return &TransactionResult{
		Success:             true,
		RolledBack:          false,
		ModifiedFiles:       modifiedPaths,
		UnifiedDiff:         diffBuilder.String(),
		CompilerOutput:      compileOutput,
		VerificationSkipped: verificationSkipped,
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
