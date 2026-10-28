// Package clihints holds the shared "did you mean" hint machinery for the
// kern command surface: symbol-suggestion ranking and the two no-match error
// message builders. It was extracted from the three byte-identical copies in
// cmd/kern/helpers.go, internal/graphcli/helpers.go and
// internal/contextcli/context.go (consolidation tranche 8). The builders
// return MESSAGE STRINGS — exit semantics stay in each family's own fatal
// sentinel, so every package keeps a thin wrapper:
//
//	func fatalNoSymbol(symbol string, ix *index.Index) {
//		fatal("%s", clihints.NoSymbolMsg(symbol, ix))
//	}
package clihints

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cliout"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// isSymbolLikeQuery reports whether a query looks like a symbol name rather
// than a path or file argument. Path-like queries ("/dev/null",
// "cmd/kern/main.go") produce pure-noise suggestions — doc symbols that
// merely MENTION the path ("F1 — multiline `>/dev/null` misclassified…") —
// so the token-ranked and wildcard-substring stages are skipped for them.
func isSymbolLikeQuery(q string) bool {
	return len(q) >= 4 && !strings.ContainsAny(q, "/\\ \t")
}

// Suggest returns up to 5 symbol names from ix that are similar to query
// (case-insensitive substring or prefix match). Used to provide "did you
// mean" hints when a symbol lookup fails. It is the extracted body of the
// three duplicated suggestSymbols helpers (V10: the suggestion set matches
// the MCP server's kern_explore/kern_graph candidates).
func Suggest(ix *index.Index, query string) []string {
	if ix == nil || query == "" {
		return nil
	}
	// Ranked-search hits first: a case-variant camelCase query
	// ("sanitizeDocName" -> "SanitizeDocName") that the anchored Search
	// below misses scores strongly here (>= 150 = matches every query
	// token). The bottom dedupe keeps the list at 5 unique full names.
	var suggestions []index.Symbol
	if isSymbolLikeQuery(query) {
		for _, h := range intel.RankedSearchScored(ix, query, 5) {
			if h.Score >= 150 {
				suggestions = append(suggestions, h.Symbol)
			}
		}
	}
	// V10: share the MCP server's candidate logic (handlers_graph.go) so the
	// CLI's "did you mean" set matches kern_explore/kern_graph: anchored
	// Search first, wildcard-substring fallback, deduped by full name.
	if len(suggestions) == 0 {
		suggestions = ix.Search(query, 10)
	}
	if len(suggestions) == 0 && isSymbolLikeQuery(query) {
		suggestions = ix.Search("*"+query+"*", 10)
	}
	if len(suggestions) == 0 {
		// Last resort: the legacy 3-char-prefix heuristic (typo tolerance)
		// when even the substring search finds nothing. The candidate's
		// length must stay within 4 of the query's: a bare 3-char prefix
		// matched wildly different lengths ("Gone" suggesting
		// "goNativeEntry"), which is noise, not a typo fix.
		q := strings.ToLower(query)
		seen := map[string]bool{}
		for _, s := range ix.Symbols {
			full := s.FullName()
			if full == "" || seen[full] {
				continue
			}
			low := strings.ToLower(full)
			if len(q) >= 3 && strings.HasPrefix(low, q[:3]) && len(low)-len(q) <= 4 && len(q)-len(low) <= 4 {
				seen[full] = true
				suggestions = append(suggestions, s)
				if len(suggestions) >= 5 {
					break
				}
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range suggestions {
		full := s.FullName()
		if full == "" || seen[full] {
			continue
		}
		seen[full] = true
		out = append(out, full)
		if len(out) >= 5 {
			break
		}
	}
	return out
}

// NoSymbolMsg builds the "no symbol found" error message with optional
// "did you mean" suggestions — the message half of the duplicated
// fatalNoSymbol helpers, without the fatal call. Callers route the returned
// message through their own exit sentinel.
func NoSymbolMsg(symbol string, ix *index.Index) string {
	msg := fmt.Sprintf("no symbol found: %s", symbol)
	if suggestions := Suggest(ix, symbol); len(suggestions) > 0 {
		msg += "\n\ndid you mean one of: " + strings.Join(suggestions, ", ")
	}
	return msg
}

// NoSearchMatchMsg builds the "no symbols matched" error message with
// optional suggestions (the same did-you-mean UX as NoSymbolMsg) — the
// message half of the duplicated fatalNoSearchMatch helpers, without the
// fatal call. The `kern search` no-match contract (F1): search used to exit
// 0 on an empty result, breaking the explore/graph convention that "not
// found" is an error for scripts and CI. The --json path is untouched: an
// empty result array is data, not an error, and stays exit 0.
//
// The suggestion list mirrors kern impact's "close candidates" (ranked
// search, no strict score gate): a nonsense query like "NoSuchSymbol12345"
// that matches no symbol still shows what the index actually contains near
// the query, so the follow-up search is guided instead of blind.
func NoSearchMatchMsg(query string, ix *index.Index) string {
	msg := fmt.Sprintf("no symbols matched: %s", query)
	if ix != nil {
		if cands := intel.CloseCandidates(ix, query, 5); len(cands) > 0 {
			msg += "\n\nclose candidates:"
			for _, h := range cands {
				c := h.Symbol
				msg += fmt.Sprintf("\n  %-10s %-7s %-24s %s:%d", c.Kind, c.Lang, c.FullName(), cliout.DisplayPath(c.File), c.Line)
			}
		} else if suggestions := Suggest(ix, query); len(suggestions) > 0 {
			msg += "\n\ndid you mean one of: " + strings.Join(suggestions, ", ")
		}
	}
	return msg
}
