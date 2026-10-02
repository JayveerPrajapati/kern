// Package mutation implements lightweight AST mutation testing for test suite gap analysis.
// It injects surgical mutations (condition inversion, zero-return replacement, boolean flips,
// boundary shifts) to verify test suite sensitivity and detect surviving mutants (false-positive tests).
package mutation

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/execution"
)

// Mutant describes a single code mutation applied to an AST node.
type Mutant struct {
	ID          string `json:"id"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Operator    string `json:"operator"` // "invert_condition", "swap_boolean", "zero_return", "boundary_shift"
	Original    string `json:"original"`
	Replacement string `json:"replacement"`
	Status      string `json:"status"` // "killed", "survived", "compile_error", "untested"
	TestOutput  string `json:"test_output,omitempty"`
}

// Options configures mutation testing execution.
type Options struct {
	Root        string        `json:"root"`
	Files       []string      `json:"files,omitempty"`
	MaxMutants  int           `json:"max_mutants,omitempty"`
	DryRun      bool          `json:"dry_run,omitempty"` // only generate mutants without running test suite
	TestCommand string        `json:"test_command,omitempty"`
	Timeout     time.Duration `json:"timeout,omitempty"`
	// Isolate evaluates every mutant inside an execution.NewWorktree copy
	// so the real tree is never touched — not even transiently during a
	// mutant's test run. The journaled in-place mode remains as the
	// fallback when the worktree snapshot cannot be built. Both the CLI
	// (kern mutate) and the MCP tool (kern_mutation_test) opt in.
	Isolate bool `json:"isolate,omitempty"`
}

// Report encapsulates the complete mutation testing results.
type Report struct {
	Root           string   `json:"root"`
	TotalMutants   int      `json:"total_mutants"`
	KilledCount    int      `json:"killed_count"`
	SurvivedCount  int      `json:"survived_count"`
	EvaluatedCount int      `json:"evaluated_count"` // killed + survived; the score's denominator
	UntestedCount  int      `json:"untested_count"`  // zero_return class: skipped without type analysis
	Score          float64  `json:"mutation_score"`  // 0-100% over EVALUATED mutants only
	Mutants        []Mutant `json:"mutants"`
}

// GenerateMutants parses Go source code and returns candidate AST mutants without altering disk.
func GenerateMutants(filePath string, src []byte) ([]Mutant, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filePath, err)
	}

	var mutants []Mutant
	mutantIdx := 0

	ast.Inspect(node, func(n ast.Node) bool {
		if n == nil {
			return true
		}

		pos := fset.Position(n.Pos())

		switch expr := n.(type) {
		case *ast.BinaryExpr:
			if invOp, ok := invertBinaryOp(expr.Op); ok {
				mutantIdx++
				origStr := exprToString(fset, expr)
				mutantExpr := &ast.BinaryExpr{
					X:  expr.X,
					Op: invOp,
					Y:  expr.Y,
				}
				repStr := exprToString(fset, mutantExpr)
				mutants = append(mutants, Mutant{
					ID:          fmt.Sprintf("mut-%d", mutantIdx),
					File:        filePath,
					Line:        pos.Line,
					Operator:    "invert_condition",
					Original:    origStr,
					Replacement: repStr,
					Status:      "untested",
				})
			} else if shiftOp, ok := shiftArithmeticOp(expr.Op); ok {
				mutantIdx++
				origStr := exprToString(fset, expr)
				mutantExpr := &ast.BinaryExpr{
					X:  expr.X,
					Op: shiftOp,
					Y:  expr.Y,
				}
				repStr := exprToString(fset, mutantExpr)
				mutants = append(mutants, Mutant{
					ID:          fmt.Sprintf("mut-%d", mutantIdx),
					File:        filePath,
					Line:        pos.Line,
					Operator:    "boundary_shift",
					Original:    origStr,
					Replacement: repStr,
					Status:      "untested",
				})
			}

		case *ast.Ident:
			if expr.Name == "true" {
				mutantIdx++
				mutants = append(mutants, Mutant{
					ID:          fmt.Sprintf("mut-%d", mutantIdx),
					File:        filePath,
					Line:        pos.Line,
					Operator:    "swap_boolean",
					Original:    "true",
					Replacement: "false",
					Status:      "untested",
				})
			} else if expr.Name == "false" {
				mutantIdx++
				mutants = append(mutants, Mutant{
					ID:          fmt.Sprintf("mut-%d", mutantIdx),
					File:        filePath,
					Line:        pos.Line,
					Operator:    "swap_boolean",
					Original:    "false",
					Replacement: "true",
					Status:      "untested",
				})
			}

		case *ast.ReturnStmt:
			if len(expr.Results) == 1 {
				origStr := exprToString(fset, expr)
				// Zero-return candidate
				if origStr != "return nil" && origStr != "return 0" && origStr != "return false" && origStr != "return \"\"" {
					mutantIdx++
					mutants = append(mutants, Mutant{
						ID:          fmt.Sprintf("mut-%d", mutantIdx),
						File:        filePath,
						Line:        pos.Line,
						Operator:    "zero_return",
						Original:    origStr,
						Replacement: "return nil // or zero-value",
						Status:      "untested",
					})
				}
			}
		}

		return true
	})

	return mutants, nil
}

// ApplyMutantToSource generates modified file source applying a single mutant.
func ApplyMutantToSource(filePath string, src []byte, mutant Mutant) ([]byte, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	applied := false
	ast.Inspect(node, func(n ast.Node) bool {
		if n == nil || applied {
			return !applied
		}

		pos := fset.Position(n.Pos())
		if pos.Line != mutant.Line {
			return true
		}

		switch expr := n.(type) {
		case *ast.BinaryExpr:
			if mutant.Operator == "invert_condition" {
				if invOp, ok := invertBinaryOp(expr.Op); ok {
					expr.Op = invOp
					applied = true
					return false
				}
			} else if mutant.Operator == "boundary_shift" {
				if shiftOp, ok := shiftArithmeticOp(expr.Op); ok {
					expr.Op = shiftOp
					applied = true
					return false
				}
			}

		case *ast.Ident:
			if mutant.Operator == "swap_boolean" {
				if expr.Name == "true" && mutant.Replacement == "false" {
					expr.Name = "false"
					applied = true
					return false
				} else if expr.Name == "false" && mutant.Replacement == "true" {
					expr.Name = "true"
					applied = true
					return false
				}
			}
		}

		return true
	})

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, node); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Run executes mutation testing across targeted files in the repository.
func Run(ctx context.Context, opts Options) (*Report, error) {
	if opts.Isolate {
		return runIsolated(ctx, opts)
	}
	if opts.Root == "" {
		opts.Root = "."
	}
	absRoot, err := filepath.Abs(opts.Root)
	if err != nil {
		absRoot = opts.Root
	}
	// Self-heal: if a previous run was interrupted (SIGKILL, crash) and left a
	// stale journal + mutated files behind, restore those files FIRST and tell
	// the user, before any new mutation happens (audit C2).
	if n, rerr := recoverStaleJournals(absRoot); n > 0 || rerr != nil {
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "mutation: WARNING: stale journal restore incomplete: %v\n", rerr)
		}
		if n > 0 {
			fmt.Fprintf(os.Stderr, "mutation: restored %d file(s) left modified by an interrupted previous mutation run (stale journal self-heal)\n", n)
		}
	}
	if opts.MaxMutants <= 0 {
		opts.MaxMutants = 50
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}

	var targetFiles []string
	if len(opts.Files) > 0 {
		targetFiles = opts.Files
	} else {
		// Discover Go files in root
		_ = filepath.Walk(absRoot, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				if info != nil && info.IsDir() {
					name := info.Name()
					if name == ".git" || name == "vendor" || name == "node_modules" || name == ".kern" {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
				rel, _ := filepath.Rel(absRoot, p)
				targetFiles = append(targetFiles, rel)
			}
			return nil
		})
	}

	var allMutants []Mutant
	for _, f := range targetFiles {
		fullPath := filepath.Join(absRoot, f)
		src, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		muts, err := GenerateMutants(f, src)
		if err == nil {
			allMutants = append(allMutants, muts...)
		}
		if len(allMutants) >= opts.MaxMutants {
			allMutants = allMutants[:opts.MaxMutants]
			break
		}
	}

	if opts.DryRun || len(allMutants) == 0 {
		return &Report{
			Root:           absRoot,
			TotalMutants:   len(allMutants),
			EvaluatedCount: 0,
			UntestedCount:  len(allMutants), // dry run: nothing is executed
			Mutants:        allMutants,
		}, nil
	}

	// Execute mutation test runner in isolated temporary file swap.
	// Every file touched is journaled (original content backed up under
	// .kern/mutation-backup/, recorded in .kern/mutation-journal-<pid>.json)
	// and restored on EVERY exit path — normal return, error, panic (defer)
	// and SIGINT/SIGTERM (signal handler). A killed run leaves a journal the
	// next Run self-heals (audit C2: mutants must never linger in the tree).
	killed := 0
	survived := 0

	journal, jerr := newMutationJournal(absRoot)
	if jerr != nil {
		// Fail closed: without a crash-safe journal we must not write mutants
		// to the real tree at all.
		return nil, fmt.Errorf("mutation: cannot create crash-safe journal: %w", jerr)
	}
	ensureSignalRestore()
	journal.register()
	defer func() {
		if r := recover(); r != nil {
			if _, rerr := journal.restore(); rerr != nil {
				fmt.Fprintf(os.Stderr, "mutation: panic restore failed: %v\n", rerr)
			}
			journal.cleanup()
			journal.unregister()
			panic(r)
		}
		if _, rerr := journal.restore(); rerr != nil {
			fmt.Fprintf(os.Stderr, "mutation: WARNING: restore failed: %v\n", rerr)
		}
		journal.cleanup()
		journal.unregister()
	}()
	fmt.Fprintf(os.Stderr, "mutation: evaluating %d mutant(s) — working-tree files are TEMPORARILY modified and are auto-restored on completion, interrupt, or crash (journal: %s)\n", len(allMutants), journal.path)

	for i := range allMutants {
		m := &allMutants[i]
		if m.Operator == "zero_return" {
			// Skip running generic zero return without explicit type analysis
			m.Status = "untested"
			continue
		}

		fullPath := filepath.Join(absRoot, m.File)
		origSrc, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		mutSrc, err := ApplyMutantToSource(m.File, origSrc, *m)
		if err != nil {
			m.Status = "compile_error"
			continue
		}

		// Journal the original content (crash-safe backup + journal entry)
		// BEFORE touching the real file; then swap the mutant in.
		if err := journal.add(fullPath, origSrc); err != nil {
			m.Status = "compile_error"
			continue
		}
		if err := os.WriteFile(fullPath, mutSrc, 0644); err != nil {
			continue
		}

		testCmd := opts.TestCommand
		if testCmd == "" {
			pkgDir := filepath.Dir(m.File)
			if pkgDir == "." {
				testCmd = "go test . -count=1"
			} else {
				testCmd = fmt.Sprintf("go test ./%s -count=1", filepath.ToSlash(pkgDir))
			}
		}

		tCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		parts := strings.Fields(testCmd)
		cmd := exec.CommandContext(tCtx, parts[0], parts[1:]...)
		cmd.Dir = absRoot
		out, testErr := cmd.CombinedOutput()
		cancel()

		// Restore original file immediately. The error is NOT dropped: if the
		// write fails, the mutant stays on disk while the journal believes it
		// was restored, so surface it loudly and abort the run. The deferred
		// journal restore retries every file and prints its own warning; the
		// caller additionally receives the error here (M3).
		if rerr := os.WriteFile(fullPath, origSrc, 0644); rerr != nil {
			fmt.Fprintf(os.Stderr, "mutation: FATAL: could not restore original %q after evaluation — the file on disk is the MUTANT, not the original: %v\n", fullPath, rerr)
			return nil, fmt.Errorf("mutation: restore original %q after test run: %w", fullPath, rerr)
		}
		if testErr != nil {
			// Test failed -> Mutant was killed (Good test coverage)
			m.Status = "killed"
			killed++
		} else {
			// Test passed despite mutation -> Mutant survived (Test gap!)
			m.Status = "survived"
			m.TestOutput = string(out)
			survived++
		}
	}

	totalEvaluated := killed + survived
	score := 0.0
	if totalEvaluated > 0 {
		score = (float64(killed) / float64(totalEvaluated)) * 100.0
	}

	untested := 0
	for i := range allMutants {
		if allMutants[i].Status == "untested" {
			untested++
		}
	}
	return &Report{
		Root:           absRoot,
		TotalMutants:   len(allMutants),
		KilledCount:    killed,
		SurvivedCount:  survived,
		EvaluatedCount: killed + survived,
		UntestedCount:  untested,
		Score:          score,
		Mutants:        allMutants,
	}, nil
}

// runIsolated evaluates mutants inside an execution.NewWorktree copy so the
// real tree is never touched. The journaled in-place mode already restores on
// every exit path and self-heals stale journals on the next run (audit C2),
// but during each mutant's test run the real tree still holds a live mutant
// (concurrent observers see corrupted code) and a SIGKILL leaves a window
// before the next run's self-heal. Isolation closes both: the real root's
// stale journals are healed BEFORE the snapshot (so the copy is built from
// originals, not lingering mutants), evaluation runs entirely in the copy,
// and the report's Root is translated back to the real root. If the snapshot
// cannot be built (e.g. tree over the size cap), it falls back to the
// journaled in-place mode with a loud warning rather than failing the tool.
func runIsolated(ctx context.Context, opts Options) (*Report, error) {
	absRoot, err := filepath.Abs(opts.Root)
	if err != nil {
		absRoot = opts.Root
	}
	// Heal the REAL root first: a stale journal from a previous interrupted
	// in-place run means the current tree holds mutants — snapshotting that
	// would evaluate mutants-of-mutants.
	if n, rerr := recoverStaleJournals(absRoot); rerr != nil {
		fmt.Fprintf(os.Stderr, "mutation: WARNING: stale journal restore incomplete: %v\n", rerr)
	} else if n > 0 {
		fmt.Fprintf(os.Stderr, "mutation: restored %d file(s) left modified by an interrupted previous mutation run (stale journal self-heal)\n", n)
	}
	wt, werr := execution.NewWorktree(opts.Root)
	if werr != nil {
		fmt.Fprintf(os.Stderr, "mutation: WARNING: isolated worktree unavailable (%v) — falling back to journaled in-place mode: working-tree files will be temporarily modified and auto-restored\n", werr)
		opts.Isolate = false
		return Run(ctx, opts)
	}
	defer func() { _ = wt.Cleanup() }()
	isoOpts := opts
	isoOpts.Isolate = false
	isoOpts.Root = wt.Dir()
	rep, rerr := Run(ctx, isoOpts)
	if rep != nil {
		rep.Root = absRoot
	}
	return rep, rerr
}

func invertBinaryOp(op token.Token) (token.Token, bool) {
	switch op {
	case token.EQL:
		return token.NEQ, true
	case token.NEQ:
		return token.EQL, true
	case token.LSS:
		return token.GEQ, true
	case token.LEQ:
		return token.GTR, true
	case token.GTR:
		return token.LEQ, true
	case token.GEQ:
		return token.LSS, true
	case token.LAND:
		return token.LOR, true
	case token.LOR:
		return token.LAND, true
	default:
		return op, false
	}
}

func shiftArithmeticOp(op token.Token) (token.Token, bool) {
	switch op {
	case token.ADD:
		return token.SUB, true
	case token.SUB:
		return token.ADD, true
	default:
		return op, false
	}
}

func exprToString(fset *token.FileSet, node ast.Node) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fset, node)
	return strings.TrimSpace(buf.String())
}
