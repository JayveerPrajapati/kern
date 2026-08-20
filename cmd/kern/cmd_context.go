package main

import (
	"context"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/doctor"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/pack"
	"github.com/JayveerPrajapati/kern/internal/prompt"
	"github.com/JayveerPrajapati/kern/internal/swap"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

func runProject(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	p, err := code.BuildProject(root, f.maxFiles, 200)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(p.Render())

}

func runPack(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	b, err := pack.Build(root, pack.Options{
		MaxTokens:        f.maxTokens,
		SkipInstructions: f.noinstructions,
	})
	if err != nil {
		fatal("%v", err)
	}
	out := b.Render()
	if f.out != "" {
		if werr := os.WriteFile(f.out, []byte(out), 0o644); werr != nil {
			fatal("%v", werr)
		}
		fmt.Printf("kern: packed %d files (%d tokens) to %s\n", len(b.Files), b.TotalTokens, f.out)
	} else {
		fmt.Print(out)
	}

}

func runPrompt(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) == 0 {
		fatalUsage("usage: kern prompt <template> [--file PATH] [--task TEXT]")
	}
	if args[0] == "list" || args[0] == "--help" || args[0] == "-h" {
		names, err := prompt.List()
		if err != nil {
			fatal("%v", err)
		}
		if args[0] != "list" {
			fmt.Println("usage: kern prompt <template> [--file PATH] [--task TEXT]")
			fmt.Println("templates:")
		}
		for _, n := range names {
			fmt.Println("  " + n)
		}
		return
	}
	vars := map[string]string{
		"ROOT":    ".",
		"LANG":    "",
		"MAP":     "",
		"FILE":    f.file,
		"TASK":    f.task,
		"SYMBOLS": "",
	}
	if abs, err := filepath.Abs("."); err == nil {
		vars["ROOT"] = abs
	}
	if p, err := code.BuildProject(".", 0, 200); err == nil {
		vars["MAP"] = p.Render()
		vars["LANG"] = projectLangs()
	}
	if f.file != "" {
		if ctx := fileContext(f.file); ctx != "" {
			vars["FILE"] = f.file + "\n" + ctx
		}
	}
	out, err := prompt.Render(args[0], vars)
	if err != nil {
		fatal("%v", err)
	}
	if f.schema != "" {
		sc, serr := loadSchema(f.schema)
		if serr != nil {
			fatal("%v", serr)
		}
		fmt.Print(out)
		fmt.Println()
		fmt.Print(sc.PromptBlock())
		return
	}
	fmt.Print(out)

}

func runSwap(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := "."
	if len(args) > 0 && isDir(args[0]) {
		root = args[0]
		args = args[1:]
	}
	var b []byte
	if len(args) > 0 && args[0] == "-" {
		b, err = readStdin()
	} else if len(args) > 0 {
		b, err = os.ReadFile(args[0])
	} else {
		b, err = readStdin()
	}
	if err != nil {
		fatal("%v", err)
	}
	text := string(b)
	switch f.mode {
	case "expand":
		fmt.Print(swap.ExpandMode(text, root))
	case "summary":
		fmt.Print(swap.SummaryMode(text, root))
	default:
		out, fits := swap.Fit(text, root, f.max)
		if !fits {
			fmt.Fprintf(os.Stderr, "kern: warning: still over budget after summarization\n")
		}
		fmt.Print(out)
	}

}

func runDoctor(rest []string) {
	root := "."
	if len(rest) > 0 {
		root = rest[0]
	}
	fmt.Println(doctor.Render(root, doctor.Run(root)))

}

func runContext(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern context <symbol> [root] [--lines N]")
	}
	symbol := args[0]
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 1 {
			root = args[1]
		}
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("%v", err)
	}
	// Honor --lines instead of parsing it as the symbol.
	lines := f.lines
	if lines <= 0 {
		lines = 12
	}
	ctxText := ix.Context(symbol, lines)
	if ctxText == "" {
		fatalNoSymbol(symbol, ix)
	}
	fmt.Println(ctxText)

}

func runLock(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern lock <scope> [root]")
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) > 1 {
		root = args[1]
	}
	scope := args[0]
	lk, err := lock.Acquire(root, scope)
	if err != nil {
		_, pid, _ := lock.Held(root, scope)
		fatal("lock %q is held (pid %d): %v", scope, pid, err)
	}
	defer lk.Release()
	if f.hold {
		// Non-blocking mode for tool/plugin callers: acquire, report, and
		// return immediately. The OS releases the flock on exit, so the
		// lock is only held for the duration of this process — use
		// `kern status` to verify and `kern unlock` to clear the marker.
		fmt.Printf("lock acquired: %s (pid %d). note: the lock marker persists until `kern unlock`; the flock releases when this process exits.\n", scope, os.Getpid())
		return
	}
	fmt.Printf("lock acquired: %s (pid %d). releasing on interrupt.\n", scope, os.Getpid())
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()
	fmt.Printf("lock released: %s\n", scope)

}

func runUnlock(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern unlock <scope> [root]")
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) > 1 {
		root = args[1]
	}
	if err := lock.Remove(root, args[0]); err != nil {
		fatal("%v", err)
	}
	fmt.Printf("lock removed: %s\n", args[0])

}

func runStatus(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	sts, err := lock.List(root)
	if err != nil {
		fatal("%v", err)
	}
	if f.json {
		printJSON(map[string]any{"locks": sts})
		return
	}
	if len(sts) == 0 {
		fmt.Println("no locks in workspace")
		return
	}
	for _, s := range sts {
		state := "free"
		if s.Held {
			state = "HELD"
		}
		holder := ""
		if s.PID > 0 {
			holder = fmt.Sprintf(" (pid %d)", s.PID)
		}
		fmt.Printf("  %-24s %-6s%s\n", s.Scope, state, holder)
	}

}

func runGuard(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	sub := "check"
	if len(args) > 0 {
		sub = args[0]
	}
	root := "."
	if len(args) > 1 {
		root = args[1]
	}
	switch sub {
	case "init":
		if err := intel.InitBoundaries(root); err != nil {
			fatal("%v", err)
		}
		fmt.Printf("wrote %s (edit it to declare boundary rules)\n", intel.DefaultBoundariesPath(root))
	case "check":
		ix, err := intel.ReadIndex(root)
		if err != nil {
			fatal("%v", err)
		}
		b, err := intel.LoadBoundaries(root)
		if err != nil {
			fatal("%v", err)
		}
		var files []string
		if f.file != "" {
			for _, p := range strings.Split(f.file, ",") {
				if p = strings.TrimSpace(p); p != "" {
					files = append(files, p)
				}
			}
		} else {
			from, to := splitRange(f.range_)
			files, err = intel.FilesForRange(root, from, to)
			if err != nil {
				fatal("%v (or pass --file f1,f2 to check specific files)", err)
			}
		}
		if len(files) == 0 {
			fatal("no changed files (use --file f1,f2 or --range a..b, or make edits)")
		}
		for _, p := range files {
			if _, err := os.Stat(filepath.Join(root, p)); err != nil {
				if os.IsNotExist(err) {
					// Deleted in the diff: nothing to check — the file's
					// symbols are gone from the index too (W2-20).
					continue
				}
				fatal("file not found: %s", p)
			}
		}
		violations := intel.CheckBoundaries(ix, b, files)
		switch {
		case f.sarif:
			fmt.Println(intel.RenderViolationsSARIF(violations, version))
		case f.json:
			printJSON(map[string]any{"violations": violations})
		default:
			fmt.Println(intel.RenderViolations(violations))
		}
		if f.threshold >= 0 && len(violations) > f.threshold {
			os.Exit(2)
		}
	default:
		fatalUsage("usage: kern guard <check|init> [root] [--file f1,f2] [--range a..b] [--json|--sarif] [--threshold N]")
	}

}
