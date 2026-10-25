// Package apply implements `kern apply`: the one-command safe edit path that
// orchestrates the existing engines — transactional multi-file apply with
// sandboxed compile gating (internal/refactor), scoped post-apply
// verification, live-tree rollback on verify failure, and a deterministic
// receipt hash over everything that happened. It builds nothing new: it
// wires impact-scoped verification to ExecuteTransaction's atomic apply.
package apply

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/fsutil"
	"github.com/JayveerPrajapati/kern/internal/refactor"
)

// Request configures one apply run.
type Request struct {
	Root   string              // project root ("" = cwd)
	Edits  []refactor.FileEdit // each edit is {path, content} with the new complete file content
	Test   bool                // run scoped `go test` on affected packages after the build gate
	DryRun bool                // preview the diff without touching the live tree
}

// Result reports everything one apply run did.
type Result struct {
	Success       bool     `json:"success"`
	Applied       bool     `json:"applied"`     // true when the live tree was modified
	RolledBack    bool     `json:"rolled_back"` // true when verify failed and the live tree was restored
	ModifiedFiles []string `json:"modified_files,omitempty"`
	UnifiedDiff   string   `json:"unified_diff,omitempty"`
	BuildOutput   string   `json:"build_output,omitempty"`
	TestOutput    string   `json:"test_output,omitempty"`
	Receipt       string   `json:"receipt,omitempty"` // sha256 over (diff + modified files + outputs) — deterministic, no timestamp
	Error         string   `json:"error,omitempty"`
}

// original records what one edit path looked like before the apply, so the
// rollback can restore it without clobbering concurrent edits.
type original struct {
	path    string
	orig    string
	existed bool
}

// Run applies the edits through the transaction engine, verifies the result
// on the live tree, and rolls back to the observed originals when
// verification fails. The rollback never clobbers a concurrent edit: a file
// whose on-disk content no longer matches what this run applied is reported,
// not overwritten.
func Run(ctx context.Context, req Request) (*Result, error) {
	if req.Root == "" {
		req.Root = "."
	}
	absRoot, err := filepath.Abs(req.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	if len(req.Edits) == 0 {
		return nil, fmt.Errorf("no edits to apply (each edit is {path, content} with the new complete file content)")
	}

	// 1. Snapshot the live originals for post-apply rollback (the
	// transaction engine snapshots them again internally for its TOCTOU
	// preflight — this copy survives after the apply, when the engine's
	// sandbox is gone).
	originals := make([]original, 0, len(req.Edits))
	for _, e := range req.Edits {
		rel := filepath.Clean(e.Path)
		full, err := fsutil.ConfinePath(absRoot, rel, "edits")
		if err != nil {
			return nil, err
		}
		o := original{path: rel, existed: true}
		if data, err := os.ReadFile(full); err == nil {
			o.orig = string(data)
		} else {
			o.existed = false
		}
		originals = append(originals, o)
	}

	// 2. Transactional apply: sandbox copy, compile gate, atomic commit.
	tx, err := refactor.ExecuteTransaction(ctx, refactor.TransactionRequest{
		Root:  absRoot,
		Edits: req.Edits,
		Apply: !req.DryRun,
	})
	if err != nil {
		return nil, err
	}
	res := &Result{
		ModifiedFiles: tx.ModifiedFiles,
		UnifiedDiff:   tx.UnifiedDiff,
		BuildOutput:   tx.CompilerOutput,
	}
	if !tx.Success {
		res.Error = tx.Error
		return finish(res), nil // sandbox rolled back; live tree untouched
	}
	if req.DryRun {
		return finish(res), nil
	}
	res.Applied = true

	// 3. Scoped verification on the live tree: `go test` on the packages of
	// every modified file (the build gate already ran in the sandbox).
	if req.Test {
		pkgs := affectedPackages(absRoot, tx.ModifiedFiles)
		if len(pkgs) > 0 {
			out, err := runGo(ctx, absRoot, "test", pkgs)
			res.TestOutput = out
			if err != nil {
				// 4. Verify failed: restore the observed originals.
				restored, why := rollback(absRoot, originals, req.Edits)
				res.RolledBack = restored
				res.Error = fmt.Sprintf("verify failed: %v (test output: %s)", err, firstLines(out, 20))
				if !restored {
					res.Error = "verify failed; ROLLBACK INCOMPLETE — " + why
				}
				return finish(res), nil
			}
		}
	}
	return finish(res), nil
}

// affectedPackages maps modified files to their Go package paths (`./dir`
// form for `go test`), relative to the root module's go.mod. Non-Go files
// and files under a NESTED go.mod are skipped — a nested module's packages
// are not addressable from the root module's `go test` invocation. Sorted
// and deduplicated.
func affectedPackages(root string, files []string) []string {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return nil // not a module root: nothing to scope-test
	}
	seen := map[string]bool{}
	var pkgs []string
	add := func(dir string) {
		d := filepath.ToSlash(dir)
		if d == "." {
			d = ""
		}
		pkg := "./" + d
		if !seen[pkg] {
			seen[pkg] = true
			pkgs = append(pkgs, pkg)
		}
	}
	for _, f := range files {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		// Nested-module check: a go.mod strictly between the file and the
		// root disqualifies the file (its package is not testable from the
		// root module).
		nested := false
		for d := filepath.Dir(filepath.Join(root, f)); d != root; d = filepath.Dir(d) {
			if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
				nested = true
				break
			}
		}
		if nested {
			continue
		}
		add(filepath.Dir(f))
	}
	sort.Strings(pkgs)
	return pkgs
}

// rollback restores the pre-apply content of every edit path. A file whose
// current on-disk content does not match what this run applied is reported
// and NOT overwritten (a concurrent edit must not be clobbered). Returns
// (fullyRestored, whyNot).
func rollback(root string, originals []original, edits []refactor.FileEdit) (bool, string) {
	applied := map[string]string{}
	for _, e := range edits {
		applied[filepath.Clean(e.Path)] = e.Content
	}
	var problems []string
	for _, o := range originals {
		full, err := fsutil.ConfinePath(root, o.path, "rollback")
		if err != nil {
			problems = append(problems, o.path+": "+err.Error())
			continue
		}
		cur, readErr := os.ReadFile(full)
		if o.existed {
			if readErr != nil || string(cur) != applied[o.path] {
				if readErr == nil && string(cur) == o.orig {
					continue // already restored
				}
				problems = append(problems, o.path+": changed on disk since apply — left as-is")
				continue
			}
			if err := os.WriteFile(full, []byte(o.orig), 0o644); err != nil {
				problems = append(problems, o.path+": "+err.Error())
			}
		} else {
			if readErr != nil {
				// already gone — nothing this run created remains
				continue
			}
			if string(cur) != applied[o.path] {
				problems = append(problems, o.path+": changed on disk since apply — left as-is")
				continue
			}
			if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
				problems = append(problems, o.path+": "+err.Error())
			}
		}
	}
	if len(problems) > 0 {
		return false, strings.Join(problems, "; ")
	}
	return true, ""
}

// runGo runs a go subcommand in dir with the minimal exec environment and
// returns its combined output.
func runGo(ctx context.Context, dir string, sub string, pkgs []string) (string, error) {
	args := append([]string{sub}, pkgs...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = fsutil.MinimalExecEnv()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// finish stamps the deterministic receipt over everything the run produced.
func finish(res *Result) *Result {
	h := sha256.New()
	h.Write([]byte(res.UnifiedDiff))
	for _, f := range res.ModifiedFiles {
		h.Write([]byte{0})
		h.Write([]byte(f))
	}
	h.Write([]byte{0})
	h.Write([]byte(res.BuildOutput))
	h.Write([]byte{0})
	h.Write([]byte(res.TestOutput))
	res.Receipt = hex.EncodeToString(h.Sum(nil))
	if res.Error == "" && !res.RolledBack {
		res.Success = true
	}
	return res
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + "…"
}
