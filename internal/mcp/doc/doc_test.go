package doc

import (
	"context"
	"strings"
	"testing"
)

func TestSanitizeDocName(t *testing.T) {
	// Valid names collapse to lowercase-alphanumeric-dash slugs.
	for in, want := range map[string]string{
		"ReactDocs":          "reactdocs",
		"react.dev/usestate": "react-dev-usestate",
		"a/b/../c":           "a-b-c",
		"with space":         "with-space",
	} {
		got, err := SanitizeDocName(in)
		if err != nil {
			t.Errorf("SanitizeDocName(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("SanitizeDocName(%q) = %q, want %q", in, got, want)
		}
	}
	// A name with no slug-able characters is rejected (would collapse to "").
	for _, in := range []string{"", "///", "...", "!!!", "日本語"} {
		if _, err := SanitizeDocName(in); err == nil {
			t.Errorf("SanitizeDocName(%q): want error, got nil", in)
		}
	}
}

func TestClip(t *testing.T) {
	if got := clip("short", 10); got != "short" {
		t.Errorf("clip(short) = %q, want short", got)
	}
	got := clip("abcdefghij", 5)
	if !strings.HasPrefix(got, "abcde") || !strings.HasSuffix(got, "…") {
		t.Errorf("clip truncation = %q, want prefix abcde + ellipsis", got)
	}
	if got := clip("", 5); got != "" {
		t.Errorf("clip(empty) = %q, want empty", got)
	}
}

func TestDocSearchSlug(t *testing.T) {
	cases := map[string]string{
		"https://react.dev/reference/usestate": "react-dev-reference-usestate",
		"https://golang.org/pkg/fmt":           "golang-org-pkg-fmt",
	}
	for in, want := range cases {
		if got := docSearchSlug(in); got != want {
			t.Errorf("docSearchSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHasSlugChar(t *testing.T) {
	for in, want := range map[string]bool{
		"abc": true,
		"123": true,
		"A9":  true,
		"":    false,
		"...": false,
		"日本語": false,
	} {
		if got := hasSlugChar(in); got != want {
			t.Errorf("hasSlugChar(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSearchRequiresQuery(t *testing.T) {
	// Fail-closed validation before any index work: an empty query is an
	// error even with nil hooks.
	if _, err := Search(context.Background(), Hooks{}, map[string]any{}); err == nil {
		t.Error("Search without query: want error, got nil")
	} else if !strings.Contains(err.Error(), "query is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFetchRequiresURL(t *testing.T) {
	if _, err := Fetch(context.Background(), map[string]any{}); err == nil {
		t.Error("Fetch without url: want error, got nil")
	} else if !strings.Contains(err.Error(), "url is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCommitmsgRejectsLeadingDashRange(t *testing.T) {
	// Fail-closed: a crafted range starting with "-" would be parsed by git
	// as an option; it must be rejected before any exec.
	if _, err := Commitmsg(context.Background(), map[string]any{"range": "--all"}); err == nil {
		t.Error("Commitmsg with option-looking range: want error, got nil")
	} else if !strings.Contains(err.Error(), "must not start with -") {
		t.Errorf("unexpected error: %v", err)
	}
}
