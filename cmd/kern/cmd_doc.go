package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/fetch"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/strutil"
)

// docsFetchCore fetches a public doc page into the local index + cache and
// prints the result. It is the single fetch implementation shared by
// `kern docs fetch <url> [name] [root]` (semantic=true allows the optional
// --semantic re-embed) and the `kern doc-fetch` / `kern doc_fetch` alias
// entries (semantic=false). A non-empty name is slugified; an empty one is
// derived from the URL.
func docsFetchCore(rawURL, name, root string, semantic bool) {
	res, err := fetch.Fetch(rawURL, 0)
	if err != nil {
		fatal("docs fetch: %v", err)
	}
	if name == "" {
		name = slugName(rawURL)
	} else {
		name = strutil.Slug(name)
	}
	if err := os.MkdirAll(cache.Path("data", "docs-fetch"), 0o755); err != nil {
		fatal("docs fetch: %v", err)
	}
	if err := os.WriteFile(cache.Path("data", "docs-fetch", name+".md"), []byte(res.Text), 0o600); err != nil {
		fatal("docs fetch: %v", err)
	}
	added, err := docsearch.MergeFetched(root, name, res.Text)
	if err != nil {
		fatal("docs fetch: %v", err)
	}
	if res.Truncated {
		fmt.Fprintf(os.Stderr, "warning: document exceeded limit, truncated to %d bytes\n", len(res.Text))
	}
	if semantic {
		client := llm.NewEmbedder()
		if !client.HasEmbeddingModel() {
			fmt.Printf("note: semantic embeddings skipped (%s not installed; run: ollama pull %s)\n", llm.EmbedModel(), llm.EmbedModel())
		} else {
			embedded, eerr := docsearch.ReembedFetch(root, name, client)
			if eerr != nil {
				fatal("%v", eerr)
			}
			if embedded > 0 {
				fmt.Printf("semantic embeddings attached to %d fetched chunks (KERN_EMBED_MODEL=%s)\n", embedded, llm.EmbedModel())
			}
		}
	}
	fmt.Printf("fetched %s (%d bytes, %d chunks indexed into %s doc index)\n", name, len(res.Text), added, root)
	if res.Title != "" {
		fmt.Printf("# %s\n\n", res.Title)
	}
	fmt.Println(clipText(res.Text, 600))
}

// docsSearchCore performs the hybrid local doc + code search over the
// indexed documents. It is the single search implementation shared by
// `kern docs <query> [root]` and the `kern doc-search` / `kern doc_search`
// alias entries. k <= 0 falls back to 5 results.
func docsSearchCore(query, root string, k int) {
	if k <= 0 {
		k = 5
	}
	ix := docsearch.Load(root)
	if ix == nil {
		var err error
		ix, err = docsearch.IndexDir(root)
		if err != nil {
			fatal("docs search: %v", err)
		}
		if err := ix.Save(); err != nil {
			fatal("docs search: %v", err)
		}
	}
	// If the persisted index carries dense vectors, re-attach the local
	// embedder so queries fuse the semantic signal too.
	if hasSemantic(ix) {
		client := llm.NewEmbedder()
		if client.HasEmbeddingModel() {
			docsearch.SetSemanticEmbedder(client)
		}
	}
	results := ix.Search(query, k)

	var codeMatches []index.Symbol
	if len(results) == 0 || (len(results) > 0 && results[0].Sim <= 1.0) {
		if codeIx, err := loadOrBuild(root); err == nil && codeIx != nil {
			codeMatches = intel.RankedSearch(codeIx, query, k)
		}
	}

	if len(results) == 0 && len(codeMatches) == 0 {
		// N3: a repo with no indexed docs gets an explanation, not a bare
		// "no matching document fragments" that reads as a confident miss —
		// the same two variants the MCP leaf (internal/mcp/doc) renders.
		if len(ix.Docs) == 0 {
			fmt.Println("no matching document fragments — this repo has no documentation indexed (kern docs searches only indexed repo docs; run `kern docs index <root>` to index a docs tree)")
			return
		}
		fmt.Printf("no matching document fragments (query matched nothing in %d indexed fragments)\n", len(ix.Docs))
		return
	}

	if len(results) > 0 && len(codeMatches) > 0 {
		fmt.Println("## Documentation")
		for i, r := range results {
			fmt.Printf("#%d score=%.3f %s:%d\n", i+1, r.Sim, r.Doc.Chunk.File, r.Doc.Chunk.Start)
			body := strings.ReplaceAll(r.Doc.Chunk.Text, "\n", " ")
			if len(body) > 300 {
				body = body[:300] + "…"
			}
			fmt.Printf("  %s\n", body)
		}
		fmt.Println("\n## Code")
		for _, m := range codeMatches {
			fmt.Printf("%-10s %-7s %-24s %s:%d\n", m.Kind, m.Lang, m.FullName(), m.File, m.Line)
		}
		return
	}

	if len(results) > 0 {
		for i, r := range results {
			fmt.Printf("#%d score=%.3f %s:%d\n", i+1, r.Sim, r.Doc.Chunk.File, r.Doc.Chunk.Start)
			body := strings.ReplaceAll(r.Doc.Chunk.Text, "\n", " ")
			if len(body) > 300 {
				body = body[:300] + "…"
			}
			fmt.Printf("  %s\n", body)
		}
		return
	}

	if len(codeMatches) > 0 {
		fmt.Println("## Code")
		for _, m := range codeMatches {
			fmt.Printf("%-10s %-7s %-24s %s:%d\n", m.Kind, m.Lang, m.FullName(), m.File, m.Line)
		}
	}
}

// runDocFetch fetches a public doc page into the local index + cache.
// Usage: kern doc_fetch <url> [--name N] [--root ROOT]. Thin wrapper over
// the shared docsFetchCore (alias of `kern docs fetch`).
func runDocFetch(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern doc_fetch <url> [--name N] [--root ROOT]")
	}
	rawURL := args[0]
	root := projectRoot(f)
	docsFetchCore(rawURL, f.name, root, false)
}

// runDocSearch performs a local vector search over the indexed documents.
// Usage: kern doc_search <query> [--root ROOT] [--limit N]. Thin wrapper
// over the shared docsSearchCore (alias of `kern docs <query>`).
func runDocSearch(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern doc_search <query> [--root ROOT] [--limit N]")
	}
	query := args[0]
	root := projectRoot(f)
	docsSearchCore(query, root, f.limit)
}
