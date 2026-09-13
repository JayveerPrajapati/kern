package index

import (
	"reflect"
	"strconv"
	"testing"
)

// vocabFixture is a small tree with camelCase symbol names and a
// word-bearing package directory, exercising the prose→symbol vocab build.
var vocabFixture = map[string]string{
	"app/main.go": `package main
// Middleware wraps a handler.
func Middleware() {}
// MiddlewareFactory builds middlewares.
func MiddlewareFactory() {}
`,
	"security/auth.go": `package security
// SecurityBuilder configures the auth middleware stack.
func SecurityBuilder() {}
`,
}

func buildVocabIndex(t *testing.T) (root string, ix *Index) {
	t.Helper()
	root = writeTree(t, vocabFixture)
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if ix.ProseVocab == nil {
		t.Fatal("Build must populate ProseVocab")
	}
	return root, ix
}

// TestVocabBuildAndLookup: a fixture symbol named "Middleware" is found by
// LookupProse("middleware"), and a multi-word query ranks the symbol that
// matches more distinct words above a single-word match.
func TestVocabBuildAndLookup(t *testing.T) {
	_, ix := buildVocabIndex(t)

	hits := ix.LookupProse("middleware", 20)
	if len(hits) == 0 {
		t.Fatal("LookupProse(middleware) found no hits")
	}
	found := false
	for _, h := range hits {
		if h.Symbol == "Middleware" {
			found = true
			if h.Matched != 1 {
				t.Errorf("Middleware matched = %d, want 1", h.Matched)
			}
		}
	}
	if !found {
		t.Errorf("LookupProse(middleware) = %+v, want Middleware among hits", hits)
	}

	// "middleware factory" matches MiddlewareFactory on two distinct words
	// and Middleware on one; the 2-word match must rank first.
	hits = ix.LookupProse("middleware factory", 20)
	if len(hits) == 0 || hits[0].Symbol != "MiddlewareFactory" {
		t.Fatalf("LookupProse(middleware factory)[0] = %+v, want MiddlewareFactory first", hits)
	}
	if hits[0].Matched != 2 {
		t.Errorf("MiddlewareFactory matched = %d, want 2", hits[0].Matched)
	}

	// Dir-derived words work too: SecurityBuilder lives in security/.
	hits = ix.LookupProse("security", 20)
	if len(hits) == 0 {
		t.Fatal("LookupProse(security) found no hits")
	}
	found = false
	for _, h := range hits {
		if h.Symbol == "SecurityBuilder" {
			found = true
		}
	}
	if !found {
		t.Errorf("LookupProse(security) = %+v, want SecurityBuilder among hits", hits)
	}
}

// TestVocabWordCap: a word shared by more than proseVocabMaxPerWord symbols
// must keep its list capped at 200, and LookupProse applies the default limit
// of 20 when limit <= 0.
func TestVocabWordCap(t *testing.T) {
	ix := &Index{}
	for i := 0; i < 250; i++ {
		ix.Symbols = append(ix.Symbols, Symbol{
			Kind: "func", Name: "WidgetHandler" + strconv.Itoa(i), File: "", Line: i + 1,
		})
	}
	ix.buildProseVocab()
	if got := len(ix.ProseVocab["widget"]); got != proseVocabMaxPerWord {
		t.Errorf("vocab[widget] = %d entries, want cap %d", got, proseVocabMaxPerWord)
	}
	if got := len(ix.ProseVocab["handler"]); got != proseVocabMaxPerWord {
		t.Errorf("vocab[handler] = %d entries, want cap %d", got, proseVocabMaxPerWord)
	}
	if got := len(ix.LookupProse("widget", 0)); got != 20 {
		t.Errorf("LookupProse(widget, 0) = %d hits, want default limit 20", got)
	}
	if got := len(ix.LookupProse("widget", 5)); got != 5 {
		t.Errorf("LookupProse(widget, 5) = %d hits, want 5", got)
	}
}

// TestVocabSaveLoadRoundtrip: Save/Load must preserve ProseVocab so a loaded
// index can serve prose lookups without a rebuild.
func TestVocabSaveLoadRoundtrip(t *testing.T) {
	root, ix := buildVocabIndex(t)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got.ProseVocab, ix.ProseVocab) {
		t.Errorf("ProseVocab after roundtrip differs:\n got %v\nwant %v", got.ProseVocab, ix.ProseVocab)
	}
	// And the loaded index serves lookups.
	if hits := got.LookupProse("middleware", 20); len(hits) == 0 {
		t.Error("loaded index LookupProse(middleware) returned no hits")
	}
}

// TestVocabNilTolerant: an index without ProseVocab (built before the
// feature, or hand-constructed) must return an empty result, never panic.
func TestVocabNilTolerant(t *testing.T) {
	ix := &Index{Symbols: []Symbol{{Kind: "func", Name: "Middleware", File: ""}}}
	if ix.ProseVocab != nil {
		t.Fatal("hand-constructed index should start with nil ProseVocab")
	}
	if hits := ix.LookupProse("middleware", 20); len(hits) != 0 {
		t.Errorf("nil-vocab LookupProse = %+v, want empty", hits)
	}
	if hits := ix.LookupProse("", 20); len(hits) != 0 {
		t.Errorf("empty-query LookupProse = %+v, want empty", hits)
	}
	// Words present in the query but absent from a nil vocab are fine too.
	if hits := ix.LookupProse("build security", 0); len(hits) != 0 {
		t.Errorf("nil-vocab multi-word LookupProse = %+v, want empty", hits)
	}
}

// TestVocabSegments: camelCase, snake and digit-boundary names produce the
// expected words; segments shorter than 3 chars are dropped.
func TestVocabSegments(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"OrderStateMachine", []string{"order", "state", "machine"}},
		{"Middleware", []string{"middleware"}},
		{"ParseJSON", []string{"parse", "json"}},
		{"HTTPServer", []string{"httpserver"}},
		{"db_conn_pool", []string{"conn", "pool"}},
		{"extract2x", []string{"extract"}},
		{"GetDB", []string{"get"}},
		{"to", nil},
		{"a_b_c", nil},
		{"handle_retry", []string{"handle", "retry"}},
	}
	for _, tc := range cases {
		got := proseWords(tc.name)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("proseWords(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestVocabDuplicateSymbols: several Symbol entries may share a full name
// (docs + code), so the same name can appear multiple times in one word's
// list. LookupProse must count DISTINCT query words, not list entries: the
// shared name scores 1 per word, once.
func TestVocabDuplicateSymbols(t *testing.T) {
	ix := &Index{
		Symbols: []Symbol{
			{Kind: "func", Name: "Middleware", File: ""},
			{Kind: "doc", Name: "Middleware", File: ""},
			{Kind: "func", Name: "MiddlewareFactory", File: ""},
		},
	}
	ix.buildProseVocab()
	if got := len(ix.ProseVocab["middleware"]); got != 3 {
		t.Fatalf("vocab[middleware] = %d entries, want 3 (Middleware x2 + MiddlewareFactory)", got)
	}
	hits := ix.LookupProse("middleware factory", 20)
	if len(hits) != 2 {
		t.Fatalf("LookupProse(middleware factory) = %+v, want 2 distinct hits", hits)
	}
	for _, h := range hits {
		if h.Symbol == "Middleware" && h.Matched != 1 {
			t.Errorf("Middleware matched = %d, want 1 (distinct words, not list entries)", h.Matched)
		}
		if h.Symbol == "MiddlewareFactory" && h.Matched != 2 {
			t.Errorf("MiddlewareFactory matched = %d, want 2", h.Matched)
		}
	}
}

// TestUsefulDirWord: hidden, short, root and non-letter directory basenames
// contribute no word.
func TestUsefulDirWord(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"security/auth.go", "security"},
		{"middleware/handler.go", "middleware"},
		{"app.go", ""},
		{".kern/index.json", ""},
		{"x/y.go", ""},
		{"123/z.go", ""},
	}
	for _, tc := range cases {
		if got := usefulDirWord(tc.file); got != tc.want {
			t.Errorf("usefulDirWord(%q) = %q, want %q", tc.file, got, tc.want)
		}
	}
}
