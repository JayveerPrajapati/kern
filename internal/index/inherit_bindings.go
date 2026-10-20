package index

import (
	"github.com/JayveerPrajapati/kern/internal/langparse"
	"path/filepath"
	"sort"
	"strings"
)

// InheritEdge is a subtype -> base relationship bound to declaration IDs.
type InheritEdge struct {
	From       string
	To         string
	Kind       string
	Reference  string
	Resolution string
}

var typeDeclarationKinds = map[string]bool{"class": true, "struct": true, "interface": true, "type": true, "trait": true, "enum": true}

func inheritTag(tagged string) (kind, base string) {
	if i := strings.IndexByte(tagged, ':'); i >= 0 {
		return tagged[:i], tagged[i+1:]
	}
	return "inherits", tagged
}

func (catalog *exportCatalog) typeCandidates(name string) []Symbol {
	var types []Symbol
	for _, symbol := range catalog.byFullName[name] {
		if typeDeclarationKinds[symbol.Kind] {
			types = append(types, symbol)
		}
	}
	return uniqueExportSymbols(types)
}

func (catalog *exportCatalog) baseTypes(source Symbol, reference string) []Symbol {
	name, qualifier := reference, ""
	if dot := strings.LastIndex(reference, "."); dot >= 0 {
		name, qualifier = reference[dot+1:], reference[:dot]
	}
	var local, imported []Symbol
	for _, target := range catalog.index.symbolsFor(name) {
		if !typeDeclarationKinds[target.Kind] || !sameBindingLanguage(source.Lang, target.Lang) {
			continue
		}
		if langparse.IsScriptLanguage(source.Lang) && target.File != source.File {
			continue
		}
		sameScope := target.File == source.File || samePackageDir(source.File, target.File)
		if qualifier == "" {
			if sameScope {
				local = append(local, target)
			}
			continue
		}
		if catalog.importsTarget(source.File, target.File, qualifier) {
			imported = append(imported, target)
			continue
		}
		if source.Lang != "go" && strings.HasSuffix(filepath.ToSlash(filepath.Dir(target.File)), strings.ReplaceAll(qualifier, ".", "/")) {
			imported = append(imported, target)
		}
	}
	if len(local) > 0 {
		return uniqueExportSymbols(local)
	}
	if qualifier == "" && !langparse.IsScriptLanguage(source.Lang) {
		for _, imp := range catalog.index.ImportsByFile[source.File] {
			if imp.Symbol == "" || imp.Alias != name {
				continue
			}
			for _, target := range catalog.index.symbolsFor(imp.Symbol) {
				if typeDeclarationKinds[target.Kind] && sameBindingLanguage(source.Lang, target.Lang) && importedModuleFile(source.File, target.File, imp.Path) {
					imported = append(imported, target)
				}
			}
		}
	}
	if len(imported) == 0 {
		for _, target := range catalog.importedViaExports(source, name, qualifier) {
			if typeDeclarationKinds[target.Kind] {
				imported = append(imported, target)
			}
		}
	}
	return uniqueExportSymbols(imported)
}

// InheritBindings binds inheritance edges to declaration IDs. A subtype name
// shared by several type declarations yields an ambiguous endpoint carrying
// every candidate, because Inherits records no source file.
func (ix *Index) InheritBindings() ([]InheritEdge, []GraphNode) {
	catalog := newExportCatalog(ix)
	nodes := map[string]GraphNode{}
	var edges []InheritEdge
	subtypes := make([]string, 0, len(ix.Inherits))
	for subtype := range ix.Inherits {
		subtypes = append(subtypes, subtype)
	}
	sort.Strings(subtypes)
	for _, subtype := range subtypes {
		owners := catalog.typeCandidates(subtype)
		from := exportEndpoint(subtype, "inherit-owner", owners)
		nodes[from.ID] = from
		for _, tagged := range ix.Inherits[subtype] {
			kind, base := inheritTag(tagged)
			var targets []Symbol
			for _, owner := range owners {
				targets = append(targets, catalog.baseTypes(owner, base)...)
			}
			to := exportEndpoint(base, "inherit-target:"+from.ID, uniqueExportSymbols(targets))
			nodes[to.ID] = to
			resolution := "resolved"
			if from.Resolution != "resolved" || to.Resolution != "resolved" {
				resolution = "unresolved"
				if from.Resolution == "ambiguous" || to.Resolution == "ambiguous" {
					resolution = "ambiguous"
				}
			}
			edges = append(edges, InheritEdge{From: from.ID, To: to.ID, Kind: kind, Reference: base, Resolution: resolution})
		}
	}
	list := make([]GraphNode, 0, len(nodes))
	for _, node := range nodes {
		list = append(list, node)
	}
	sort.Slice(list, func(first, second int) bool { return exportNodeLess(list[first], list[second]) })
	return edges, list
}
