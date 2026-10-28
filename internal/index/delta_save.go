//go:build !nosqlite

package index

import (
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/indexdb"
)

// SaveSQLiteDelta writes only the rows named by plan; the result is equivalent
// to a full Save of the same index (generation and counts computed alike).
func SaveSQLiteDelta(root string, ix *Index, plan *deltaPlan) error {
	s, err := OpenSQLite(root)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	if plan == nil || (len(plan.changed) == 0 && len(plan.removed) == 0 && len(plan.owners) == 0) {
		return nil
	}
	if err := indexdb.ApplyDelta(s.db, buildDelta(ix, plan)); err != nil {
		return err
	}
	return s.maybeCheckpoint()
}

// buildDelta converts the changed slice of ix into the row plan indexdb writes.
func buildDelta(ix *Index, plan *deltaPlan) *indexdb.Delta {
	touched := make([]string, 0, len(plan.changed)+len(plan.removed))
	touched = append(append(touched, plan.changed...), plan.removed...)
	touchedSet := make(map[string]bool, len(touched))
	for _, f := range touched {
		touchedSet[f] = true
	}
	owners := make(map[string]bool, len(plan.owners))
	for _, o := range plan.owners {
		owners[o] = true
	}
	d := &indexdb.Delta{
		Meta:    sqliteMeta(ix),
		Touched: touched,
		Changed: plan.changed,
		Removed: plan.removed,
		Owners:  plan.owners,
	}
	for _, sym := range ix.Symbols {
		if !touchedSet[sym.File] {
			continue
		}
		entry := 0
		if sym.Entry {
			entry = 1
		}
		d.Symbols = append(d.Symbols, indexdb.DeltaSymbol{
			Kind: sym.Kind, Name: sym.Name, Receiver: sym.Receiver, File: sym.File, Line: sym.Line, End: sym.End,
			Lang: sym.Lang, Entry: entry, Framework: sym.Framework, Route: sym.Route,
			Params: indexdb.MarshalJSONBlob(sym.Params), Returns: indexdb.MarshalJSONBlob(sym.Returns),
			Confidence: string(sym.Confidence), Outer: sym.Outer,
		})
	}
	for caller, edges := range ix.Calls {
		for _, ce := range edges {
			if !owners[caller] && !touchedSet[ce.SourceFile] {
				continue
			}
			dynamic := 0
			if ce.Dynamic {
				dynamic = 1
			}
			d.Calls = append(d.Calls, indexdb.DeltaCall{
				Caller: caller, Callee: ce.Target, Kind: "call", Confidence: string(ce.Confidence), Synth: ce.Synth,
				SourceFile: ce.SourceFile, Dynamic: dynamic, FromID: ce.FromID, ToID: ce.ToID, Binding: ce.Binding,
			})
		}
	}
	for subtype, bases := range ix.Inherits {
		for _, b := range bases {
			d.Inherits = append(d.Inherits, indexdb.DeltaInherit{Subtype: subtype, Base: b})
		}
	}
	for sym, comm := range ix.CommunityLabels() {
		d.Communities = append(d.Communities, indexdb.DeltaCommunity{Symbol: sym, Community: comm})
	}
	for path, pkg := range ix.Pkgs {
		d.Packages = append(d.Packages, indexdb.DeltaPackage{
			Path: path, Name: pkg.Name, Lang: pkg.Lang,
			Imports: indexdb.MarshalJSONBlob(pkg.Imports), Files: indexdb.MarshalJSONBlob(pkg.Files),
			StructFields: indexdb.MarshalJSONBlob(pkg.StructFields), Constructors: indexdb.MarshalJSONBlob(pkg.Constructors),
		})
	}
	for _, f := range plan.changed {
		if imps, ok := ix.ImportsByFile[f]; ok {
			d.Imports = append(d.Imports, indexdb.DeltaBlob{File: f, JSON: indexdb.MarshalJSONBlob(imps)})
		}
		if exps, ok := ix.ExportsByFile[f]; ok {
			d.Exports = append(d.Exports, indexdb.DeltaBlob{File: f, JSON: indexdb.MarshalJSONBlob(exps)})
		}
		gen := 0
		if ix.GeneratedFiles[f] {
			gen = 1
		}
		d.Files = append(d.Files, indexdb.DeltaFile{Path: f, Hash: ix.FileHashes[f], Generated: gen})
	}
	return d
}

// sqliteMeta is the meta table content; Save and SaveDelta share it.
func sqliteMeta(ix *Index) map[string]string {
	meta := map[string]string{
		"root":                 ix.Root,
		"version":              fmt.Sprintf("%d", ix.Version),
		"updated_at":           ix.UpdatedAt.UTC().Format(time.RFC3339Nano),
		"max_mtime":            fmt.Sprintf("%d", ix.MaxMtime),
		"index_kind":           "symbols",
		"go_types":             fmt.Sprintf("%t", ix.GoTypes),
		"java_compiler":        fmt.Sprintf("%t", ix.JavaCompiler),
		"java_classpath":       ix.JavaClasspath,
		"promoted_low_edges":   fmt.Sprintf("%d", ix.PromotedLowEdges),
		"unresolved_low_edges": fmt.Sprintf("%d", ix.UnresolvedLowEdges),
		"generation":           indexGeneration(ix),
	}
	if ix.Identity != nil {
		meta["identity"] = indexdb.MarshalJSONBlob(*ix.Identity)
	}
	meta["build_stats"] = jsonBlob(ix.BuildStats)
	meta["precision_by_lang"] = jsonBlob(ix.PrecisionByLang)
	meta["call_resolution"] = jsonBlob(ix.CallResolution)
	return meta
}

func jsonBlob(v any) string { return indexdb.MarshalJSONBlob(v) }
