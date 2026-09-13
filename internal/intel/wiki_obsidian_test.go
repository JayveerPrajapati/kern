package intel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// wikiFixture builds a tiny two-package index: lib.Public is called by
// web.Use, so the lib page has a resolvable caller with a definition file.
func wikiFixture(t *testing.T) *index.Index {
	t.Helper()
	dir := writeTree(t, map[string]string{
		"lib/lib.go": "package lib\n\nfunc Public() {}\n",
		"web/web.go": "package web\n\nimport \"demo/lib\"\n\nfunc Use() { lib.Public() }\n",
	})
	return buildIndex(t, dir)
}

// wikiPageFor exports the wiki into a fresh temp dir and returns the page for
// the given slug.
func wikiPageFor(t *testing.T, ix *index.Index, obsidian bool, slug string) string {
	t.Helper()
	out := t.TempDir()
	if _, err := WikiExport(ix, out, obsidian); err != nil {
		t.Fatalf("WikiExport: %v", err)
	}
	page, err := os.ReadFile(filepath.Join(out, slug+".md"))
	if err != nil {
		t.Fatalf("read %s.md: %v", slug, err)
	}
	return string(page)
}

// TestWikiObsidianFrontmatter: obsidian pages start with the YAML frontmatter
// fence and carry the title/community/file/hub lines before the first heading.
func TestWikiObsidianFrontmatter(t *testing.T) {
	page := wikiPageFor(t, wikiFixture(t), true, "lib")
	if !strings.Contains(page, "---\ntitle: lib\n") {
		t.Errorf("page missing title frontmatter line:\n%s", page)
	}
	if !strings.Contains(page, "\ncommunity: ") {
		t.Errorf("page missing community frontmatter line:\n%s", page)
	}
	if !strings.Contains(page, "\nfile: lib\n") {
		t.Errorf("page missing file frontmatter line:\n%s", page)
	}
	if !strings.Contains(page, "\nhub: true\n") && !strings.Contains(page, "\nhub: false\n") {
		t.Errorf("page missing hub frontmatter line:\n%s", page)
	}
	if !strings.Contains(page, "---\n\n# package lib") {
		t.Errorf("frontmatter fence must precede the first heading:\n%s", page)
	}
}

// TestWikiObsidianWikilinks: an obsidian page renders a resolvable caller as
// an Obsidian wikilink "[[<dir-slug>|<caller>]]"; the plain mode renders the
// plain caller name and never emits "[[". The caller web.Use lives in
// web/web.go, so its slug is the web page.
func TestWikiObsidianWikilinks(t *testing.T) {
	obs := wikiPageFor(t, wikiFixture(t), true, "lib")
	if !strings.Contains(obs, "[[web|Use]]") {
		t.Errorf("obsidian page should link caller as [[web|Use]]:\n%s", obs)
	}
	plain := wikiPageFor(t, wikiFixture(t), false, "lib")
	if !strings.Contains(plain, "callers (1): Use") {
		t.Errorf("plain page should list the plain caller name:\n%s", plain)
	}
	if strings.Contains(plain, "[[") {
		t.Errorf("plain page must not contain wikilinks:\n%s", plain)
	}
}

// TestWikiNoObsidianIdenticalBaseline: the plain output keeps the existing
// format — no frontmatter, no wikilinks, and the usual heading/symbols line.
func TestWikiNoObsidianIdenticalBaseline(t *testing.T) {
	page := wikiPageFor(t, wikiFixture(t), false, "lib")
	if strings.HasPrefix(page, "---") {
		t.Errorf("plain page must not start with frontmatter:\n%s", page)
	}
	if strings.Contains(page, "[[") {
		t.Errorf("plain page must not contain wikilinks:\n%s", page)
	}
	if !strings.HasPrefix(page, "# package lib\n\nSymbols: 1\n\n") {
		t.Errorf("plain page heading/symbols line changed:\n%s", page)
	}
}
