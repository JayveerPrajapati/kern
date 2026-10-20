package graphcli

import (
	"context"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/clietag"
	"github.com/JayveerPrajapati/kern/internal/doctor"
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
	// L15: single pass is the DEFAULT; --watch opts into the never-exiting
	// daemon. --once stays as an explicit no-op alias (the opencode plugin
	// calls `kern precache --once` and must keep working).
	watch := false
	precacheArgs := make([]string, 0, len(rest))
	for _, a := range rest {
		if a == "--watch" {
			watch = true
			continue
		}
		precacheArgs = append(precacheArgs, a)
	}
	f, args := parseFlagsOrDie(precacheArgs)
	// A silently-ignored --interval (single-pass mode) is a user trap: pass
	// it without --watch and the daemon-only setting just disappears. Fail
	// loud instead so the invocation is corrected, not quietly half-honored.
	if (!watch || f.once) && f.interval > 0 {
		fatal("--interval only applies to watch mode — pass --watch (and drop --once), or drop --interval")
	}
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 {
		root = args[0]
	}
	if !watch || f.once {
		rep := precache.Warm(root)
		fmt.Printf("kern: warmed %d summaries (%d cache hits), %d doc chunks, index %s, docs saved=%v in %s\n",
			rep.Warmed, rep.CacheHits, rep.DocChunks, rep.IndexStatus, rep.DocsSaved, rep.Dur.Round(time.Millisecond))
		return
	}
	// Watch mode is a never-exiting daemon: validate the root first so a
	// typo'd --root fails loud (rc=1) instead of silently watching the wrong
	// directory forever.
	if st, serr := os.Stat(root); serr != nil || !st.IsDir() {
		fatal("precache: root %q does not exist", root)
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

// runEnsureFresh implements `kern index ensure-fresh [root] [--json]`: the
// consolidated freshness subcommand (Phase 2). In ONE invocation it loads the
// cached index, cheaply checks staleness via the git tree OID (no content
// walk), updates (or full-builds) when stale, strictly re-verifies from disk,
// and reports the outcome — replacing the former probe → update → re-verify →
// loose-check subprocess sequence (~2.6s) with a single process. It exits 2
// when the index is stale and did not converge after a rebuild (fail-closed);
// the JSON is still printed so callers can inspect the proof.
func runEnsureFresh(jsonOut bool, root string) {
	res, err := index.EnsureFresh(root)
	if err != nil {
		fatal("Index: %v", err)
	}
	if jsonOut {
		printJSON(res)
	} else {
		fmt.Printf("index: %s (%d symbols, %d files, %d packages, version %d)\n",
			res.Freshness, res.Symbols, res.Files, res.Packages, res.Version)
		if res.CallResolution.Total > 0 {
			cr := res.CallResolution
			pct := 100 * cr.Unresolved / cr.Total
			if cr.External+cr.InRepo == cr.Unresolved {
				fmt.Printf("  call resolution: %d/%d callees (%d%% unresolved: %d external stdlib/vendor, %d in-repo/untyped)\n",
					cr.Total-cr.Unresolved, cr.Total, pct, cr.External, cr.InRepo)
			} else {
				fmt.Printf("  call resolution: %d/%d callees (%d%% unresolved)\n",
					cr.Total-cr.Unresolved, cr.Total, pct)
			}
		}
	}
	if res.Freshness == "stale" {
		// Fail-closed: a stale non-converged index must not be trusted. The
		// JSON was printed above; the non-zero exit is the signal.
		fatalUsage("index: stale (non-converged) — rebuild with kern index (see output above)")
	}
}

// indexWhereResult is the `kern index where` payload: the resolved on-disk
// store path the root actually serves, the freshness verdict, and the symbol
// count, plus any parent index that a nested .kern would shadow (F5).
type indexWhereResult struct {
	Root      string `json:"root"`
	Store     string `json:"store"`
	Built     bool   `json:"built"`
	Symbols   int    `json:"symbols"`
	Files     int    `json:"files"`
	Packages  int    `json:"packages"`
	Freshness string `json:"freshness"` // "fresh" | "stale" | "not-built"
	// Parent is the nearest ancestor directory that also holds its own
	// index store ("" when none). Resolution is explicit-root, so the nested
	// index IS what is served; the field makes the shadowing visible.
	Parent string `json:"parent_index,omitempty"`
}

// indexStoreDisplay returns the on-disk store path to report for root: the
// SQLite store in the default build (the SQLite-primary write path), the JSON
// cache under -tags nosqlite. Used in user-facing "-> path" and "store: path"
// messages so they name the file that was actually written/read.
func indexStoreDisplay(root string) string {
	if index.SQLiteEnabled() {
		return index.SQLitePath(root)
	}
	return index.StorePath(root)
}

// indexWhere resolves "which index am I serving" for root without building
// anything: StorePath(root), the freshness of the on-disk index, and the
// symbol count. Shared by `kern index where` and the doctor's index checks.
func indexWhere(root string) indexWhereResult {
	res := indexWhereResult{Root: root, Store: indexStoreDisplay(root), Freshness: "not-built"}
	if ix, err := index.Load(root); err == nil && ix != nil {
		res.Built = true
		res.Symbols = len(ix.Symbols)
		res.Files = len(ix.FileHashes)
		res.Packages = len(ix.Pkgs)
		if indexIsFresh(root, ix) {
			res.Freshness = "fresh"
		} else {
			res.Freshness = "stale"
		}
	}
	res.Parent = doctor.ParentIndexDir(root)
	return res
}

// runIndexWhere implements `kern index where [root] [--json]`: the quick
// "which index am I serving" answer — the resolved store path, freshness and
// symbol count — plus a warning when a parent directory also holds an index
// (nested .kern shadowing a parent index, F5c). Read-only, never builds.
func runIndexWhere(jsonOut bool, root string) {
	res := indexWhere(root)
	if jsonOut {
		printJSON(res)
		return
	}
	if res.Built {
		fmt.Printf("index: %s\n", res.Store)
		fmt.Printf("  freshness: %s\n", res.Freshness)
		fmt.Printf("  symbols: %d (%d files, %d packages)\n", res.Symbols, res.Files, res.Packages)
	} else {
		fmt.Printf("index: NOT BUILT for %s (would be %s)\n", root, res.Store)
	}
	if res.Parent != "" {
		fmt.Printf("  warning: nested index shadows a parent index at %s/.kern — pass the parent root to serve the full index\n", res.Parent)
	}
}

func runIndex(rest []string) {
	goTypes := false
	javaCompiler, javaClasspath := false, ""
	filtered := make([]string, 0, len(rest))
	for position := 0; position < len(rest); position++ {
		argument := rest[position]
		if argument == "--go-types" {
			goTypes = true
		} else if argument == "--java-compiler" {
			javaCompiler = true
		} else if argument == "--java-classpath" {
			if position+1 >= len(rest) {
				fatal("--java-classpath requires a value")
			}
			position++
			javaClasspath = rest[position]
		} else if strings.HasPrefix(argument, "--java-classpath=") {
			javaClasspath = strings.TrimPrefix(argument, "--java-classpath=")
		} else {
			filtered = append(filtered, argument)
		}
	}
	rest = filtered
	f, args := parseFlagsOrDie(rest)
	if javaClasspath != "" && !javaCompiler {
		fatal("--java-classpath requires --java-compiler")
	}
	if (goTypes || javaCompiler) && (f.status || f.update || (len(args) > 0 && (args[0] == "where" || args[0] == "ensure-fresh"))) {
		fatal("compiler bindings require a full index build, not --status/--update/where/ensure-fresh")
	}
	if len(args) > 0 && args[0] == "ensure-fresh" {
		root := projectRoot(f)
		if f.root == "" && len(args) > 1 {
			root = args[1]
		}
		runEnsureFresh(f.json, root)
		return
	}
	if len(args) > 0 && args[0] == "where" {
		root := projectRoot(f)
		if f.root == "" && len(args) > 1 {
			root = args[1]
		}
		runIndexWhere(f.json, root)
		return
	}
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 {
		root = args[0]
	}
	// `kern index --status [--json]` is read-only: it reports the cached
	// index's health without rebuilding anything, so CI and agents can gate
	// on freshness cheaply.
	if f.status {
		status, serr := index.StatusReport(root, f.strict)
		if serr != nil {
			fatal("Index: %v", serr)
		}
		if f.json {
			printJSON(status)
			return
		}
		if status.Built {
			fmt.Printf("index: BUILT (%d symbols, %d files, %d packages, version %d)\n",
				status.Symbols, status.Files, status.Packages, status.Version)
			fmt.Printf("  languages: %s\n", status.Languages)
			fmt.Printf("  stale: %v\n", status.Stale)
			fmt.Printf("  store: %s\n", status.Store)
			if status.CallResolution.Total > 0 {
				cr := status.CallResolution
				pct := 100 * cr.Unresolved / cr.Total
				if cr.External+cr.InRepo == cr.Unresolved {
					fmt.Printf("  call resolution: %d/%d callees (%d%% unresolved: %d external stdlib/vendor, %d in-repo/untyped)\n",
						cr.Total-cr.Unresolved, cr.Total, pct, cr.External, cr.InRepo)
				} else {
					fmt.Printf("  call resolution: %d/%d callees (%d%% unresolved)\n",
						cr.Total-cr.Unresolved, cr.Total, pct)
				}
			}
			if status.Build.Kind != "" {
				fmt.Printf("  build: %s in %d ms (%d workers, %d file results reused)\n",
					status.Build.Kind, status.Build.Ms, status.Build.Workers, status.Build.Reused)
			}
		} else {
			fmt.Printf("index: NOT BUILT for %s\n", root)
		}
		return
	}
	// `kern index --update [root]` incrementally refreshes the persisted
	// index: load the cached index, and when it is stale run index.Update
	// (re-parses ONLY changed/new files; symbols and edges of unchanged files
	// are copied verbatim — the same update-over-build pattern project.go and
	// LoadOrBuild use). A stale index is NOT invalidated wholesale by a small
	// commit: a 2-file change re-parses 2 files. Falls back to a full build
	// when no previous index loads or Update fails. The plain `kern index`
	// command (below) remains the explicit full rebuild.
	if f.update {
		// Always run the incremental update when a previous index loads —
		// Update's own walk detects changed files content-addressed (same
		// trust model as Build), so a separate Stale() proof gate here would
		// re-derive the same staleness decision only to discard it on the
		// stale path (measured ~1.1s of redundant tree walk + git staging per
		// pre-commit `kern check` invocation). On a fresh tree Update copies
		// every file's result verbatim and reports it as reused; correctness
		// is unchanged because Update is content-based end to end.
		prev, lerr := index.Load(root)
		var ix *index.Index
		if lerr == nil && prev != nil {
			if uix, uerr := index.Update(root, prev); uerr == nil && uix != nil {
				ix = uix
			}
		}
		if ix == nil {
			// No loadable previous index, or Update failed: full build
			// (index.BuildPersisted persists the result itself).
			var berr error
			ix, berr = index.BuildPersisted(root)
			if berr != nil {
				fatal("Index: %v", berr)
			}
			if f.json {
				printJSON(indexJSONSummary(ix, "fresh", "updated", root))
				return
			}
			fmt.Printf("index updated: %d symbols in %d files (%d packages, %d reused) -> %s\n",
				len(ix.Symbols), len(ix.FileHashes), len(ix.Pkgs), ix.ReusedResults(), indexStoreDisplay(root))
			return
		}
		if serr := ix.Save(); serr != nil {
			fatal("Index: persist updated index: %v", serr)
		}
		if f.json {
			sum := indexJSONSummary(ix, "fresh", "updated", root)
			sum.Reused = ix.ReusedResults()
			printJSON(sum)
			return
		}
		fmt.Printf("index updated: %d symbols in %d files (%d packages, %d reused) -> %s\n",
			len(ix.Symbols), len(ix.FileHashes), len(ix.Pkgs), ix.ReusedResults(), indexStoreDisplay(root))
		return
	}
	// Plain `kern index [root]` (no --status/--update/ensure-fresh): build
	// only when the persisted index is missing or stale. A fresh index is
	// reported as-is with its symbol counts and the command exits without
	// re-parsing anything — the historical always-rebuild behavior made the
	// command useless as a cache-hit no-op and wasted a full tree parse on
	// every invocation. `kern index --force` restores the unconditional
	// rebuild for scripts that depend on it.
	if !f.force && !goTypes && !javaCompiler {
		if prev, lerr := index.Load(root); lerr == nil && prev != nil && indexIsFresh(root, prev) {
			if f.json {
				printJSON(indexJSONSummary(prev, "fresh", "skipped", root))
				return
			}
			fmt.Printf("index: FRESH (%d symbols, %d files, %d packages, version %d) — index is up to date; use `kern index --force` to rebuild\n",
				len(prev.Symbols), len(prev.FileHashes), len(prev.Pkgs), prev.Version)
			return
		}
	}
	var ix *index.Index
	var err error
	if goTypes || javaCompiler {
		var options []index.BuildOption
		if goTypes {
			options = append(options, index.WithGoTypes())
		}
		if javaCompiler {
			options = append(options, index.WithJavaCompiler(javaClasspath))
		}
		ix, err = index.BuildWithOptions(root, options...)
		if err == nil {
			err = ix.Save()
		}
	} else if f.force {
		// --force is the corruption-recovery / schema-bump escape hatch: it
		// bypasses the cross-process debounce and re-parses the tree
		// unconditionally (a fresh cached index must NOT be reused here).
		ix, err = index.BuildPersistedForce(root)
	} else {
		ix, err = index.BuildPersisted(root)
	}
	if err != nil {
		fatal("Index: %v", err)
	}
	if f.json {
		printJSON(indexJSONSummary(ix, "rebuilt", "rebuilt", root))
		return
	}
	store := index.StorePath(root)
	if index.SQLiteEnabled() {
		store = index.SQLitePath(root)
	}
	fmt.Printf("indexed %d symbols in %d files (%d packages) -> %s\n",
		len(ix.Symbols), len(ix.FileHashes), len(ix.Pkgs), store)
	fmt.Printf("languages: %s\n", strings.Join(ix.Languages(), ", "))

}

// indexIsFresh reports whether the persisted index for root is fresh enough
// to skip a rebuild, mirroring the LoadOrBuild / `kern index --status`
// freshness decision: the cheap git tree-OID probe when it is decisive, and
// the loose content proof otherwise (non-git worktree or a legacy index
// without a recorded tree OID). A nil or missing index is never fresh.
func indexIsFresh(root string, ix *index.Index) bool {
	if ix == nil {
		return false
	}
	// The cheap git tree-OID probe is a FAST PATH, not a verdict: a decided
	// match proves fresh, but a decided MISMATCH (a commit that only sealed
	// already-indexed content moved HEAD^{tree}) must not short-circuit to
	// stale — fall through to the loose content proof, which re-hashes file
	// contents and is the authoritative decision (V7 parity with diskview
	// and EnsureFresh). An inconclusive probe (no recorded tree OID) also
	// falls through.
	if fresh, decided, _ := ix.TreeOIDProbe(root); decided && fresh {
		return true
	}
	return ix.FreshnessProof(root).Verdict == index.FreshnessFresh
}

// indexJSONSummary is the `kern index --json` payload: a valid JSON summary
// of the index operation, mirroring the top-level fields `kern index
// ensure-fresh --json` emits (a "freshness" verdict plus the IndexStatus
// shape: symbols/files/packages/version/languages/store).
type indexJSONResult struct {
	Freshness string   `json:"freshness"`
	Action    string   `json:"action"`
	Root      string   `json:"root"`
	Built     bool     `json:"built"`
	Symbols   int      `json:"symbols"`
	Files     int      `json:"files"`
	Packages  int      `json:"packages"`
	Version   int      `json:"version"`
	Stale     bool     `json:"stale"`
	Languages []string `json:"languages"`
	Store     string   `json:"store"`
	// CallResolution is the honest per-repo call-target resolution rate
	// (distinct callees that fail to resolve against the symbol table).
	// Zero on indexes built before the pass existed.
	CallResolution index.CallResStats `json:"call_resolution,omitempty"`
	// Build reports how the index was produced (kind/duration/workers/
	// reused), mirroring `kern index --status`'s build block. Omitted on
	// indexes built before the field existed.
	Build *index.BuildStats `json:"build,omitempty"`
	// Reused is how many per-file parse results were copied verbatim from the
	// previous index during an incremental `--update` (0 for a full build).
	Reused       int  `json:"reused,omitempty"`
	GoTypes      bool `json:"go_types,omitempty"`
	JavaCompiler bool `json:"java_compiler,omitempty"`
}

func indexJSONSummary(ix *index.Index, freshness, action, root string) indexJSONResult {
	store := index.StorePath(root)
	if index.SQLiteEnabled() {
		store = index.SQLitePath(root)
	}
	sum := indexJSONResult{
		Freshness:      freshness,
		Action:         action,
		Root:           root,
		Built:          true,
		Symbols:        len(ix.Symbols),
		Files:          len(ix.FileHashes),
		Packages:       len(ix.Pkgs),
		Version:        ix.Version,
		Stale:          freshness != "fresh",
		Languages:      ix.Languages(),
		Store:          store,
		CallResolution: ix.CallResolution,
		GoTypes:        ix.GoTypes,
		JavaCompiler:   ix.JavaCompiler,
	}
	if ix.BuildStats.Kind != "" {
		bs := ix.BuildStats
		sum.Build = &bs
	}
	return sum
}

func runWatch(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 {
		root = args[0]
	}
	// Validate the root BEFORE daemonizing: a missing root used to start a
	// long-lived watcher that only ever reported lstat errors (and an unknown
	// flag was silently monitored as the root). Fail loud with rc=1 instead.
	if st, serr := os.Stat(root); serr != nil || !st.IsDir() {
		fatal("watch: root %q does not exist", root)
	}
	interval := 5
	if f.interval > 0 {
		interval = f.interval
	}
	mode := project.WatchMode(root)
	fmt.Printf("kern watch: monitoring %s (mode: %s)\n", root, mode)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err = project.Watch(ctx, root, time.Duration(interval)*time.Second, func(changes []index.Change, ix *index.Index) {
		for _, c := range changes {
			fmt.Printf("[kern] %-8s %s\n", c.Kind, c.File)
		}
		fmt.Printf("[kern] index updated: %d symbols, %d packages (%s)\n",
			len(ix.Symbols), len(ix.Pkgs), strings.Join(ix.Languages(), ", "))
	}, func(err error) {
		fmt.Fprintf(os.Stderr, "[kern] watch error: %v\n", err)
	})
	if err != nil && err != context.Canceled {
		fatal("Watch: %v", err)
	}

}

func runAst(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 {
		fatalUsage("usage: kern ast <pattern> [root] [--all]")
	}
	pattern := args[0]
	// Determine root: --root flag > positional arg > "" (CWD).
	root := f.root
	if root == "" && len(args) > 1 {
		root = args[1]
	}
	// F13: `kern ast func run` (unquoted) used to split into pattern="func"
	// path="run" and then SILENTLY "succeed" (exit 0) indexing a nonexistent
	// junk path (0 symbols, junk root positions). Reject excess positionals
	// and unresolvable root paths as usage errors (exit 2) instead — the
	// same for the quoted multi-word pattern form, which must stay a single
	// argument (`kern ast "func run"`).
	if len(args) > 2 {
		fatalUsage("usage: kern ast <pattern> [root] [--all]\n  too many arguments (pattern and optional root only); quote multi-word patterns: kern ast \"func run\"")
	}
	if root != "" {
		if st, serr := os.Stat(root); serr != nil || !st.IsDir() {
			fatalUsage("usage: kern ast <pattern> [root] [--all]\n  root %q does not exist or is not a directory (multi-word patterns must be quoted: kern ast \"func run\")", root)
		}
	}
	// --all searches across ALL cached project indexes. But if a root was
	// explicitly provided (--root or positional), scope to just that repo
	// instead — the user asked for one project, not every cache entry.
	// Modern indexes live per-project under .kern/: search the federated
	// repos (registered + the current repo and its subprojects) —
	// deep-verify L2 F2-9: --all used to read only the legacy global cache
	// and never contributed on modern installs. The search is READ-ONLY:
	// a repo without an existing index is skipped and reported, never
	// auto-built (auto-building discovered subprojects would scatter .kern/
	// directories through the tree), and a candidate root nested inside
	// another candidate is dropped — the parent index already covers that
	// subtree, so searching both duplicates every match. The legacy cache
	// (~/.cache/kern/index) is still scanned when present for
	// pre-per-project installs.
	if f.all && root == "" {
		type candidate struct {
			root string
			ix   *index.Index
		}
		var candidates []candidate
		skipped := 0
		for _, repo := range intel.FederatedRepos(".") {
			// A registered repo whose path is gone must not silently count
			// as searched (same guard as intel.SearchReposIn).
			if st, serr := os.Stat(repo.Root); serr != nil || !st.IsDir() {
				skipped++
				continue
			}
			// index.Load is the read-only loader (intel.ReadIndex would
			// rebuild-and-save a missing/stale index — a side effect a
			// search must not have, especially for discovered subprojects).
			ix, err := index.Load(repo.Root)
			if err != nil || ix == nil {
				skipped++
				continue
			}
			candidates = append(candidates, candidate{root: repo.Root, ix: ix})
		}
		if files, err := os.ReadDir(cache.Path("index")); err == nil {
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
				candidates = append(candidates, candidate{root: ix.Root, ix: ix})
			}
		}
		searched := 0
		for i, c := range candidates {
			nested := false
			for j, o := range candidates {
				if i != j && strings.HasPrefix(c.root, o.root+string(filepath.Separator)) {
					nested = true
					break
				}
			}
			if nested {
				skipped++
				continue
			}
			searched++
			for _, m := range c.ix.Search(pattern, 50) {
				fmt.Printf("%-28s %-10s %-7s %-24s %s:%d\n", c.root, m.Kind, m.Lang, m.FullName(), m.File, m.Line)
			}
		}
		if searched == 0 {
			fmt.Fprintf(os.Stderr, "kern: --all: no project indexes found (skipped %d unindexed/stale/temp; modern indexes live per-project in .kern/ — run `kern index <root>` to index a repo, `kern repos add <path>` to register cross-repo searches, or pass a root to search a specific repo)\n", skipped)
			return
		}
		idxWord := "index"
		if searched != 1 {
			idxWord = "indexes"
		}
		fmt.Fprintf(os.Stderr, "kern: --all searched %d project %s", searched, idxWord)
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, " (skipped %d unindexed/stale/temp/nested)", skipped)
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
		fatal("Ast: %v", err)
	}
	// V7: a bare token is a prefix query — "clean" must find clean_jsonc_mcp_file,
	// not demand an exact full-name match (wildcards still do exact-anchored
	// matching as before).
	matches := ix.Search(pattern, 50)
	if len(matches) == 0 && !strings.Contains(pattern, "*") {
		matches = ix.Search(pattern+"*", 50)
	}
	if len(matches) == 0 {
		// 6.4: a no-match pattern without a kind prefix gets the kind-glob
		// hint (`func handle*` misses methods, whose kind is "method").
		fmt.Println("no symbols matched: " + pattern + index.KindGlobHint(pattern))
	}
	for _, m := range matches {
		fmt.Printf("%-10s %-7s %-24s %s:%d\n", m.Kind, m.Lang, m.FullName(), m.File, m.Line)
	}

}

func runRepos(rest []string) {
	// QA: `kern repos list --nonsense` (and friends) previously ignored
	// unknown flags (exit 0). repos takes a fixed subcommand plus
	// positionals; any token starting with "-" that is not a known flag is
	// a usage error. The first token is the subcommand word when present;
	// known flags (--limit/--root/--json for `search`, --help/-h handled by
	// the dispatcher) are whitelisted.
	reposKnownFlags := map[string]bool{"--limit": true, "--root": true, "--json": true, "--help": true, "-h": true}
	reposScanStart := 0
	if len(rest) > 0 && (rest[0] == "list" || rest[0] == "add" || rest[0] == "remove" || rest[0] == "search") {
		reposScanStart = 1
	}
	for _, a := range rest[reposScanStart:] {
		if strings.HasPrefix(a, "-") && !reposKnownFlags[a] {
			fatalUsage("unknown flag %q", a)
		}
	}
	if len(rest) == 0 || rest[0] == "list" {
		reg, err := intel.LoadRepos()
		if err != nil {
			fatal("Repos: %v", err)
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
		if len(rest) < 2 || len(rest) > 3 {
			fatalUsage("usage: kern repos add <path> [name]")
		}
		name := ""
		if len(rest) > 2 {
			name = rest[2]
		}
		reg, err := intel.LoadRepos()
		if err != nil {
			fatal("Repos: %v", err)
		}
		if err := reg.Add(rest[1], name); err != nil {
			fatal("Repos: %v", err)
		}
		if err := reg.Save(); err != nil {
			fatal("Repos: %v", err)
		}
		added, _ := reg.Get(name)
		if name == "" {
			added, _ = reg.Get(filepath.Base(rest[1]))
		}
		fmt.Printf("added %s -> %s\n", added.Name, added.Root)
	case "remove":
		if len(rest) < 2 || len(rest) > 2 {
			fatalUsage("usage: kern repos remove <name>")
		}
		reg, err := intel.LoadRepos()
		if err != nil {
			fatal("Repos: %v", err)
		}
		if !reg.Remove(rest[1]) {
			fatal("no repo named: %s", rest[1])
		}
		if err := reg.Save(); err != nil {
			fatal("Repos: %v", err)
		}
		fmt.Printf("removed %s\n", rest[1])
	case "search":
		// `kern repos search <query> [--limit N]` — the CLI surface for the
		// cross-repo search that otherwise exists only as the kern_repo_search
		// MCP tool. Mirrors the MCP handler's rendering (intel.FormatRepoHits,
		// "<repo> <kind> <lang> <symbol> <file>:<line>") and defaults to 20 hits.
		f, args := parseFlagsOrDie(rest[1:])
		if len(args) < 1 {
			fatalUsage("usage: kern repos search <query> [--limit N]")
		}
		// All positional words form ONE query (joined with spaces).
		query := strings.Join(args, " ")
		limit := f.limit
		if limit <= 0 {
			limit = 20
		}
		hits := intel.SearchRepos(query, limit)
		if len(hits) == 0 {
			if reg, lerr := intel.LoadRepos(); lerr != nil || len(reg.Repos) == 0 {
				fmt.Println("no repos registered (kern repos add <path> [name])")
				return
			}
			// F1: no-match is an error (exit 1), not success — same contract
			// as the single-repo path below and kern explore/graph.
			fatal("no symbols matched across repos: %s", query)
		}
		fmt.Println(intel.FormatRepoHits(hits))
	default:
		fatalUsage("usage: kern repos (list|add <path> [name]|search <query>|remove <name>)")
	}
}
func runSearch(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 {
		fatalUsage("usage: kern search <query> [root] [--limit N] [--repos] [--json] [--semantic] [--etag H]")
	}
	// All positional words form ONE query (joined with spaces), so
	// `kern search user service` searches for "user service" instead of
	// treating the 2nd token as a repo root and failing with lstat ENOENT.
	// The trailing positional root is honored ONLY when it names an existing
	// directory (`kern search FindUser /some/repo` keeps working); to scope
	// the search by a path that is not an existing directory, use --root.
	query := strings.Join(args, " ")
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 1 && isDir(args[len(args)-1]) {
			root = args[len(args)-1]
			query = strings.Join(args[:len(args)-1], " ")
		}
	}
	limit := f.limit
	if limit <= 0 {
		limit = 20
	}
	if f.repos {
		var hits []intel.RepoHit
		if f.semantic {
			client := llm.NewEmbedder()
			if !client.HasEmbeddingModel() {
				fatal("embedding model %q not installed (run: ollama pull %s)", llm.EmbedModel(), llm.EmbedModel())
			}
			if _, err := client.EmbedText("probe"); err != nil {
				// Model configured but server unreachable: semantic ranking
				// degrades silently — say so (audit: byte-identical output
				// with --semantic, zero stderr).
				fmt.Fprintf(os.Stderr, "kern: --semantic: embedding server unreachable — using ranked search; enable: start ollama with model %s (run: ollama pull %s)\n", llm.EmbedModel(), llm.EmbedModel())
				hits = intel.SearchReposIn(root, query, limit)
			} else {
				hits = intel.SemanticSearchReposIn(root, query, limit, client)
			}
		} else {
			hits = intel.SearchReposIn(root, query, limit)
		}
		if f.json {
			if hits == nil {
				hits = []intel.RepoHit{}
			}
			printJSON(map[string]any{
				"version": version,
				"query":   query,
				"results": hits,
				"total":   len(hits),
			})
			return
		}
		if len(hits) == 0 {
			fmt.Printf("no symbols matched across repos: %s\n", query)
			return
		}
		fmt.Println(intel.FormatRepoHits(hits))
		return
	}
	// L11: a root that is not a kern repo (no persisted index, no indexable
	// sources) must say so instead of building an empty index and reporting
	// "no symbols matched" — that read as a confident miss on a repo that
	// was never indexed.
	if !indexStoreExists(root) && !index.HasIndexableSources(root) {
		fatal("no kern index at %s — run: kern index %s", root, root)
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("Search: %v", err)
	}
	var matches []index.Symbol
	if f.semantic {
		client := llm.NewEmbedder()
		if !client.HasEmbeddingModel() {
			fatal("embedding model %q not installed (run: ollama pull %s)", llm.EmbedModel(), llm.EmbedModel())
		}
		if _, err := client.EmbedText("probe"); err != nil {
			fmt.Fprintf(os.Stderr, "kern: --semantic: embedding server unreachable — using ranked search; enable: start ollama with model %s (run: ollama pull %s)\n", llm.EmbedModel(), llm.EmbedModel())
			f.semantic = false
			matches = intel.RankedSearch(ix, query, limit)
		} else {
			matches = intel.SemanticSearch(ix, query, limit, client)
		}
	} else {
		matches = intel.RankedSearch(ix, query, limit)
	}
	if f.json {
		if matches == nil {
			matches = []index.Symbol{}
		}
		// F-SE2: the flat symbol dump reported only extraction-provenance
		// "confidence" (always HIGH for Go) with no match-relevance signal
		// — a distant fuzzy hit looked identical to an exact one. The
		// non-semantic JSON path now carries the ranked score and the
		// matched-all flag (additive: every existing field is unchanged).
		if !f.semantic {
			type scoredSymbol struct {
				index.Symbol
				Score      int  `json:"score"`
				MatchedAll bool `json:"matched_all"`
			}
			hits := intel.RankedSearchScored(ix, query, limit)
			scored := make([]scoredSymbol, 0, len(hits))
			for _, h := range hits {
				scored = append(scored, scoredSymbol{Symbol: h.Symbol, Score: h.Score, MatchedAll: h.MatchedAll})
			}
			printJSON(map[string]any{
				"version": version,
				"query":   query,
				"results": scored,
				"total":   len(scored),
			})
			return
		}
		printJSON(map[string]any{
			"version": version,
			"query":   query,
			"results": matches,
			"total":   len(matches),
		})
		return
	}
	if len(matches) == 0 {
		// F1: no-match exits 1 with did-you-mean suggestions (same contract
		// as kern explore / kern graph via fatalNoSymbol). The message moves
		// to stderr with the "kern: " prefix — a failed search is an error.
		fatalNoSearchMatch(query, ix)
	}
	// V7d: collapse same-name symbols duplicated across sibling modules.
	matches, collapsed := intel.CollapseModuleDuplicates(matches)
	if collapsed > 0 {
		fmt.Fprintf(os.Stderr, "kern: %d duplicate(s) across sibling modules collapsed\n", collapsed)
	}
	var b strings.Builder
	for _, m := range matches {
		fmt.Fprintf(&b, "%-10s %-7s %-24s %s:%d\n", m.Kind, m.Lang, m.FullName(), m.File, m.Line)
	}
	// Conditional fetch (C1/P4, ADR-0012): --etag matches → `unchanged (etag
	// <E>)` and exit 0; otherwise the full listing plus an `etag: <hash>`
	// footer on stderr. Byte-identical listing render.
	clietag.PrintIndexStamp(ix)
	if clietag.EtagResponse("kern_search", f.etag, b.String()) {
		return
	}
	fmt.Print(b.String())

}

// indexStoreExists reports whether <root>/.kern carries a persisted index
// (SQLite-primary, with the JSON store as the legacy/fallback form).
func indexStoreExists(root string) bool {
	if _, err := os.Stat(index.SQLitePath(root)); err == nil {
		return true
	}
	if _, err := os.Stat(index.StorePath(root)); err == nil {
		return true
	}
	return false
}

func runFts(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 {
		fatalUsage("usage: kern fts \"<query>\" [root] [--limit N]")
	}
	root := projectRoot(f)
	if f.root == "" && len(args) > 1 {
		root = args[1]
	}
	limit := f.limit
	if limit <= 0 {
		limit = 20
	}
	matches, err := index.FTS5Search(root, args[0], limit)
	if err != nil {
		fatal("Fts: %v", err)
	}
	if f.json {
		printJSON(matches)
		return
	}
	for _, m := range matches {
		fmt.Printf("%s %s %s:%d\n", m.Kind, m.FullName(), m.File, m.Line)
	}

}
