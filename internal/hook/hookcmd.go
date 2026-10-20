package hook

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/setup"
)

// hookcmd.go implements the `kern hook` command family (install/uninstall/
// diff/store + the native agent hook handlers). The core hook logic lives in
// this package (Install/Uninstall/Diff/Store/ClaudePost/ClaudePrompt/
// GeminiAfter/GeminiPrompt); this file adds the CLI shim that dispatches the
// subcommands, the global core.hooksPath shadow handling, and the Run*
// entrypoint that returns the process exit code (int) to the cmd/kern
// dispatch table. Extracted from cmd/kern (workstream 3.6-A — hook family
// extraction) following the internal/approvalcli precedent.
//
// Exit-code contract: usage errors -> 2, runtime errors -> 1. The command
// bodies keep the house fatal style (panic with the exitError sentinel);
// RunHook recovers the sentinel and converts it back into the exit code, so
// cmd/kern's main() never sees a foreign panic.

// exitError is the sentinel panic type used by fatal and fatalUsage — the
// cmd/kern helpers.go convention, preserved verbatim. It is recovered inside
// this package by run (the exported entrypoint wrapper), which converts it
// back into the process exit code.
type exitError struct{ code int }

// fatal prints an error to stderr and signals exit code 1 (the Unix
// convention for runtime errors).
func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 1})
}

// fatalUsage prints an error to stderr and signals exit code 2 (the Unix
// convention for usage errors). Use it for bad flags, missing required
// arguments, and unknown subcommands. It must never print a stack trace.
func fatalUsage(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 2})
}

// run executes a family command body, recovering the exitError sentinel and
// returning its code. Non-sentinel panics are re-panicked (a genuine bug
// must not be swallowed into an exit code).
func run(fn func() int) (code int) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitError); ok {
				code = e.code
				return
			}
			panic(r) // re-panic non-sentinel
		}
	}()
	return fn()
}

// hookFlags holds the parsed `kern hook` flags the family reads. The parser
// below replicates the cmd/kern unified parseFlags' documented semantics for
// exactly these flags (single-dash long forms normalize to double-dash,
// --flag=value and --flag value both work, bool flags accept
// --flag/--flag=true/--flag=false, a trailing value-less flag is
// accept-and-default, and unknown flags are a sticky parse error).
type hookFlags struct {
	range_ string
	global bool
}

// parseHookFlags parses the hook family's flags from rest, replicating the
// unified cmd/kern parser's semantics for the flags runHook reads, via the
// shared cliflags.Parser (single-dash long forms normalize, --flag=value and
// --flag value both work, a trailing value-less flag is accept-and-default,
// unknown flags are a sticky parse error). It returns the parsed flags, the
// positional args, and the first parse error (sticky).
func parseHookFlags(args []string) (hookFlags, []string, error) {
	var f hookFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--range":
			p.SetStr(&f.range_, inline, hasInline)
		case "--global":
			p.SetBool(&f.global, "--global", inline, hasInline)
		case "--help", "-h":
			// Consumed by resolveCommandAndFlags before dispatch; tolerated
			// defensively like the unified parser.
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

func parseHookFlagsOrDie(rest []string) (hookFlags, []string) {
	f, args, err := parseHookFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	return f, args
}

// RunHook executes `kern hook <install|uninstall|diff|store|claude-post|
// claude-prompt|gemini-after|gemini-prompt> [root] [--range a..b] [--global]`
// and returns the process exit code (0 ok, 1 runtime failure, 2 usage error).
func RunHook(rest []string) int {
	return run(func() int { runHook(rest); return 0 })
}

func runHook(rest []string) {
	f, args := parseHookFlagsOrDie(rest)
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
		if err := Install("."); err != nil {
			fatal("hooks install: %v", err)
		}
		hookPath := filepath.Join(".", ".git", "hooks", "post-commit")
		fmt.Printf("post-commit hook installed at %s (compresses each commit diff into project memory).\n", hookPath)
		// A global core.hooksPath makes git run hooks from that directory and
		// ignore .git/hooks — the local hook just installed would be shadowed.
		// Warn (and point at the global form) instead of claiming success.
		if note := globalHooksShadowNote(); note != "" {
			fmt.Fprintln(os.Stderr, note)
		}
	case "uninstall":
		if f.global {
			uninstallGlobalGitHooks()
			return
		}
		root := "."
		if len(args) > 1 && args[1] != "" {
			root = args[1]
		}
		removed, err := Uninstall(root)
		if err != nil {
			fatal("hooks uninstall: %v", err)
		}
		if len(removed) == 0 {
			fmt.Println("no kern-installed hooks found in .git/hooks")
		} else {
			fmt.Printf("removed kern-installed hook(s) from .git/hooks: %s\n", strings.Join(removed, ", "))
		}
	case "diff":
		out, err := Diff(from, to)
		if err != nil {
			fatal("hooks diff: %v", err)
		}
		fmt.Println(out)
	case "store":
		if err := Store(".", from, to); err != nil {
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
		in, err := cliflags.ReadStdin()
		if err != nil {
			fatal("hooks: %v", err)
		}
		switch sub {
		case "claude-post":
			out, err := ClaudePost(root, in)
			if err != nil {
				fatal("hooks claude-post: %v", err)
			}
			fmt.Println(out)
		case "claude-prompt":
			if err := ClaudePrompt(root, in); err != nil {
				fatal("hooks claude-prompt: %v", err)
			}
		case "gemini-after":
			repl, err := GeminiAfter(root, in)
			if err != nil {
				fatal("hooks gemini-after: %v", err)
			}
			if repl != "" {
				// Gemini: exit code 2 + stderr text hides the real tool
				// result and substitutes the stderr content.
				fatalUsage("%s", repl)
			}
		case "gemini-prompt":
			if err := GeminiPrompt(root, in); err != nil {
				fatal("hooks gemini-prompt: %v", err)
			}
		}
	default:
		fatalUsage("usage: kern hook <install|uninstall|diff|store|claude-post|claude-prompt|gemini-after|gemini-prompt> [root] [--range a..b] [--global]")
	}
}

// globalHooksShadowNote returns a warning when git's global core.hooksPath is
// configured: under hooksPath precedence git runs hooks from that directory
// and ignores a repo's .git/hooks, so a freshly installed local hook is
// shadowed. Empty when there is nothing to warn about (git absent or no
// global hooksPath).
func globalHooksShadowNote() string {
	if _, err := exec.LookPath("git"); err != nil {
		return ""
	}
	out, err := exec.Command("git", "config", "--global", "core.hooksPath").Output()
	if err != nil {
		return ""
	}
	configured := strings.TrimSpace(string(out))
	if configured == "" {
		return ""
	}
	return fmt.Sprintf("warning: git core.hooksPath is set to %s — git runs hooks from there and ignores .git/hooks, shadowing this local hook; use `kern install hook --global` to install kern's hooks globally instead", configured)
}

// uninstallGlobalGitHooks implements `kern hook uninstall --global`: it
// unsets git's global core.hooksPath only when it points at kern's global
// hook dir (~/.kern/git-hooks). A hooksPath owned by something else is
// refused loudly, never clobbered.
func uninstallGlobalGitHooks() {
	hooksDir, err := setup.GlobalGitHooksDir()
	if err != nil {
		fatal("hooks uninstall --global: %v", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		fatal("hooks uninstall --global: git not found on PATH")
	}
	out, err := exec.Command("git", "config", "--global", "core.hooksPath").Output()
	if err != nil {
		fmt.Println("core.hooksPath is not configured; no global kern hooks to uninstall")
		return
	}
	configured := strings.TrimSpace(string(out))
	if configured == "" {
		fmt.Println("core.hooksPath is not configured; no global kern hooks to uninstall")
		return
	}
	if configured != hooksDir {
		fatal("refusing to unset core.hooksPath: it points to %s, not kern's %s — leaving it untouched", configured, hooksDir)
	}
	unset := exec.Command("git", "config", "--global", "--unset", "core.hooksPath")
	if uOut, uErr := unset.CombinedOutput(); uErr != nil {
		fatal("hooks uninstall --global: failed to unset core.hooksPath: %v (%s)", uErr, string(uOut))
	}
	fmt.Printf("unset git core.hooksPath (was %s); global kern hooks at %s are no longer active across repos\n", configured, hooksDir)
}
