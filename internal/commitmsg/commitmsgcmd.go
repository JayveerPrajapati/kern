package commitmsg

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
)

// commitmsgcmd.go implements the `kern commit` command family. The core
// message logic lives in this package (Generate/Enhance); this file adds
// the CLI shim (staging/commit plumbing) and the RunCommit entrypoint that
// returns the process exit code (int) to the cmd/kern dispatch table.
// Extracted from cmd/kern (workstream 3.6-A) following the
// internal/approvalcli precedent.
//
// Exit-code contract: usage errors -> 2, runtime errors -> 1. The command
// bodies keep the house fatal style (panic with the exitError sentinel);
// the Run* entrypoints recover the sentinel and convert it back into the
// exit code, so cmd/kern's main() never sees a foreign panic.

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

// commitFlags holds the parsed `kern commit` flags the family reads.
type commitFlags struct {
	root    string
	message string
	all     bool
	dryRun  bool
}

// parseCommitFlags parses the commit family's flags from rest, replicating
// the unified cmd/kern parser's semantics for the flags runCommit reads, via
// the shared cliflags.Parser (single-dash long forms normalize, --flag=value
// and --flag value both work, a trailing value-less flag is
// accept-and-default, unknown flags are a sticky parse error). It returns the
// parsed flags, the positional args, and the first parse error (sticky).
func parseCommitFlags(args []string) (commitFlags, []string, error) {
	var f commitFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--message":
			p.SetStr(&f.message, inline, hasInline)
		case "--all":
			p.SetBool(&f.all, "--all", inline, hasInline)
		case "--dry-run":
			p.SetBool(&f.dryRun, "--dry-run", inline, hasInline)
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--help", "-h":
			// Consumed by resolveCommandAndFlags before dispatch; tolerated
			// defensively like the unified parser.
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

func parseCommitFlagsOrDie(rest []string) (commitFlags, []string) {
	f, args, err := parseCommitFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	return f, args
}

// RunCommit executes `kern commit [flags]` and returns the process exit code
// (0 ok, 1 runtime failure, 2 usage error).
func RunCommit(rest []string) int {
	return run(func() int { runCommit(rest); return 0 })
}

func runCommit(rest []string) {
	f, args := parseCommitFlagsOrDie(rest)
	// D2: an explicitly given message (flag or positional) is committed
	// VERBATIM — generation/rewriting only happens when no message is given.
	explicit := f.message
	if explicit == "" && len(args) > 0 && args[0] != "" {
		explicit = strings.TrimSpace(args[0])
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
		msg := Generate(preview)
		subject, body := explicit, ""
		if subject == "" {
			subject = msg.Subject
			body = strings.Join(msg.Body, "\n")
		}
		fmt.Println("kern: would commit with:")
		fmt.Println()
		fmt.Println(subject)
		if body != "" {
			fmt.Println(body)
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
	msg := Generate(string(diffOut))
	subject, body := explicit, ""
	if subject == "" {
		subject = msg.Subject
		body = strings.Join(msg.Body, "\n")
	}
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

// gitDiff runs a git diff subcommand in the current directory and returns its
// stdout.
func gitDiff(args string) ([]byte, error) {
	cmd := exec.Command("git", strings.Split(args, " ")...)
	return cmd.Output()
}

// gitOutput runs a git subcommand and returns its combined output.
func gitOutput(args ...string) ([]byte, error) {
	return exec.Command("git", args...).CombinedOutput()
}

// gitCommit creates a commit with the given message fed over stdin, so no
// message ever appears in a shell argument or the process table.
func gitCommit(message string) ([]byte, error) {
	cmd := exec.Command("git", "commit", "-F", "-")
	cmd.Stdin = strings.NewReader(message)
	return cmd.CombinedOutput()
}

// shortHash returns the current HEAD's short hash, or "" if none exists yet.
func shortHash() string {
	out, err := gitDiff("rev-parse --short HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
