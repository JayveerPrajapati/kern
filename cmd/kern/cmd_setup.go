package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/brief"
	"github.com/JayveerPrajapati/kern/internal/commitmsg"
	"github.com/JayveerPrajapati/kern/internal/fw"
	"github.com/JayveerPrajapati/kern/internal/hook"
	"github.com/JayveerPrajapati/kern/internal/hooks"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/setup"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func runSetup(rest []string) {
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if f.check {
		for _, s := range setup.Check(root) {
			mark := "-"
			if s.Installed {
				mark = "x"
			}
			fmt.Printf("[%s] %-22s %s\n", mark, s.Agent, s.Note)
		}
		return
	}
	if f.verify {
		if err := verifyMCP(root); err != nil {
			fatal("[FAIL] mcp binary not reachable: %v", err)
		}
		fmt.Println("[ok] mcp binary responds to initialize")
		return
	}
	var agents []string
	if f.agents != "" {
		agents = strings.Split(f.agents, ",")
	}
	var detected []string
	if f.detect && len(f.agents) == 0 {
		detected = setup.DetectAgents(root)
	}
	failed := 0
	// The --global flag gates ALL user-global writes (home-scoped hooks,
	// home/global MCP adapters, global git ignore): without it, Wire touches
	// only project-scope files.
	for _, s := range setup.Wire(root, agents, f.detect, f.global) {
		mark := "ok"
		if s.Skipped {
			mark = "--"
		} else if !s.Installed {
			mark = "!!"
			failed++
		}
		fmt.Printf("[%s] %-32s %s\n", mark, s.Agent, s.Note)
	}
	if f.global {
		// Global pre-wiring targets ALL agents (or the explicit --agents
		// list), never just the detected subset: an agent installed later is
		// already wired with no re-run. WireGlobal adds the global
		// instruction files (AGENTS.md, CLAUDE.md) and the opencode plugin.
		for _, s := range setup.WireGlobal(agents) {
			mark := "ok"
			if s.Skipped {
				mark = "--"
			} else if !s.Installed {
				mark = "!!"
				failed++
			}
			fmt.Printf("[%s] %-32s %s\n", mark, s.Agent, s.Note)
		}
	}
	if len(f.agents) == 0 && !f.detect {
		fmt.Println("\nWired all agents. Use --detect to wire only detected agents, or --agents to target specific ones.")
	} else if f.detect && len(f.agents) == 0 {
		fmt.Printf("\nDetected agents: %v\n", detected)
	}
	fmt.Println("Restart your agent (opencode reload / claude) to pick up the MCP servers and kern-first instructions.")
	// Fail closed: a partial wiring (some agents errored) must not look like a
	// clean success. Exit non-zero so install scripts and CI can detect it.
	if failed > 0 {
		fatal("setup: %d agent(s) failed to wire — fix the errors above and rerun", failed)
	}
}

// verifyMCP spawns the kern-mcp command configured in .mcp.json and checks it
// completes an MCP initialize handshake within 5 seconds. This catches a
// missing binary or a stale command path at setup time instead of at agent
// launch time.
func verifyMCP(root string) error {
	raw, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		return fmt.Errorf("read .mcp.json: %w", err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("parse .mcp.json: %w", err)
	}
	server, ok := cfg.MCPServers["kern"]
	if !ok {
		return fmt.Errorf(".mcp.json has no \"kern\" mcpServers entry")
	}
	cmd := server.Command
	if cmd == "" {
		cmd = "kern-mcp"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	proc := exec.CommandContext(ctx, cmd, server.Args...)
	stdin, err := proc.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	// StdoutPipe blocks on Read until the server writes, unlike a bytes.Buffer
	// whose Read returns io.EOF immediately while empty.
	stdout, err := proc.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	proc.Stderr = io.Discard
	if err := proc.Start(); err != nil {
		return fmt.Errorf("spawn %q: %w", cmd, err)
	}
	// MCP servers are long-lived; always reap the spawned process.
	defer func() {
		_ = proc.Process.Kill()
		_ = proc.Wait()
	}()

	req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"kern-setup-check","version":"1"}}}`
	if _, err := io.WriteString(stdin, req+"\n"); err != nil {
		return fmt.Errorf("write initialize request: %w", err)
	}

	type lineResult struct {
		line string
		err  error
	}
	ch := make(chan lineResult, 1)
	go func() {
		line, rerr := bufio.NewReader(stdout).ReadString('\n')
		ch <- lineResult{line, rerr}
	}()

	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return fmt.Errorf("read initialize response: %w", r.err)
		}
		if err := json.Unmarshal([]byte(r.line), &resp); err != nil {
			return fmt.Errorf("invalid initialize response %q: %w", r.line, err)
		}
		if resp.Error != nil {
			return fmt.Errorf("initialize error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if len(resp.Result) == 0 {
			return fmt.Errorf("initialize returned no result: %q", r.line)
		}
	case <-ctx.Done():
		return fmt.Errorf("no initialize response within 5s: %w", ctx.Err())
	}
	return nil
}

func runBuddy(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	// A single positional argument is the project root; more than one is a
	// usage error, and an unknown flag is rejected by parseFlags above
	// (previously `kern buddy --nope` silently treated --nope as the root).
	if len(args) > 1 {
		fatalUsage("usage: kern buddy [root]")
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
	}
	if _, err := os.Stat(root); err != nil {
		fatal("buddy: %v", err)
	}
	out, err := brief.Build(root)
	if err != nil {
		fatal("buddy: %v", err)
	}
	fmt.Println(out)

}

// runOnboard ensures a working directory is fully wired to kern: registers the
// repo in the registry, builds/refreshes the index, writes AGENTS.md if
// missing, and prints a status report. Mirrors the MCP tool kern_onboard.
// Usage: kern onboard [--root DIR]
func runOnboard(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 && args[0] != "" {
			root = args[0]
		}
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

	// AGENTS.md wiring, only if the file is missing.
	wired := ""
	if _, serr := os.Stat(filepath.Join(abs, "AGENTS.md")); errors.Is(serr, fs.ErrNotExist) {
		agents := setup.DetectAgents(abs)
		setup.Wire(abs, agents, false, false) // onboard is project-scoped: never touch user-global config
		if _, werr := os.Stat(filepath.Join(abs, "AGENTS.md")); werr == nil {
			wired = "written"
		} else {
			wired = "write failed"
		}
	} else {
		wired = "present"
	}

	fmt.Printf("root:       %s\n", abs)
	fmt.Printf("registered: %s\n", registered)
	fmt.Printf("indexed:    %s\n", indexed)
	if timing != "" {
		fmt.Printf("timing:     %s\n", timing)
	}
	fmt.Printf("AGENTS.md:  %s\n", wired)
	fmt.Printf("next:       explore with kern_explore / kern_code_graph, or kern buddy for a session digest\n")

	// A4: onboard previously folded failures into the status strings above
	// and still exited 0, so scripts and CI could not detect a failed
	// onboarding. Fail closed instead.
	failed := strings.HasPrefix(registered, "error:") || strings.Contains(registered, "save error") || strings.HasPrefix(indexed, "error:")
	if failed {
		fmt.Fprintln(os.Stderr, "\nonboard: one or more steps failed — run `kern doctor` to diagnose, or `kern index` to rebuild the index")
		os.Exit(1)
	}
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
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(pos) > 0 {
			root = pos[0]
		}
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
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 && args[0] != "" {
			root = args[0]
		}
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

func runHook(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	sub := "store"
	if len(args) > 0 {
		sub = args[0]
	}
	from, to := "", ""
	if f.range_ != "" {
		if p := strings.SplitN(f.range_, "..", 2); len(p) == 2 {
			from, to = p[0], p[1]
		} else {
			// A single ref means the range ending at that ref (HEAD ->
			// HEAD~1..HEAD, matching the hook default); never drop it silently.
			from, to = f.range_+"~1", f.range_
		}
	}
	switch sub {
	case "install":
		if err := hooks.Install("."); err != nil {
			fatal("hooks install: %v", err)
		}
		fmt.Println("post-commit hook installed (compresses each commit diff into project memory).")
	case "diff":
		out, err := hooks.Diff(from, to)
		if err != nil {
			fatal("hooks diff: %v", err)
		}
		fmt.Println(out)
	case "store":
		if err := hooks.Store(".", from, to); err != nil {
			fatal("hooks store: %v", err)
		}
		fmt.Println("diff stored in project memory.")
	case "claude-post", "claude-prompt", "gemini-after", "gemini-prompt":
		// Native hooks for non-opencode agents (wired by `kern setup`).
		// Reads the agent's hook JSON from stdin and responds in that
		// agent's framing. Failures never break the agent's tool call:
		// handlers swallow their own parse errors.
		root := "."
		if len(args) > 1 && args[1] != "" {
			root = args[1]
		}
		in, err := readStdin()
		if err != nil {
			fatal("hooks: %v", err)
		}
		switch sub {
		case "claude-post":
			out, err := hook.ClaudePost(root, in)
			if err != nil {
				fatal("hooks claude-post: %v", err)
			}
			fmt.Println(out)
		case "claude-prompt":
			if err := hook.ClaudePrompt(root, in); err != nil {
				fatal("hooks claude-prompt: %v", err)
			}
		case "gemini-after":
			repl, err := hook.GeminiAfter(root, in)
			if err != nil {
				fatal("hooks gemini-after: %v", err)
			}
			if repl != "" {
				// Gemini: exit code 2 + stderr text hides the real tool
				// result and substitutes the stderr content.
				fatalUsage("%s", repl)
			}
		case "gemini-prompt":
			if err := hook.GeminiPrompt(root, in); err != nil {
				fatal("hooks gemini-prompt: %v", err)
			}
		}
	default:
		fatalUsage("usage: kern hook <install|diff|store|claude-post|claude-prompt|gemini-after|gemini-prompt> [root] [--range a..b]")
	}

}

func runCommitmsg(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" && len(args) > 0 {
		root = args[0]
	}
	if root == "" {
		root = "."
	}
	var out []byte
	// Prefer piped stdin when available, so `git diff | kern commitmsg` works
	// like every other diff-consuming kern command (log, optimize, schema).
	// Without this, a piped diff is silently discarded and the command reads
	// the git working-tree diff instead.
	if b, serr := readStdin(); serr == nil && len(b) > 0 {
		out = b
	} else if f.staged {
		out, err = gitDiffC(root, "diff", "--cached")
	} else if f.range_ != "" {
		out, err = gitDiffC(root, "diff", "--unified=0", f.range_)
	} else {
		// If changes are staged, prioritize the staged commit diff
		out, err = gitDiffC(root, "diff", "--cached")
		if err != nil || len(strings.TrimSpace(string(out))) == 0 {
			out, err = gitDiffC(root, "diff", "HEAD")
			if err != nil || len(strings.TrimSpace(string(out))) == 0 {
				out, err = gitDiffC(root, "diff")
			}
		}
	}
	if err != nil {
		fatal("commitmsg: %v", err)
	}
	msg := commitmsg.Generate(string(out))
	if f.subject {
		fmt.Println(msg.Subject)
	} else {
		fmt.Println(msg.String())
	}

}

func runCommit(rest []string) {
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	// A dry-run must never mutate the index, so --all may only stage when the
	// commit is real. The preview still reflects --all by diffing tracked
	// changes against HEAD and rendering untracked files as no-index diffs.
	if f.dryRun {
		preview, perr := commitPreviewDiff(f.all)
		if perr != nil {
			fatal("commit: %v", perr)
		}
		if len(strings.TrimSpace(preview)) == 0 {
			fatal("nothing staged to commit (use --all to stage tracked+untracked changes)")
		}
		msg := commitmsg.Generate(preview)
		subject := f.message
		if subject == "" {
			subject = msg.Subject
		}
		fmt.Println("kern: would commit with:")
		fmt.Println()
		fmt.Println(subject)
		for _, l := range msg.Body {
			fmt.Println(l)
		}
		return
	}
	if f.all {
		if _, err := gitOutput("add", "-A"); err != nil {
			fatal("staging failed: %v", err)
		}
	}
	diffOut, err := gitDiff("diff --cached")
	if err != nil {
		fatal("commit: %v", err)
	}
	if len(strings.TrimSpace(string(diffOut))) == 0 {
		fatal("nothing staged to commit (use --all to stage tracked+untracked changes)")
	}
	msg := commitmsg.Generate(string(diffOut))
	subject := f.message
	if subject == "" {
		subject = msg.Subject
	}
	body := strings.Join(msg.Body, "\n")
	full := subject
	if body != "" {
		full += "\n\n" + body
	}
	_, err = gitCommit(full)
	if err != nil {
		fatal("commit failed: %v — see output above", err)
	}
	short := shortHash()
	fmt.Printf("committed %s %s\n", short, subject)
}

// commitPreviewDiff renders what a commit would include without touching the
// index. Without all it mirrors the real commit exactly (the staged diff,
// which is a read-only query). With all it approximates `git add -A` by
// diffing tracked changes against HEAD and rendering each untracked file as a
// no-index diff against /dev/null.
func commitPreviewDiff(all bool) (string, error) {
	if !all {
		out, err := gitDiff("diff --cached")
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
	var b strings.Builder
	// git diff HEAD covers staged + unstaged tracked changes. In a repo with
	// no commits yet it fails; fall back to the staged diff, which diffs
	// against the empty tree.
	if out, err := gitDiff("diff HEAD"); err == nil {
		b.Write(out)
	} else if out, err := gitDiff("diff --cached"); err != nil {
		return "", err
	} else {
		b.Write(out)
	}
	out, err := gitOutput("ls-files", "--others", "--exclude-standard")
	if err != nil {
		return "", err
	}
	for _, p := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if p == "" {
			continue
		}
		// git diff --no-index exits 1 when the file differs from /dev/null — the
		// expected case — so only treat empty output as failure.
		if d, derr := gitOutput("diff", "--no-index", "--", "/dev/null", p); derr != nil && len(d) == 0 {
			continue
		} else {
			b.Write(d)
		}
	}
	return b.String(), nil
}
