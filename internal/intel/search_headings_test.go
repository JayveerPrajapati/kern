package intel

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestRankedSearchExcludesHeadingsByDefault pins I5 (deep-dive A2,
// 2026-10-03): markdown headings are prose, not symbols — a code-shaped
// query must never surface them, even when the heading matches every query
// word (the pollution path that fed "Test with curl" into kern_plan as a
// "test"). Doc-shaped queries keep heading hits reachable.
func TestRankedSearchExcludesHeadingsByDefault(t *testing.T) {
	root := writeTree(t, map[string]string{
		"README.md": "# Retry handling\n\nConfigure the retry helper.\n\n## Testing commands\n\nRun tests.\n",
		"lib.go":    "package lib\n\nfunc RetryHandling() {}\n",
	})
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var haveHeading, haveFunc bool
	for _, s := range ix.Symbols {
		if s.Kind == "heading" {
			haveHeading = true
		}
		if s.Name == "RetryHandling" {
			haveFunc = true
		}
	}
	if !haveHeading || !haveFunc {
		t.Fatalf("fixture must contain a heading and RetryHandling (heading=%v func=%v)", haveHeading, haveFunc)
	}

	// Code-shaped query: headings excluded, the real symbol found.
	hits := RankedSearch(ix, "retry handling", 10)
	found := false
	for _, h := range hits {
		if h.Kind == "heading" {
			t.Fatalf("code query surfaced heading %q — headings must be excluded by default", h.FullName())
		}
		if h.Name == "RetryHandling" {
			found = true
		}
	}
	if !found {
		t.Fatalf("RetryHandling not found by code query: %d hits", len(hits))
	}

	// Doc-shaped query: the heading is reachable (best answer for "what is…").
	hits = RankedSearch(ix, "what is retry handling", 10)
	found = false
	for _, h := range hits {
		if h.Kind == "heading" {
			found = true
		}
	}
	if !found {
		t.Fatalf("doc query did not surface the heading (doc-intent must keep headings reachable)")
	}
}

// TestRankedSearchDocIntentWinsOverCodeIntent: a doc question that names
// code ("how does the api server work") keeps headings in the candidate set
// — doc intent governs heading inclusion; code intent only ranks.
func TestRankedSearchDocIntentWinsOverCodeIntent(t *testing.T) {
	root := writeTree(t, map[string]string{
		"docs.md": "# API server\n\nThe api server docs.\n",
		"srv.go":  "package srv\n\nfunc APIServer() {}\n",
	})
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	hits := RankedSearch(ix, "how does the api server work", 10)
	var heading, fn bool
	for _, h := range hits {
		if h.Kind == "heading" {
			heading = true
		}
		if h.Name == "APIServer" {
			fn = true
		}
	}
	if !heading {
		t.Fatalf("doc-intent query with code words must keep the heading reachable")
	}
	if !fn {
		t.Fatalf("APIServer must still outrank and appear for the code part of the query")
	}
	if hits[0].Kind == "heading" {
		t.Fatalf("code symbol must rank above the heading (codeIntent demotion)")
	}
}
