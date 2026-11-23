package main

// runDocs implements `kern docs` — local doc search over indexed repo docs
// trees (query / index / clear / fetch subcommands). Restored to its own
// file after the dead-CLI sweep removed cmd_agent.go (where it had been
// co-located with the orchestrator shells).
//
// A query NEVER creates its target root: the positional root is validated as
// an existing directory first (2026-11-10 break campaign — `kern docs search
// X` used to materialise a full .kern index inside a new directory named X).

import (
	"fmt"
	"os"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/llm"
)

func runDocs(rest []string) {
	f, args := parseFlagsOrDie(rest)
	sub := ""
	if len(args) > 0 {
		switch args[0] {
		case "index", "clear", "fetch", "search":
			sub = args[0]
			args = args[1:]
		}
	}
	root := "."
	query := ""
	if sub == "" || sub == "search" {
		if len(args) == 0 {
			fatalUsage("usage: kern docs <query> [root] | kern docs search <query> [root] | kern docs index [root] [--semantic] | kern docs clear")
		}
		query = args[0]
		args = args[1:]
	}
	if sub == "fetch" {
		if len(args) == 0 {
			fatalUsage("usage: kern docs fetch <url> [name] [root]")
		}
		rawURL := args[0]
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		if len(args) > 2 {
			root = args[2]
		}
		if f.root != "" {
			root = f.root
		}
		docsFetchCore(rawURL, name, root, f.semantic)
		return
	}
	if len(args) > 0 {
		root = args[0]
		args = args[1:]
	}
	if len(args) > 0 {
		fatalUsage("docs: unexpected extra argument %q (usage: kern docs <query> [root])", args[0])
	}
	if f.root != "" {
		root = f.root
	}
	if query != "" {
		// A read must never create its root (2026-11-10: a mistyped root
		// silently built ./<typo>/.kern and reported a confident no-match).
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			fatalUsage("docs: root %q is not an existing directory — a query never creates one (usage: kern docs <query> [root])", root)
		}
	}
	switch sub {
	case "index":
		var ix *docsearch.Index
		var err error
		if f.semantic {
			client := llm.NewEmbedder()
			if !client.Available() {
				fatal("ollama not reachable (semantic index requires a local Ollama); run without --semantic for deterministic indexing")
			}
			if !client.HasEmbeddingModel() {
				fatal("embedding model %q not installed (run: ollama pull %s)", llm.EmbedModel(), llm.EmbedModel())
			}
			docsearch.SemanticEmbedder = client
			ix, err = docsearch.IndexDirSemantic(root, client)
		} else {
			ix, err = docsearch.IndexDir(root)
		}
		if err != nil {
			fatal("docs index: %v", err)
		}
		if err := ix.Save(); err != nil {
			fatal("docs index: %v", err)
		}
		fmt.Printf("indexed %d chunks from %s\n", len(ix.Docs), root)
	case "clear":
		_ = os.RemoveAll(cache.Path("data", "docs"))
		_ = os.RemoveAll(cache.Path("data", "docs-fetch"))
		fmt.Println("cleared document index and fetched-doc cache")
	default:
		if query == "" {
			fatalUsage("usage: kern docs <query> [root] [--root ROOT] [--limit N] | kern docs index [root] [--semantic] | kern docs clear")
		}
		docsSearchCore(query, root, f.limit)
	}
}
