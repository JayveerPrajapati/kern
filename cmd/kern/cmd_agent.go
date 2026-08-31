package main

import (
	"encoding/json"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/fetch"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"os"
	"strings"
)

func runTeam(rest []string) {
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	text, err := renderTeamText(root)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Print(text)

}

// runWorkflow runs an intent through the agent team workflow.
func runWorkflow(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if f.task != "" {
		text, err := runWorkflowResumeCLI(root, f.task)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Print(text)
		return
	}
	intent := strings.Join(args, " ")
	if intent == "" {
		fatalUsage("usage: kern workflow <intent> [--task TASK_ID] [--root ROOT]")
	}
	text, err := runWorkflowCLI(root, intent)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Print(text)
}

func runLoop(cmd string, rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern %s <intent> [--level L0..L5] [--root ROOT]", cmd)
	}
	intent := args[0]
	text, err := runLoopCLI(root, f.level, intent)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Print(text)

}

func runIncident(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) < 1 || args[0] == "" {
		fatalUsage("usage: kern incident <alert-json> [snapshot-json] [--root ROOT]")
	}
	var al domain.Alert
	if err := json.Unmarshal([]byte(args[0]), &al); err != nil {
		fatal("invalid alert JSON: %v", err)
	}
	// Route through TaskService.InvestigateIncident for the full lifecycle.
	p, err := app.New(root)
	if err != nil {
		fatal("%v", err)
	}
	if len(args) > 1 && args[1] != "" {
		store, err := runtime.ParseSnapshot([]byte(args[1]))
		if err != nil {
			fatal("invalid snapshot JSON: %v", err)
		}
		p.WithRuntimeSource(store)
	}
	ts := app.NewTaskService(p, nil).WithPRProvider(app.AutoPRProvider())
	t, inc, text, err := ts.InvestigateIncident(al)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Print(text)
	fmt.Printf("[task: %s — state: %s — incident: %s]\n", t.ID, t.State, inc.ID)
}

func runDocs(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	sub := ""
	if len(args) > 0 && (args[0] == "index" || args[0] == "clear" || args[0] == "fetch") {
		sub = args[0]
		args = args[1:]
	}
	root := "."
	query := ""
	if sub == "" && len(args) > 0 {
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
		res, err := fetch.Fetch(rawURL, 0)
		if err != nil {
			fatal("%v", err)
		}
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
		if f.semantic {
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
		fmt.Printf("fetched %s -> %s (%d chars, %d chunks indexed into %s doc index)\n", rawURL, name, len(res.Text), added, root)
		if res.Title != "" {
			fmt.Printf("# %s\n\n", res.Title)
		}
		fmt.Println(clipText(res.Text, 600))
		return
	}
	if len(args) > 0 {
		root = args[0]
	}
	if f.root != "" {
		root = f.root
	}
	if sub == "index" {
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
			fatal("%v", err)
		}
		if err := ix.Save(); err != nil {
			fatal("%v", err)
		}
		fmt.Printf("indexed %d chunks from %s\n", len(ix.Docs), root)
	} else if sub == "clear" {
		_ = os.RemoveAll(cache.Path("data", "docs"))
		_ = os.RemoveAll(cache.Path("data", "docs-fetch"))
		fmt.Println("cleared document index and fetched-doc cache")
	} else {
		if query == "" {
			fatalUsage("usage: kern docs <query> [root] [--root ROOT] [--limit N] | kern docs index [root] [--semantic] | kern docs clear")
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
		// If the persisted index carries dense vectors, re-attach the
		// local embedder so queries fuse the semantic signal too.
		if hasSemantic(ix) {
			client := llm.NewEmbedder()
			if client.HasEmbeddingModel() {
				docsearch.SemanticEmbedder = client
			}
		}
		k := f.limit
		results := ix.Search(query, k)
		if len(results) == 0 {
			fmt.Println("no matching document fragments")
			return
		}
		for i, r := range results {
			fmt.Printf("#%d sim=%.3f %s:%d\n", i+1, r.Sim, r.Doc.Chunk.File, r.Doc.Chunk.Start)
			body := strings.ReplaceAll(r.Doc.Chunk.Text, "\n", " ")
			if len(body) > 300 {
				body = body[:300] + "…"
			}
			fmt.Printf("  %s\n", body)
		}
	}

}
