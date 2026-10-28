package claims

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/astrefs"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/refscan"
)

const (
	blastClaimID = "blast-radius-reference-evidence"
	blastClaim   = "a heavily-referenced non-callable symbol is never reported as unused"

	// blastCandidatePool bounds the ranked set the claim probes; each target
	// costs one AST-verified lookup, so the candidate list is wider than the
	// proof needs but the LOOKUP count stays capped (maxBlastLookups).
	blastCandidatePool = 12
	// blastProofTargets is how many symbols must report reference evidence
	// for the claim to count as proven. One lucky path proves little; three
	// covers the struct/const/var kinds that share the failure mode.
	blastProofTargets = 3
	// maxBlastLookups caps the per-target AST verification so the gate stays
	// a seconds-scale check even when the top-ranked names collide.
	maxBlastLookups = 10
)

// claimBlastRadius proves the flagship blast-radius claim on the symbols the
// call graph cannot see. The failure it guards against is specific: a struct,
// const or var used across dozens of files must never render as "0 callers /
// nothing uses this". The probe ranks non-callable symbols by real selector
// uses, then asks the SAME entry point `kern explore` uses (intel.Explore) and
// asserts the reference evidence reaches the rendered answer.
func claimBlastRadius(ix *index.Index, ixErr error) Result {
	if ixErr != nil {
		return fail(blastClaimID, blastClaim, "index unavailable: "+ixErr.Error())
	}
	targets, err := referenceTargets(ix)
	if err != nil {
		return fail(blastClaimID, blastClaim, "ranking failed: "+err.Error())
	}
	if len(targets) == 0 {
		return skip(blastClaimID, blastClaim, "no non-callable production symbol has selector uses in this repo — the claim cannot be exercised here")
	}
	var evidence []string
	proven := 0
	lookups := 0
	for _, sym := range targets {
		if proven >= blastProofTargets || lookups >= maxBlastLookups {
			break
		}
		lookups++
		verified := astrefs.Lookup(ix, sym)
		if verified.Count == 0 {
			// The ranking is textual-over-approximate; a symbol with no
			// AST-verified reference cannot prove anything either way.
			evidence = append(evidence, fmt.Sprintf("skipped %s (%s:%d): no AST-verified references", sym.FullName(), sym.File, sym.Line))
			continue
		}
		rep, err := intel.Explore(ix, qualifiedQuery(sym), 2, 30)
		if err != nil {
			return fail(blastClaimID, blastClaim,
				fmt.Sprintf("%s: explore failed: %v", sym.FullName(), err))
		}
		if rep.Definition.File != sym.File || rep.Definition.Line != sym.Line {
			// A bare name resolved to a different declaration: exercising it
			// would test the wrong symbol, so it proves nothing.
			evidence = append(evidence, fmt.Sprintf("skipped %s (%s:%d): name resolved to %s:%d",
				sym.FullName(), sym.File, sym.Line, rep.Definition.File, rep.Definition.Line))
			continue
		}
		// The contract is "never a false all-clear", not "always the
		// reference block": when the call graph covers the symbol (callers or
		// a real radius), that IS the answer, and the reference safety net is
		// deliberately not added. The failure is the vacuous answer — silent
		// graph AND no references.
		graphCovers := len(rep.Callers) > 0 || len(rep.BlastRadius) > 1
		surfaced := rep.ReferenceCount > 0 &&
			strings.Contains(intel.RenderExplore(rep), "== references (")
		if !graphCovers && !surfaced {
			return fail(blastClaimID, blastClaim,
				fmt.Sprintf("%s has %d AST-verified references but the answer reported neither callers nor references", sym.FullName(), verified.Count),
				evidence...)
		}
		proven++
		if graphCovers {
			evidence = append(evidence, fmt.Sprintf("%s (%s:%d): %d AST-verified refs; call graph covers it (%d callers, radius %d)",
				sym.FullName(), sym.File, sym.Line, verified.Count, len(rep.Callers), len(rep.BlastRadius)))
			continue
		}
		evidence = append(evidence, fmt.Sprintf("%s (%s:%d): %d refs in %d file(s), %s — surfaced by explore (blast radius %d)",
			sym.FullName(), sym.File, sym.Line, verified.Count, len(verified.Files), verified.Confidence, len(rep.BlastRadius)))
	}
	if proven == 0 {
		return skip(blastClaimID, blastClaim, "ranked candidates carried no AST-verified references — the claim cannot be exercised on this repo")
	}
	detail := fmt.Sprintf("%d probed non-callable symbols report reference evidence", proven)
	if proven < blastProofTargets {
		detail = fmt.Sprintf("%d/%d target proofs (%d lookups) — partial coverage", proven, blastProofTargets, lookups)
	}
	return pass(blastClaimID, blastClaim, detail, evidence...)
}

// referenceTargets ranks production non-callable symbols (struct, interface,
// type, const, var, field) by the number of selector uses across production
// Go files, so the claim is exercised on symbols real code depends on rather
// than on an arbitrary alphabetical pick. One AST pass over the indexed files
// keeps the ranking cheap; the assertion itself is AST-verified per target by
// astrefs.
func referenceTargets(ix *index.Index) ([]index.Symbol, error) {
	if ix == nil {
		return nil, fmt.Errorf("nil index")
	}
	byName := map[string]index.Symbol{}
	names := make([]string, 0, len(ix.Symbols))
	for _, s := range ix.Symbols {
		// Fields are excluded on purpose: a field name is scoped to its
		// struct, so a ubiquitous name like "File" or "mu" ranks high on
		// unrelated uses and proves nothing about type-level references.
		// Short names are excluded for the same reason (they collide with
		// locals in the textual ranking).
		if s.Name == "" || len(s.Name) < 3 || s.Kind == "heading" || s.Kind == "field" ||
			!strings.HasSuffix(s.File, ".go") ||
			!refscan.ReferenceOnlyKind(s.Kind) || intel.IsNonProduction(s) {
			continue
		}
		if _, seen := byName[s.Name]; seen {
			continue
		}
		byName[s.Name] = s
		names = append(names, s.Name)
	}
	if len(byName) == 0 {
		return nil, nil
	}
	// Deterministic pick among same-named declarations: index order is not a
	// contract, so sort by file/line before keeping the first.
	sort.Slice(names, func(i, j int) bool {
		a, b := byName[names[i]], byName[names[j]]
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	files := make([]string, 0, len(ix.FileHashes))
	for rel := range ix.FileHashes {
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || ix.GeneratedFiles[rel] {
			continue
		}
		files = append(files, rel)
	}
	sort.Strings(files)
	counts := map[string]int{}
	fset := token.NewFileSet()
	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(ix.Root, rel))
		if err != nil {
			continue
		}
		f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if _, ok := sel.X.(*ast.Ident); !ok {
				return true
			}
			if _, tracked := byName[sel.Sel.Name]; tracked {
				counts[sel.Sel.Name]++
			}
			return true
		})
	}
	type ranked struct {
		sym  index.Symbol
		uses int
	}
	pool := make([]ranked, 0, len(counts))
	for name, n := range counts {
		if n < 2 {
			continue
		}
		pool = append(pool, ranked{sym: byName[name], uses: n})
	}
	sort.Slice(pool, func(i, j int) bool {
		if pool[i].uses != pool[j].uses {
			return pool[i].uses > pool[j].uses
		}
		if pool[i].sym.FullName() != pool[j].sym.FullName() {
			return pool[i].sym.FullName() < pool[j].sym.FullName()
		}
		return pool[i].sym.File < pool[j].sym.File
	})
	limit := blastCandidatePool
	if len(pool) < limit {
		limit = len(pool)
	}
	out := make([]index.Symbol, 0, limit)
	for _, r := range pool[:limit] {
		out = append(out, r.sym)
	}
	return out, nil
}

// qualifiedQuery builds the "dir/pkg.Symbol" form of a target so entry
// resolution binds the exact declaration instead of picking whichever
// same-named definition the bare-name heuristics prefer. pathSuffixMatch
// accepts the directory as a path suffix, so the full relative dir works.
func qualifiedQuery(sym index.Symbol) string {
	dir := filepath.ToSlash(filepath.Dir(sym.File))
	if dir == "." || dir == "" {
		return sym.Name
	}
	return dir + "." + sym.Name
}
