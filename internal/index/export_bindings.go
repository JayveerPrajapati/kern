package index

import (
	"sort"

	"github.com/JayveerPrajapati/kern/internal/langparse"
)

// moduleFiles lists indexed files that the import path resolves to.
func (catalog *exportCatalog) moduleFiles(source, module string) []string {
	var files []string
	for _, file := range catalog.files {
		if importedModuleFile(source, file, module) {
			files = append(files, file)
		}
	}
	return files
}

func (catalog *exportCatalog) fileSymbols(file, name string) []Symbol {
	var result []Symbol
	for _, symbol := range catalog.index.symbolsFor(name) {
		if langparse.IsScriptLanguage(symbol.Lang) && (symbol.Kind == "interface" || symbol.Kind == "type") {
			continue
		}
		if symbol.File == file && symbol.Receiver == "" && symbol.Outer == "" {
			result = append(result, symbol)
		}
	}
	return result
}

// exported resolves a module's export name through default declarations,
// named reexports, and `export *` chains. seen bounds cycles and depth.
func (catalog *exportCatalog) exported(file, name string, seen map[string]bool) []Symbol {
	key := file + "\x00" + name
	if seen[key] || len(seen) > 64 {
		return nil
	}
	seen[key] = true
	exports := catalog.index.ExportsByFile[file]
	var result []Symbol
	explicit := false
	for _, export := range exports {
		if (export.Kind == "default" && name == "default") || (export.Kind == "named" && export.Name == name) {
			explicit = true
			if export.Path == "" {
				result = append(result, catalog.localExport(file, export.Symbol, seen)...)
			} else {
				result = append(result, catalog.reexported(file, export, export.Symbol, seen)...)
			}
		}
	}
	if !explicit && name != "default" {
		for _, export := range exports {
			if export.Kind == "star" {
				result = append(result, catalog.reexported(file, export, name, seen)...)
			}
		}
	}
	return uniqueExportSymbols(result)
}

func (catalog *exportCatalog) localExport(file, name string, seen map[string]bool) []Symbol {
	if local := catalog.fileSymbols(file, name); len(local) > 0 {
		return local
	}
	var result []Symbol
	for _, imp := range catalog.index.ImportsByFile[file] {
		if imp.Alias != name || imp.Symbol == "" {
			continue
		}
		for _, target := range catalog.moduleFiles(file, imp.Path) {
			result = append(result, catalog.exported(target, imp.Symbol, seen)...)
		}
	}
	return uniqueExportSymbols(result)
}

func (catalog *exportCatalog) reexported(file string, export ExportEdge, name string, seen map[string]bool) []Symbol {
	var result []Symbol
	for _, target := range catalog.moduleFiles(file, export.Path) {
		result = append(result, catalog.exported(target, name, seen)...)
	}
	return result
}

// importedViaExports binds a call through JS/TS default imports, namespace
// imports and barrel reexports that the direct module match cannot see.
func (catalog *exportCatalog) importedViaExports(source Symbol, name, qualifier string) []Symbol {
	if !langparse.IsScriptLanguage(source.Lang) {
		return nil
	}
	var result []Symbol
	for _, imp := range catalog.index.ImportsByFile[source.File] {
		if imp.Alias == "" || (qualifier == "" && imp.Symbol == "") || (qualifier != "" && imp.Alias != qualifier) || (qualifier == "" && imp.Alias != name) {
			continue
		}
		for _, file := range catalog.moduleFiles(source.File, imp.Path) {
			switch {
			case qualifier == "":
				result = append(result, catalog.exported(file, imp.Symbol, map[string]bool{})...)
			case imp.Symbol == "":
				result = append(result, catalog.exported(file, name, map[string]bool{})...)
			default:
				for _, declaration := range catalog.exported(file, imp.Symbol, map[string]bool{}) {
					for _, method := range catalog.index.symbolsFor(name) {
						if method.File == declaration.File && method.Receiver == declaration.Name {
							result = append(result, method)
						}
					}
				}
			}
		}
	}
	return uniqueExportSymbols(result)
}

func sortedModuleFiles(index *Index, symbols map[string]Symbol) []string {
	seen := map[string]bool{}
	for _, symbol := range symbols {
		seen[symbol.File] = true
	}
	for file := range index.ExportsByFile {
		seen[file] = true
	}
	files := make([]string, 0, len(seen))
	for file := range seen {
		files = append(files, file)
	}
	sort.Strings(files)
	return files
}
