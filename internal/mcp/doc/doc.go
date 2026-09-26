// Package doc owns the doc-family tool bodies (kern_doc_search,
// kern_doc_index, kern_doc_fetch, kern_commitmsg, kern_precache) as
// plain functions. Search takes a Hooks bundle (index loading +
// per-call authorization for its code-results fallback) injected by the
// mcp adapter; the rest are Server-independent. SanitizeDocName is the
// canonical doc-name sanitizer (server.go aliases it).
package doc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/commitmsg"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/fetch"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/llm"
	mcpgov "github.com/JayveerPrajapati/kern/internal/mcp/gov"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/precache"
	"github.com/JayveerPrajapati/kern/internal/strutil"
)

// Hooks carries the kernel callbacks Search needs: code-index loading
// and the per-call governor for the code-results fallback.
type Hooks struct {
	LoadIndex   func(ctx context.Context, root string) (*index.Index, error)
	NewGovernor func(ctx context.Context, args map[string]any, ix *index.Index) (*mcpgov.Governor, error)
}

// sanitizeDocName constrains a doc name to a safe cache filename:
// lowercase alphanumerics and dashes only. Path separators, dot-dot and
// other punctuation are replaced (or collapse to nothing), so a name can
// never escape the cache root via ../ or produce a bogus index key.
// Empty input (or a name with no slug-able characters) yields an error.
// clip trims a string to n bytes for a tool summary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// docSearchSlug derives a filesystem-safe doc name from a URL, e.g.
// https://react.dev/reference/usestate -> react-dev-reference-usestate.
func docSearchSlug(rawURL string) string {
	return strutil.DocSlug(rawURL)
}

// hasSlugChar reports whether s contains at least one character a slug keeps,
// so sanitizeDocName can reject names that would collapse to nothing.
func hasSlugChar(s string) bool {
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// SanitizeDocName collapses path separators and traversal
// sequences into a safe flat doc slug.
func SanitizeDocName(name string) (string, error) {
	if !hasSlugChar(name) {
		return "", fmt.Errorf("invalid doc name %q", name)
	}
	return strutil.Slug(name), nil
}

func Search(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	query := mcpargs.ArgString(args, "query")
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	root := mcpargs.ArgString(args, "root")
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
	if v := mcpargs.ArgString(args, "k"); v != "" {
		n, err := mcpargs.AtoiArg(v, k)
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
		if cix, err := h.LoadIndex(ctx, root); err == nil && cix != nil {
			codeIx = cix
			codeMatches = intel.RankedSearch(cix, query, k)
			if gov, err := h.NewGovernor(ctx, args, cix); err == nil && gov != nil {
				var kept []index.Symbol
				for _, m := range codeMatches {
					if gov.Allowed[m.FullName()] {
						kept = append(kept, m)
					}
				}
				codeMatches = kept
			}
		}
	}

	if len(results) == 0 && len(codeMatches) == 0 {
		// N3: a repo with no indexed docs gets an explanation, not a bare
		// "no matching document fragments" that reads as a confident miss.
		if len(ix.Docs) == 0 {
			return "no matching document fragments — this repo has no documentation indexed (kern docs searches only indexed repo docs; run `kern docs index <root>` to index a docs tree)", nil
		}
		return fmt.Sprintf("no matching document fragments (query matched nothing in %d indexed fragments)", len(ix.Docs)), nil
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

func Index(ctx context.Context, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	var ix *docsearch.Index
	var err error
	if mcpargs.ArgString(args, "semantic") == "true" || mcpargs.ArgString(args, "semantic") == "1" {
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

func Fetch(ctx context.Context, args map[string]any) (string, error) {
	rawURL := mcpargs.ArgString(args, "url")
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	name := mcpargs.ArgString(args, "name")
	res, err := fetch.Fetch(rawURL, 0)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = docSearchSlug(rawURL)
	} else if name, err = SanitizeDocName(name); err != nil {
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
	if mcpargs.ArgString(args, "semantic") == "true" {
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

func Commitmsg(ctx context.Context, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	var out []byte
	var err error
	staged := mcpargs.ArgString(args, "staged")
	rng := mcpargs.ArgString(args, "range")
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

func Precache(ctx context.Context, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
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
