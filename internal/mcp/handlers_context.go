package mcp

import (
	"context"
	"errors"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/brief"
	"github.com/JayveerPrajapati/kern/internal/code"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/fit"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/pack"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Server) handleCompact(ctx context.Context, args map[string]any) (string, error) {
	{
		path := argString(args, "path")
		if path == "" {
			return "", fmt.Errorf("path is required")
		}
		root := argString(args, "root")
		if root == "" && filepath.IsAbs(path) {
			if cwd, err := os.Getwd(); err == nil && within(cwd, path) {
				root = cwd
			} else {
				for _, r := range s.roots {
					if r != "/" && within(r, path) {
						root = r
						break
					}
				}
			}
		}
		abs, err := rootedPath(root, path)
		if err != nil {
			return "", err
		}
		content, err := code.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("cannot read %s: %w", path, err)
		}
		// The default tier preserves the historical behavior: a symbolic
		// summary. tier=full returns the whole file, tier=folded returns
		// signatures with bodies elided (each elision counts the removed
		// lines, so the agent can request the full file knowing what it
		// missed).
		tier := code.TierSummary
		if v := argString(args, "tier"); v != "" {
			t, terr := code.ParseTier(v)
			if terr != nil {
				return "", terr
			}
			tier = t
		}
		if argBool(args, "terse_code") || argBool(args, "terse") {
			content = kernctx.PruneCode(abs, content, true)
		}
		return code.RenderTier(abs, content, tier), nil

	}
}

func (s *Server) handleBuddy(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		// Warm the index in the background so later calls render the fast path.
		go func() {
			if werr := brief.Warm(root); werr != nil {
				fmt.Fprintf(os.Stderr, "kern: buddy: could not warm index for %q: %v\n", root, werr)
			}
		}()
		out, err := brief.Build(root)
		if err != nil {
			return "", err
		}
		return out, nil

	}
}

func (s *Server) handleOnboard(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			root, _ = os.Getwd()
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			abs = root
		}
		abs = filepath.Clean(abs)

		var lb strings.Builder

		// 1. Register the repo if not already present in the registry.
		registered := ""
		reg, rerr := intel.LoadRepos()
		if rerr != nil {
			registered = "error: " + rerr.Error()
		} else {
			already := false
			for _, r := range reg.Repos {
				if filepath.Clean(r.Root) == abs {
					already = true
					break
				}
			}
			if already {
				registered = "present"
			} else if aerr := reg.Add(abs, ""); aerr != nil {
				registered = "error: " + aerr.Error()
			} else if serr := reg.Save(); serr != nil {
				registered = "added (save error: " + serr.Error() + ")"
			} else {
				registered = "added"
			}
		}

		// 2. Ensure the index is built/refreshed (loadIndex auto-builds if
		// stale or missing — do NOT build manually here).
		indexed := ""
		timing := ""
		t0 := time.Now()
		prev, _ := index.Load(abs)
		ix, ierr := s.loadIndex(ctx, abs)
		elapsed := time.Since(t0)
		if ierr != nil {
			indexed = "error: " + ierr.Error()
		} else {
			edges := 0
			for _, callees := range ix.Calls {
				edges += len(callees)
			}
			staleFiles := 0
			if prev == nil {
				staleFiles = len(ix.FileHashes)
			} else if ix.ReusedResults() > 0 {
				staleFiles = len(ix.FileHashes) - ix.ReusedResults()
			} else if prev.Stale() {
				staleFiles = len(ix.FileHashes)
			}
			indexed = fmt.Sprintf("%d symbols, %d call edges, %d files", len(ix.Symbols), edges, len(ix.FileHashes))
			timing = fmt.Sprintf("stale files: %d, rebuild: %.1fs", staleFiles, elapsed.Seconds())
		}

		// 3. AGENTS.md wiring, only if the file is missing. setup.Wire cannot
		// be called from the MCP package (internal/setup's tests import
		// internal/mcp, which would create an import cycle), so report the
		// missing file and direct the caller to `kern setup` / `kern onboard`.
		wired := ""
		if _, serr := os.Stat(filepath.Join(abs, "AGENTS.md")); errors.Is(serr, fs.ErrNotExist) {
			wired = "missing — run kern setup (or kern onboard) to write it"
		} else {
			wired = "present"
		}

		fmt.Fprintf(&lb, "root:       %s\n", abs)
		fmt.Fprintf(&lb, "registered: %s\n", registered)
		fmt.Fprintf(&lb, "indexed:    %s\n", indexed)
		if timing != "" {
			fmt.Fprintf(&lb, "timing:     %s\n", timing)
		}
		fmt.Fprintf(&lb, "AGENTS.md:  %s\n", wired)
		fmt.Fprintf(&lb, "next:       explore the repo with kern_explore / kern_code_graph, or run kern_buddy for a session digest\n")
		return lb.String(), nil

	}
}

func (s *Server) handleProjectMap(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		// Default to 0 (unlimited) so the full project map is returned; a
		// caller can still cap it with max_files. Previously hardcoded to 500,
		// which silently truncated repos larger than that (e.g. 758 files).
		maxFiles := 0
		if v := argString(args, "max_files"); v != "" {
			n, err := atoiArg(v, maxFiles)
			if err != nil {
				return "", err
			}
			maxFiles = n
		}
		p, err := code.BuildProject(root, maxFiles, 200)
		if err != nil {
			return "", err
		}
		return p.Render(), nil

	}
}

func (s *Server) handlePack(ctx context.Context, args map[string]any) (string, error) {
	{
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		opts := pack.Options{}
		if v := argString(args, "max_tokens"); v != "" {
			n, err := atoiArg(v, opts.MaxTokens)
			if err != nil {
				return "", err
			}
			opts.MaxTokens = n
		} else {
			opts.MaxTokens = 8000
		}
		if v := argString(args, "instructions"); v != "" {
			opts.SkipInstructions = v == "false"
		}
		// Content tier: fold=true is shorthand for tier=folded. Default (no
		// args) packs full source, exactly as before.
		if argString(args, "fold") == "true" {
			opts.Tier = code.TierFolded
		} else if v := argString(args, "tier"); v != "" {
			t, err := code.ParseTier(v)
			if err != nil {
				return "", err
			}
			opts.Tier = t
		}
		// Graph mode: pack the call-graph snapshot (adjacency + signatures +
		// fingerprint) instead of file contents. symbol selects the subgraph;
		// an empty symbol packs the whole graph. symbol is ignored when
		// graph=false (files mode is unaffected, documented in the tool def).
		if argString(args, "graph") == "true" {
			opts.GraphSymbol = argString(args, "symbol")
			gb, err := pack.BuildGraph(root, opts)
			if err != nil {
				return "", err
			}
			return gb.Render(), nil
		}
		b, err := pack.Build(root, opts)
		if err != nil {
			return "", err
		}
		if argString(args, "format") == "json" {
			return b.JSON()
		}
		return b.Render(), nil

	}
}

func (s *Server) handleFitContext(ctx context.Context, args map[string]any) (string, error) {
	root := argString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	maxTokens := 8000
	if v := argString(args, "max_tokens"); v != "" {
		if n, err := atoiArg(v, maxTokens); err == nil {
			maxTokens = n
		}
	}
	var files []string
	if v := argString(args, "files"); v != "" {
		for _, f := range strings.Split(v, ",") {
			if trimmed := strings.TrimSpace(f); trimmed != "" {
				files = append(files, trimmed)
			}
		}
	}
	var symbols []string
	if v := argString(args, "symbols"); v != "" {
		for _, sym := range strings.Split(v, ",") {
			if trimmed := strings.TrimSpace(sym); trimmed != "" {
				symbols = append(symbols, trimmed)
			}
		}
	}
	query := argString(args, "query")

	res, err := fit.FitContext(ctx, fit.Request{
		Root:      root,
		MaxTokens: maxTokens,
		Files:     files,
		Symbols:   symbols,
		Query:     query,
	})
	if err != nil {
		return "", err
	}
	if argString(args, "format") == "json" {
		return res.RenderJSON(), nil
	}
	return res.Content, nil
}
