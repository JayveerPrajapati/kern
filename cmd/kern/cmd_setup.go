package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/commitmsg"
	"github.com/JayveerPrajapati/kern/internal/fw"
	"github.com/JayveerPrajapati/kern/internal/hook"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/setup"
)

// runSetup implements `kern setup`. The setup family's business logic
// (flag collection, wiring/check/verify/remove dispatch, the interactive
// render surface) lives in internal/setup (workstream 3.6-A — setup family
// extraction); this is a thin delegate that forwards the args and returns
// the entry point's exit code (usage errors -> 2, runtime errors -> 1,
// success -> 0).
func runSetup(rest []string) int {
	return setup.RunSetup(rest)
}

// runOnboard ensures a working directory is fully wired to kern: registers the
// repo in the registry, builds/refreshes the index, writes AGENTS.md if
// missing, and prints a status report. Mirrors the MCP tool kern_onboard.
// Usage: kern onboard [--root ROOT] [--index-only]
func runOnboard(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 && args[0] != "" {
		root = args[0]
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	abs = filepath.Clean(abs)

	// Register the repo if not already present.
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

	// Agent wiring, BEFORE the index build below: the index identity records
	// the tree it was built from, so wiring after the build would leave every
	// freshly written file (AGENTS.md, .mcp.json, opencode.json, ...) outside
	// the identity — `kern doctor` would immediately report the index STALE
	// right after a successful onboard (H6). --index-only skips wiring
	// entirely and only builds the index.
	wired := "present"
	var wiredFiles []string
	if f.indexOnly {
		wired = "skipped (--index-only)"
	} else if _, serr := os.Stat(filepath.Join(abs, "AGENTS.md")); errors.Is(serr, fs.ErrNotExist) {
		agents := setup.DetectAgents(abs)
		for _, s := range setup.Wire(abs, agents, false, false) { // onboard is project-scoped: never touch user-global config
			if s.Skipped || s.Path == "" {
				continue
			}
			wiredFiles = append(wiredFiles, s.Path)
		}
		if _, werr := os.Stat(filepath.Join(abs, "AGENTS.md")); werr == nil {
			wired = "written"
		} else {
			wired = "write failed"
		}
	}

	// Build/refresh the index (loads a fresh cached one if present).
	indexed := ""
	timing := ""
	t0 := time.Now()
	prev, _ := index.Load(abs)
	ix, ierr := loadOrBuild(abs)
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

	fmt.Printf("root:       %s\n", abs)
	fmt.Printf("registered: %s\n", registered)
	fmt.Printf("wired:      %s\n", wiredDisplay(abs, wiredFiles, f.indexOnly))
	fmt.Printf("indexed:    %s\n", indexed)
	if timing != "" {
		fmt.Printf("timing:     %s\n", timing)
	}
	fmt.Printf("AGENTS.md:  %s\n", wired)
	fmt.Printf("next:       explore with kern_explore / kern_graph, or kern buddy for a session digest\n")

	// A4: onboard previously folded failures into the status strings above
	// and still exited 0, so scripts and CI could not detect a failed
	// onboarding. Fail closed instead.
	failed := strings.HasPrefix(registered, "error:") || strings.Contains(registered, "save error") || strings.HasPrefix(indexed, "error:")
	if failed {
		fmt.Fprintln(os.Stderr, "\nonboard: one or more steps failed — run `kern doctor` to diagnose, or `kern index` to rebuild the index")
		os.Exit(1)
	}
}

// wiredDisplay renders the "wired:" surface. When this onboard run wrote
// files, every project file kern wrote is listed (paths deduped and sorted
// for a stable, readable list). When nothing was written — the project was
// already wired, e.g. by `kern setup` — the ACTUAL wiring state from setup's
// own registry (setup.WiredState) is shown instead of "none", which would be
// a lie about an already-wired fixture (L8). --index-only reports "none".
func wiredDisplay(root string, paths []string, indexOnly bool) string {
	if indexOnly {
		return "none (--index-only: agent wiring skipped)"
	}
	if len(paths) == 0 {
		return setup.WiredState(root)
	}
	seen := map[string]bool{}
	var uniq []string
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		uniq = append(uniq, p)
	}
	sort.Strings(uniq)
	return strings.Join(uniq, ", ")
}

func runFw(rest []string) {
	args := rest
	var filter string
	if len(args) > 0 && args[0] == "--catalog" {
		args = args[1:]
		if len(args) > 0 {
			filter = args[0]
		}
		langs := fw.Langs()
		if filter != "" {
			var fs []fw.Framework
			for _, fr := range fw.Catalog() {
				if fr.Lang == filter || strings.Contains(strings.ToLower(fr.Name), strings.ToLower(filter)) {
					fs = append(fs, fr)
				}
			}
			if len(fs) == 0 {
				fatal("no frameworks match %q (languages: %s)", filter, strings.Join(langs, ", "))
			}
			for _, fr := range fs {
				fmt.Printf("%-20s %s\n", fr.Name, fr.Summary)
			}
			return
		}
		for _, lang := range langs {
			fmt.Printf("%s\n", lang)
			for _, fr := range fw.ByLang(lang) {
				fmt.Printf("  %-20s %s\n", fr.Name, fr.Summary)
			}
		}
		return
	}
	f, pos := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if f.root == "" && len(pos) > 0 {
		root = pos[0]
	}
	if f.root != "" {
		// An explicit --root must name an existing directory; a bogus root
		// must fail loud (rc=1) instead of reporting "No known frameworks
		// detected" (the walk silently produces zero signals on a missing
		// dir), matching arch/dead fail-loud behavior.
		if st, serr := os.Stat(f.root); serr != nil || !st.IsDir() {
			fatal("fw: root %q does not exist", f.root)
		}
	}
	det, err := fw.DetectWithStdlib(root)
	if err != nil {
		fatal("fw: %v", err)
	}
	fmt.Println(fw.Render(det))

}

// runEntryPoints lists framework-detected entry points (handlers, controllers,
// route targets) from the index. Mirrors the MCP tool kern_entry_points.
// Usage: kern entry-points [root] [--limit N] [--pattern GLOB]
func runEntryPoints(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 && args[0] != "" {
		root = args[0]
	}
	limit := f.limit
	if limit <= 0 {
		limit = 50
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("entry-points: %v", err)
	}
	var re *regexp.Regexp
	if f.pattern != "" {
		re, err = regexp.Compile("^" + strings.ReplaceAll(regexp.QuoteMeta(f.pattern), `\*`, `.*`) + "$")
		if err != nil {
			fatal("bad pattern %q: %v", f.pattern, err)
		}
	}
	type entryJSON struct {
		Framework string `json:"framework"`
		Symbol    string `json:"symbol"`
		Route     string `json:"route"`
		File      string `json:"file"`
		Line      int    `json:"line"`
	}
	var out []entryJSON
	n := 0
	pkgOf := map[string]string{} // file -> package name
	for _, p := range ix.Pkgs {
		for _, f := range p.Files {
			if _, ok := pkgOf[f]; !ok {
				pkgOf[f] = p.Name
			}
		}
	}
	for _, s := range ix.Symbols {
		framework := ""
		route := ""
		if s.Entry && s.Framework != "" {
			// Framework-tagged entry point (handler, route, controller).
			framework = s.Framework
			route = s.Route
		} else if fwID, ok := goNativeEntry(s, pkgOf); ok {
			// Language-native entry point: Go main (package main) and init
			// funcs are entry points even when no framework tagged them
			// (`kern entry-points` reported none on a plain Go
			// module). Listed alongside framework-tagged entries.
			framework = fwID
			route = "-"
		}
		if framework == "" {
			continue
		}
		if re != nil && !re.MatchString(s.Name) && (route == "" || !re.MatchString(route)) {
			continue
		}
		out = append(out, entryJSON{framework, s.FullName(), route, s.File, s.Line})
		n++
		if n >= limit {
			break
		}
	}
	if f.json {
		// --json was previously ignored here (plain text always emitted);
		// emit the same shape as `kern entries --json`.
		if out == nil {
			out = []entryJSON{}
		}
		printJSON(map[string]any{"entries": out})
		return
	}
	for _, e := range out {
		fmt.Printf("%s %s %s %s:%d\n", e.Framework, e.Symbol, e.Route, e.File, e.Line)
	}
	if n == 0 {
		fmt.Println("no framework entry points in index (run kern index to populate)")
	}
}

// goNativeEntry reports whether a Go symbol is a language-native entry point
// and the framework label to display for it. Go's own entry points are `main`
// (only meaningful in a package named main) and `init` funcs (run once per
// package at startup); both are entry points even when no framework tags
// them. pkgOf maps a source file to its package name.
func goNativeEntry(s index.Symbol, pkgOf map[string]string) (string, bool) {
	if s.Kind != "func" || !strings.HasSuffix(s.File, ".go") {
		return "", false
	}
	switch s.Name {
	case "main":
		return "go", pkgOf[s.File] == "main"
	case "init":
		return "go", true
	}
	return "", false
}

// runHook implements `kern hook`. The hook family's business logic (subcommand
// dispatch, global core.hooksPath shadow handling) lives in internal/hook
// (workstream 3.6-A — hook family extraction); this is a thin delegate that
// forwards the args and returns the entry point's exit code.
func runHook(rest []string) int {
	return hook.RunHook(rest)
}

// runCommit implements `kern commit`. The commit family's business logic
// (staging, preview, generate+commit plumbing) lives in internal/commitmsg;
// this is a thin delegate that forwards the args and returns the entry
// point's exit code.
func runCommit(rest []string) int {
	return commitmsg.RunCommit(rest)
}
