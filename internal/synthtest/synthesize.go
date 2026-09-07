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

	// Synthesize the test body
	casesList := []string{"standard valid input", "zero value boundary"}
	testCode := generateTestFunction(testFuncName, targetDecl.Name.Name, recvType, isPointerRecv, params, returns, hasError)

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
		if needsReflect(returns, hasError) {
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

func generateTestFunction(testFuncName, funcName, recvType string, isPtrRecv bool, params, returns []paramInfo, hasError bool) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("func %s(t *testing.T) {\n", testFuncName))

	// Table-driven struct definition
	sb.WriteString("\ttests := []struct {\n")
	sb.WriteString("\t\tname string\n")
	for _, p := range params {
		sb.WriteString(fmt.Sprintf("\t\t%s %s\n", p.name, p.typeStr))
	}
	if len(returns) > 0 {
		for _, r := range returns {
			if r.typeStr != "error" {
				sb.WriteString(fmt.Sprintf("\t\twant%s %s\n", exportName(r.name), r.typeStr))
			}
		}
	}
	if hasError {
		sb.WriteString("\t\twantErr bool\n")
	}
	sb.WriteString("\t}{\n")

	// Test Case 1: Standard valid
	sb.WriteString("\t\t{\n")
	sb.WriteString("\t\t\tname: \"standard valid input\",\n")
	for _, p := range params {
		sb.WriteString(fmt.Sprintf("\t\t\t%s: %s,\n", p.name, defaultValueForType(p.typeStr, false)))
	}
	if len(returns) > 0 {
		for _, r := range returns {
			if r.typeStr != "error" {
				sb.WriteString(fmt.Sprintf("\t\t\twant%s: %s,\n", exportName(r.name), defaultValueForType(r.typeStr, false)))
			}
		}
	}
	if hasError {
		sb.WriteString("\t\t\twantErr: false,\n")
	}
	sb.WriteString("\t\t},\n")

	// Test Case 2: Zero value boundary
	sb.WriteString("\t\t{\n")
	sb.WriteString("\t\t\tname: \"zero value boundary\",\n")
	for _, p := range params {
		sb.WriteString(fmt.Sprintf("\t\t\t%s: %s,\n", p.name, defaultValueForType(p.typeStr, true)))
	}
	if len(returns) > 0 {
		for _, r := range returns {
			if r.typeStr != "error" {
				sb.WriteString(fmt.Sprintf("\t\t\twant%s: %s,\n", exportName(r.name), defaultValueForType(r.typeStr, true)))
			}
		}
	}
	if hasError {
		sb.WriteString("\t\t\twantErr: false,\n")
	}
	sb.WriteString("\t\t},\n")

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
	for _, p := range params {
		argNames = append(argNames, "tt."+p.name)
	}
	argsCall := strings.Join(argNames, ", ")

	// Invocation & assertion
	if len(returns) == 0 {
		sb.WriteString(fmt.Sprintf("\t\t\t%s%s(%s)\n", callPrefix, funcName, argsCall))
	} else if len(returns) == 1 && hasError {
		sb.WriteString(fmt.Sprintf("\t\t\terr := %s%s(%s)\n", callPrefix, funcName, argsCall))
		sb.WriteString("\t\t\tif (err != nil) != tt.wantErr {\n")
		sb.WriteString(fmt.Sprintf("\t\t\t\tt.Errorf(\"%s() error = %%v, wantErr %%v\", err, tt.wantErr)\n", funcName))
		sb.WriteString("\t\t\t}\n")
	} else {
		var retVars []string
		for _, r := range returns {
			if r.typeStr == "error" {
				retVars = append(retVars, "err")
			} else {
				retVars = append(retVars, "got"+exportName(r.name))
			}
		}
		sb.WriteString(fmt.Sprintf("\t\t\t%s := %s%s(%s)\n", strings.Join(retVars, ", "), callPrefix, funcName, argsCall))
		if hasError {
			sb.WriteString("\t\t\tif (err != nil) != tt.wantErr {\n")
			sb.WriteString(fmt.Sprintf("\t\t\t\tt.Errorf(\"%s() error = %%v, wantErr %%v\", err, tt.wantErr)\n", funcName))
			sb.WriteString("\t\t\t\treturn\n")
			sb.WriteString("\t\t\t}\n")
		}
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
