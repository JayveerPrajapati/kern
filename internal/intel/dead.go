package intel

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Confidence tiers for dead-code verdicts. Interface dispatch is invisible to
// the call graph, so an exported symbol with no in-project callers may still
// be live. The tiers are emitted as the "confidence" field on DeadSymbol.
const (
	// ConfidenceCertain means the symbol is unexported and has no in-project
	// callers; no other package can reach it through an interface, so it is
	// certainly dead.
	ConfidenceCertain = "certain"
	// ConfidenceProbable means the symbol is exported and has no in-project
	// callers; it MIGHT be invoked through interface dispatch (invisible to the
	// index), so it is probably dead but not certain.
	ConfidenceProbable = "probable"
	// ConfidenceUncertain means the symbol is an exported method in a package
	// that declares interfaces; it is the most likely shape for an
	// interface-dispatch false positive. Signature matching is not verified —
	// this is a cheap heuristic, not proof that the method satisfies an
	// interface. It is also the verdict for dynamically-dispatched languages
	// (shell, JavaScript/TypeScript, Python), where by-name invocation is
	// invisible to the call graph and an absent caller set cannot prove death.
	ConfidenceUncertain = "uncertain"
)

// DeadSymbol is a symbol that nothing in the project calls. Public (exported)
// names are flagged separately because they may be external API; private names
// with no callers are dead in the strict sense. Confidence rates how sure the
// dead verdict is given that interface dispatch is invisible to the call graph.
type DeadSymbol struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Lines      int    `json:"lines"`
	Public     bool   `json:"public"`
	Confidence string `json:"confidence"` // one of ConfidenceCertain, ConfidenceProbable, ConfidenceUncertain
	// TestOnly is true when the symbol has callers but every one is a test.
	TestOnly bool `json:"test_only,omitempty"`
	// Note explains a downgraded verdict (unattributable same-named callers).
	Note string `json:"note,omitempty"`
}

// DeadCode finds callable symbols (functions and methods) with no in-project
// callers. Types, vars and consts are skipped — the call graph only tracks
// function calls. Symbols called only from tests and symbols with zero callers
// anywhere are reported. Results are sorted by size (largest first).
// A symbol referenced outside a call position (a function/method value, an
// interface member, an argument) is not reported dead: the call graph alone
// cannot see it, but it is clearly still in use.
// Known limitation: interface dispatch is still not fully modelled. A method
// that satisfies an interface and is invoked only through that interface may be
// reported dead even though it is in use. Each result carries a Confidence
// tier that reflects this: unexported symbols (no callers) are "certain",
// exported symbols (no callers) are "probable", and exported methods in
// packages that declare interfaces are "uncertain". Shell, JavaScript/
// TypeScript and Python symbols are always "uncertain": they dispatch by name
// (script invocation, HTML/event wiring, entry points) in ways the call graph
// cannot see, so the index cannot prove them dead.
func DeadCode(ix *index.Index) []DeadSymbol {
	fileMap := buildFileMap(ix)
	// Scan the indexed Go files once for non-call references. Hoisting this out
	// of the loop keeps DeadCode linear in the number of files.
	refs := newNonCallRefIndex(ix)
	// Package directories that declare interfaces: an exported method there may
	// satisfy one of them and be invoked only via interface dispatch.
	ifaceDirs := interfaceDirs(ix)
	nameCount := make(map[string]int, len(ix.Symbols))
	for _, s := range ix.Symbols {
		nameCount[s.Name]++
	}
	var out []DeadSymbol
	for _, s := range ix.Symbols {
		if s.Kind != "func" && s.Kind != "method" {
			continue
		}
		full := s.FullName()
		if IsTestFile(s.File) || isEntryPoint(full) {
			continue
		}
		// A bare-identifier reference outside a call position means the symbol
		// is in use even with no callers (e.g. `f := Foo; f()` or a method
		// value). Don't list it as dead.
		if refs.referencedOutsideCall(simpleName(full)) {
			continue
		}
		callers := ix.CallersFor(s)
		if len(callers) == 0 {
			d := DeadSymbol{
				Name: full, Kind: s.Kind, File: s.File, Line: s.Line,
				Lines: s.Lines(), Public: isPublic(s.Name),
				Confidence: deadConfidence(s, ifaceDirs),
			}
			// Plain functions are keyed by bare name, so edges to a name defined
			// in several packages share one bucket and CallersFor drops any it
			// cannot pin to this package. Recorded-but-dropped callers mean
			// "zero callers" is unproven.
			if s.Receiver == "" && nameCount[s.Name] > 1 && len(ix.Callers[full]) > 0 {
				d.Confidence = ConfidenceUncertain
				d.Note = fmt.Sprintf("%d call site(s) to the same-named %q in other packages could not be attributed", len(ix.Callers[full]), s.Name)
			}
			out = append(out, d)
			continue
		}
		// Called only from tests: production-dead, test-alive.
		testOnly := true
		for _, c := range callers {
			if f := fileMap[c]; f == "" || !IsTestFile(f) {
				testOnly = false
				break
			}
		}
		if testOnly {
			out = append(out, DeadSymbol{
				Name: full, Kind: s.Kind, File: s.File, Line: s.Line,
				Lines: s.Lines(), Public: isPublic(s.Name),
				Confidence: deadConfidence(s, ifaceDirs), TestOnly: true,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lines != out[j].Lines {
			return out[i].Lines > out[j].Lines
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// isPublic reports whether a name follows the exported-name convention
// (leading uppercase, matching Go and most languages' export rules). The
// first rune is decoded properly (utf8.DecodeRuneInString), NOT taken from
// name[0]: a multi-byte UTF-8 first character would otherwise classify by a
// single continuation byte (e.g. lowercase "éclair" starts with byte 0xC3,
// which as a rune is uppercase 'Ã'), inverting the verdict for unexported
// non-ASCII identifiers.
func isPublic(name string) bool {
	if name == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}

// deadConfidence rates how sure we are that a zero-caller symbol is truly dead.
// An unexported name cannot be reached from another package via interface
// dispatch, so the verdict is certain. An exported name may satisfy an
// interface elsewhere and be invoked only through it — invisible to the call
// graph — so it is only probable. An exported method in a package that declares
// interfaces is the most likely interface-dispatch candidate: uncertain.
func deadConfidence(s index.Symbol, ifaceDirs map[string]bool) string {
	// Dynamic languages dispatch by name and the index cannot see those
	// bindings: shell functions are invoked by name from other scripts or via
	// $PATH, JS/TS symbols are wired through HTML/event handlers and dynamic
	// imports, and Python functions are reached through __main__/entry-point
	// metadata. An absent caller set in the index therefore cannot prove death
	// for these languages — always report them uncertain, never certain or
	// probable.
	switch s.Lang {
	case "shell", "javascript", "typescript", "python":
		return ConfidenceUncertain
	}
	if !isPublic(s.Name) {
		return ConfidenceCertain
	}
	if s.Kind == "method" && ifaceDirs[filepath.Dir(s.File)] {
		return ConfidenceUncertain
	}
	return ConfidenceProbable
}

// interfaceDirs returns the package directories (as recorded on symbol File
// paths) that declare at least one interface type.
func interfaceDirs(ix *index.Index) map[string]bool {
	dirs := make(map[string]bool)
	if ix == nil {
		return dirs
	}
	for _, s := range ix.Symbols {
		if s.Kind == "interface" {
			dirs[filepath.Dir(s.File)] = true
		}
	}
	return dirs
}

// RenderDead returns a compact dead-code report. Counts are shown at the end
// so the report is immediately actionable. Each entry carries a confidence
// caveat: unexported symbols are certainly dead, exported symbols are probably
// dead (interface dispatch is invisible to the index), and exported methods in
// packages declaring interfaces are merely uncertain.
func RenderDead(dead []DeadSymbol) string { return RenderDeadLimited(dead, 0) }

// FilterDeadByPath keeps symbols whose file is at or under any of the given
// repo-relative prefixes. No usable prefix keeps everything.
func FilterDeadByPath(dead []DeadSymbol, prefixes ...string) []DeadSymbol {
	var ps []string
	for _, p := range prefixes {
		if p = strings.Trim(filepath.ToSlash(p), "/"); p != "" && p != "." {
			ps = append(ps, p)
		}
	}
	if len(ps) == 0 {
		return dead
	}
	var out []DeadSymbol
	for _, d := range dead {
		for _, p := range ps {
			if d.File == p || strings.HasPrefix(d.File, p+"/") {
				out = append(out, d)
				break
			}
		}
	}
	return out
}

// RenderDeadLimited renders the report summary-first so a display cap
// (limit > 0) can never hide the totals.
func RenderDeadLimited(dead []DeadSymbol, limit int) string {
	public, private, testOnly, uncertain := 0, 0, 0, 0
	for _, d := range dead {
		if d.Public {
			public++
		} else {
			private++
		}
		if d.TestOnly {
			testOnly++
		}
		if d.Confidence == ConfidenceUncertain {
			uncertain++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "summary: %d dead symbols (%d private, %d public-API; %d test-only, %d uncertain)\n",
		len(dead), private, public, testOnly, uncertain)
	b.WriteString("dead code (no production callers):\n")
	shown := dead
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	for _, d := range shown {
		fmt.Fprintf(&b, "  %-8s %-32s %s:%d  (%d lines)%s\n",
			d.Kind, d.Name, d.File, d.Line, d.Lines, deadCaveat(d))
	}
	if len(dead) == 0 {
		b.WriteString("  (none — every non-entry symbol has a production caller)\n")
	}
	if len(shown) < len(dead) {
		fmt.Fprintf(&b, "  … %d more not shown (limit %d)\n", len(dead)-len(shown), limit)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// deadCaveat renders the confidence caveat for a single dead symbol.
func deadCaveat(d DeadSymbol) string {
	if d.Note != "" {
		return "  [uncertain: " + d.Note + "]"
	}
	if d.TestOnly {
		return "  [test-only callers — " + strings.ToLower(d.Confidence) + "]"
	}
	switch d.Confidence {
	case ConfidenceCertain:
		return "  [certainly dead]"
	case ConfidenceProbable:
		return "  [probably dead (may be called via interface dispatch)]"
	case ConfidenceUncertain:
		return "  [uncertain (may satisfy an interface in this package)]"
	}
	return ""
}
