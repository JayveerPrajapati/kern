package mcp

import (
	"context"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/commitmsg"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/fetch"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/precache"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleDocSearch(ctx context.Context, args map[string]any) (string, error) {
	{
		query := argString(args, "query")
		if query == "" {
			return "", fmt.Errorf("query is required")
		}
		root := argString(args, "root")
		ix := docsearch.Load(root)
		if ix == nil {
			var err error
			ix, err = docsearch.IndexDir(root)
			if err != nil {
				return "", err
			}
			if err := ix.Save(); err != nil {
				return "", err
			}
		}
		k := 5
		if v := argString(args, "k"); v != "" {
			n, err := atoiArg(v, k)
			if err != nil {
				return "", err
			}
			k = n
		}
		// If the persisted index carries dense vectors, re-attach the local
		// embedder so queries fuse the semantic signal too.
		hasDense := false
		for _, d := range ix.Docs {
			if len(d.Semantic) > 0 {
				hasDense = true
				break
			}
		}
		if hasDense {
			client := llm.NewEmbedder()
			if client.HasEmbeddingModel() {
				docsearch.SetSemanticEmbedder(client)
			}
		}
		results := ix.Search(query, k)

		var codeMatches []index.Symbol
		var codeIx *index.Index
		if len(results) == 0 || (len(results) > 0 && results[0].Sim <= 1.0) {
			if cix, err := s.loadIndex(ctx, root); err == nil && cix != nil {
				codeIx = cix
				codeMatches = intel.RankedSearch(cix, query, k)
				if gov, err := s.newGovernor(ctx, args, cix); err == nil && gov != nil {
					var kept []index.Symbol
					for _, m := range codeMatches {
						if gov.allowed[m.FullName()] {
							kept = append(kept, m)
						}
					}
					codeMatches = kept
				}
			}
		}

		if len(results) == 0 && len(codeMatches) == 0 {
			return "no matching document fragments", nil
		}

		var b strings.Builder
		if len(results) > 0 && len(codeMatches) > 0 {
			b.WriteString("## Documentation\n")
			for i, r := range results {
				fmt.Fprintf(&b, "#%d score=%.3f %s:%d\n", i+1, r.Sim, r.Doc.Chunk.File, r.Doc.Chunk.Start)
				b.WriteString(r.Doc.Chunk.Text)
				if i < len(results)-1 {
					b.WriteString("\n\n")
				}
			}
			b.WriteString("\n\n## Code\n")
			for _, m := range codeMatches {
				b.WriteString(m.Kind)
				b.WriteString(" ")
				b.WriteString(m.FullName())
				b.WriteString(" ")
				b.WriteString(m.File)
				b.WriteString(":")
				b.WriteString(strconv.Itoa(m.Line))
				if codeIx != nil && codeIx.IsGenerated(m.File) {
					b.WriteString(" (generated)")
				}
				b.WriteString("\n")
			}
			return strings.TrimSuffix(b.String(), "\n"), nil
		}

		if len(results) > 0 {
			for i, r := range results {
				fmt.Fprintf(&b, "#%d score=%.3f %s:%d\n", i+1, r.Sim, r.Doc.Chunk.File, r.Doc.Chunk.Start)
				b.WriteString(r.Doc.Chunk.Text)
				if i < len(results)-1 {
					b.WriteString("\n\n")
				}
			}
			return b.String(), nil
		}

		if len(codeMatches) > 0 {
			b.WriteString("## Code\n")
			for _, m := range codeMatches {
				b.WriteString(m.Kind)
				b.WriteString(" ")
				b.WriteString(m.FullName())
				b.WriteString(" ")
				b.WriteString(m.File)
				b.WriteString(":")
				b.WriteString(strconv.Itoa(m.Line))
				if codeIx != nil && codeIx.IsGenerated(m.File) {
					b.WriteString(" (generated)")
				}
				b.WriteString("\n")
			}
			return strings.TrimSuffix(b.String(), "\n"), nil
		}

		return "", nil
	}
}

func (s *Server) handleDocIndex(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		var ix *docsearch.Index
		var err error
		if argString(args, "semantic") == "true" || argString(args, "semantic") == "1" {
			client := llm.NewEmbedder()
			if !client.Available() {
				return "", fmt.Errorf("ollama not reachable (semantic index requires a local Ollama); run kern_doc_index without semantic for deterministic indexing")
			}
			if !client.HasEmbeddingModel() {
				return "", fmt.Errorf("embedding model %q not installed (run: ollama pull %s)", llm.EmbedModel(), llm.EmbedModel())
			}
			docsearch.SetSemanticEmbedder(client)
			ix, err = docsearch.IndexDirSemantic(root, client)
		} else {
			ix, err = docsearch.IndexDir(root)
		}
		if err != nil {
			return "", err
		}
		if err := ix.Save(); err != nil {
			return "", err
		}
		return "indexed " + strconv.Itoa(len(ix.Docs)) + " chunks from " + root, nil
	}
}

func (s *Server) handleDocFetch(ctx context.Context, args map[string]any) (string, error) {
	{
		rawURL := argString(args, "url")
		if rawURL == "" {
			return "", fmt.Errorf("url is required")
		}
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		name := argString(args, "name")
		res, err := fetch.Fetch(rawURL, 0)
		if err != nil {
			return "", err
		}
		if name == "" {
			name = docSearchSlug(rawURL)
		} else if name, err = sanitizeDocName(name); err != nil {
			return "", err
		}
		if err := os.MkdirAll(cache.Path("data", "docs-fetch"), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(cache.Path("data", "docs-fetch", name+".md"), []byte(res.Text), 0o600); err != nil {
			return "", err
		}
		added, err := docsearch.MergeFetched(root, name, res.Text)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		if res.Truncated {
			fmt.Fprintf(&b, "[Warning: document was truncated at size limit]\n\n")
		}
		fmt.Fprintf(&b, "fetched %s -> fetch/%s.md (%d chars, %d chunks indexed into %s)\n\n", rawURL, name, len(res.Text), added, root)
		if res.Title != "" {
			fmt.Fprintf(&b, "# %s\n\n", res.Title)
		}
		if argString(args, "semantic") == "true" {
			client := llm.NewEmbedder()
			if !client.HasEmbeddingModel() {
				fmt.Fprintf(&b, "note: semantic embeddings skipped (%s not installed)\n\n", llm.EmbedModel())
			} else {
				embedded, eerr := docsearch.ReembedFetch(root, name, client)
				if eerr != nil {
					return "", eerr
				}
				if embedded > 0 {
					fmt.Fprintf(&b, "semantic embeddings attached to %d fetched chunks\n\n", embedded)
				}
			}
		}
		b.WriteString(clip(res.Text, 800))
		return b.String(), nil

	}
}

func (s *Server) handleCommitmsg(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		var out []byte
		var err error
		staged := argString(args, "staged")
		rng := argString(args, "range")
		// A crafted range starting with "-" would be parsed by git as an
		// option rather than a revision range: reject it fail-closed.
		if rng != "" && strings.HasPrefix(rng, "-") {
			return "", fmt.Errorf("invalid range %q: must not start with -", rng)
		}
		if staged == "true" || staged == "1" {
			out, err = exec.CommandContext(ctx, "git", "-C", root, "diff", "--cached").Output()
		} else if rng != "" {
			out, err = exec.CommandContext(ctx, "git", "-C", root, "diff", "--unified=0", rng).Output()
		} else {
			out, err = exec.CommandContext(ctx, "git", "-C", root, "diff", "--cached").Output()
			if err != nil || len(strings.TrimSpace(string(out))) == 0 {
				out, err = exec.CommandContext(ctx, "git", "-C", root, "diff", "HEAD").Output()
				if err != nil || len(strings.TrimSpace(string(out))) == 0 {
					out, err = exec.CommandContext(ctx, "git", "-C", root, "diff").Output()
				}
			}
		}
		if err != nil {
			return "", fmt.Errorf("git diff failed: %w", err)
		}
		return commitmsg.Generate(string(out)).String(), nil

	}
}

func (s *Server) handlePrecache(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root = "."
		}
		rep := precache.Warm(root)
		if rep.SourceMiss {
			return "no project at " + root, nil
		}
		return fmt.Sprintf("pre-cached %d summaries (%d hits), %d doc chunks (docs saved=%v) in %s",
			rep.Warmed, rep.CacheHits, rep.DocChunks, rep.DocsSaved, rep.Dur.Round(time.Millisecond)), nil

	}
}
