// Package synthtest generates synthetic reproduction and edge-case unit tests.
package synthtest

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/JayveerPrajapati/kern/internal/diff"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// Request defines parameters for synthesizing tests.
type Request struct {
	Target  string       // Function or method name to test (e.g. "Compute", "Config.Validate")
	File    string       // Path to source file containing the function
	Code    string       // Raw code string if file is not on disk
	Root    string       // Project root directory
	AutoGap bool         // If true and Target is empty, pick top untested gap from index
	Apply   bool         // If true, writes test to <file>_test.go
	Index   *index.Index // Optional preloaded index
}

// Result describes the generated test harness.
type Result struct {
	TargetSymbol string   `json:"target_symbol"`
	TargetFile   string   `json:"target_file"`
	TestFile     string   `json:"test_file"`
	TestFunction string   `json:"test_function"`
	TestCode     string   `json:"test_code"`
	Diff         string   `json:"diff,omitempty"`
	Applied      bool     `json:"applied"`
	Cases        []string `json:"cases"`
	Message      string   `json:"message,omitempty"`
}

type paramInfo struct {
	name    string
	typeStr string
}

// wantCase is the statically-derived expectation for one generated argument
// set: known is true when every non-error return value was constant-folded,
// in which case exprs holds one Go literal expression per non-error return
// (in declaration order). known=false means the value is not statically
// derivable and the generated test must NOT assert a want (call-only smoke
// assertion) — never a wrong zero-value want (F9).
type wantCase struct {
	known bool
	exprs []string
}

// foldEnv constant-folds pure-expression function bodies over literal
// arguments. A function is foldable when its body is a single return
// statement whose expressions are built from int/float/string/bool literals,
// binary arithmetic (+ - * / %), comparisons, and calls to other foldable
// functions in the same file (cycle-safe). Anything else (globals, maps,
// slices, structs, control flow) is not foldable.
type foldEnv struct {
	funcs    map[string]*ast.FuncDecl
	visiting map[string]bool // in-progress function names (recursion guard)
}

func newFoldEnv(f *ast.File) *foldEnv {
	fe := &foldEnv{funcs: map[string]*ast.FuncDecl{}, visiting: map[string]bool{}}
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			fe.funcs[fn.Name.Name] = fn
		}
	}
	return fe
}

// evalArgs folds the generated default-argument expressions (e.g. "1",
// `"test"`, "true") into concrete values, or reports not-foldable for
// non-literal defaults (context.Background(), &T{}, []string{...}).
func (fe *foldEnv) evalArgs(argExprs []string) ([]any, bool) {
	vals := make([]any, 0, len(argExprs))
	for _, a := range argExprs {
		expr, err := parser.ParseExpr(a)
		if err != nil {
			return nil, false
		}
		v, ok := fe.evalExpr(expr, map[string]any{})
		if !ok {
			return nil, false
		}
		vals = append(vals, v)
	}
	return vals, true
}

// evalBody evaluates a foldable function's single return statement with the
// given positional argument values. It returns the folded values of the
// non-error results in declaration order; error results are tolerated (e.g.
// `return a*b, nil` folds the int and skips the nil).
func (fe *foldEnv) evalBody(fn *ast.FuncDecl, args []any) ([]any, bool) {
	if fe.visiting[fn.Name.Name] {
		return nil, false // recursion: not foldable
	}
	fe.visiting[fn.Name.Name] = true
	defer delete(fe.visiting, fn.Name.Name)
	if fn.Body == nil || len(fn.Body.List) != 1 {
		return nil, false
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) == 0 {
		return nil, false
	}
	env := map[string]any{}
	if fn.Type.Params != nil {
		idx := 0
		for _, field := range fn.Type.Params.List {
			if _, isVariadic := field.Type.(*ast.Ellipsis); isVariadic {
				return nil, false
			}
			for _, id := range field.Names {
				if idx >= len(args) {
					return nil, false
				}
				env[id.Name] = args[idx]
				idx++
			}
		}
	}
	resTypes := resultTypeStrings(fn)
	var out []any
	for i, r := range ret.Results {
		if i < len(resTypes) && resTypes[i] == "error" {
			continue
		}
		v, ok := fe.evalExpr(r, env)
		if !ok {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

// evalExpr evaluates a pure literal expression, or reports not-foldable.
// env maps parameter names to their folded argument values.
func (fe *foldEnv) evalExpr(e ast.Expr, env map[string]any) (any, bool) {
	switch n := e.(type) {
	case *ast.BasicLit:
		switch n.Kind {
		case token.INT:
			v, err := strconv.ParseInt(n.Value, 0, 64)
			if err != nil {
				return nil, false
			}
			return v, true
		case token.FLOAT:
			v, err := strconv.ParseFloat(n.Value, 64)
			if err != nil {
				return nil, false
			}
			return v, true
		case token.STRING:
			v, err := strconv.Unquote(n.Value)
			if err != nil {
				return nil, false
			}
			return v, true
		}
		return nil, false
	case *ast.ParenExpr:
		return fe.evalExpr(n.X, env)
	case *ast.UnaryExpr:
		switch n.Op {
		case token.SUB:
			v, ok := fe.evalExpr(n.X, env)
			if !ok {
				return nil, false
			}
			switch t := v.(type) {
			case int64:
				return -t, true
			case float64:
				return -t, true
			}
		case token.ADD:
			return fe.evalExpr(n.X, env)
		case token.NOT:
			v, ok := fe.evalExpr(n.X, env)
			if !ok {
				return nil, false
			}
			if b, ok := v.(bool); ok {
				return !b, true
			}
		}
		return nil, false
	case *ast.BinaryExpr:
		return fe.evalBinary(n, env)
	case *ast.Ident:
		switch n.Name {
		case "true":
			return true, true
		case "false":
			return false, true
		}
		if v, ok := env[n.Name]; ok {
			return v, true
		}
		return nil, false // global/package-level identifier (map, var, ...)
	case *ast.CallExpr:
		fun, ok := n.Fun.(*ast.Ident)
		if !ok {
			return nil, false
		}
		callee, ok := fe.funcs[fun.Name]
		if !ok {
			return nil, false
		}
		var args []any
		for _, a := range n.Args {
			v, ok := fe.evalExpr(a, env)
			if !ok {
				return nil, false
			}
			args = append(args, v)
		}
		vals, ok := fe.evalBody(callee, args)
		if !ok || len(vals) != 1 {
			return nil, false
		}
		return vals[0], true
	}
	return nil, false
}

// evalBinary folds + - * / % and comparisons over same-typed scalar values.
func (fe *foldEnv) evalBinary(n *ast.BinaryExpr, env map[string]any) (any, bool) {
	l, ok := fe.evalExpr(n.X, env)
	if !ok {
		return nil, false
	}
	r, ok := fe.evalExpr(n.Y, env)
	if !ok {
		return nil, false
	}
	switch n.Op {
	case token.ADD:
		switch lv := l.(type) {
		case int64:
			if rv, ok := r.(int64); ok {
				return lv + rv, true
			}
		case float64:
			if rv, ok := r.(float64); ok {
				return lv + rv, true
			}
		case string:
			if rv, ok := r.(string); ok {
				return lv + rv, true // string concatenation
			}
		}
	case token.SUB:
		switch lv := l.(type) {
		case int64:
			if rv, ok := r.(int64); ok {
				return lv - rv, true
			}
		case float64:
			if rv, ok := r.(float64); ok {
				return lv - rv, true
			}
		}
	case token.MUL:
		switch lv := l.(type) {
		case int64:
			if rv, ok := r.(int64); ok {
				return lv * rv, true
			}
		case float64:
			if rv, ok := r.(float64); ok {
				return lv * rv, true
			}
		}
	case token.QUO:
		switch lv := l.(type) {
		case int64:
			if rv, ok := r.(int64); ok {
				if rv == 0 {
					return nil, false // division by zero is not a value
				}
				return lv / rv, true
			}
		case float64:
			if rv, ok := r.(float64); ok {
				if rv == 0 {
					return nil, false
				}
				return lv / rv, true
			}
		}
	case token.REM:
		if lv, ok := l.(int64); ok {
			if rv, ok := r.(int64); ok {
				if rv == 0 {
					return nil, false
				}
				return lv % rv, true
			}
		}
	case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
		return compareValues(n.Op, l, r)
	}
	return nil, false
}

// compareValues compares two same-typed scalar values with a comparison
// operator, returning the boolean result.
func compareValues(op token.Token, l, r any) (any, bool) {
	switch lv := l.(type) {
	case int64:
		rv, ok := r.(int64)
		if !ok {
			return nil, false
		}
		switch op {
		case token.EQL:
			return lv == rv, true
		case token.NEQ:
			return lv != rv, true
		case token.LSS:
			return lv < rv, true
		case token.GTR:
			return lv > rv, true
		case token.LEQ:
			return lv <= rv, true
		case token.GEQ:
			return lv >= rv, true
		}
	case float64:
		rv, ok := r.(float64)
		if !ok {
			return nil, false
		}
		switch op {
		case token.EQL:
			return lv == rv, true
		case token.NEQ:
			return lv != rv, true
		case token.LSS:
			return lv < rv, true
		case token.GTR:
			return lv > rv, true
		case token.LEQ:
			return lv <= rv, true
		case token.GEQ:
			return lv >= rv, true
		}
	case string:
		rv, ok := r.(string)
		if !ok {
			return nil, false
		}
		switch op {
		case token.EQL:
			return lv == rv, true
		case token.NEQ:
			return lv != rv, true
		case token.LSS:
			return lv < rv, true
		case token.GTR:
			return lv > rv, true
		case token.LEQ:
			return lv <= rv, true
		case token.GEQ:
			return lv >= rv, true
		}
	case bool:
		rv, ok := r.(bool)
		if !ok {
			return nil, false
		}
		switch op {
		case token.EQL:
			return lv == rv, true
		case token.NEQ:
			return lv != rv, true
		}
	}
	return nil, false
}

// resultTypeStrings returns the type strings of a function's result list,
// expanded per named result.
func resultTypeStrings(fn *ast.FuncDecl) []string {
	if fn.Type.Results == nil {
		return nil
	}
	fset := token.NewFileSet()
	var out []string
	for _, field := range fn.Type.Results.List {
		ts := extractTypeStr(fset, field.Type)
		n := len(field.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			out = append(out, ts)
		}
	}
	return out
}

// basicReturnKind classifies a scalar return type, or "" for non-basic types
// (slices, structs, maps, pointers) which are never statically asserted.
func basicReturnKind(typeStr string) string {
	switch typeStr {
	case "string":
		return "string"
	case "bool":
		return "bool"
	case "float64", "float32":
		return "float"
	case "int", "int64", "int32", "int16", "int8", "rune",
		"uint", "uint64", "uint32", "uint16", "uint8", "byte":
		return "int"
	}
	return ""
}

// literalFromValue renders a folded value as a Go literal expression, checking
// that its kind matches the declared return type (an int result cannot be
// asserted against a string-typed return).
func literalFromValue(v any, typeStr string) (string, bool) {
	kind := basicReturnKind(typeStr)
	switch t := v.(type) {
	case int64:
		if kind != "int" {
			return "", false
		}
		return strconv.FormatInt(t, 10), true
	case float64:
		if kind != "float" {
			return "", false
		}
		return strconv.FormatFloat(t, 'g', -1, 64), true
	case string:
		if kind != "string" {
			return "", false
		}
		return strconv.Quote(t), true
	case bool:
		if kind != "bool" {
			return "", false
		}
		return strconv.FormatBool(t), true
	}
	return "", false
}

// foldWants computes the exact expected non-error return values for one
// generated argument set (zeroValue selects the zero-value boundary args vs
// the standard valid args). known=false when the function's body is not a
// pure foldable expression over literals, or a generated default argument is
// not a literal.
func foldWants(fe *foldEnv, fn *ast.FuncDecl, params []paramInfo, zeroValue bool, returns []paramInfo) wantCase {
	argExprs := make([]string, 0, len(params))
	for _, p := range params {
		argExprs = append(argExprs, defaultValueForType(p.typeStr, zeroValue))
	}
	args, ok := fe.evalArgs(argExprs)
	if !ok {
		return wantCase{known: false}
	}
	vals, ok := fe.evalBody(fn, args)
	if !ok {
		return wantCase{known: false}
	}
	var nonErr []paramInfo
	for _, r := range returns {
		if r.typeStr != "error" {
			nonErr = append(nonErr, r)
		}
	}
	if len(vals) != len(nonErr) {
		return wantCase{known: false}
	}
	wc := wantCase{known: true}
	for i, v := range vals {
		lit, ok := literalFromValue(v, nonErr[i].typeStr)
		if !ok {
			return wantCase{known: false}
		}
		wc.exprs = append(wc.exprs, lit)
	}
	return wc
}

// confinePath resolves p against root (nearest-existing-ancestor symlink
// resolution, re-appending the remaining components) and rejects any path
// that escapes root — "..", absolute paths outside the root, and symlinked
// parents (root/link -> /etc) that would smuggle a read or write outside the
// workspace. It returns the absolute cleaned path on success.
func confinePath(root, p string) (string, error) {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = filepath.Clean(cwd)
		} else {
			root = "."
		}
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Join(root, p)
	}
	real, err := nearestExisting(abs)
	if err != nil {
		return "", err
	}
	rr, err := filepath.EvalSymlinks(root)
	if err != nil {
		rr = root
	}
	rel, err := filepath.Rel(rr, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("kern_synthesize_test: file %q escapes workspace root", p)
	}
	return abs, nil
}

// nearestExisting resolves the real location of the nearest existing ancestor
// of abs (walking up until EvalSymlinks succeeds) and re-appends the
// remaining components, so a not-yet-existing file under a symlinked
// directory is judged by its real location.
func nearestExisting(abs string) (string, error) {
	var rem []string
	probe := abs
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if len(rem) == 0 {
				return real, nil
			}
			return filepath.Join(append([]string{real}, rem...)...), nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", fmt.Errorf("kern_synthesize_test: cannot resolve %q", abs)
		}
		rem = append([]string{filepath.Base(probe)}, rem...)
		probe = parent
	}
}

// Synthesize generates a deterministic, idiomatic table-driven test for a function.
func Synthesize(req Request) (*Result, error) {
	root := req.Root
	if root == "" {
		root = "."
	}

	targetFile := req.File
	targetSymbol := strings.TrimSpace(req.Target)

	// If AutoGap is true and Target is empty, find the top untested hotspot from index
	if targetSymbol == "" && req.AutoGap && req.Index != nil {
		gaps := intel.TestGaps(req.Index, 1)
		if len(gaps) > 0 {
			targetSymbol = gaps[0].Symbol
			if targetFile == "" {
				targetFile = gaps[0].File
			}
		}
	}

	var srcBytes []byte
	if req.Code != "" {
		srcBytes = []byte(req.Code)
		if targetFile == "" {
			targetFile = "source.go"
		}
	} else if targetFile != "" {
		p := targetFile
		if !filepath.IsAbs(p) && root != "" {
			p = filepath.Join(root, p)
		}
		if _, cerr := confinePath(root, p); cerr != nil {
			return nil, cerr
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read source file: %w", err)
		}
		srcBytes = b
	} else {
		return nil, fmt.Errorf("either target file or code snippet must be provided")
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, targetFile, srcBytes, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse source file: %w", err)
	}

	pkgName := f.Name.Name

	// Locate the target function/method
	var targetDecl *ast.FuncDecl
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fn.Name.Name
		var fullName string
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			recvStr := extractTypeStr(fset, fn.Recv.List[0].Type)
			recvStr = strings.TrimPrefix(recvStr, "*")
			fullName = recvStr + "." + name
		} else {
			fullName = name
		}

		if targetSymbol == "" {
			// If still empty, pick the first exported function or any function
			if unicode.IsUpper(rune(name[0])) || targetDecl == nil {
				targetDecl = fn
				targetSymbol = fullName
			}
		} else if fullName == targetSymbol || name == targetSymbol {
			targetDecl = fn
			targetSymbol = fullName
			break
		}
	}

	if targetDecl == nil {
		return nil, fmt.Errorf("symbol %q not found in %s", targetSymbol, targetFile)
	}

	// Extract parameters
	var params []paramInfo
	for i, field := range targetDecl.Type.Params.List {
		typeStr := extractTypeStr(fset, field.Type)
		if len(field.Names) == 0 {
			params = append(params, paramInfo{
				name:    fmt.Sprintf("arg%d", i),
				typeStr: typeStr,
			})
		} else {
			for _, id := range field.Names {
				params = append(params, paramInfo{
					name:    id.Name,
					typeStr: typeStr,
				})
			}
		}
	}

	// Extract return types
	var returns []paramInfo
	hasError := false
	if targetDecl.Type.Results != nil {
		for i, field := range targetDecl.Type.Results.List {
			typeStr := extractTypeStr(fset, field.Type)
			name := fmt.Sprintf("out%d", i)
			if len(field.Names) > 0 {
				name = field.Names[0].Name
			}
			if typeStr == "error" {
				hasError = true
			}
			returns = append(returns, paramInfo{name: name, typeStr: typeStr})
		}
	}

	// Determine receiver details if method
	var recvType string
	var isPointerRecv bool
	if targetDecl.Recv != nil && len(targetDecl.Recv.List) > 0 {
		rawRecv := extractTypeStr(fset, targetDecl.Recv.List[0].Type)
		if strings.HasPrefix(rawRecv, "*") {
			isPointerRecv = true
			recvType = strings.TrimPrefix(rawRecv, "*")
		} else {
			recvType = rawRecv
		}
	}

	// Construct test function name
	testFuncName := "Test" + exportName(targetDecl.Name.Name)
	if recvType != "" {
		testFuncName = "Test" + exportName(recvType) + "_" + exportName(targetDecl.Name.Name)
	}

	// Synthesize the test body. Statically derive the expected return values
	// for the generated argument sets (standard + zero-value boundary) via
	// constant folding: foldable functions get exact want assertions;
	// non-foldable ones get a call-only smoke assertion with an explanatory
	// comment — never a wrong zero-value want (F9).
	casesList := []string{"standard valid input", "zero value boundary"}
	fe := newFoldEnv(f)
	stdWant := foldWants(fe, targetDecl, params, false, returns)
	zeroWant := foldWants(fe, targetDecl, params, true, returns)
	testCode := generateTestFunction(testFuncName, targetDecl.Name.Name, recvType, isPointerRecv, params, returns, hasError, stdWant, zeroWant)

	// Determine test file path
	testFilePath := targetFile
	if strings.HasSuffix(testFilePath, "_test.go") {
		// already a test file
	} else if strings.HasSuffix(testFilePath, ".go") {
		testFilePath = strings.TrimSuffix(testFilePath, ".go") + "_test.go"
	} else {
		testFilePath = targetFile + "_test.go"
	}

	// Check if test file exists on disk
	var diskPath string
	if filepath.IsAbs(testFilePath) {
		diskPath = testFilePath
	} else {
		diskPath = filepath.Join(root, testFilePath)
	}
	// Reject ".." escapes and symlinked-parent escapes before touching disk:
	// the derived test file must stay inside the workspace root.
	if _, cerr := confinePath(root, diskPath); cerr != nil {
		return nil, cerr
	}

	var finalTestContent string
	var oldTestContent string
	existingBytes, err := os.ReadFile(diskPath)
	if err == nil && len(existingBytes) > 0 {
		oldTestContent = string(existingBytes)
		if strings.Contains(oldTestContent, "func "+testFuncName+"(") {
			return &Result{
				TargetSymbol: targetSymbol,
				TargetFile:   targetFile,
				TestFile:     testFilePath,
				TestFunction: testFuncName,
				TestCode:     testCode,
				Message:      fmt.Sprintf("test function %s already exists in %s", testFuncName, testFilePath),
			}, nil
		}
		// Append test to existing test file
		mergedContent, merr := appendTestToFile(existingBytes, testCode)
		if merr == nil {
			finalTestContent = mergedContent
		} else {
			finalTestContent = oldTestContent + "\n\n" + testCode + "\n"
		}
	} else {
		// Scaffold fresh test file
		var sb strings.Builder
		sb.WriteString("package " + pkgName + "\n\n")
		sb.WriteString("import (\n\t\"testing\"\n")
		// reflect is only needed when an exact want is asserted with
		// reflect.DeepEqual; a smoke-only test never emits it.
		if needsReflect(returns, hasError) && (stdWant.known || zeroWant.known) {
			sb.WriteString("\t\"reflect\"\n")
		}
		if needsContext(params) {
			sb.WriteString("\t\"context\"\n")
		}
		sb.WriteString(")\n\n")
		sb.WriteString(testCode)
		sb.WriteString("\n")

		formatted, ferr := format.Source([]byte(sb.String()))
		if ferr == nil {
			finalTestContent = string(formatted)
		} else {
			finalTestContent = sb.String()
		}
	}

	applied := false
	if req.Apply && diskPath != "" {
		if _, cerr := confinePath(root, diskPath); cerr != nil {
			return nil, cerr
		}
		if werr := os.WriteFile(diskPath, []byte(finalTestContent), 0o644); werr != nil {
			return nil, fmt.Errorf("write test file: %w", werr)
		}
		applied = true
	}

	oldLines := strings.Split(oldTestContent, "\n")
	newLines := strings.Split(finalTestContent, "\n")
	diffStr := diff.Unified(testFilePath, testFilePath, oldLines, newLines)

	return &Result{
		TargetSymbol: targetSymbol,
		TargetFile:   targetFile,
		TestFile:     testFilePath,
		TestFunction: testFuncName,
		TestCode:     testCode,
		Diff:         diffStr,
		Applied:      applied,
		Cases:        casesList,
	}, nil
}

// generateTestFunction emits the table-driven test. std/zero carry the
// constant-folded expectations for the "standard valid input" and "zero value
// boundary" cases (wantCase.known=false → no exact want is asserted):
//   - both known   → exact want columns, straight assertions;
//   - one known    → want columns plus a wantKnown gate so the unknown case
//     is exercised but never asserts a fabricated value;
//   - neither known → no want columns at all: a call-only smoke assertion
//     under a "// expected value not statically derivable" comment.
func generateTestFunction(testFuncName, funcName, recvType string, isPtrRecv bool, params, returns []paramInfo, hasError bool, std, zero wantCase) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("func %s(t *testing.T) {\n", testFuncName))

	hasWants := std.known || zero.known
	mixed := std.known != zero.known

	// Parameter columns are keyed by parameter name, but the case struct also
	// owns reserved columns: the case title (name), the wantKnown/wantErr
	// gates and every want<Return> column. A parameter named "name" (or
	// "wantOut", …) would declare the same field twice — a compile error —
	// and shadow the case title in t.Run(tt.name, …), so rename colliding
	// parameters to arg<Name>.
	reservedField := func(n string) bool {
		return n == "name" || n == "wantKnown" || n == "wantErr" || strings.HasPrefix(n, "want")
	}
	fields := make([]string, len(params))
	for i, p := range params {
		f := p.name
		if reservedField(f) {
			f = "arg" + exportName(f)
		}
		fields[i] = f
	}
	// Table-driven struct definition
	sb.WriteString("\ttests := []struct {\n")
	sb.WriteString("\t\tname string\n")
	for i, p := range params {
		sb.WriteString(fmt.Sprintf("\t\t%s %s\n", fields[i], p.typeStr))
	}
	if hasWants {
		for _, r := range returns {
			if r.typeStr != "error" {
				sb.WriteString(fmt.Sprintf("\t\twant%s %s\n", exportName(r.name), r.typeStr))
			}
		}
		if mixed {
			sb.WriteString("\t\twantKnown bool\n")
		}
	}
	if hasError {
		sb.WriteString("\t\twantErr bool\n")
	}
	sb.WriteString("\t}{\n")

	stdArgs := make([]string, len(params))
	zeroArgs := make([]string, len(params))
	for i, p := range params {
		stdArgs[i] = defaultValueForType(p.typeStr, false)
		zeroArgs[i] = defaultValueForType(p.typeStr, true)
	}

	writeRow := func(name string, args []string, wc wantCase) {
		sb.WriteString("\t\t{\n")
		sb.WriteString(fmt.Sprintf("\t\t\tname: %q,\n", name))
		for i := range params {
			sb.WriteString(fmt.Sprintf("\t\t\t%s: %s,\n", fields[i], args[i]))
		}
		if hasWants {
			nonErrIdx := 0
			for _, r := range returns {
				if r.typeStr == "error" {
					continue
				}
				// Unknown cases still need a compiling placeholder in the
				// row; it is never asserted because wantKnown=false gates it.
				expr := defaultValueForType(r.typeStr, true)
				if wc.known && nonErrIdx < len(wc.exprs) {
					expr = wc.exprs[nonErrIdx]
				}
				nonErrIdx++
				sb.WriteString(fmt.Sprintf("\t\t\twant%s: %s,\n", exportName(r.name), expr))
			}
			if mixed {
				sb.WriteString(fmt.Sprintf("\t\t\twantKnown: %t,\n", wc.known))
			}
		}
		if hasError {
			sb.WriteString("\t\t\twantErr: false,\n")
		}
		sb.WriteString("\t\t},\n")
	}
	writeRow("standard valid input", stdArgs, std)
	writeRow("zero value boundary", zeroArgs, zero)

	sb.WriteString("\t}\n\n")

	// Runner loop
	sb.WriteString("\tfor _, tt := range tests {\n")
	sb.WriteString("\t\tt.Run(tt.name, func(t *testing.T) {\n")

	// Receiver setup if method
	callPrefix := ""
	if recvType != "" {
		if isPtrRecv {
			sb.WriteString(fmt.Sprintf("\t\t\treceiver := &%s{}\n", recvType))
		} else {
			sb.WriteString(fmt.Sprintf("\t\t\treceiver := %s{}\n", recvType))
		}
		callPrefix = "receiver."
	}

	// Arguments list
	var argNames []string
	for i := range params {
		argNames = append(argNames, "tt."+fields[i])
	}
	argsCall := strings.Join(argNames, ", ")

	// Invocation & assertion
	if len(returns) == 0 {
		sb.WriteString(fmt.Sprintf("\t\t\t%s%s(%s)\n", callPrefix, funcName, argsCall))
	} else if len(returns) == 1 && hasError {
		if !hasWants {
			sb.WriteString("\t\t\t// expected value not statically derivable — call-only smoke assertion\n")
		}
		sb.WriteString(fmt.Sprintf("\t\t\terr := %s%s(%s)\n", callPrefix, funcName, argsCall))
		sb.WriteString("\t\t\tif (err != nil) != tt.wantErr {\n")
		sb.WriteString(fmt.Sprintf("\t\t\t\tt.Errorf(\"%s() error = %%v, wantErr %%v\", err, tt.wantErr)\n", funcName))
		sb.WriteString("\t\t\t}\n")
	} else {
		if hasWants && mixed {
			// Unknown case: no want is derivable, and calling with these args
			// can be guaranteed to panic at runtime (e.g. a zero-value
			// division by zero) — that would crash the test suite instead of
			// asserting anything. Skip the case honestly rather than assert a
			// fabricated want; the known case's call already exercises the
			// function.
			sb.WriteString("\t\t\tif !tt.wantKnown {\n")
			sb.WriteString("\t\t\t\t// expected value not statically derivable for this case — skipped, never assert a wrong want\n")
			sb.WriteString("\t\t\t\treturn\n")
			sb.WriteString("\t\t\t}\n")
		}
		var retVars []string
		for _, r := range returns {
			if r.typeStr == "error" {
				retVars = append(retVars, "err")
			} else if hasWants {
				retVars = append(retVars, "got"+exportName(r.name))
			} else {
				retVars = append(retVars, "_")
			}
		}
		// `_ = f(...)` (all blanks) is an assignment, not a declaration.
		declOp := ":="
		if !hasWants && !hasError {
			declOp = "="
		}
		if !hasWants {
			sb.WriteString("\t\t\t// expected value not statically derivable — call-only smoke assertion\n")
		}
		sb.WriteString(fmt.Sprintf("\t\t\t%s %s %s%s(%s)\n", strings.Join(retVars, ", "), declOp, callPrefix, funcName, argsCall))
		if hasError {
			sb.WriteString("\t\t\tif (err != nil) != tt.wantErr {\n")
			sb.WriteString(fmt.Sprintf("\t\t\t\tt.Errorf(\"%s() error = %%v, wantErr %%v\", err, tt.wantErr)\n", funcName))
			sb.WriteString("\t\t\t\treturn\n")
			sb.WriteString("\t\t\t}\n")
		}
		if hasWants {
			for _, r := range returns {
				if r.typeStr != "error" {
					varName := "got" + exportName(r.name)
					wantName := "tt.want" + exportName(r.name)
					if isBasicType(r.typeStr) {
						sb.WriteString(fmt.Sprintf("\t\t\tif %s != %s {\n", varName, wantName))
						sb.WriteString(fmt.Sprintf("\t\t\t\tt.Errorf(\"%s() %s = %%v, want %%v\", %s, %s)\n", funcName, varName, varName, wantName))
						sb.WriteString("\t\t\t}\n")
					} else {
						sb.WriteString(fmt.Sprintf("\t\t\tif !reflect.DeepEqual(%s, %s) {\n", varName, wantName))
						sb.WriteString(fmt.Sprintf("\t\t\t\tt.Errorf(\"%s() %s = %%v, want %%v\", %s, %s)\n", funcName, varName, varName, wantName))
						sb.WriteString("\t\t\t}\n")
					}
				}
			}
		}
	}

	sb.WriteString("\t\t})\n")
	sb.WriteString("\t}\n")
	sb.WriteString("}")

	return sb.String()
}

func appendTestToFile(existingBytes []byte, newTestCode string) (string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", existingBytes, parser.ParseComments)
	if err != nil {
		return "", err
	}

	// Check if reflect or context are needed in newTestCode
	hasReflect := strings.Contains(newTestCode, "reflect.DeepEqual")
	hasContext := strings.Contains(newTestCode, "context.Background()")

	existingImports := map[string]bool{}
	for _, imp := range f.Imports {
		existingImports[strings.Trim(imp.Path.Value, `"`)] = true
	}

	var missingImports []string
	if hasReflect && !existingImports["reflect"] {
		missingImports = append(missingImports, `"reflect"`)
	}
	if hasContext && !existingImports["context"] {
		missingImports = append(missingImports, `"context"`)
	}

	content := string(existingBytes)
	if len(missingImports) > 0 {
		importPos := strings.Index(content, "import (")
		if importPos != -1 {
			insertIdx := importPos + len("import (\n")
			var ins strings.Builder
			for _, m := range missingImports {
				ins.WriteString("\t" + m + "\n")
			}
			content = content[:insertIdx] + ins.String() + content[insertIdx:]
		}
	}

	combined := strings.TrimSpace(content) + "\n\n" + newTestCode + "\n"
	formatted, err := format.Source([]byte(combined))
	if err == nil {
		return string(formatted), nil
	}
	return combined, nil
}

func defaultValueForType(typeStr string, zeroValue bool) string {
	switch typeStr {
	case "string":
		if zeroValue {
			return `""`
		}
		return `"test"`
	case "int", "int64", "int32", "int16", "int8", "rune":
		if zeroValue {
			return "0"
		}
		return "1"
	case "uint", "uint64", "uint32", "uint16", "uint8", "byte":
		if zeroValue {
			return "0"
		}
		return "1"
	case "float64", "float32":
		if zeroValue {
			return "0.0"
		}
		return "1.5"
	case "bool":
		if zeroValue {
			return "false"
		}
		return "true"
	case "context.Context":
		return "context.Background()"
	case "error":
		return "nil"
	case "[]byte":
		if zeroValue {
			return "nil"
		}
		return `[]byte("test")`
	case "[]string":
		if zeroValue {
			return "nil"
		}
		return `[]string{"test"}`
	default:
		if strings.HasPrefix(typeStr, "*") {
			if zeroValue {
				return "nil"
			}
			return "&" + strings.TrimPrefix(typeStr, "*") + "{}"
		}
		if strings.HasPrefix(typeStr, "[]") {
			if zeroValue {
				return "nil"
			}
			return typeStr + "{}"
		}
		if strings.HasPrefix(typeStr, "map[") {
			if zeroValue {
				return "nil"
			}
			return typeStr + "{}"
		}
		return typeStr + "{}"
	}
}

func isBasicType(typeStr string) bool {
	switch typeStr {
	case "string", "int", "int64", "int32", "int16", "int8",
		"uint", "uint64", "uint32", "uint16", "uint8", "byte",
		"float64", "float32", "bool":
		return true
	default:
		return false
	}
}

func needsReflect(returns []paramInfo, hasError bool) bool {
	for _, r := range returns {
		if r.typeStr != "error" && !isBasicType(r.typeStr) {
			return true
		}
	}
	return false
}

func needsContext(params []paramInfo) bool {
	for _, p := range params {
		if p.typeStr == "context.Context" {
			return true
		}
	}
	return false
}

func extractTypeStr(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fset, expr)
	return buf.String()
}

func exportName(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
