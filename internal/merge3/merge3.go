// Package merge3 implements universal structural AST 3-way merging for multi-agent
// and collaborative code modifications. It resolves non-overlapping additions,
// modifications, and imports at the AST node level without conflict markers across
// Go, Python, TypeScript/JavaScript, and structured text.
package merge3

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/diff"
)

// Conflict describes a semantic conflict between concurrent versions.
type Conflict struct {
	Symbol  string `json:"symbol"`
	Kind    string `json:"kind"` // "func", "method", "type", "field", "import", "hunk"
	Local   string `json:"local"`
	Remote  string `json:"remote"`
	Message string `json:"message"`
}

// Result represents the output of a 3-way structural merge.
type Result struct {
	Clean      bool       `json:"clean"`
	MergedCode string     `json:"merged_code"`
	Conflicts  []Conflict `json:"conflicts,omitempty"`
	Diff       string     `json:"diff,omitempty"`
}

// Merge3Way executes a structural 3-way merge on base, local, and remote source versions.
func Merge3Way(filePath string, base, local, remote []byte) (*Result, error) {
	if bytes.Equal(local, remote) {
		return &Result{
			Clean:      true,
			MergedCode: string(local),
			Diff:       diff.Unified(filePath, filePath, strings.Split(string(base), "\n"), strings.Split(string(local), "\n")),
		}, nil
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		if res, err := mergeGo(filePath, base, local, remote); err == nil {
			return res, nil
		}
	case ".py":
		if res, err := mergePython(filePath, base, local, remote); err == nil {
			return res, nil
		}
	case ".ts", ".js", ".tsx", ".jsx":
		if res, err := mergeTS(filePath, base, local, remote); err == nil {
			return res, nil
		}
	}

	// Universal hunk-aligned 3-way merge fallback
	return mergeHunks3Way(filePath, base, local, remote)
}

// -----------------------------------------------------------------------------
// Go AST Structural Merge
// -----------------------------------------------------------------------------

func mergeGo(filePath string, base, local, remote []byte) (*Result, error) {
	fsetBase := token.NewFileSet()
	fBase, errBase := parser.ParseFile(fsetBase, filePath, base, parser.ParseComments)
	fsetLocal := token.NewFileSet()
	fLocal, errLocal := parser.ParseFile(fsetLocal, filePath, local, parser.ParseComments)
	fsetRemote := token.NewFileSet()
	fRemote, errRemote := parser.ParseFile(fsetRemote, filePath, remote, parser.ParseComments)

	if errBase != nil || errLocal != nil || errRemote != nil {
		return nil, fmt.Errorf("invalid Go syntax in one or more inputs")
	}

	pkgName := fBase.Name.Name
	if fLocal.Name.Name != pkgName || fRemote.Name.Name != pkgName {
		return nil, fmt.Errorf("package name mismatch across versions")
	}

	mergedImports := mergeGoImports(fBase, fLocal, fRemote)

	baseDecls := extractGoDecls(fsetBase, fBase)
	localDecls := extractGoDecls(fsetLocal, fLocal)
	remoteDecls := extractGoDecls(fsetRemote, fRemote)

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
			if lDecl.code == bDecl.code && rDecl.code == bDecl.code {
				mergedDecls = append(mergedDecls, bDecl.code)
			} else if lDecl.code != bDecl.code && rDecl.code == bDecl.code {
				mergedDecls = append(mergedDecls, lDecl.code)
			} else if rDecl.code != bDecl.code && lDecl.code == bDecl.code {
				mergedDecls = append(mergedDecls, rDecl.code)
			} else if lDecl.code == rDecl.code {
				mergedDecls = append(mergedDecls, lDecl.code)
			} else {
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
			mergedDecls = append(mergedDecls, lDecl.code)
		} else if !inBase && !inLocal && inRemote {
			mergedDecls = append(mergedDecls, rDecl.code)
		} else if !inBase && inLocal && inRemote {
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
			if rDecl.code != bDecl.code {
				conflicts = append(conflicts, Conflict{
					Symbol:  key,
					Kind:    bDecl.kind,
					Local:   "<deleted>",
					Remote:  rDecl.code,
					Message: fmt.Sprintf("deleted in local but modified in remote: %s", key),
				})
			}
		} else if inBase && inLocal && !inRemote {
			if lDecl.code != bDecl.code {
				conflicts = append(conflicts, Conflict{
					Symbol:  key,
					Kind:    bDecl.kind,
					Local:   lDecl.code,
					Remote:  "<deleted>",
					Message: fmt.Sprintf("modified in local but deleted in remote: %s", key),
				})
			}
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

	oldLines := strings.Split(string(base), "\n")
	newLines := strings.Split(finalCode, "\n")

	return &Result{
		Clean:      len(conflicts) == 0,
		MergedCode: finalCode,
		Conflicts:  conflicts,
		Diff:       diff.Unified(filePath, filePath, oldLines, newLines),
	}, nil
}

type goDeclItem struct {
	key  string
	kind string
	code string
	node ast.Decl
}

type goDeclMap struct {
	items map[string]goDeclItem
	keys  []string
}

func extractGoDecls(fset *token.FileSet, f *ast.File) goDeclMap {
	dm := goDeclMap{items: map[string]goDeclItem{}}

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
			dm.items[key] = goDeclItem{key: key, kind: kind, code: code, node: d}
			dm.keys = append(dm.keys, key)

		case *ast.GenDecl:
			if decl.Tok == token.IMPORT {
				continue
			}
			for _, spec := range decl.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					key := fmt.Sprintf("type:%s", s.Name.Name)
					var sBuf bytes.Buffer
					_ = printer.Fprint(&sBuf, fset, d)
					dm.items[key] = goDeclItem{key: key, kind: "type", code: strings.TrimSpace(sBuf.String()), node: d}
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
					dm.items[key] = goDeclItem{key: key, kind: kind, code: code, node: d}
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

func mergeGoImports(fBase, fLocal, fRemote *ast.File) []string {
	impSet := map[string]string{}
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

func tryMergeStruct(b, l, r goDeclItem) (string, bool) {
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

// -----------------------------------------------------------------------------
// Python Structural Merge
// -----------------------------------------------------------------------------

var (
	pyDefRe   = regexp.MustCompile(`(?m)^(?:async\s+)?def\s+([a-zA-Z0-9_]+)\s*\(`)
	pyClassRe = regexp.MustCompile(`(?m)^class\s+([a-zA-Z0-9_]+)(?:\s*\([^)]*\))?\s*:`)
	pyImportRe = regexp.MustCompile(`(?m)^(?:from\s+[^\n]+\s+import\s+[^\n]+|import\s+[^\n]+)`)
)

type pyBlock struct {
	key  string
	kind string
	code string
}

func parsePyBlocks(src string) (imports []string, blocks []pyBlock) {
	lines := strings.Split(src, "\n")
	var currentBlock []string
	var currentKey, currentKind string
	inBlock := false

	flush := func() {
		if inBlock && len(currentBlock) > 0 {
			code := strings.TrimSpace(strings.Join(currentBlock, "\n"))
			blocks = append(blocks, pyBlock{key: currentKey, kind: currentKind, code: code})
			currentBlock = nil
			inBlock = false
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if pyImportRe.MatchString(line) && !inBlock {
			imports = append(imports, line)
			continue
		}

		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" {
			if m := pyDefRe.FindStringSubmatch(line); len(m) > 1 {
				flush()
				inBlock = true
				currentKey = "func:" + m[1]
				currentKind = "func"
				currentBlock = append(currentBlock, line)
				continue
			} else if m := pyClassRe.FindStringSubmatch(line); len(m) > 1 {
				flush()
				inBlock = true
				currentKey = "class:" + m[1]
				currentKind = "class"
				currentBlock = append(currentBlock, line)
				continue
			}
		}

		if inBlock {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || trimmed == "" {
				currentBlock = append(currentBlock, line)
			} else {
				flush()
				if trimmed != "" {
					blocks = append(blocks, pyBlock{key: "top:" + trimmed, kind: "stmt", code: line})
				}
			}
		} else if trimmed != "" {
			blocks = append(blocks, pyBlock{key: "top:" + trimmed, kind: "stmt", code: line})
		}
	}
	flush()
	return imports, blocks
}

func mergePython(filePath string, base, local, remote []byte) (*Result, error) {
	bImps, bBlocks := parsePyBlocks(string(base))
	lImps, lBlocks := parsePyBlocks(string(local))
	rImps, rBlocks := parsePyBlocks(string(remote))

	// Merge imports
	impSet := map[string]bool{}
	var mergedImps []string
	addImp := func(imp string) {
		imp = strings.TrimSpace(imp)
		if imp != "" && !impSet[imp] {
			impSet[imp] = true
			mergedImps = append(mergedImps, imp)
		}
	}
	for _, imp := range bImps {
		addImp(imp)
	}
	for _, imp := range lImps {
		addImp(imp)
	}
	for _, imp := range rImps {
		addImp(imp)
	}

	bMap := toPyMap(bBlocks)
	lMap := toPyMap(lBlocks)
	rMap := toPyMap(rBlocks)

	keySet := map[string]bool{}
	var orderedKeys []string
	addKey := func(k string) {
		if !keySet[k] {
			keySet[k] = true
			orderedKeys = append(orderedKeys, k)
		}
	}
	for _, b := range bBlocks {
		addKey(b.key)
	}
	for _, b := range lBlocks {
		addKey(b.key)
	}
	for _, b := range rBlocks {
		addKey(b.key)
	}

	var mergedDecls []string
	var conflicts []Conflict

	for _, key := range orderedKeys {
		b, inB := bMap[key]
		l, inL := lMap[key]
		r, inR := rMap[key]

		if inB && inL && inR {
			if l.code == b.code && r.code == b.code {
				mergedDecls = append(mergedDecls, b.code)
			} else if l.code != b.code && r.code == b.code {
				mergedDecls = append(mergedDecls, l.code)
			} else if r.code != b.code && l.code == b.code {
				mergedDecls = append(mergedDecls, r.code)
			} else if l.code == r.code {
				mergedDecls = append(mergedDecls, l.code)
			} else {
				conflicts = append(conflicts, Conflict{
					Symbol:  key,
					Kind:    l.kind,
					Local:   l.code,
					Remote:  r.code,
					Message: fmt.Sprintf("conflicting modifications on %s", key),
				})
				mergedDecls = append(mergedDecls, fmt.Sprintf("<<<<<<< LOCAL\n%s\n=======\n%s\n>>>>>>> REMOTE", l.code, r.code))
			}
		} else if !inB && inL && !inR {
			mergedDecls = append(mergedDecls, l.code)
		} else if !inB && !inL && inR {
			mergedDecls = append(mergedDecls, r.code)
		} else if !inB && inL && inR {
			if l.code == r.code {
				mergedDecls = append(mergedDecls, l.code)
			} else {
				conflicts = append(conflicts, Conflict{
					Symbol:  key,
					Kind:    l.kind,
					Local:   l.code,
					Remote:  r.code,
					Message: fmt.Sprintf("concurrently added conflicting definition for %s", key),
				})
				mergedDecls = append(mergedDecls, fmt.Sprintf("<<<<<<< LOCAL\n%s\n=======\n%s\n>>>>>>> REMOTE", l.code, r.code))
			}
		}
	}

	var sb strings.Builder
	if len(mergedImps) > 0 {
		sb.WriteString(strings.Join(mergedImps, "\n") + "\n\n")
	}
	sb.WriteString(strings.Join(mergedDecls, "\n\n") + "\n")

	finalCode := sb.String()
	oldLines := strings.Split(string(base), "\n")
	newLines := strings.Split(finalCode, "\n")

	return &Result{
		Clean:      len(conflicts) == 0,
		MergedCode: finalCode,
		Conflicts:  conflicts,
		Diff:       diff.Unified(filePath, filePath, oldLines, newLines),
	}, nil
}

func toPyMap(blocks []pyBlock) map[string]pyBlock {
	m := make(map[string]pyBlock, len(blocks))
	for _, b := range blocks {
		m[b.key] = b
	}
	return m
}

// -----------------------------------------------------------------------------
// TypeScript / JavaScript Structural Merge
// -----------------------------------------------------------------------------

var (
	tsImportRe = regexp.MustCompile(`(?m)^import\s+(?:[^;\n]+)\s+from\s+['"][^'"]+['"];?`)
	tsExportRe = regexp.MustCompile(`(?m)^(?:export\s+)?(?:default\s+)?(?:async\s+)?(?:function|class|interface|type|const|let|var)\s+([a-zA-Z0-9_]+)`)
)

func mergeTS(filePath string, base, local, remote []byte) (*Result, error) {
	// Extract imports and top-level definitions
	bImps, bBlocks := parseTSBlocks(string(base))
	lImps, lBlocks := parseTSBlocks(string(local))
	rImps, rBlocks := parseTSBlocks(string(remote))

	impSet := map[string]bool{}
	var mergedImps []string
	addImp := func(imp string) {
		imp = strings.TrimSpace(imp)
		if imp != "" && !impSet[imp] {
			impSet[imp] = true
			mergedImps = append(mergedImps, imp)
		}
	}
	for _, imp := range bImps {
		addImp(imp)
	}
	for _, imp := range lImps {
		addImp(imp)
	}
	for _, imp := range rImps {
		addImp(imp)
	}

	bMap := toTSMap(bBlocks)
	lMap := toTSMap(lBlocks)
	rMap := toTSMap(rBlocks)

	keySet := map[string]bool{}
	var orderedKeys []string
	addKey := func(k string) {
		if !keySet[k] {
			keySet[k] = true
			orderedKeys = append(orderedKeys, k)
		}
	}
	for _, b := range bBlocks {
		addKey(b.key)
	}
	for _, b := range lBlocks {
		addKey(b.key)
	}
	for _, b := range rBlocks {
		addKey(b.key)
	}

	var mergedDecls []string
	var conflicts []Conflict

	for _, key := range orderedKeys {
		b, inB := bMap[key]
		l, inL := lMap[key]
		r, inR := rMap[key]

		if inB && inL && inR {
			if l.code == b.code && r.code == b.code {
				mergedDecls = append(mergedDecls, b.code)
			} else if l.code != b.code && r.code == b.code {
				mergedDecls = append(mergedDecls, l.code)
			} else if r.code != b.code && l.code == b.code {
				mergedDecls = append(mergedDecls, r.code)
			} else if l.code == r.code {
				mergedDecls = append(mergedDecls, l.code)
			} else {
				conflicts = append(conflicts, Conflict{
					Symbol:  key,
					Kind:    l.kind,
					Local:   l.code,
					Remote:  r.code,
					Message: fmt.Sprintf("conflicting modifications on %s", key),
				})
				mergedDecls = append(mergedDecls, fmt.Sprintf("<<<<<<< LOCAL\n%s\n=======\n%s\n>>>>>>> REMOTE", l.code, r.code))
			}
		} else if !inB && inL && !inR {
			mergedDecls = append(mergedDecls, l.code)
		} else if !inB && !inL && inR {
			mergedDecls = append(mergedDecls, r.code)
		} else if !inB && inL && inR {
			if l.code == r.code {
				mergedDecls = append(mergedDecls, l.code)
			} else {
				conflicts = append(conflicts, Conflict{
					Symbol:  key,
					Kind:    l.kind,
					Local:   l.code,
					Remote:  r.code,
					Message: fmt.Sprintf("concurrently added conflicting definition for %s", key),
				})
				mergedDecls = append(mergedDecls, fmt.Sprintf("<<<<<<< LOCAL\n%s\n=======\n%s\n>>>>>>> REMOTE", l.code, r.code))
			}
		}
	}

	var sb strings.Builder
	if len(mergedImps) > 0 {
		sb.WriteString(strings.Join(mergedImps, "\n") + "\n\n")
	}
	sb.WriteString(strings.Join(mergedDecls, "\n\n") + "\n")

	finalCode := sb.String()
	oldLines := strings.Split(string(base), "\n")
	newLines := strings.Split(finalCode, "\n")

	return &Result{
		Clean:      len(conflicts) == 0,
		MergedCode: finalCode,
		Conflicts:  conflicts,
		Diff:       diff.Unified(filePath, filePath, oldLines, newLines),
	}, nil
}

type tsBlock struct {
	key  string
	kind string
	code string
}

func parseTSBlocks(src string) (imports []string, blocks []tsBlock) {
	lines := strings.Split(src, "\n")
	var current []string
	var curKey, curKind string
	depth := 0
	inBlock := false

	flush := func() {
		if len(current) > 0 {
			code := strings.TrimSpace(strings.Join(current, "\n"))
			blocks = append(blocks, tsBlock{key: curKey, kind: curKind, code: code})
			current = nil
			inBlock = false
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if tsImportRe.MatchString(line) && !inBlock {
			imports = append(imports, line)
			continue
		}

		if !inBlock && trimmed != "" {
			if m := tsExportRe.FindStringSubmatch(line); len(m) > 1 {
				inBlock = true
				curKey = m[1]
				curKind = "symbol"
				current = append(current, line)
				depth += strings.Count(line, "{") - strings.Count(line, "}")
				if depth <= 0 && strings.Contains(line, ";") {
					flush()
				}
				continue
			}
		}

		if inBlock {
			current = append(current, line)
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			if depth <= 0 {
				flush()
			}
		} else if trimmed != "" {
			blocks = append(blocks, tsBlock{key: trimmed, kind: "stmt", code: line})
		}
	}
	flush()
	return imports, blocks
}

func toTSMap(blocks []tsBlock) map[string]tsBlock {
	m := make(map[string]tsBlock, len(blocks))
	for _, b := range blocks {
		m[b.key] = b
	}
	return m
}

// -----------------------------------------------------------------------------
// Universal Hunk-Aligned 3-Way Merge
// -----------------------------------------------------------------------------

func mergeHunks3Way(filePath string, base, local, remote []byte) (*Result, error) {
	bLines := strings.Split(string(base), "\n")
	lLines := strings.Split(string(local), "\n")
	rLines := strings.Split(string(remote), "\n")

	if bytes.Equal(local, base) {
		return &Result{Clean: true, MergedCode: string(remote)}, nil
	}
	if bytes.Equal(remote, base) {
		return &Result{Clean: true, MergedCode: string(local)}, nil
	}

	var out []string
	var conflicts []Conflict

	max := len(bLines)
	if len(lLines) > max {
		max = len(lLines)
	}
	if len(rLines) > max {
		max = len(rLines)
	}

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
	return &Result{
		Clean:      len(conflicts) == 0,
		MergedCode: merged,
		Conflicts:  conflicts,
		Diff:       diff.Unified(filePath, filePath, bLines, strings.Split(merged, "\n")),
	}, nil
}
