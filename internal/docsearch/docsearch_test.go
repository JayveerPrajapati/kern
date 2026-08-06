package docsearch

import (
	"os"
	"path/filepath"
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
		b.WriteString(itoa(i))
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
