package index

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/langparse"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
)

const GraphSchemaVersion = 2

type exportCatalog struct {
	index      *Index
	byID       map[string]Symbol
	byFullName map[string][]Symbol
	importDirs map[string]string
	records    []exportCall
	byEndpoint map[string][]int
	files      []string
	names      map[string]string
}

type exportCall struct {
	from GraphNode
	to   GraphNode
	edge GraphEdge
}

func newExportCatalog(ix *Index) *exportCatalog {
	cache := ix.getCache()
	cache.bindingOnce.Do(func() {
		catalog := buildExportCatalog(ix)
		catalog.records = catalog.buildCalls()
		sort.Slice(catalog.records, func(first, second int) bool {
			return exportEdgeLess(catalog.records[first].edge, catalog.records[second].edge)
		})
		catalog.byEndpoint = map[string][]int{}
		for position, call := range catalog.records {
			identities := map[string]bool{call.from.ID: true, call.to.ID: true}
			for _, identity := range append(append([]string{}, call.from.Candidates...), call.to.Candidates...) {
				identities[identity] = true
			}
			for identity := range identities {
				catalog.byEndpoint[identity] = append(catalog.byEndpoint[identity], position)
			}
		}
		cache.bindings = catalog
	})
	return cache.bindings
}

func buildExportCatalog(ix *Index) *exportCatalog {
	catalog := &exportCatalog{index: ix, byID: map[string]Symbol{}, byFullName: map[string][]Symbol{}, importDirs: map[string]string{}}
	directories := map[string]bool{}
	for _, symbol := range ix.Symbols {
		identity := symbol.DeclarationID()
		if _, exists := catalog.byID[identity]; exists {
			continue
		}
		catalog.byID[identity] = symbol
		directories[filepath.ToSlash(filepath.Dir(symbol.File))] = true
		catalog.byFullName[symbol.FullName()] = append(catalog.byFullName[symbol.FullName()], symbol)
	}
	pkgNames := make(map[string]string, len(ix.Pkgs))
	for dir, pkg := range ix.Pkgs {
		if pkg != nil && pkg.Name != "" {
			pkgNames[filepath.ToSlash(dir)] = pkg.Name
		}
	}
	for _, imports := range ix.ImportsByFile {
		for _, imp := range imports {
			path := strings.Trim(imp.Path, `"' `)
			if _, cached := catalog.importDirs[path]; cached {
				continue
			}
			directory := matchImportDirectory(path, directories)
			if directory == "" {
				// The module root is not necessarily the index root (a Go
				// module under src/ or backend/): the import path then shares
				// only a suffix with the repo-relative package directory.
				// Match by package name + longest shared path suffix, and
				// refuse to guess when two directories tie.
				directory = matchImportByPackage(path, directories, pkgNames)
			}
			catalog.importDirs[path] = directory
		}
	}
	catalog.files = sortedModuleFiles(ix, catalog.byID)
	catalog.names = declarationNames(catalog.byID)
	return catalog
}

// matchImportDirectory resolves an import path against repo-relative package
// directories by exact match or path suffix (the module-at-root case).
func matchImportDirectory(path string, directories map[string]bool) string {
	directory := ""
	for candidate := range directories {
		if candidate != "." && (path == candidate || strings.HasSuffix(path, "/"+candidate)) && len(candidate) > len(directory) {
			directory = candidate
		}
	}
	return directory
}

// matchImportByPackage resolves an import path whose module root is a
// subdirectory of the index root. It requires the package name to equal the
// import path's final segment (Go's package convention), then picks the
// directory with the longest shared path suffix from the end. A tie between
// two directories is ambiguous and stays unresolved — binding the wrong
// package is worse than an unresolved edge.
func matchImportByPackage(path string, directories map[string]bool, pkgNames map[string]string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	tail := parts[len(parts)-1]
	if tail == "" {
		return ""
	}
	best, bestSegments, ambiguous := "", 0, false
	for candidate := range directories {
		if candidate == "." || pkgNames[candidate] != tail {
			continue
		}
		dirParts := strings.Split(candidate, "/")
		maxSegments := len(dirParts)
		if len(parts) < maxSegments {
			maxSegments = len(parts)
		}
		matched := 0
		for n := 1; n <= maxSegments; n++ {
			if dirParts[len(dirParts)-n] != parts[len(parts)-n] {
				break
			}
			matched = n
		}
		if matched == 0 {
			continue
		}
		switch {
		case matched > bestSegments:
			best, bestSegments, ambiguous = candidate, matched, false
		case matched == bestSegments && candidate != best:
			ambiguous = true
		}
	}
	if ambiguous {
		return ""
	}
	return best
}

func (catalog *exportCatalog) roots(query string) []Symbol {
	if symbol, found := catalog.byID[query]; found {
		return []Symbol{symbol}
	}
	defs := catalog.index.symbolsFor(query)
	if len(defs) == 0 {
		if dot := strings.LastIndex(query, "."); dot > 0 {
			qualifier, name := query[:dot], query[dot+1:]
			defs = catalog.index.ResolveDottedMethod(qualifier, name)
			if len(defs) == 0 {
				for _, symbol := range catalog.index.symbolsFor(name) {
					if symbol.Receiver == "" && filepath.Base(filepath.Dir(symbol.File)) == qualifier {
						defs = append(defs, symbol)
					}
				}
			}
		}
	}
	return uniqueExportSymbols(defs)
}

func uniqueExportSymbols(symbols []Symbol) []Symbol {
	if len(symbols) < 2 {
		return symbols
	}
	byID := map[string]Symbol{}
	for _, symbol := range symbols {
		byID[symbol.DeclarationID()] = symbol
	}
	result := make([]Symbol, 0, len(byID))
	for _, symbol := range byID {
		result = append(result, symbol)
	}
	sort.Slice(result, func(first, second int) bool {
		return exportNodeLess(declarationNode(result[first], ""), declarationNode(result[second], ""))
	})
	return result
}

func declarationNode(symbol Symbol, role string) GraphNode {
	return GraphNode{ID: symbol.DeclarationID(), Name: symbol.FullName(), Kind: symbol.Kind,
		Role: role, File: symbol.File, Line: symbol.Line, Resolution: "resolved"}
}

func exportEndpoint(name, scope string, candidates []Symbol) GraphNode {
	if len(candidates) == 1 {
		return declarationNode(candidates[0], "")
	}
	node := GraphNode{Name: name, Kind: "call", Resolution: "unresolved"}
	if len(candidates) > 1 {
		node.Resolution = "ambiguous"
	}
	for _, symbol := range candidates {
		node.Candidates = append(node.Candidates, symbol.DeclarationID())
	}
	sort.Strings(node.Candidates)
	data, _ := json.Marshal([]any{scope, name, node.Candidates})
	node.ID = fmt.Sprintf("ref:%x", sha256.Sum256(data))
	return node
}

func (catalog *exportCatalog) buildCalls() []exportCall {
	var result []exportCall
	for owner, edges := range catalog.index.Calls {
		for _, edge := range edges {
			var owners []Symbol
			for _, symbol := range catalog.byFullName[owner] {
				if edge.SourceFile == "" || filepath.ToSlash(symbol.File) == filepath.ToSlash(edge.SourceFile) {
					owners = append(owners, symbol)
				}
			}
			owners = uniqueExportSymbols(owners)
			if declarationBinding(edge.Binding) {
				owners = nil
				if symbol, found := catalog.byID[edge.FromID]; found && symbol.File == edge.SourceFile && symbol.FullName() == owner {
					owners = []Symbol{symbol}
				}
			}
			from := exportEndpoint(owner, "owner:"+edge.SourceFile, owners)
			var targets []Symbol
			for _, source := range owners {
				if declarationBinding(edge.Binding) {
					if symbol, found := catalog.byID[edge.ToID]; found && !edge.Dynamic {
						targets = append(targets, symbol)
					}
				} else if !edge.Dynamic {
					targets = append(targets, catalog.targets(source, edge.Target)...)
				}
			}
			if len(targets) == 0 && edge.Synth != "" && !edge.Dynamic {
				for _, target := range catalog.byFullName[edge.Target] {
					if edge.Synth == "dispatch" || edge.SourceFile == "" || target.File == edge.SourceFile {
						targets = append(targets, target)
					}
				}
			}
			to := exportEndpoint(edge.Target, "target:"+from.ID, uniqueExportSymbols(targets))
			resolution := "resolved"
			if from.Resolution != "resolved" || to.Resolution != "resolved" {
				resolution = "unresolved"
				if from.Resolution == "ambiguous" || to.Resolution == "ambiguous" {
					resolution = "ambiguous"
				}
			}
			confidence := strings.ToLower(edge.Confidence.String())
			result = append(result, exportCall{from: from, to: to, edge: GraphEdge{
				From: from.ID, To: to.ID, Reference: edge.Target, SourceFile: edge.SourceFile,
				Resolution: resolution, Synth: edge.Synth, Binding: edge.Binding,
				Confidence: confidence, ConfidenceLabel: confidenceLabel(confidence),
			}})
		}
	}
	return result
}

func declarationBinding(binding string) bool {
	return binding == "go/types" || binding == "go/ssa-cha" || binding == "javac"
}

func (catalog *exportCatalog) calls(roots []Symbol) []exportCall {
	if len(roots) == 0 {
		return catalog.records
	}
	positions := map[int]bool{}
	for _, root := range roots {
		for _, position := range catalog.byEndpoint[root.DeclarationID()] {
			positions[position] = true
		}
	}
	ordered := make([]int, 0, len(positions))
	for position := range positions {
		ordered = append(ordered, position)
	}
	sort.Ints(ordered)
	result := make([]exportCall, 0, len(ordered))
	for _, position := range ordered {
		result = append(result, catalog.records[position])
	}
	return result
}

func (catalog *exportCatalog) targets(source Symbol, reference string) []Symbol {
	dot := strings.LastIndex(reference, ".")
	name, qualifier := reference, ""
	if dot >= 0 {
		name, qualifier = reference[dot+1:], reference[:dot]
	}
	var local, imported []Symbol
	for _, target := range catalog.index.symbolsFor(name) {
		if !sameBindingLanguage(source.Lang, target.Lang) {
			continue
		}
		if langparse.IsScriptLanguage(source.Lang) && target.File != source.File {
			continue
		}
		if qualifier == "" {
			if target.Receiver != "" && (source.Lang == "go" || target.Receiver != source.Receiver) {
				continue
			}
			if target.Outer != source.Outer {
				continue
			}
			if target.File == source.File || (source.Lang == "go" && samePackageDir(source.File, target.File)) {
				local = append(local, target)
			} else if source.Lang == "go" && target.Receiver == "" && token.IsExported(target.Name) && catalog.importsTarget(source.File, target.File, ".") {
				imported = append(imported, target)
			}
			continue
		}
		if target.Receiver != "" {
			receiver, namespace := qualifier, ""
			if source.Lang == "go" {
				if receiverDot := strings.LastIndex(qualifier, "."); receiverDot >= 0 {
					namespace, receiver = qualifier[:receiverDot], qualifier[receiverDot+1:]
				}
			}
			if target.Receiver != receiver && !strings.HasSuffix(target.Receiver, "."+receiver) {
				continue
			}
			if target.Outer != "" && target.Outer != source.Outer {
				continue
			}
			if namespace != "" {
				if token.IsExported(target.Name) && catalog.importsTarget(source.File, target.File, namespace) {
					imported = append(imported, target)
				}
			} else if target.File == source.File || samePackageDir(source.File, target.File) {
				local = append(local, target)
			} else if source.Lang != "go" && catalog.importsTarget(source.File, target.File, "") {
				imported = append(imported, target)
			}
			continue
		}
		if catalog.importsTarget(source.File, target.File, qualifier) {
			imported = append(imported, target)
		}
	}
	if len(local) > 0 {
		return uniqueExportSymbols(local)
	}
	if langparse.IsScriptLanguage(source.Lang) {
		return catalog.importedViaExports(source, name, qualifier)
	}
	for _, imp := range catalog.index.ImportsByFile[source.File] {
		if imp.Symbol == "" {
			continue
		}
		importedName := imp.Symbol
		if qualifier != "" {
			if qualifier != imp.Alias {
				continue
			}
			importedName = name
		} else if name != imp.Alias {
			continue
		}
		for _, target := range catalog.index.symbolsFor(importedName) {
			if !sameBindingLanguage(source.Lang, target.Lang) || !importedModuleFile(source.File, target.File, imp.Path) {
				continue
			}
			if qualifier == "" && target.Receiver != "" {
				continue
			}
			if qualifier != "" && target.Receiver != imp.Symbol && !strings.HasSuffix(target.Receiver, "."+imp.Symbol) {
				continue
			}
			imported = append(imported, target)
		}
	}
	if len(imported) == 0 {
		imported = catalog.importedViaExports(source, name, qualifier)
	}
	return uniqueExportSymbols(imported)
}

func (catalog *exportCatalog) importsTarget(sourceFile, targetFile, qualifier string) bool {
	targetDir := filepath.ToSlash(filepath.Dir(targetFile))
	for _, imp := range catalog.index.ImportsByFile[sourceFile] {
		path := strings.Trim(imp.Path, `"' `)
		if imp.Alias == "_" {
			continue
		}
		if imp.Symbol == "" && imp.Alias == qualifier && importedModuleFile(sourceFile, targetFile, path) {
			return true
		}
		directory, cached := catalog.importDirs[path]
		if !cached {
			continue
		}
		alias := imp.Alias
		if alias == "" {
			alias = filepath.Base(path)
			if module := catalog.index.Pkgs[directory]; module != nil && module.Lang == "go" && module.Name != "" {
				alias = module.Name
			}
		}
		if qualifier != "" && qualifier != alias {
			continue
		}
		if directory != "" && directory == targetDir {
			return true
		}
	}
	return false
}

func exportTouchesRoot(node GraphNode, roots map[string]bool) bool {
	if roots[node.ID] {
		return true
	}
	for _, candidate := range node.Candidates {
		if roots[candidate] {
			return true
		}
	}
	return false
}

func exportNodeLess(first, second GraphNode) bool {
	if first.Name != second.Name {
		return first.Name < second.Name
	}
	if first.File != second.File {
		return first.File < second.File
	}
	if first.Line != second.Line {
		return first.Line < second.Line
	}
	return first.ID < second.ID
}

func (graph *GraphResult) sort() {
	sort.Slice(graph.Nodes, func(first, second int) bool { return exportNodeLess(graph.Nodes[first], graph.Nodes[second]) })
	sort.Strings(graph.RootIDs)
	sort.Slice(graph.Edges, func(first, second int) bool {
		return exportEdgeLess(graph.Edges[first], graph.Edges[second])
	})
}

func exportEdgeLess(first, second GraphEdge) bool {
	left := []string{first.From, first.To, first.Reference, first.SourceFile, first.Confidence, first.ConfidenceLabel, first.Resolution, first.Synth, first.Binding}
	right := []string{second.From, second.To, second.Reference, second.SourceFile, second.Confidence, second.ConfidenceLabel, second.Resolution, second.Synth, second.Binding}
	for position, value := range left {
		if value != right[position] {
			return value < right[position]
		}
	}
	return false
}

func (graph *GraphResult) Cap(limit int) {
	if limit <= 0 {
		return
	}
	roots := map[string]bool{}
	for _, identity := range graph.RootIDs {
		roots[identity] = true
	}
	nodes := append([]GraphNode(nil), graph.Nodes...)
	sort.SliceStable(nodes, func(first, second int) bool { return roots[nodes[first].ID] && !roots[nodes[second].ID] })
	if len(nodes) > limit {
		nodes = nodes[:limit]
	}
	kept := map[string]bool{}
	for _, node := range nodes {
		kept[node.ID] = true
	}
	graph.Nodes = nodes
	var edges []GraphEdge
	for _, edge := range graph.Edges {
		if kept[edge.From] && kept[edge.To] && len(edges) < limit {
			edges = append(edges, edge)
		}
	}
	graph.Edges = edges
	var rootIDs []string
	for _, identity := range graph.RootIDs {
		if kept[identity] {
			rootIDs = append(rootIDs, identity)
		}
	}
	graph.RootIDs = rootIDs
	graph.sort()
}
