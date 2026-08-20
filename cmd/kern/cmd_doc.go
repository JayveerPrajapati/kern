package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/fetch"
	"github.com/JayveerPrajapati/kern/internal/llm"
)

// runDocFetch fetches a public doc page into the local index + cache.
// Usage: kern doc_fetch <url> [--name N] [--root ROOT]
func runDocFetch(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern doc_fetch <url> [--name N] [--root ROOT]")
	}
	rawURL := args[0]
	root := f.root
	if root == "" {
		root = "."
	}
	res, err := fetch.Fetch(rawURL, 0)
	if err != nil {
		fatal("%v", err)
	}
	name := f.name
	if name == "" {
		name = slugName(rawURL)
	} else {
		name = sanitizeDocName(name)
	}
	if err := os.MkdirAll(cache.Path("data", "docs-fetch"), 0o755); err != nil {
		fatal("%v", err)
	}
	if err := os.WriteFile(cache.Path("data", "docs-fetch", name+".md"), []byte(res.Text), 0o600); err != nil {
		fatal("%v", err)
	}
	added, err := docsearch.MergeFetched(root, name, res.Text)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Printf("fetched %s (%d bytes, %d chunks indexed into %s doc index)\n", name, len(res.Text), added, root)
	if res.Title != "" {
		fmt.Printf("# %s\n\n", res.Title)
	}
	fmt.Println(clipText(res.Text, 600))
}

// runDocSearch performs a local vector search over the indexed documents.
// Usage: kern doc_search <query> [--root ROOT] [--limit N]
func runDocSearch(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern doc_search <query> [--root ROOT] [--limit N]")
	}
	query := args[0]
	root := f.root
	if root == "" {
		root = "."
	}
	k := f.limit
	if k <= 0 {
		k = 5
	}
	ix := docsearch.Load(root)
	if ix == nil {
		var err error
		ix, err = docsearch.IndexDir(root)
		if err != nil {
			fatal("%v", err)
		}
		_ = ix.Save()
	}
	// If the persisted index carries dense vectors, re-attach the local
	// embedder so queries fuse the semantic signal too.
	if hasSemantic(ix) {
		client := llm.NewEmbedder()
		if client.HasEmbeddingModel() {
			docsearch.SemanticEmbedder = client
		}
	}
	results := ix.Search(query, k)
	if len(results) == 0 {
		fmt.Println("no matching document fragments")
		return
	}
	for i, r := range results {
		fmt.Printf("#%d score=%.3f %s:%d\n", i+1, r.Sim, r.Doc.Chunk.File, r.Doc.Chunk.Start)
		body := strings.ReplaceAll(r.Doc.Chunk.Text, "\n", " ")
		if len(body) > 300 {
			body = body[:300] + "…"
		}
		fmt.Printf("  %s\n", body)
	}
}