package main

import (
	"context"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/precache"
	"github.com/JayveerPrajapati/kern/internal/project"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// isTempOrMissingRoot reports whether root is a stale or ephemeral cache entry
// that should be excluded from `kern ast --all` cross-project search: the root
// no longer exists on disk, or it lives under a system temp directory (kern's
// own test fixtures under /var/folders/, /tmp/, $TMPDIR leak into the shared
// index cache and pollute results).
func isTempOrMissingRoot(root string) bool {
	if root == "" {
		return true
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return true // missing or not a directory
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	// System temp dirs where kern's tests create throwaway fixture projects.
	tempPrefixes := []string{
		filepath.Clean(os.TempDir()), // /var/folders/.../T on macOS, /tmp on Linux
		"/tmp",
		"/var/tmp",
	}
	for _, p := range tempPrefixes {
		if abs == p || strings.HasPrefix(abs, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func runPrecache(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	if f.once {
		rep := precache.Warm(root)
		fmt.Printf("kern: warmed %d summaries (%d cache hits), %d doc chunks, index %s, docs saved=%v in %s\n",
			rep.Warmed, rep.CacheHits, rep.DocChunks, rep.IndexStatus, rep.DocsSaved, rep.Dur.Round(time.Millisecond))
		return
	}
	interval := time.Duration(f.interval) * time.Second
	if interval <= 0 {
		interval = 60 * time.Second
	}
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; close(stop) }()
	fmt.Printf("kern: pre-caching %s every %s (Ctrl-C to stop)\n", root, interval)
	for rep := range precache.Watch(root, interval, stop) {
		if rep.SourceMiss {
			fmt.Printf("kern: no project at %s\n", root)
			return
		}
		fmt.Printf("kern: warmed %d (%d hits), %d doc chunks\n", rep.Warmed, rep.CacheHits, rep.DocChunks)
	}

}

func runIndex(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
	}
	// `kern index --status [--json]` is read-only: it reports the cached
	// index's health without rebuilding anything, so CI and agents can gate
	// on freshness cheaply.
	if f.status {
		status := indexStatus(root, f.strict)
		if f.json {
			printJSON(status)
			return
		}
		if status["built"].(bool) {
			fmt.Printf("index: BUILT (%d symbols, %d files, %d packages, version %d)\n",
				status["symbols"], status["files"], status["packages"], status["version"])
			fmt.Printf("  languages: %s\n", status["languages"])
			fmt.Printf("  stale: %v\n", status["stale"])
			fmt.Printf("  store: %s\n", status["store"])
		} else {
			fmt.Printf("index: NOT BUILT for %s\n", root)
		}
		return
	}
	ix, err := index.Build(root)
	if err != nil {
		fatal("%v", err)
	}
	if err := ix.Save(); err != nil {
		fatal("%v", err)
	}
	store := index.StorePath(root)
	if index.SQLiteEnabled() {
		if err := index.SaveSQLite(root, ix); err != nil {
			fatal("%v", err)
		}
		store = index.SQLitePath(root)
	}
	fmt.Printf("indexed %d symbols in %d files (%d packages) -> %s\n",
		len(ix.Symbols), len(ix.FileHashes), len(ix.Pkgs), store)
	fmt.Printf("languages: %s\n", strings.Join(ix.Languages(), ", "))

}

// indexStatus reports the cached index's state for `kern index --status`.
// Read-only: it never builds or saves an index. The returned map is
// JSON-ready so callers can render text or pass it straight to printJSON.
// strict selects FreshnessProofStrict (full content re-hash) over the default
// fast FreshnessProof (git tree-OID compare).
func indexStatus(root string, strict bool) map[string]any {
	status := map[string]any{
		"schema_version":      "2",
		"root":                root,
		"built":               false,
		"symbols":             0,
		"files":               0,
		"packages":            0,
		"version":             0,
		"stale":               true,
		"languages":           []string{},
		"store":               "",
		"precision_by_lang":   map[string]string{},
		"tree_sitter_enabled": index.TreesitterEnabled(),
	}
	ix, err := index.Load(root)
	if err != nil || ix == nil {
		return status
	}
	status["built"] = true
	status["symbols"] = len(ix.Symbols)
	status["files"] = len(ix.FileHashes)
	status["packages"] = len(ix.Pkgs)
	status["version"] = ix.Version
	status["stale"] = ix.Stale()
	status["languages"] = ix.Languages()
	store := index.StorePath(root)
	if index.SQLiteEnabled() {
		store = index.SQLitePath(root)
	}
	status["store"] = store
	// Per-language edge-precision tier (resolved/ast/heuristic) from the
	// index itself, so consumers (blueprint, CI, humans) can see which
	// languages are skipped under --precision strict without building.
	status["precision_by_lang"] = ix.PrecisionByLang
	// Content-addressed freshness proof: the contract the blueprint fixer
	// codes against. --strict forces a full content re-hash instead of the
	// git tree-OID fast path.
	proof := ix.FreshnessProof(root)
	if strict {
		proof = ix.FreshnessProofStrict(root)
	}
	status["freshness_proof"] = proof
	if ix.Identity != nil {
		status["index_identity"] = *ix.Identity
	}
	return status
}

func runWatch(rest []string) {
	root := "."
	interval := 5
	if len(rest) > 0 {
		root = rest[0]
	}
	mode := project.WatchMode(root)
	fmt.Printf("kern watch: monitoring %s (mode: %s)\n", root, mode)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := project.Watch(ctx, root, time.Duration(interval)*time.Second, func(changes []index.Change, ix *index.Index) {
		for _, c := range changes {
			fmt.Printf("[kern] %-8s %s\n", c.Kind, c.File)
		}
		fmt.Printf("[kern] index updated: %d symbols, %d packages (%s)\n",
			len(ix.Symbols), len(ix.Pkgs), strings.Join(ix.Languages(), ", "))
	}, func(err error) {
		fmt.Fprintf(os.Stderr, "[kern] watch error: %v\n", err)
	})
	if err != nil && err != context.Canceled {
		fatal("%v", err)
	}

}

func runAst(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern ast <pattern> [root] [--all]")
	}
	pattern := args[0]
	// Determine root: --root flag > positional arg > "" (CWD).
	root := f.root
	if root == "" && len(args) > 1 {
		root = args[1]
	}
	// --all searches across ALL cached project indexes. But if a root was
	// explicitly provided (--root or positional), scope to just that repo
	// instead — the user asked for one project, not every cache entry.
	if f.all && root == "" {
		files, err := os.ReadDir(cache.Path("index"))
		if err != nil {
			fatal("%v", err)
		}
		searched := 0
		skipped := 0
		for _, e := range files {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			ix, err := index.LoadFile(filepath.Join(cache.Path("index"), e.Name()))
			if err != nil {
				continue
			}
			// Skip stale cache entries: indexes whose root no longer exists
			// on disk, or whose root is a temp/ephemeral directory (kern's
			// own test fixtures under /var/folders/, /tmp/, or the system
			// temp dir leak into the cache and pollute --all searches).
			if ix.Root == "" || isTempOrMissingRoot(ix.Root) {
				skipped++
				continue
			}
			searched++
			for _, m := range ix.Search(pattern, 50) {
				fmt.Printf("%-28s %-10s %-7s %-24s %s:%d\n", ix.Root, m.Kind, m.Lang, m.FullName(), m.File, m.Line)
			}
		}
		idxWord := "index"
		if searched != 1 {
			idxWord = "indexes"
		}
		fmt.Fprintf(os.Stderr, "kern: --all searched %d cached project %s", searched, idxWord)
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, " (skipped %d stale/temp)", skipped)
		}
		fmt.Fprintln(os.Stderr)
		return
	}
	if f.all && root != "" {
		// --all with an explicit root: just search that one repo.
		fmt.Fprintf(os.Stderr, "kern: --all ignored (root %q specified)\n", root)
	}
	if root == "" {
		root = "."
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("%v", err)
	}
	for _, m := range ix.Search(pattern, 50) {
		fmt.Printf("%-10s %-7s %-24s %s:%d\n", m.Kind, m.Lang, m.FullName(), m.File, m.Line)
	}

}

func runRepos(rest []string) {
	if len(rest) == 0 || rest[0] == "list" {
		reg, err := intel.LoadRepos()
		if err != nil {
			fatal("%v", err)
		}
		if len(reg.Repos) == 0 {
			fmt.Println("no repos registered (kern repos add <path> [name])")
			return
		}
		for _, r := range reg.Repos {
			fmt.Printf("%-16s %s\n", r.Name, r.Root)
		}
		return
	}
	switch rest[0] {
	case "add":
		if len(rest) < 2 {
			fatalUsage("usage: kern repos add <path> [name]")
		}
		name := ""
		if len(rest) > 2 {
			name = rest[2]
		}
		reg, err := intel.LoadRepos()
		if err != nil {
			fatal("%v", err)
		}
		if err := reg.Add(rest[1], name); err != nil {
			fatal("%v", err)
		}
		if err := reg.Save(); err != nil {
			fatal("%v", err)
		}
		added, _ := reg.Get(name)
		if name == "" {
			added, _ = reg.Get(filepath.Base(rest[1]))
		}
		fmt.Printf("added %s -> %s\n", added.Name, added.Root)
	case "remove":
		if len(rest) < 2 {
			fatalUsage("usage: kern repos remove <name>")
		}
		reg, err := intel.LoadRepos()
		if err != nil {
			fatal("%v", err)
		}
		if !reg.Remove(rest[1]) {
			fatal("no repo named: %s", rest[1])
		}
		if err := reg.Save(); err != nil {
			fatal("%v", err)
		}
		fmt.Printf("removed %s\n", rest[1])
	default:
		fatalUsage("usage: kern repos (list|add <path> [name]|remove <name>)")
	}

}

func runSearch(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern search <query> [root] [--limit N] [--repos] [--json] [--semantic]")
	}
	query := args[0]
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 1 {
			root = args[1]
		}
	}
	limit := f.limit
	if limit <= 0 {
		limit = 20
	}
	if f.repos {
		hits := intel.SearchRepos(query, limit)
		if len(hits) == 0 {
			fmt.Printf("no symbols matched across repos: %s\n", query)
			return
		}
		if f.json {
			printJSON(hits)
			return
		}
		fmt.Println(intel.FormatRepoHits(hits))
		return
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("%v", err)
	}
	var matches []index.Symbol
	if f.semantic {
		client := llm.NewEmbedder()
		if !client.HasEmbeddingModel() {
			fatal("embedding model %q not installed (run: ollama pull %s)", llm.EmbedModel(), llm.EmbedModel())
		}
		matches = intel.SemanticSearch(ix, query, limit, client)
	} else {
		matches = intel.RankedSearch(ix, query, limit)
	}
	if len(matches) == 0 {
		fmt.Printf("no symbols matched: %s\n", query)
		return
	}
	if f.json {
		printJSON(matches)
		return
	}
	for _, m := range matches {
		fmt.Printf("%-10s %-7s %-24s %s:%d\n", m.Kind, m.Lang, m.FullName(), m.File, m.Line)
	}

}

func runFts(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern fts \"<query>\" [root] [--limit N]")
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 1 {
			root = args[1]
		}
	}
	limit := f.limit
	if limit <= 0 {
		limit = 20
	}
	matches, err := index.FTS5Search(root, args[0], limit)
	if err != nil {
		fatal("%v", err)
	}
	if f.json {
		printJSON(matches)
		return
	}
	for _, m := range matches {
		fmt.Printf("%s %s %s:%d\n", m.Kind, m.FullName(), m.File, m.Line)
	}

}
