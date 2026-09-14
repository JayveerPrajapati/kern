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
	Root        string   `json:"root"`
	Files       []string `json:"files,omitempty"`
	MaxMutants  int      `json:"max_mutants,omitempty"`
	DryRun      bool     `json:"dry_run,omitempty"` // only generate mutants without running test suite
	TestCommand string   `json:"test_command,omitempty"`
	Timeout     time.Duration `json:"timeout,omitempty"`
}

// Report encapsulates the complete mutation testing results.
type Report struct {
	Root          string   `json:"root"`
	TotalMutants  int      `json:"total_mutants"`
	KilledCount   int      `json:"killed_count"`
	SurvivedCount int      `json:"survived_count"`
	Score         float64  `json:"mutation_score"` // 0-100%
	Mutants       []Mutant `json:"mutants"`
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
	if opts.Root == "" {
		opts.Root = "."
	}
	absRoot, err := filepath.Abs(opts.Root)
	if err != nil {
		absRoot = opts.Root
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
			Root:         absRoot,
			TotalMutants: len(allMutants),
			Mutants:      allMutants,
		}, nil
	}

	// Execute mutation test runner in isolated temporary file swap
	killed := 0
	survived := 0

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

		// Swap file on disk temporarily
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

		// Restore original file immediately
		_ = os.WriteFile(fullPath, origSrc, 0644)

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

	return &Report{
		Root:          absRoot,
		TotalMutants:  len(allMutants),
		KilledCount:   killed,
		SurvivedCount: survived,
		Score:         score,
		Mutants:       allMutants,
	}, nil
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
