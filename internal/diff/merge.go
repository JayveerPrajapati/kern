package diff

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"sort"
	"strings"
)

// Conflict describes a semantic conflict between two concurrent versions.
type Conflict struct {
	Symbol  string `json:"symbol"`
	Kind    string `json:"kind"` // "func", "method", "type", "field", "delete_vs_modify"
	Local   string `json:"local"`
	Remote  string `json:"remote"`
	Message string `json:"message"`
}

// MergeResult represents the output of a 3-way semantic merge.
type MergeResult struct {
	Clean      bool       `json:"clean"`
	MergedCode string     `json:"merged_code"`
	Conflicts  []Conflict `json:"conflicts,omitempty"`
	Diff       string     `json:"diff,omitempty"`
}

// SemanticMerge3Way performs an AST-aware 3-way merge between base, local, and remote.
// If code is valid Go, it merges declarations, imports, and struct fields at the AST level.
// Otherwise, it falls back to a deterministic line-based 3-way merge.
func SemanticMerge3Way(filePath string, base, local, remote []byte) (*MergeResult, error) {
	// If local and remote are identical, return local directly
	if bytes.Equal(local, remote) {
		return &MergeResult{
			Clean:      true,
			MergedCode: string(local),
			Diff:       Unified(filePath, filePath, strings.Split(string(base), "\n"), strings.Split(string(local), "\n")),
		}, nil
	}

	// Try Go AST semantic merge
	res, err := mergeGoAST(filePath, base, local, remote)
	if err == nil {
		oldLines := strings.Split(string(base), "\n")
		newLines := strings.Split(res.MergedCode, "\n")
		res.Diff = Unified(filePath, filePath, oldLines, newLines)
		return res, nil
	}

	// Fallback to line-based 3-way merge
	return mergeLines3Way(filePath, base, local, remote)
}

func mergeGoAST(filePath string, base, local, remote []byte) (*MergeResult, error) {
	fsetBase := token.NewFileSet()
	fBase, errBase := parser.ParseFile(fsetBase, filePath, base, parser.ParseComments)
	fsetLocal := token.NewFileSet()
	fLocal, errLocal := parser.ParseFile(fsetLocal, filePath, local, parser.ParseComments)
	fsetRemote := token.NewFileSet()
	fRemote, errRemote := parser.ParseFile(fsetRemote, filePath, remote, parser.ParseComments)

	if errBase != nil || errLocal != nil || errRemote != nil {
		return nil, fmt.Errorf("not valid Go AST")
	}

	pkgName := fBase.Name.Name
	if fLocal.Name.Name != pkgName || fRemote.Name.Name != pkgName {
		return nil, fmt.Errorf("package name mismatch")
	}

	// 1. Merge imports
	mergedImports := mergeImports(fBase, fLocal, fRemote)

	// 2. Extract declarations by key
	baseDecls := extractDecls(fsetBase, fBase)
	localDecls := extractDecls(fsetLocal, fLocal)
	remoteDecls := extractDecls(fsetRemote, fRemote)

	// All unique keys
	keySet := map[string]bool{}
	var orderedKeys []string
	addKey := func(k string) {
		if !keySet[k] {
			keySet[k] = true
			orderedKeys = append(orderedKeys, k)
		}
	}
	for _, k := range baseDecls.keys {
		addKey(k)
	}
	for _, k := range localDecls.keys {
		addKey(k)
	}
	for _, k := range remoteDecls.keys {
		addKey(k)
	}

	var mergedDecls []string
	var conflicts []Conflict

	for _, key := range orderedKeys {
		bDecl, inBase := baseDecls.items[key]
		lDecl, inLocal := localDecls.items[key]
		rDecl, inRemote := remoteDecls.items[key]

		if inBase && inLocal && inRemote {
			// Present in all three
			if lDecl.code == bDecl.code && rDecl.code == bDecl.code {
				// Unchanged
				mergedDecls = append(mergedDecls, bDecl.code)
			} else if lDecl.code != bDecl.code && rDecl.code == bDecl.code {
				// Only local changed
				mergedDecls = append(mergedDecls, lDecl.code)
			} else if rDecl.code != bDecl.code && lDecl.code == bDecl.code {
				// Only remote changed
				mergedDecls = append(mergedDecls, rDecl.code)
			} else if lDecl.code == rDecl.code {
				// Both made same change
				mergedDecls = append(mergedDecls, lDecl.code)
			} else {
				// Both modified differently: check if it's a struct type whose fields can be merged
				mergedStruct, ok := tryMergeStruct(bDecl, lDecl, rDecl)
				if ok {
					mergedDecls = append(mergedDecls, mergedStruct)
				} else {
					conflicts = append(conflicts, Conflict{
						Symbol:  key,
						Kind:    lDecl.kind,
						Local:   lDecl.code,
						Remote:  rDecl.code,
						Message: fmt.Sprintf("conflicting modifications on %s", key),
					})
					mergedDecls = append(mergedDecls, fmt.Sprintf("<<<<<<< LOCAL\n%s\n=======\n%s\n>>>>>>> REMOTE", lDecl.code, rDecl.code))
				}
			}
		} else if !inBase && inLocal && !inRemote {
			// Added in local only
			mergedDecls = append(mergedDecls, lDecl.code)
		} else if !inBase && !inLocal && inRemote {
			// Added in remote only
			mergedDecls = append(mergedDecls, rDecl.code)
		} else if !inBase && inLocal && inRemote {
			// Added in both
			if lDecl.code == rDecl.code {
				mergedDecls = append(mergedDecls, lDecl.code)
			} else {
				conflicts = append(conflicts, Conflict{
					Symbol:  key,
					Kind:    lDecl.kind,
					Local:   lDecl.code,
					Remote:  rDecl.code,
					Message: fmt.Sprintf("concurrently added conflicting definition for %s", key),
				})
				mergedDecls = append(mergedDecls, fmt.Sprintf("<<<<<<< LOCAL\n%s\n=======\n%s\n>>>>>>> REMOTE", lDecl.code, rDecl.code))
			}
		} else if inBase && !inLocal && inRemote {
			// Deleted in local
			if rDecl.code != bDecl.code {
				conflicts = append(conflicts, Conflict{
					Symbol:  key,
					Kind:    bDecl.kind,
					Local:   "<deleted>",
					Remote:  rDecl.code,
					Message: fmt.Sprintf("deleted in local but modified in remote: %s", key),
				})
			}
			// if unchanged in remote, clean deletion: omit from mergedDecls
		} else if inBase && inLocal && !inRemote {
			// Deleted in remote
			if lDecl.code != bDecl.code {
				conflicts = append(conflicts, Conflict{
					Symbol:  key,
					Kind:    bDecl.kind,
					Local:   lDecl.code,
					Remote:  "<deleted>",
					Message: fmt.Sprintf("modified in local but deleted in remote: %s", key),
				})
			}
			// if unchanged in local, clean deletion: omit from mergedDecls
		}
	}

	var sb strings.Builder
	sb.WriteString("package " + pkgName + "\n\n")

	if len(mergedImports) > 0 {
		sb.WriteString("import (\n")
		for _, imp := range mergedImports {
			sb.WriteString("\t" + imp + "\n")
		}
		sb.WriteString(")\n\n")
	}

	for i, d := range mergedDecls {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(d)
	}
	sb.WriteString("\n")

	mergedFormatted, err := format.Source([]byte(sb.String()))
	var finalCode string
	if err == nil {
		finalCode = string(mergedFormatted)
	} else {
		finalCode = sb.String()
	}

	return &MergeResult{
		Clean:      len(conflicts) == 0,
		MergedCode: finalCode,
		Conflicts:  conflicts,
	}, nil
}

type declItem struct {
	key  string
	kind string
	code string
	node ast.Decl
}

type declMap struct {
	items map[string]declItem
	keys  []string
}

func extractDecls(fset *token.FileSet, f *ast.File) declMap {
	dm := declMap{items: map[string]declItem{}}

	for _, d := range f.Decls {
		var buf bytes.Buffer
		_ = printer.Fprint(&buf, fset, d)
		code := strings.TrimSpace(buf.String())

		switch decl := d.(type) {
		case *ast.FuncDecl:
			var key string
			kind := "func"
			if decl.Recv != nil && len(decl.Recv.List) > 0 {
				kind = "method"
				recvName := extractTypeStr(fset, decl.Recv.List[0].Type)
				key = fmt.Sprintf("method:%s.%s", recvName, decl.Name.Name)
			} else {
				key = fmt.Sprintf("func:%s", decl.Name.Name)
			}
			dm.items[key] = declItem{key: key, kind: kind, code: code, node: d}
			dm.keys = append(dm.keys, key)

		case *ast.GenDecl:
			if decl.Tok == token.IMPORT {
				continue // handled separately
			}
			for _, spec := range decl.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					key := fmt.Sprintf("type:%s", s.Name.Name)
					var sBuf bytes.Buffer
					_ = printer.Fprint(&sBuf, fset, d)
					dm.items[key] = declItem{key: key, kind: "type", code: strings.TrimSpace(sBuf.String()), node: d}
					dm.keys = append(dm.keys, key)
				case *ast.ValueSpec:
					kind := "var"
					if decl.Tok == token.CONST {
						kind = "const"
					}
					var names []string
					for _, name := range s.Names {
						names = append(names, name.Name)
					}
					key := fmt.Sprintf("%s:%s", kind, strings.Join(names, ","))
					dm.items[key] = declItem{key: key, kind: kind, code: code, node: d}
					dm.keys = append(dm.keys, key)
				}
			}
		}
	}
	return dm
}

func extractTypeStr(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fset, expr)
	return buf.String()
}

func mergeImports(fBase, fLocal, fRemote *ast.File) []string {
	impSet := map[string]string{} // path -> spec line
	addImports := func(f *ast.File) {
		for _, imp := range f.Imports {
			path := imp.Path.Value
			var spec string
			if imp.Name != nil {
				spec = imp.Name.Name + " " + path
			} else {
				spec = path
			}
			impSet[path] = spec
		}
	}
	addImports(fBase)
	addImports(fLocal)
	addImports(fRemote)

	var out []string
	for _, spec := range impSet {
		out = append(out, spec)
	}
	sort.Strings(out)
	return out
}

func tryMergeStruct(b, l, r declItem) (string, bool) {
	// Attempt struct field level 3-way merge
	if !strings.Contains(b.code, "struct {") || !strings.Contains(l.code, "struct {") || !strings.Contains(r.code, "struct {") {
		return "", false
	}

	bFields := extractStructFieldLines(b.code)
	lFields := extractStructFieldLines(l.code)
	rFields := extractStructFieldLines(r.code)

	fieldSet := map[string]bool{}
	var mergedFields []string
	addField := func(f string) {
		if !fieldSet[f] {
			fieldSet[f] = true
			mergedFields = append(mergedFields, f)
		}
	}

	for _, f := range bFields {
		addField(f)
	}
	for _, f := range lFields {
		addField(f)
	}
	for _, f := range rFields {
		addField(f)
	}

	// Extract struct header
	header := b.code[:strings.Index(b.code, "struct {")+len("struct {")]
	var sb strings.Builder
	sb.WriteString(header + "\n")
	for _, f := range mergedFields {
		sb.WriteString("\t" + f + "\n")
	}
	sb.WriteString("}")
	return sb.String(), true
}

func extractStructFieldLines(code string) []string {
	start := strings.Index(code, "struct {")
	end := strings.LastIndex(code, "}")
	if start == -1 || end == -1 || end <= start {
		return nil
	}
	inner := code[start+len("struct {") : end]
	var fields []string
	for _, line := range strings.Split(inner, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			fields = append(fields, strings.Join(strings.Fields(trimmed), " "))
		}
	}
	return fields
}

// mergeLines3Way performs a deterministic fallback 3-way merge on text lines.
func mergeLines3Way(filePath string, base, local, remote []byte) (*MergeResult, error) {
	bLines := strings.Split(string(base), "\n")
	lLines := strings.Split(string(local), "\n")
	rLines := strings.Split(string(remote), "\n")

	if bytes.Equal(local, base) {
		return &MergeResult{Clean: true, MergedCode: string(remote)}, nil
	}
	if bytes.Equal(remote, base) {
		return &MergeResult{Clean: true, MergedCode: string(local)}, nil
	}

	// Basic fallback line merge
	var out []string
	var conflicts []Conflict

	max := max(len(bLines), len(lLines), len(rLines))

	for i := 0; i < max; i++ {
		var b, l, r string
		if i < len(bLines) {
			b = bLines[i]
		}
		if i < len(lLines) {
			l = lLines[i]
		}
		if i < len(rLines) {
			r = rLines[i]
		}

		if l == b && r == b {
			out = append(out, b)
		} else if l != b && r == b {
			out = append(out, l)
		} else if r != b && l == b {
			out = append(out, r)
		} else if l == r {
			out = append(out, l)
		} else {
			conflicts = append(conflicts, Conflict{
				Symbol:  fmt.Sprintf("line:%d", i+1),
				Kind:    "line",
				Local:   l,
				Remote:  r,
				Message: fmt.Sprintf("conflicting line %d", i+1),
			})
			out = append(out, fmt.Sprintf("<<<<<<< LOCAL\n%s\n=======\n%s\n>>>>>>> REMOTE", l, r))
		}
	}

	merged := strings.Join(out, "\n")
	return &MergeResult{
		Clean:      len(conflicts) == 0,
		MergedCode: merged,
		Conflicts:  conflicts,
		Diff:       Unified(filePath, filePath, bLines, strings.Split(merged, "\n")),
	}, nil
}
