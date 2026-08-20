package main

import (
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/brief"
	"github.com/JayveerPrajapati/kern/internal/commitmsg"
	"github.com/JayveerPrajapati/kern/internal/fw"
	"github.com/JayveerPrajapati/kern/internal/hook"
	"github.com/JayveerPrajapati/kern/internal/hooks"
	"github.com/JayveerPrajapati/kern/internal/setup"
	"os"
	"regexp"
	"strings"
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
	var agents []string
	if f.agents != "" {
		agents = strings.Split(f.agents, ",")
	}
	var detected []string
	if f.detect && len(f.agents) == 0 {
		detected = setup.DetectAgents(root)
	}
	failed := 0
	for _, s := range setup.Wire(root, agents, f.detect) {
		mark := "ok"
		if !s.Installed {
			mark = "!!"
			failed++
		}
		fmt.Printf("[%s] %-32s %s\n", mark, s.Agent, s.Note)
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
		os.Exit(1)
	}
}

func runBuddy(rest []string) {
	root := "."
	if len(rest) > 0 {
		root = rest[0]
	}
	out, err := brief.Build(root)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Println(out)

}

func runFw(rest []string) {
	args := rest
	var filter string
	if len(args) > 0 && args[0] == "--catalog" {
		args = args[1:]
		if len(args) > 0 {
			filter = args[0]
			args = args[1:]
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
	root := "."
	if len(args) > 0 && args[0] != "" {
		root = args[0]
	}
	det, err := fw.Detect(root)
	if err != nil {
		fatal("%v", err)
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
		fatal("%v", err)
	}
	var re *regexp.Regexp
	if f.pattern != "" {
		re, err = regexp.Compile("^" + strings.ReplaceAll(regexp.QuoteMeta(f.pattern), `\*`, `.*`) + "$")
		if err != nil {
			fatal("bad pattern %q: %v", f.pattern, err)
		}
	}
	n := 0
	for _, s := range ix.Symbols {
		if !s.Entry || s.Framework == "" {
			continue
		}
		if re != nil && !re.MatchString(s.Name) && (s.Route == "" || !re.MatchString(s.Route)) {
			continue
		}
		route := s.Route
		if route == "" {
			route = "-"
		}
		fmt.Printf("%s %s %s %s:%d\n", s.Framework, s.FullName(), route, s.File, s.Line)
		n++
		if n >= limit {
			break
		}
	}
	if n == 0 {
		fmt.Println("no framework entry points in index (run kern index to populate)")
	}
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
			fatal("%v", err)
		}
		fmt.Println("post-commit hook installed (compresses each commit diff into project memory).")
	case "diff":
		out, err := hooks.Diff(from, to)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println(out)
	case "store":
		if err := hooks.Store(".", from, to); err != nil {
			fatal("%v", err)
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
			fatal("%v", err)
		}
		switch sub {
		case "claude-post":
			out, err := hook.ClaudePost(root, in)
			if err != nil {
				fatal("%v", err)
			}
			fmt.Println(out)
		case "claude-prompt":
			if err := hook.ClaudePrompt(root, in); err != nil {
				fatal("%v", err)
			}
		case "gemini-after":
			repl, err := hook.GeminiAfter(root, in)
			if err != nil {
				fatal("%v", err)
			}
			if repl != "" {
				// Gemini: exit code 2 + stderr text hides the real tool
				// result and substitutes the stderr content.
				fmt.Fprintln(os.Stderr, repl)
				os.Exit(2)
			}
		case "gemini-prompt":
			if err := hook.GeminiPrompt(root, in); err != nil {
				fatal("%v", err)
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
		out, err = gitDiffC(root, "diff", "HEAD")
		if err != nil {
			out, err = gitDiffC(root, "diff")
		}
	}
	if err != nil {
		fatal("%v", err)
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
	if f.all {
		if _, err := gitOutput("add", "-A"); err != nil {
			fatal("staging failed: %v", err)
		}
	}
	diffOut, err := gitDiff("diff --cached")
	if err != nil {
		fatal("%v", err)
	}
	if len(strings.TrimSpace(string(diffOut))) == 0 {
		fatal("nothing staged to commit (use --all to stage tracked+untracked changes)")
	}
	msg := commitmsg.Generate(string(diffOut))
	subject := f.message
	if subject == "" {
		subject = msg.Subject
	}
	if f.dryRun {
		fmt.Println("kern: would commit with:")
		fmt.Println()
		fmt.Println(subject)
		for _, l := range msg.Body {
			fmt.Println(l)
		}
		return
	}
	body := strings.Join(msg.Body, "\n")
	full := subject
	if body != "" {
		full += "\n\n" + body
	}
	out, err := gitCommit(full)
	if err != nil {
		fatal("commit failed: %v\n%s", err, out)
	}
	short := shortHash()
	fmt.Printf("committed %s %s\n", short, subject)

}
