// This file is the structural fingerprint oracle, ported from the blueprint
// duplication check (Phase 6, spec Section 15). Fingerprints are
// identifier-independent structural summaries of Go functions: signature shape
// (param/return arity and normalized types), control-flow shape
// (if/for/range/switch/return/defer/go/assign/call counts), called symbols
// (normalized to arity), literal counts and statement counts. They feed
// probabilistic similarity comparisons (blueprint's duplication thresholds) and
// are NEVER a gate on their own.
package intel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// Fingerprint is a structural summary of a Go function, independent of
// identifier names. It captures:
//   - Signature shape (param/return arity and types, normalized)
//   - Control-flow shape (if/for/switch/return counts)
//   - Called symbols (normalized to arity)
//   - Literal structure (number of string/int/bool literals)
//   - Statement count (rough size proxy)
type Fingerprint struct {
	FuncName       string // original name (for reporting only)
	SignatureShape string // normalized signature, e.g. "func(1ptr,1int)1err"
	ParamCount     int    // number of parameters
	ReturnCount    int    // number of return values
	ControlFlow    CFFingerprint
	CalledSymbols  []string // normalized call signatures
	LiteralCount   int      // total literals
	StatementCount int      // total statements
	File           string   // source path (set by the caller)
	Line           int      // FuncDecl start line
}

// CFFingerprint captures control-flow shape counts.
type CFFingerprint struct {
	IfCount     int `json:"if"`
	ForCount    int `json:"for"`
	RangeCount  int `json:"range"`
	SwitchCount int `json:"switch"`
	ReturnCount int `json:"return"`
	DeferCount  int `json:"defer"`
	GoCount     int `json:"go"`
	AssignCount int `json:"assign"`
	CallCount   int `json:"call"`
}

// ComputeFingerprint parses a Go source file and returns fingerprints for every
// top-level function declaration in it. Line is set to each function's start
// line; File is left empty for the caller to fill in.
func ComputeFingerprint(src string) ([]Fingerprint, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var fps []Fingerprint
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		fp := fingerprintFunc(fn)
		fp.Line = fset.Position(fn.Pos()).Line
		fps = append(fps, fp)
	}
	return fps, nil
}

// fingerprintFunc computes the structural fingerprint of a single function.
func fingerprintFunc(fn *ast.FuncDecl) Fingerprint {
	fp := Fingerprint{FuncName: fn.Name.Name}
	// Signature shape.
	if fn.Type.Params != nil {
		for _, p := range fn.Type.Params.List {
			fp.ParamCount += len(p.Names)
			if len(p.Names) == 0 {
				fp.ParamCount++
			}
		}
	}
	if fn.Type.Results != nil {
		for _, r := range fn.Type.Results.List {
			fp.ReturnCount += len(r.Names)
			if len(r.Names) == 0 {
				fp.ReturnCount++
			}
		}
	}
	fp.SignatureShape = normalizeSignature(fn.Type)

	// Body analysis.
	if fn.Body != nil {
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.IfStmt:
				fp.ControlFlow.IfCount++
			case *ast.ForStmt:
				fp.ControlFlow.ForCount++
			case *ast.RangeStmt:
				fp.ControlFlow.RangeCount++
			case *ast.SwitchStmt:
				fp.ControlFlow.SwitchCount++
			case *ast.ReturnStmt:
				fp.ControlFlow.ReturnCount++
			case *ast.DeferStmt:
				fp.ControlFlow.DeferCount++
			case *ast.GoStmt:
				fp.ControlFlow.GoCount++
			case *ast.AssignStmt:
				fp.ControlFlow.AssignCount++
			case *ast.CallExpr:
				fp.ControlFlow.CallCount++
				fp.CalledSymbols = append(fp.CalledSymbols, normalizeCall(v))
			case *ast.BasicLit:
				fp.LiteralCount++
			}
			return true
		})
		fp.StatementCount = countStatements(fn.Body)
	}

	// Sort called symbols for deterministic comparison.
	sort.Strings(fp.CalledSymbols)
	return fp
}

// normalizeSignature produces an identifier-independent signature string.
// e.g. "func(req *Request) error" → "func(1ptr)1err"
func normalizeSignature(ft *ast.FuncType) string {
	var sb strings.Builder
	sb.WriteString("func(")
	if ft.Params != nil {
		first := true
		for _, p := range ft.Params.List {
			if !first {
				sb.WriteString(",")
			}
			first = false
			count := len(p.Names)
			if count == 0 {
				count = 1
			}
			sb.WriteString(strconv.Itoa(count))
			sb.WriteString(normalizeType(p.Type))
		}
	}
	sb.WriteString(")")
	if ft.Results != nil {
		total := 0
		for _, r := range ft.Results.List {
			c := len(r.Names)
			if c == 0 {
				c = 1
			}
			total += c
		}
		sb.WriteString(strconv.Itoa(total))
		// Use first result type as shape proxy.
		if len(ft.Results.List) > 0 {
			sb.WriteString(normalizeType(ft.Results.List[0].Type))
		}
	}
	return sb.String()
}

// normalizeType maps an AST type to a short, identifier-independent tag.
func normalizeType(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return typeTag(t.Name)
	case *ast.StarExpr:
		return "ptr"
	case *ast.ArrayType:
		return "slice"
	case *ast.MapType:
		return "map"
	case *ast.ChanType:
		return "chan"
	case *ast.FuncType:
		return "func"
	case *ast.InterfaceType:
		return "iface"
	case *ast.StructType:
		return "struct"
	case *ast.SelectorExpr:
		return "pkg"
	case *ast.Ellipsis:
		return "variadic"
	default:
		return "other"
	}
}

// typeTag normalizes common Go type names to short tags.
func typeTag(name string) string {
	switch name {
	case "string", "int", "int32", "int64", "uint", "uint32", "uint64", "float32", "float64", "bool", "byte", "rune":
		return name
	case "error":
		return "err"
	default:
		return "ident"
	}
}

// normalizeCall produces a normalized call signature: "name(arity)".
func normalizeCall(call *ast.CallExpr) string {
	name := "unknown"
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		name = fn.Name
	case *ast.SelectorExpr:
		if ident, ok := fn.X.(*ast.Ident); ok {
			name = ident.Name + "." + fn.Sel.Name
		} else {
			name = "." + fn.Sel.Name
		}
	}
	return name + "(" + strconv.Itoa(len(call.Args)) + ")"
}

// countStatements counts top-level statements in a block.
func countStatements(body *ast.BlockStmt) int {
	count := 0
	for _, stmt := range body.List {
		count += countStmtRecursive(stmt)
	}
	return count
}

func countStmtRecursive(stmt ast.Stmt) int {
	if stmt == nil {
		return 0
	}
	count := 1
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		for _, sub := range s.List {
			count += countStmtRecursive(sub)
		}
	case *ast.IfStmt:
		count += countStmtRecursive(s.Body)
		count += countStmtRecursive(s.Else)
	case *ast.ForStmt:
		count += countStmtRecursive(s.Body)
	case *ast.RangeStmt:
		count += countStmtRecursive(s.Body)
	case *ast.SwitchStmt:
		for _, c := range s.Body.List {
			if cc, ok := c.(*ast.CaseClause); ok {
				for _, sub := range cc.Body {
					count += countStmtRecursive(sub)
				}
			}
		}
	case *ast.SelectStmt:
		for _, c := range s.Body.List {
			if cc, ok := c.(*ast.CommClause); ok {
				for _, sub := range cc.Body {
					count += countStmtRecursive(sub)
				}
			}
		}
	}
	return count
}
