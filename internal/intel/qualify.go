package intel

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// same-named symbols in different packages (twelve bare `func Load`,
// six bare `func New`) used to share one Callers bucket and one display name,
// so `kern bridges` printed eleven identical `Load` rows and `kern hubs`
// ranked every `New` with the same 82 callers. This file scopes ambiguous
// names by package so each definition is ranked and displayed on its own
// evidence (bridges shows `cache.Load`, not `Load`).
//
// The package-scoping convention mirrors the intelligence graph
// (internal/intelligence/graph.go nodeID via packagePathByFile): a symbol in
// package path P displays as P.FullName, and the root package keeps bare
// names. Names that are unique across the index keep their bare FullName, so
// unique symbols (and every existing test fixture) behave exactly as before.

// packagePathByFile maps each indexed file to its package path, mirroring
// internal/intelligence packagePathByFile (kept local so intel does not grow
// a dependency on the intelligence package).
func packagePathByFile(ix *index.Index) map[string]string {
	m := make(map[string]string, len(ix.Pkgs))
	for path, pkg := range ix.Pkgs {
		if pkg == nil {
			continue
		}
		for _, f := range pkg.Files {
			m[f] = path
		}
	}
	return m
}

// pkgOfFile returns the package path owning file: the index package table,
// falling back to the file's directory (the intelligence graph's nodeID uses
// the same fallback, so qualified names stay cross-referenceable).
func pkgOfFile(pkgByFile map[string]string, file string) string {
	if p := pkgByFile[file]; p != "" {
		return p
	}
	if file == "" {
		return ""
	}
	return filepath.Dir(file)
}

// dupFullNames returns the bare FullNames defined in more than one file.
// Only multi-file names are ambiguous: same-file duplicates are one
// definition seen twice, and unique names keep bare display and today's
// aggregated lookups untouched.
func dupFullNames(ix *index.Index) map[string]bool {
	files := map[string]map[string]bool{}
	for _, s := range ix.Symbols {
		full := s.FullName()
		if full == "" {
			continue
		}
		set, ok := files[full]
		if !ok {
			set = map[string]bool{}
			files[full] = set
		}
		if s.File != "" {
			set[s.File] = true
		}
	}
	out := map[string]bool{}
	for full, set := range files {
		if len(set) > 1 {
			out[full] = true
		}
	}
	return out
}

// qualifiedName returns the display/identity key for s: pkg.FullName when the
// bare FullName is ambiguous across files, else the bare FullName. The root
// package ("" or ".") always keeps bare names, matching the graph convention.
func qualifiedName(pkgByFile map[string]string, dups map[string]bool, s index.Symbol) string {
	full := s.FullName()
	if !dups[full] {
		return full
	}
	pkg := pkgOfFile(pkgByFile, s.File)
	if pkg == "" || pkg == "." {
		return full
	}
	return pkg + "." + full
}

// defUnit is one definition site of a bare symbol name: the ranking/display
// granularity for ambiguous names. Units group same-package redefinitions
// (possible across languages in one dir) into a single row.
type defUnit struct {
	qual string       // identity key (qualifiedName of the representative)
	name string       // bare FullName (the index Callers/Calls key)
	file string       // representative defining file
	pkg  string       // package path of file
	sym  index.Symbol // representative symbol (file/line/kind for display)
}

// defUnits groups func/method symbols by qualified name in first-seen order.
// Non-func/method symbols never rank as hubs or bridges and are skipped.
func defUnits(ix *index.Index, pkgByFile map[string]string, dups map[string]bool) []defUnit {
	var units []defUnit
	seen := map[string]bool{}
	for _, s := range ix.Symbols {
		if s.Kind != "func" && s.Kind != "method" {
			continue
		}
		qual := qualifiedName(pkgByFile, dups, s)
		if seen[qual] {
			continue
		}
		seen[qual] = true
		units = append(units, defUnit{
			qual: qual,
			name: s.FullName(),
			file: s.File,
			pkg:  pkgOfFile(pkgByFile, s.File),
			sym:  s,
		})
	}
	return units
}

// fileImportsPkg reports whether file imports package pkg: an import path
// equal to pkg or ending with "/"+pkg (repo-relative package paths versus
// full import strings). Mirrors intelligence importMatchesQualifier's suffix
// rule. The root package never matches (same-dir callers are attributed by
// the same-package rule instead).
func fileImportsPkg(ix *index.Index, file, pkg string) bool {
	if pkg == "" || pkg == "." {
		return false
	}
	for _, ie := range ix.ImportsByFile[file] {
		p := strings.Trim(ie.Path, `"' `)
		if p == "" {
			continue
		}
		if p == pkg || strings.HasSuffix(p, "/"+pkg) {
			return true
		}
	}
	return false
}

// splitProdCallers attributes each production caller in the shared
// ix.Callers[name] bucket to the definition unit(s) it can honestly belong
// to: same-package callers (caller dir == unit package) and callers whose
// file imports the unit's package. Callers attributable to no unit are
// ambiguous across packages and are dropped rather than credited to every
// same-named definition (the old behavior that gave eleven `Load` rows 67
// callers each). Unknown-file callers are kept only for single-unit names,
// mirroring prodCallersWithFileMap keeping unknown symbols.
//
// Test-file callers are excluded, matching prodCallers semantics.
func splitProdCallers(ix *index.Index, fileMap map[string]string, name string, units []defUnit) map[string][]string {
	out := map[string][]string{}
	for _, c := range ix.Callers[name] {
		cf := fileMap[c]
		if cf != "" && isTestFile(cf) {
			continue
		}
		if cf == "" {
			if len(units) == 1 {
				out[units[0].qual] = append(out[units[0].qual], c)
			}
			continue
		}
		cdir := filepath.Dir(cf)
		for _, u := range units {
			if cdir == u.pkg || fileImportsPkg(ix, cf, u.pkg) {
				out[u.qual] = append(out[u.qual], c)
			}
		}
	}
	for qual := range out {
		out[qual] = dedupeSorted(out[qual])
	}
	return out
}

// unitsByName groups definition units by their bare index key.
func unitsByName(units []defUnit) map[string][]defUnit {
	out := map[string][]defUnit{}
	for _, u := range units {
		out[u.name] = append(out[u.name], u)
	}
	return out
}

// hubUnitCallers returns the production callers attributable to unit u:
// the honestly-split subset for ambiguous names, or today's aggregated
// lookup verbatim for unique names (zero behavior change there). splits
// caches one split per bare name across a Hubs/Bridges pass.
func hubUnitCallers(ix *index.Index, fileMap map[string]string, dups map[string]bool, byName map[string][]defUnit, splits map[string]map[string][]string, u defUnit) []string {
	if !dups[u.name] {
		return prodCallersWithFileMap(ix, u.name, fileMap)
	}
	split, ok := splits[u.name]
	if !ok {
		split = splitProdCallers(ix, fileMap, u.name, byName[u.name])
		splits[u.name] = split
	}
	return split[u.qual]
}

// bestUnitCallers returns the display name and caller count of the strongest
// definition unit for a bare index key: the attributed-split winner for
// ambiguous names, or today's aggregated lookup verbatim otherwise (including
// names with no func/method definitions, which keep legacy behavior).
func bestUnitCallers(ix *index.Index, fileMap map[string]string, dups map[string]bool, byName map[string][]defUnit, splits map[string]map[string][]string, name string) (string, int) {
	if !dups[name] {
		return name, len(prodCallersWithFileMap(ix, name, fileMap))
	}
	units := byName[name]
	if len(units) == 0 {
		return name, len(prodCallersWithFileMap(ix, name, fileMap))
	}
	split, ok := splits[name]
	if !ok {
		split = splitProdCallers(ix, fileMap, name, units)
		splits[name] = split
	}
	best, bestN := name, -1
	for _, u := range units {
		if n := len(split[u.qual]); n > bestN || (n == bestN && u.qual < best) {
			best, bestN = u.qual, n
		}
	}
	return best, bestN
}

// dedupeSorted deduplicates and sorts a caller list for deterministic output.
func dedupeSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	cp := append([]string(nil), in...)
	slices.Sort(cp)
	return slices.Compact(cp)
}
