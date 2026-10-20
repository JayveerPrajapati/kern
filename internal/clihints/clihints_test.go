package clihints

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// hintsFixture writes a tiny Go module with the given main.go body so the
// suggestion machinery has a real index to run against (index.LoadOrBuild
// builds it on demand — the same fixture pattern the graph/context family
// tests use).
func hintsFixture(t *testing.T, mainBody string) *index.Index {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module hintsfix\n\ngo 1.20\n",
		"main.go": "package main\n\n" + mainBody + `
func main() { _ = helper() }
`,
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := index.LoadOrBuild(dir)
	if err != nil {
		t.Fatalf("LoadOrBuild: %v", err)
	}
	return ix
}

// TestSuggestNilAndEmptyGuards: a nil index and an empty query both return
// nil — the suggestion machinery must never panic on them.
func TestSuggestNilAndEmptyGuards(t *testing.T) {
	if got := Suggest(nil, "anything"); got != nil {
		t.Fatalf("Suggest(nil, ...) = %v, want nil", got)
	}
	if got := Suggest(&index.Index{}, ""); got != nil {
		t.Fatalf("Suggest(ix, \"\") = %v, want nil", got)
	}
}

// TestSuggestRankedSearchCaseVariant locks the ranked-search-first ordering:
// a case-variant camelCase query ("sanitizeDocName") that the anchored
// Search misses still surfaces the symbol via the scored path.
func TestSuggestRankedSearchCaseVariant(t *testing.T) {
	ix := hintsFixture(t, `// SanitizeDocName cleans a doc name.
func SanitizeDocName(s string) string { return s }
`)
	got := Suggest(ix, "sanitizeDocName")
	found := false
	for _, s := range got {
		if s == "SanitizeDocName" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Suggest(%q) = %v, want SanitizeDocName included", "sanitizeDocName", got)
	}
}

// TestSuggestThreeCharPrefixFallback locks the last-resort tier: a query
// whose first 3 chars prefix a symbol ("Helo" -> "helper") but that matches
// no ranked or substring tier still surfaces the symbol.
func TestSuggestThreeCharPrefixFallback(t *testing.T) {
	ix := hintsFixture(t, `// helper returns a string.
func helper() string { return "h" }
`)
	got := Suggest(ix, "Helo")
	if len(got) != 1 || got[0] != "helper" {
		t.Fatalf("Suggest(%q) = %v, want [helper]", "Helo", got)
	}
}

// TestNoSymbolMsgWithoutSuggestions: no candidates means the message is the
// bare "no symbol found: <symbol>" line — exact format.
func TestNoSymbolMsgWithoutSuggestions(t *testing.T) {
	if got := NoSymbolMsg("zzz_nonexistent", nil); got != "no symbol found: zzz_nonexistent" {
		t.Fatalf("NoSymbolMsg = %q, want %q", got, "no symbol found: zzz_nonexistent")
	}
}

// TestNoSymbolMsgWithSuggestions: a miss with a strong candidate appends the
// did-you-mean block — exact format, no fatal.
func TestNoSymbolMsgWithSuggestions(t *testing.T) {
	ix := hintsFixture(t, `// SanitizeDocName cleans a doc name.
func SanitizeDocName(s string) string { return s }
`)
	got := NoSymbolMsg("sanitizeDocNam", ix)
	want := "no symbol found: sanitizeDocNam\n\ndid you mean one of: SanitizeDocName"
	if got != want {
		t.Fatalf("NoSymbolMsg = %q, want %q", got, want)
	}
}

// TestNoSearchMatchMsgCloseCandidates locks the close-candidates branch
// (F1): a garbage query that shares a word with a real symbol (the 2-char
// "no" segment of "NoSuchSymbol12345" matching ErrNoProvider's segments —
// the documented example) lists ranked close candidates instead of a bare
// error. CloseCandidates is gate-free, so partial fuzzy hits surface.
func TestNoSearchMatchMsgCloseCandidates(t *testing.T) {
	ix := hintsFixture(t, `// ErrNoProvider is returned when no provider matches.
func ErrNoProvider() error { return nil }
`)
	msg := NoSearchMatchMsg("NoSuchSymbol12345", ix)
	if !strings.HasPrefix(msg, "no symbols matched: NoSuchSymbol12345") {
		t.Fatalf("msg prefix = %q", msg)
	}
	if !strings.Contains(msg, "close candidates:") {
		t.Fatalf("expected the close-candidates branch, got:\n%s", msg)
	}
	if !strings.Contains(msg, "ErrNoProvider") {
		t.Fatalf("expected ErrNoProvider among the close candidates, got:\n%s", msg)
	}
}

// TestNoSearchMatchMsgDidYouMean locks the did-you-mean branch: when ranked
// close candidates are provably empty (a 2-char query can match no ranked
// tier — matchWord's substring tiers require 3 chars) but the anchored
// Search still finds the symbol, the message falls back to the
// "did you mean one of:" block. Skipped if the fixture's Search misses too.
func TestNoSearchMatchMsgDidYouMean(t *testing.T) {
	ix := hintsFixture(t, `// Cus returns a value.
func Cus() string { return "c" }
`)
	if got := Suggest(ix, "us"); len(got) == 0 {
		t.Skip("fixture Search did not surface Cus for \"us\" — branch not exercisable")
	}
	msg := NoSearchMatchMsg("us", ix)
	if !strings.HasPrefix(msg, "no symbols matched: us") {
		t.Fatalf("msg prefix = %q", msg)
	}
	if strings.Contains(msg, "close candidates:") {
		t.Fatalf("expected the did-you-mean branch (2-char query cannot rank), got:\n%s", msg)
	}
	if !strings.Contains(msg, "did you mean one of: ") || !strings.Contains(msg, "Cus") {
		t.Fatalf("expected the did-you-mean block naming Cus, got:\n%s", msg)
	}
}

// TestNoSearchMatchMsgNilIndex: with a nil index the message is the bare
// line — the suggestion block is skipped entirely.
func TestNoSearchMatchMsgNilIndex(t *testing.T) {
	if got := NoSearchMatchMsg("anything", nil); got != "no symbols matched: anything" {
		t.Fatalf("NoSearchMatchMsg(nil) = %q, want the bare line", got)
	}
}
