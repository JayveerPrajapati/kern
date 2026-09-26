// Package context owns the context-family MCP tool bodies (kern_compact_file,
// kern_buddy, kern_onboard, kern_project_map, kern_pack, kern_fit_context) as
// plain functions. Compact and Onboard take a Hooks bundle (workspace roots
// + session index loading) injected by the mcp adapter; the rest are
// Server-independent.
package context

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/brief"
	"github.com/JayveerPrajapati/kern/internal/code"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/fit"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/pack"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

// Hooks carries the kernel callbacks Compact and Onboard need: the workspace
// roots Compact consults to resolve a bare absolute path, and the session
// index loader Onboard uses to refresh the project index. Neither handler
// touches the service layer or platform cache, so no other hooks are needed.
type Hooks struct {
	LoadIndex func(ctx context.Context, root string) (*index.Index, error)
	Roots     func() []string
}

// Compact renders a symbolic summary of a source file (or the whole file with
// tier=full, signatures with tier=folded). A bare absolute path is resolved
// against the workspace roots so the file can be read without a root
// argument.
func Compact(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	path := mcpargs.ArgString(args, "path")
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	root := mcpargs.ArgString(args, "root")
	if root == "" && filepath.IsAbs(path) {
		if cwd, err := os.Getwd(); err == nil && within(cwd, path) {
			root = cwd
		} else if h.Roots != nil {
			for _, r := range h.Roots() {
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
	if v := mcpargs.ArgString(args, "tier"); v != "" {
		t, terr := code.ParseTier(v)
		if terr != nil {
			return "", terr
		}
		tier = t
	}
	if mcpargs.ArgBool(args, "terse_code") || mcpargs.ArgBool(args, "terse") {
		content = kernctx.PruneCode(abs, content, true)
	}
	return code.RenderTier(abs, content, tier), nil
}

// Buddy builds a session digest for the root (index warm-up happens in the
// background so later calls render the fast path).
func Buddy(ctx context.Context, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
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

// Onboard registers the root in the repo registry, refreshes its index and
// reports the AGENTS.md wiring status.
func Onboard(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
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
	ix, ierr := h.LoadIndex(ctx, abs)
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
	fmt.Fprintf(&lb, "next:       explore the repo with kern_explore / kern_graph, or run kern_buddy for a session digest\n")
	return lb.String(), nil
}

// ProjectMap renders the project file map, optionally capped at max_files.
func ProjectMap(ctx context.Context, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	// Default to 0 (unlimited) so the full project map is returned; a
	// caller can still cap it with max_files. Previously hardcoded to 500,
	// which silently truncated repos larger than that (e.g. 758 files).
	maxFiles := 0
	if v := mcpargs.ArgString(args, "max_files"); v != "" {
		n, err := mcpargs.AtoiArg(v, maxFiles)
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

// Pack bundles a context pack: full source by default, tier=folded for
// signatures, graph=true for the call-graph snapshot.
func Pack(ctx context.Context, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	opts := pack.Options{}
	if v := mcpargs.ArgString(args, "max_tokens"); v != "" {
		n, err := mcpargs.AtoiArg(v, opts.MaxTokens)
		if err != nil {
			return "", err
		}
		opts.MaxTokens = n
	} else {
		opts.MaxTokens = 8000
	}
	if v := mcpargs.ArgString(args, "instructions"); v != "" {
		opts.SkipInstructions = v == "false"
	}
	// Content tier: fold=true is shorthand for tier=folded. Default (no
	// args) packs full source, exactly as before.
	if mcpargs.ArgString(args, "fold") == "true" {
		opts.Tier = code.TierFolded
	} else if v := mcpargs.ArgString(args, "tier"); v != "" {
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
	if mcpargs.ArgString(args, "graph") == "true" {
		opts.GraphSymbol = mcpargs.ArgString(args, "symbol")
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
	if mcpargs.ArgString(args, "format") == "json" {
		return b.JSON()
	}
	return b.Render(), nil
}

// FitContext fits a code context (files, symbols, query) into a token budget.
func FitContext(ctx context.Context, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	maxTokens := 8000
	if v := mcpargs.ArgString(args, "max_tokens"); v != "" {
		if n, err := mcpargs.AtoiArg(v, maxTokens); err == nil {
			maxTokens = n
		}
	}
	var files []string
	if v := mcpargs.ArgString(args, "files"); v != "" {
		for _, f := range strings.Split(v, ",") {
			if trimmed := strings.TrimSpace(f); trimmed != "" {
				files = append(files, trimmed)
			}
		}
	}
	var symbols []string
	if v := mcpargs.ArgString(args, "symbols"); v != "" {
		for _, sym := range strings.Split(v, ",") {
			if trimmed := strings.TrimSpace(sym); trimmed != "" {
				symbols = append(symbols, trimmed)
			}
		}
	}
	query := mcpargs.ArgString(args, "query")

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
	if mcpargs.ArgString(args, "format") == "json" {
		return res.RenderJSON(), nil
	}
	return res.Content, nil
}

// within reports whether child is parent or a descendant of parent. Unlike
// verification.WithinAbs it also rejects an absolute rel path (e.g. a different
// drive root on Windows). Mirrors the mcp adapter's helper so bare absolute
// paths resolve against workspace roots identically.
func within(parent, child string) bool {
	if !verification.WithinAbs(parent, child) {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return !filepath.IsAbs(rel)
}

// rootedPath resolves p for a file-reading tool. When root is given, the path
// must stay inside it (rejecting "..", absolute paths outside, and symlink
// escapes). A rootless call may only reference a path relative to the current
// working directory: an absolute path is rejected outright, since otherwise a
// caller could pass e.g. path=/etc/shadow and read any file on the system
// outside the confined workspace. Mirrors the mcp adapter's rootedPath.
func rootedPath(root, p string) (string, error) {
	if root == "" {
		if filepath.IsAbs(p) {
			return "", fmt.Errorf("absolute path requires root argument")
		}
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return withinRoot(cwd, p)
	}
	return withinRoot(root, p)
}

// withinRoot resolves file against root (absolute paths are used as-is) and
// requires the result to stay inside root, rejecting `..` escapes, absolute
// paths that point outside the project boundary, and symlink escapes (a
// symlink inside the project that points outside). It returns the resolved
// absolute path. Mirrors the mcp adapter's withinRoot.
func withinRoot(root, file string) (string, error) {
	var abs string
	if filepath.IsAbs(file) {
		abs = filepath.Clean(file)
	} else {
		abs = filepath.Join(root, file)
	}
	// Resolve symlinks on both the root and the candidate so a symlink inside
	// the project that points outside cannot read/escape the project boundary.
	// A candidate that does not exist yet (e.g. a file about to be written)
	// cannot be resolved directly, so resolve the NEAREST EXISTING ANCESTOR
	// and re-append the remaining components: a symlinked parent directory
	// (root/link -> /etc) is then judged by its real location instead of its
	// lexical text, closing the escape where the old pure-lexical fallback
	// let root/link/newfile land in /etc.
	rRoot, rerr := filepath.EvalSymlinks(root)
	if rerr != nil {
		rRoot = root
	}
	real := abs
	var rem []string
	probe := abs
	for {
		if r, err := filepath.EvalSymlinks(probe); err == nil {
			real = r
			if len(rem) > 0 {
				real = filepath.Join(append([]string{r}, rem...)...)
			}
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			// Nothing resolvable up to the filesystem root: fall back to the
			// lexical Clean+Rel check rather than denying an unresolvable path.
			real = abs
			break
		}
		rem = append([]string{filepath.Base(probe)}, rem...)
		probe = parent
	}
	rel, err := filepath.Rel(rRoot, real)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", file, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %s escapes project root %s", abs, root)
	}
	return abs, nil
}
