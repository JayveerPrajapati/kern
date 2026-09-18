// Package transform provides source-level AST rewrites and structural transformations.
package transform

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/diff"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// Standard interfaces recognized out of the box.
var stdInterfaces = map[string][]MethodSig{
	"io.Reader": {
		{Name: "Read", Params: "p []byte", Returns: "(n int, err error)", Body: `panic("not implemented")`},
	},
	"io.Writer": {
		{Name: "Write", Params: "p []byte", Returns: "(n int, err error)", Body: `panic("not implemented")`},
	},
	"io.Closer": {
		{Name: "Close", Params: "", Returns: "error", Body: "return nil"},
	},
	"io.ReadCloser": {
		{Name: "Read", Params: "p []byte", Returns: "(n int, err error)", Body: `panic("not implemented")`},
		{Name: "Close", Params: "", Returns: "error", Body: "return nil"},
	},
	"fmt.Stringer": {
		{Name: "String", Params: "", Returns: "string", Body: `return ""`},
	},
	"error": {
		{Name: "Error", Params: "", Returns: "string", Body: `return ""`},
	},
	"http.Handler": {
		{Name: "ServeHTTP", Params: "w http.ResponseWriter, r *http.Request", Returns: "", Body: `panic("not implemented")`},
	},
}

// MethodSig describes a method signature to generate.
type MethodSig struct {
	Name    string
	Params  string
	Returns string
	Body    string
}

// Request defines the parameters for an AST transformation.
type Request struct {
	Action          string       // "implement_interface" | "add_field" | "add_method"
	File            string       // Path to target source file
	Code            string       // Raw code if File is not provided
	Root            string       // Workspace root
	TargetSymbol    string       // Target struct name (e.g. "Server")
	InterfaceName   string       // Interface to implement (e.g. "io.Reader")
	ReceiverName    string       // Receiver variable name (default derived from target, e.g. "s")
	ReceiverType    string       // Pointer or value (default "*TargetSymbol")
	FieldName       string       // For add_field: field name
	FieldType       string       // For add_field: field type (e.g. "int", "string")
	FieldTag        string       // For add_field: optional struct tag
	MethodSignature string       // For add_method: signature e.g. "Close() error"
	MethodBody      string       // For add_method: body e.g. "return nil"
	Apply           bool         // Write result to file
	Index           *index.Index // Optional symbol index for interface lookup
}

// Result holds the output of the transformation.
type Result struct {
	Action       string   `json:"action"`
	TargetSymbol string   `json:"target_symbol"`
	File         string   `json:"file,omitempty"`
	Diff         string   `json:"diff"`
	NewCode      string   `json:"new_code,omitempty"`
	Added        []string `json:"added"`
	Applied      bool     `json:"applied"`
}

// Transform executes the requested AST transformation.
func Transform(req Request) (*Result, error) {
	var src []byte
	var filePath string

	if req.File != "" {
		filePath = req.File
		if req.Root != "" && !filepath.IsAbs(filePath) {
			filePath = filepath.Join(req.Root, filePath)
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read target file: %w", err)
		}
		src = data
	} else if req.Code != "" {
		src = []byte(req.Code)
		filePath = "draft.go"
	} else {
		return nil, fmt.Errorf("either file or code must be specified")
	}

	res := &Result{
		Action:       req.Action,
		TargetSymbol: req.TargetSymbol,
		File:         req.File,
	}

	var newCode []byte
	var added []string
	var err error

	switch req.Action {
	case "implement_interface":
		newCode, added, err = implementInterface(src, filePath, req)
	case "add_field":
		newCode, added, err = addField(src, filePath, req)
	case "add_method":
		newCode, added, err = addMethod(src, filePath, req)
	default:
		return nil, fmt.Errorf("unsupported action %q: expected implement_interface, add_field, or add_method", req.Action)
	}

	if err != nil {
		return nil, err
	}

	res.Added = added
	res.NewCode = string(newCode)

	oldLines := strings.Split(string(src), "\n")
	newLines := strings.Split(string(newCode), "\n")
	res.Diff = diff.Unified(filePath, filePath, oldLines, newLines)

	if req.Apply && req.File != "" && res.Diff != "" {
		// Never write output that does not parse: a mistyped -target
		// (e.g. a path instead of a struct name) or a generator
		// regression would otherwise silently corrupt the target file
		// with syntactically invalid Go (QA F-6).
		if _, perr := parser.ParseFile(token.NewFileSet(), filePath, newCode, parser.ParseComments); perr != nil {
			return nil, fmt.Errorf("refusing to apply: generated code does not parse: %w", perr)
		}
		if err := os.WriteFile(filePath, newCode, 0644); err != nil {
			return nil, fmt.Errorf("write transformed file: %w", err)
		}
		res.Applied = true
	}
	return res, nil
}

func implementInterface(src []byte, filename string, req Request) ([]byte, []string, error) {
	if req.TargetSymbol == "" {
		return nil, nil, fmt.Errorf("target_symbol is required for implement_interface")
	}
	if req.InterfaceName == "" {
		return nil, nil, fmt.Errorf("interface_name is required for implement_interface")
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("parse error: %w", err)
	}

	// 1. Resolve methods of the interface
	var methods []MethodSig
	if std, ok := stdInterfaces[req.InterfaceName]; ok {
		methods = std
	} else if req.Index != nil {
		// Look up in index
		for _, s := range req.Index.Symbols {
			if s.Kind == "method" && (s.Receiver == req.InterfaceName || strings.HasSuffix(s.Receiver, "."+req.InterfaceName)) {
				params := strings.Join(s.Params, ", ")
				returns := strings.Join(s.Returns, ", ")
				if len(s.Returns) > 1 {
					returns = "(" + returns + ")"
				}
				methods = append(methods, MethodSig{
					Name:    s.Name,
					Params:  params,
					Returns: returns,
					Body:    `panic("not implemented")`,
				})
			}
		}
		// The index records interface declarations but not the methods
		// declared inside them, so the symbol lookup above matches only
		// concrete receivers. Fall back to parsing the interface
		// declaration itself from source when the lookup came up empty.
		if len(methods) == 0 {
			methods = interfaceMethodsFromIndex(req.Index, req.InterfaceName, req.Root)
		}
	}
	if len(methods) == 0 {
		return nil, nil, fmt.Errorf("unknown interface %q or no methods found", req.InterfaceName)
	}

	// 2. Identify already implemented methods on the target struct in this file
	existingMethods := map[string]bool{}
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv != nil && len(fn.Recv.List) > 0 {
			recvType := extractTypeName(fn.Recv.List[0].Type)
			if recvType == req.TargetSymbol {
				existingMethods[fn.Name.Name] = true
			}
		}
	}

	// 3. Format receiver
	recvName := req.ReceiverName
	if recvName == "" {
		recvName = strings.ToLower(req.TargetSymbol[:1])
	}
	recvType := req.ReceiverType
	if recvType == "" {
		recvType = "*" + req.TargetSymbol
	}

	var sb strings.Builder
	sb.Write(src)
	if !bytes.HasSuffix(src, []byte("\n")) {
		sb.WriteString("\n")
	}

	var added []string
	for _, m := range methods {
		if existingMethods[m.Name] {
			continue // already implemented
		}
		sb.WriteString("\n// " + m.Name + " implements " + req.InterfaceName + ".\n")
		if m.Returns != "" {
			sb.WriteString(fmt.Sprintf("func (%s %s) %s(%s) %s {\n\t%s\n}\n", recvName, recvType, m.Name, m.Params, m.Returns, m.Body))
		} else {
			sb.WriteString(fmt.Sprintf("func (%s %s) %s(%s) {\n\t%s\n}\n", recvName, recvType, m.Name, m.Params, m.Body))
		}
		added = append(added, m.Name)
	}

	if len(added) == 0 {
		return src, nil, nil
	}

	formatted, err := format.Source([]byte(sb.String()))
	if err != nil {
		return []byte(sb.String()), added, nil
	}
	return formatted, added, nil
}

// interfaceMethodsFromIndex resolves the method set of an interface declared
// in the indexed project. It locates the interface's declaration file via the
// index (Kind "interface"), parses the declaration, and extracts the method
// signatures. This covers interfaces whose methods are not indexed as
// standalone method symbols.
func interfaceMethodsFromIndex(ix *index.Index, iface, root string) []MethodSig {
	var file string
	for _, s := range ix.Symbols {
		if s.Kind == "interface" && (s.Name == iface || strings.HasSuffix(s.FullName(), "."+iface)) {
			file = s.File
			break
		}
	}
	if file == "" {
		return nil
	}
	if root != "" && !filepath.IsAbs(file) {
		file = filepath.Join(root, file)
	}
	src, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, parser.ParseComments)
	if err != nil {
		return nil
	}
	var methods []MethodSig
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != iface {
			return true
		}
		ifaceType, ok := ts.Type.(*ast.InterfaceType)
		if !ok || ifaceType.Methods == nil {
			return false
		}
		for _, m := range ifaceType.Methods.List {
			fnType, ok := m.Type.(*ast.FuncType)
			if !ok || len(m.Names) == 0 {
				continue // embedded interface, not a method
			}
			methods = append(methods, MethodSig{
				Name:    m.Names[0].Name,
				Params:  formatFieldList(fnType.Params),
				Returns: formatFieldList(fnType.Results),
				Body:    `panic("not implemented")`,
			})
		}
		return false
	})
	return methods
}

// formatFieldList renders a field list as a comma-separated "name type" string
// suitable for a generated signature (e.g. "ctx context.Context, opts Options").
func formatFieldList(fl *ast.FieldList) string {
	if fl == nil || len(fl.List) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fl.List))
	for _, f := range fl.List {
		typeStr := exprString(f.Type)
		if len(f.Names) == 0 {
			parts = append(parts, typeStr)
			continue
		}
		for _, n := range f.Names {
			parts = append(parts, n.Name+" "+typeStr)
		}
	}
	return strings.Join(parts, ", ")
}

// exprString renders an AST expression as Go source text.
func exprString(e ast.Expr) string {
	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), e); err != nil {
		return ""
	}
	return buf.String()
}

func addField(src []byte, filename string, req Request) ([]byte, []string, error) {
	if req.TargetSymbol == "" {
		return nil, nil, fmt.Errorf("target_symbol is required for add_field")
	}
	if req.FieldName == "" {
		return nil, nil, fmt.Errorf("field_name is required for add_field")
	}
	if req.FieldType == "" {
		return nil, nil, fmt.Errorf("field_type is required for add_field")
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("parse error: %w", err)
	}

	var targetStruct *ast.StructType
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if ok && ts.Name.Name == req.TargetSymbol {
				if st, ok := ts.Type.(*ast.StructType); ok {
					targetStruct = st
					break
				}
			}
		}
	}

	if targetStruct == nil {
		return nil, nil, fmt.Errorf("struct %q not found in file", req.TargetSymbol)
	}

	// Check if field already exists
	for _, field := range targetStruct.Fields.List {
		for _, name := range field.Names {
			if name.Name == req.FieldName {
				return src, nil, fmt.Errorf("field %q already exists on struct %q", req.FieldName, req.TargetSymbol)
			}
		}
	}

	// Create new field node
	newField := &ast.Field{
		Names: []*ast.Ident{ast.NewIdent(req.FieldName)},
		Type:  ast.NewIdent(req.FieldType),
	}
	if req.FieldTag != "" {
		tagVal := req.FieldTag
		if !strings.HasPrefix(tagVal, "`") {
			tagVal = "`" + tagVal + "`"
		}
		newField.Tag = &ast.BasicLit{
			Kind:  token.STRING,
			Value: tagVal,
		}
	}

	targetStruct.Fields.List = append(targetStruct.Fields.List, newField)

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return nil, nil, fmt.Errorf("format error: %w", err)
	}

	return buf.Bytes(), []string{req.FieldName}, nil
}

func addMethod(src []byte, filename string, req Request) ([]byte, []string, error) {
	if req.TargetSymbol == "" {
		return nil, nil, fmt.Errorf("target_symbol is required for add_method")
	}
	if req.MethodSignature == "" {
		return nil, nil, fmt.Errorf("method_signature is required for add_method")
	}

	recvName := req.ReceiverName
	if recvName == "" {
		recvName = strings.ToLower(req.TargetSymbol[:1])
	}
	recvType := req.ReceiverType
	if recvType == "" {
		recvType = "*" + req.TargetSymbol
	}

	body := req.MethodBody
	if body == "" {
		body = `panic("not implemented")`
	}

	var sb strings.Builder
	sb.Write(src)
	if !bytes.HasSuffix(src, []byte("\n")) {
		sb.WriteString("\n")
	}

	sig := strings.TrimSpace(req.MethodSignature)
	if !strings.HasPrefix(sig, "func ") {
		sig = fmt.Sprintf("func (%s %s) %s", recvName, recvType, sig)
	}

	sb.WriteString("\n" + sig + " {\n\t" + body + "\n}\n")

	formatted, err := format.Source([]byte(sb.String()))
	if err != nil {
		return []byte(sb.String()), []string{sig}, nil
	}

	return formatted, []string{sig}, nil
}

func extractTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return extractTypeName(t.X)
	default:
		return ""
	}
}
