package docsearch

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestChunkTextKeepsSmallDocIntact(t *testing.T) {
	in := "# Title\n\nSome body text about deploying the service.\n"
	chunks := ChunkText(in, 2000)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Start != 1 {
		t.Errorf("expected start line 1, got %d", chunks[0].Start)
	}
	if !strings.Contains(chunks[0].Text, "deploying") {
		t.Errorf("chunk lost content: %q", chunks[0].Text)
	}
}

func TestChunkTextSplitsLarge(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("paragraph number ")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(" with some filler words here.\n\n")
	}
	chunks := ChunkText(b.String(), 2000)
	if len(chunks) < 2 {
		t.Fatalf("expected several chunks, got %d", len(chunks))
	}
	total := 0
	for _, c := range chunks {
		total += strings.Count(c.Text, "\n\n")
		if c.Start <= 0 {
			t.Errorf("bad start line %d", c.Start)
		}
	}
	if total == 0 {
		t.Error("content lost across chunks")
	}
}

func TestEmbedDeterministic(t *testing.T) {
	a := Embed("deploy the service to production")
	b := Embed("deploy the service to production")
	if len(a) != len(b) {
		t.Fatalf("embedding size differs: %d vs %d", len(a), len(b))
	}
	for k, v := range a {
		if b[k] != v {
			t.Fatalf("embedding not deterministic at %d: %v vs %v", k, v, b[k])
		}
	}
}

func TestCosineRanksRelatedHigher(t *testing.T) {
	related := Embed("how do I deploy the application to the server")
	unrelated := Embed("recipes for baking sourdough bread loaves")
	query := Embed("deploy application to server")
	if Cosine(query, related) <= Cosine(query, unrelated) {
		t.Errorf("related doc should rank higher: related=%v unrelated=%v", Cosine(query, related), Cosine(query, unrelated))
	}
}

func writeDoc(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestIndexAndSearch(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "deploy.md", "# Deployment\n\nTo deploy, push to main and the CI pipeline builds the container image.\n\nMore filler text about unrelated topics.\n")
	writeDoc(t, root, "bakery.md", "# Bakery\n\nThis document is all about flour, yeast, and proofing sourdough bread.\n")

	ix, err := IndexDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Docs) < 2 {
		t.Fatalf("expected >=2 docs, got %d", len(ix.Docs))
	}
	got := ix.Search("how to deploy a container", 3)
	if len(got) == 0 {
		t.Fatal("no search results")
	}
	top := got[0].Doc.Chunk.File
	if !strings.HasPrefix(top, "deploy") {
		t.Errorf("expected deploy.md on top, got %s", top)
	}
	if got[0].Sim < 0 {
		t.Errorf("negative similarity %v", got[0].Sim)
	}
}

func TestSearchHybridBM25RanksExactKeyword(t *testing.T) {
	root := t.TempDir()
	// bakery.md contains the phrase "sourdough bread" only in prose; bread.md
	// contains the exact rare term "proofer" twice.
	writeDoc(t, root, "bakery.md", "# Bakery\n\nAbout sourdough bread and the proofing process in a bakery context.\n")
	writeDoc(t, root, "bread.md", "# Proofer\n\nThe proofer is a specialized proofer used by bakers.\n")

	ix, err := IndexDir(root)
	if err != nil {
		t.Fatal(err)
	}
	got := ix.Search("proofer", 5)
	if len(got) == 0 {
		t.Fatal("no results")
	}
	if got[0].Doc.Chunk.File != "bread.md" {
		t.Errorf("BM25 should rank exact rare term first, got %s", got[0].Doc.Chunk.File)
	}
}

func TestBM25FuseUnion(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "a.md", "alpha widgets and the alpha system with plenty of supporting detail here.\n")
	writeDoc(t, root, "b.md", "nothing in common with the query terms except the word beta, truly nothing.\n")
	ix, err := IndexDir(root)
	if err != nil {
		t.Fatal(err)
	}
	// "alpha" matches only a.md lexically; cosine alone could pull in b.md on
	// fuzzy n-grams, but RRF must keep a.md on top.
	got := ix.Search("alpha", 5)
	if len(got) == 0 || got[0].Doc.Chunk.File != "a.md" {
		t.Fatalf("expected a.md on top, got %+v", got)
	}
}

func TestIndexIgnoresCodeAndVendor(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "readme.md", "just a readme with some words\n")
	writeDoc(t, root, "vendor/third.md", "vendored documentation here\n")
	writeDoc(t, root, "main.go", "package main\nfunc main() {}\n")
	ix, err := IndexDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ix.Docs {
		if strings.HasPrefix(d.Chunk.File, "vendor") || strings.HasSuffix(d.Chunk.File, ".go") {
			t.Errorf("indexed non-document file: %s", d.Chunk.File)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "a.md", "words about the alpha system and its components.\n")
	ix, err := IndexDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	loaded := Load(root)
	if loaded == nil {
		t.Fatal("failed to load persisted index")
	}
	if len(loaded.Docs) != len(ix.Docs) {
		t.Fatalf("doc count mismatch: %d vs %d", len(loaded.Docs), len(ix.Docs))
	}
}

// mockEmbedder produces a deterministic dense vector: tokenize chars and hash
// into a fixed 64-dim bucket, so similar text yields similar vectors without
// any external model.
type mockEmbedder struct{}

func (mockEmbedder) EmbedText(text string) ([]float32, error) {
	vec := make([]float32, 64)
	for i := 0; i < len(text); i++ {
		vec[int(text[i])%64]++
	}
	return vec, nil
}

func TestIndexDirSemanticAndSearchFusion(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.md", "The service deploys containers to the cluster with a rolling update.\n")
	write("b.md", "The banana bread recipe needs three ripe bananas and butter.\n")

	ix, err := IndexDirSemantic(dir, mockEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(ix.Docs))
	}
	for _, d := range ix.Docs {
		if len(d.Vec) == 0 {
			t.Errorf("doc %s missing deterministic Vec", d.ID)
		}
		if len(d.Semantic) != 64 {
			t.Errorf("doc %s missing semantic vector (got %d)", d.ID, len(d.Semantic))
		}
	}

	SetSemanticEmbedder(mockEmbedder{})
	defer func() { SetSemanticEmbedder(nil) }()

	res := ix.Search("deploy container cluster", 2)
	if len(res) == 0 {
		t.Fatal("no results")
	}
	if res[0].Doc.Chunk.File != "a.md" {
		t.Errorf("expected a.md first (deployment topic), got %s", res[0].Doc.Chunk.File)
	}

	// Without the embedder the dense signal is skipped and search still works.
	SetSemanticEmbedder(nil)
	res = ix.Search("deploy container cluster", 2)
	if len(res) == 0 {
		t.Fatal("no results without semantic embedder")
	}
}

func TestIndexDirRootHiddenDirBug(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.md"), []byte("hello world content for indexing purposes here"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := IndexDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Docs) != 1 {
		t.Fatalf("expected 1 doc from non-hidden root, got %d (root-skip regression)", len(ix.Docs))
	}
}

func TestMergeFetchedSearchableAndReplaceable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "local.md"), []byte("local project notes about the deploy pipeline here"), 0o644); err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("The react useState hook lets you manage component state. ", 40)
	added, err := MergeFetched(dir, "react", text)
	if err != nil {
		t.Fatal(err)
	}
	if added == 0 {
		t.Fatal("expected chunks to be added")
	}
	ix := Load(dir)
	if ix == nil {
		t.Fatal("index not persisted")
	}
	if len(ix.Docs) != added+1 {
		t.Fatalf("expected %d docs (local + fetched), got %d", added+1, len(ix.Docs))
	}

	res := ix.Search("useState component state", 2)
	if len(res) == 0 {
		t.Fatal("fetched doc not searchable")
	}
	if res[0].Doc.Chunk.File != "fetch/react.md" {
		t.Fatalf("expected fetch/react.md to rank first, got %s", res[0].Doc.Chunk.File)
	}

	// Re-fetching the same name replaces, not appends.
	added2, err := MergeFetched(dir, "react", text+"extra detail here. ")
	if err != nil {
		t.Fatal(err)
	}
	ix2 := Load(dir)
	if len(ix2.Docs) != added2+1 {
		t.Fatalf("re-fetch should replace: got %d docs", len(ix2.Docs))
	}
}

// TestIndexDirPreservesFetchedDocs: a full re-index (what `kern docs index`
// and kern_doc_index do) must not silently drop documents previously merged
// via MergeFetched — indexing must not undo fetching.
func TestIndexDirPreservesFetchedDocs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "local.md"), []byte("local project notes about the deploy pipeline here"), 0o644); err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("The react useState hook lets you manage component state. ", 40)
	if _, err := MergeFetched(dir, "react", text); err != nil {
		t.Fatal(err)
	}
	// Full re-index (what kern doc index / kern_doc_index does).
	ix, err := IndexDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	res := ix.Search("useState component state", 2)
	if len(res) == 0 {
		t.Fatal("fetched doc lost after re-index")
	}
	if res[0].Doc.Chunk.File != "fetch/react.md" {
		t.Fatalf("expected fetch/react.md to rank first, got %s", res[0].Doc.Chunk.File)
	}
	// And the re-indexed result persists across a reload.
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	ix2 := Load(dir)
	if ix2 == nil {
		t.Fatal("index not persisted")
	}
	res2 := ix2.Search("useState component state", 2)
	if len(res2) == 0 || res2[0].Doc.Chunk.File != "fetch/react.md" {
		t.Fatalf("fetched doc lost after persisted re-index: %+v", res2)
	}
}

func TestReembedFetchAttachesDenseVectors(t *testing.T) {
	dir := t.TempDir()
	text := strings.Repeat("The react useState hook manages component state. ", 40)
	if _, err := MergeFetched(dir, "react", text); err != nil {
		t.Fatal(err)
	}
	n, err := ReembedFetch(dir, "react", mockEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	ix := Load(dir)
	var semantic, total int
	for _, d := range ix.Docs {
		if d.Chunk.File != "fetch/react.md" {
			continue
		}
		total++
		if len(d.Semantic) > 0 {
			semantic++
		}
	}
	if n != semantic || semantic == 0 || semantic != total {
		t.Fatalf("expected %d/%d fetched chunks embedded, reembedded %d", total, total, n)
	}

	if nn, err := ReembedFetch(dir, "missing", mockEmbedder{}); err != nil || nn != 0 {
		t.Fatalf("unknown fetch name should reembed nothing: n=%d err=%v", nn, err)
	}
}
