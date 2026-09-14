// Package repair provides automated AST-level fixes for common compilation errors and lints.
package repair

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Diagnostic is a parsed compiler diagnostic.
type Diagnostic struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
	Kind    string `json:"kind"`
}

// Result is the result of applying automated AST repair.
type Result struct {
	File     string `json:"file"`
	Repaired bool   `json:"repaired"`
	Action   string `json:"action"`
	Content  string `json:"content"`
	Error    string `json:"error,omitempty"`
}

var (
	// Matches: file.go:12:3: imported and not used: "fmt"
	// Matches: file.go:12:3: "fmt" imported and not used
	unusedImportRe = regexp.MustCompile(`(?:imported and not used:\s*"?([^"\s]+)"?|"([^"]+)"\s*imported and not used)`)

	// Matches: file.go:15:2: x declared and not used
	unusedVarRe = regexp.MustCompile(`(\w+)\s+declared and not used`)

	// Matches: file.go:20:5: undefined: fmt.Println
	undefinedPkgRe = regexp.MustCompile(`undefined:\s*([a-zA-Z0-9_]+)\.[a-zA-Z0-9_]+`)

	// Line locator: path/file.go:12:3: message or file.go:12: message
	diagLineRe = regexp.MustCompile(`(?m)^([^\s:]+\.go):(\d+)(?::(\d+))?:\s*(.+)$`)
)

// ParseDiagnostics parses compiler error output into structured diagnostics.
func ParseDiagnostics(output string) []Diagnostic {
	var diags []Diagnostic
	matches := diagLineRe.FindAllStringSubmatch(output, -1)
	for _, m := range matches {
		file := m[1]
		line, _ := strconv.Atoi(m[2])
		col := 0
		if m[3] != "" {
			col, _ = strconv.Atoi(m[3])
		}
		msg := strings.TrimSpace(m[4])
		kind := "unknown"
		if unusedImportRe.MatchString(msg) {
			kind = "unused_import"
		} else if unusedVarRe.MatchString(msg) {
			kind = "unused_var"
		} else if undefinedPkgRe.MatchString(msg) {
			kind = "undefined_pkg"
		}

		diags = append(diags, Diagnostic{
			File:    file,
			Line:    line,
			Column:  col,
			Message: msg,
			Kind:    kind,
		})
	}
	return diags
}

// RepairFile applies deterministic AST repairs to src based on diagnostics.
func RepairFile(filename string, src []byte, diags []Diagnostic) (*Result, error) {
	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filename, err)
	}

	repaired := false
	actions := []string{}

	for _, d := range diags {
		switch d.Kind {
		case "unused_import":
			pkg := extractUnusedImport(d.Message)
			if pkg != "" {
				if removeImport(fileNode, pkg) {
					repaired = true
					actions = append(actions, fmt.Sprintf("removed unused import %q", pkg))
				}
			}

		case "undefined_pkg":
			pkg := extractUndefinedPkg(d.Message)
			if pkg != "" {
				if addImport(fileNode, pkg) {
					repaired = true
					actions = append(actions, fmt.Sprintf("added missing import %q", pkg))
				}
			}

		case "unused_var":
			v := extractUnusedVar(d.Message)
			if v != "" && d.Line > 0 {
				if blankAssignVar(fset, fileNode, v, d.Line) {
					repaired = true
					actions = append(actions, fmt.Sprintf("silenced unused var %q with blank identifier", v))
				}
			}
		}
	}

	if !repaired {
		return &Result{
			File:     filename,
			Repaired: false,
			Action:   "no auto-repairable diagnostics found",
			Content:  string(src),
		}, nil
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, fileNode); err != nil {
		return nil, fmt.Errorf("format %s: %w", filename, err)
	}

	return &Result{
		File:     filename,
		Repaired: true,
		Action:   strings.Join(actions, "; "),
		Content:  buf.String(),
	}, nil
}

// RepairRoot walks compiler diagnostics, reads files from root, and applies repairs.
func RepairRoot(root, compilerOutput string, apply bool) ([]Result, error) {
	diags := ParseDiagnostics(compilerOutput)
	if len(diags) == 0 {
		return nil, nil
	}

	byFile := make(map[string][]Diagnostic)
	for _, d := range diags {
		byFile[d.File] = append(byFile[d.File], d)
	}

	var results []Result
	for relPath, fileDiags := range byFile {
		fullPath := relPath
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(root, relPath)
		}

		src, err := os.ReadFile(fullPath)
		if err != nil {
			results = append(results, Result{
				File:     relPath,
				Repaired: false,
				Error:    err.Error(),
			})
			continue
		}

		res, err := RepairFile(relPath, src, fileDiags)
		if err != nil {
			results = append(results, Result{
				File:     relPath,
				Repaired: false,
				Error:    err.Error(),
			})
			continue
		}

		if apply && res.Repaired {
			_ = os.WriteFile(fullPath, []byte(res.Content), 0o644)
		}
		results = append(results, *res)
	}

	return results, nil
}

func extractUnusedImport(msg string) string {
	m := unusedImportRe.FindStringSubmatch(msg)
	if len(m) > 1 && m[1] != "" {
		return m[1]
	}
	if len(m) > 2 && m[2] != "" {
		return m[2]
	}
	return ""
}

func extractUndefinedPkg(msg string) string {
	m := undefinedPkgRe.FindStringSubmatch(msg)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func extractUnusedVar(msg string) string {
	m := unusedVarRe.FindStringSubmatch(msg)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func removeImport(f *ast.File, importPath string) bool {
	cleanPath := strings.Trim(importPath, `"`)
	changed := false

	// Remove from f.Imports
	newImports := make([]*ast.ImportSpec, 0, len(f.Imports))
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == cleanPath || filepath.Base(path) == cleanPath {
			changed = true
			continue
		}
		newImports = append(newImports, imp)
	}
	f.Imports = newImports

	// Remove from f.Decls
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			continue
		}
		newSpecs := make([]ast.Spec, 0, len(gen.Specs))
		for _, spec := range gen.Specs {
			imp, ok := spec.(*ast.ImportSpec)
			if !ok {
				newSpecs = append(newSpecs, spec)
				continue
			}
			path := strings.Trim(imp.Path.Value, `"`)
			if path == cleanPath || filepath.Base(path) == cleanPath {
				continue
			}
			newSpecs = append(newSpecs, spec)
		}
		gen.Specs = newSpecs
	}

	return changed
}

func addImport(f *ast.File, pkgName string) bool {
	// Standard library known mapping
	stdPkgs := map[string]string{
		"fmt":     "fmt",
		"os":      "os",
		"strings": "strings",
		"bytes":   "bytes",
		"time":    "time",
		"context": "context",
		"sync":    "sync",
		"json":    "encoding/json",
		"io":      "io",
		"math":    "math",
		"sort":    "sort",
		"errors":  "errors",
		"log":     "log",
		"http":    "net/http",
	}

	importPath, ok := stdPkgs[pkgName]
	if !ok {
		importPath = pkgName
	}

	// Check if already imported
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) == importPath {
			return false
		}
	}

	spec := &ast.ImportSpec{
		Path: &ast.BasicLit{
			Kind:  token.STRING,
			Value: strconv.Quote(importPath),
		},
	}
	f.Imports = append(f.Imports, spec)

	// Add to existing import GenDecl or prepend new GenDecl
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if ok && gen.Tok == token.IMPORT {
			gen.Specs = append(gen.Specs, spec)
			return true
		}
	}

	newDecl := &ast.GenDecl{
		Tok:   token.IMPORT,
		Specs: []ast.Spec{spec},
	}
	f.Decls = append([]ast.Decl{newDecl}, f.Decls...)
	return true
}

func blankAssignVar(fset *token.FileSet, f *ast.File, varName string, line int) bool {
	inserted := false
	ast.Inspect(f, func(n ast.Node) bool {
		if inserted {
			return false
		}
		if block, ok := n.(*ast.BlockStmt); ok {
			startLine := fset.Position(block.Pos()).Line
			endLine := fset.Position(block.End()).Line
			if line >= startLine && line <= endLine {
				// Insert `_ = varName` at the end of the block or next position
				assign := &ast.AssignStmt{
					Lhs: []ast.Expr{ast.NewIdent("_")},
					Tok: token.ASSIGN,
					Rhs: []ast.Expr{ast.NewIdent(varName)},
				}
				block.List = append(block.List, assign)
				inserted = true
				return false
			}
		}
		return true
	})
	return inserted
}
