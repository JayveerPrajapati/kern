package context

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	pyDocstringMultiDouble = regexp.MustCompile(`(?s)"""(.*?)"""`)
	pyDocstringMultiSingle = regexp.MustCompile(`(?s)'''(.*?)'''`)
	cBlockComment          = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cLineComment           = regexp.MustCompile(`(?m)^\s*//.*$`)
	pyLineComment          = regexp.MustCompile(`(?m)^\s*#.*$`)
	buildDirectiveRe       = regexp.MustCompile(`(?m)^\s*//\s*(\+build|go:build).*$`)
)

// PruneCode strips docstrings, verbose comments, and compiler directives from source code.
// For Go files, it performs AST-aware stripping using native go/ast, slicing off CommentGroups.
// For other languages, it uses deterministic parsing heuristics to keep functional signatures.
func PruneCode(path string, content []byte, terseCode bool) []byte {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		if pruned, err := PruneGo(content); err == nil {
			return pruned
		}
		return pruneGoLines(content)
	case ".py":
		return PrunePython(content)
	case ".js", ".ts", ".jsx", ".tsx", ".java", ".c", ".cpp", ".h", ".hpp", ".rs":
		return PruneCStyle(content)
	default:
		if terseCode {
			return pruneGeneric(content)
		}
		return content
	}
}

// PruneGo uses native go/ast to slice off all CommentGroup nodes and compiler directives
// from standard struct, function, and interface definitions.
func PruneGo(content []byte) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", content, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	// Slice off all comment groups from the AST
	f.Comments = nil
	f.Doc = nil

	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.File:
			node.Doc = nil
			node.Comments = nil
		case *ast.GenDecl:
			node.Doc = nil
		case *ast.FuncDecl:
			node.Doc = nil
		case *ast.TypeSpec:
			node.Doc = nil
			node.Comment = nil
		case *ast.Field:
			node.Doc = nil
			node.Comment = nil
		case *ast.ValueSpec:
			node.Doc = nil
			node.Comment = nil
		}
		return true
	})

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return nil, err
	}

	return pruneDirectivesAndEmptyLines(buf.Bytes()), nil
}

func pruneGoLines(content []byte) []byte {
	lines := strings.Split(string(content), "\n")
	var out []string
	inBlockComment := false
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if inBlockComment {
			if strings.Contains(trimmed, "*/") {
				inBlockComment = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "/*") {
			if !strings.Contains(trimmed, "*/") {
				inBlockComment = true
			}
			continue
		}
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		out = append(out, l)
	}
	return []byte(strings.Join(out, "\n"))
}

// PrunePython strips multi-line docstrings and comments from Python files.
func PrunePython(content []byte) []byte {
	s := string(content)
	s = pyDocstringMultiDouble.ReplaceAllString(s, "")
	s = pyDocstringMultiSingle.ReplaceAllString(s, "")
	s = pyLineComment.ReplaceAllString(s, "")
	return pruneBlankLines([]byte(s))
}

// PruneCStyle strips block comments and line comments from C-style languages.
func PruneCStyle(content []byte) []byte {
	s := string(content)
	s = cBlockComment.ReplaceAllString(s, "")
	s = cLineComment.ReplaceAllString(s, "")
	return pruneBlankLines([]byte(s))
}

func pruneGeneric(content []byte) []byte {
	s := string(content)
	s = cBlockComment.ReplaceAllString(s, "")
	s = cLineComment.ReplaceAllString(s, "")
	s = pyLineComment.ReplaceAllString(s, "")
	return pruneBlankLines([]byte(s))
}

func pruneDirectivesAndEmptyLines(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	var out []string
	consecutiveBlank := 0
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if buildDirectiveRe.MatchString(trimmed) || strings.HasPrefix(trimmed, "//go:build") || strings.HasPrefix(trimmed, "// +build") {
			continue
		}
		if trimmed == "" {
			consecutiveBlank++
			if consecutiveBlank > 1 {
				continue
			}
		} else {
			consecutiveBlank = 0
		}
		out = append(out, l)
	}
	return []byte(strings.Join(out, "\n"))
}

func pruneBlankLines(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	var out []string
	consecutiveBlank := 0
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			consecutiveBlank++
			if consecutiveBlank > 1 {
				continue
			}
		} else {
			consecutiveBlank = 0
		}
		out = append(out, l)
	}
	return []byte(strings.Join(out, "\n"))
}
