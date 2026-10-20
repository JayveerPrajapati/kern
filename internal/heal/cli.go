package heal

// cli.go implements the `kern heal` CLI entry point — extracted from
// cmd/kern/cmd_exec.go (workstream 3.6-A — exec/sandbox/validate/udiff/heal
// family extraction) following the internal/verifycmd precedent: this file
// owns the family's flag parsing, the HIGH-risk repair gate, the playbook
// adapter and the exported RunHeal entry point that returns the process exit
// code (int) to the cmd/kern dispatch table.
//
// Exit-code contract (pinned by the moved cmd/kern tests, must survive
// EXACTLY):
//
//	usage errors   -> 2 (no stack trace)
//	runtime errors -> 1
//
// The command body keeps the house fatal style (panic with the exitError
// sentinel); RunHeal recovers the sentinel and converts it back into the
// exit code, so cmd/kern's main() never sees a foreign panic and the dispatch
// table always receives a plain int.
//
// SECURITY: the HIGH-risk repair gate is preserved verbatim from cmd/kern —
// --force without --yes requires an explicit interactive y/Y confirmation and
// a non-interactive stdin refuses outright (fail closed).

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/llm"
)

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

// RunHeal executes `kern heal [flags] [root]` — self-correct failing files.
// It returns the process exit code (0 ok, 1 runtime failure, 2 usage error).
func RunHeal(rest []string) int {
	return run(func() int { runHeal(rest); return 0 })
}

// healFlags holds the parsed `kern heal` flags the command reads — the
// cmd/kern unified flags struct's subset for this command family. The parser
// below replicates parseFlags' documented semantics for exactly these flags
// (single-dash long forms normalize to double-dash, --flag=value and
// --flag value both work, bool flags accept --flag/--flag=true/--flag=false,
// a trailing value-less flag is accept-and-default, and unknown flags are a
// sticky parse error).
type healFlags struct {
	force      bool
	yes        bool
	llm        string
	task       string
	file       string
	max        int
	timeout    int
	timeoutDur string
	timeoutSet bool
	root       string // accepted (parseFlags knows it) but unused: root is positional
}

// parseHealFlags parses the heal family's flags from rest, replicating the
// unified cmd/kern parser's semantics for the flags runHeal reads (and
// -h/--help, which the CLI consumes before dispatch), via the shared
// cliflags.Parser (single-dash long forms normalize, --flag=value and
// --flag value both work, a trailing value-less flag is accept-and-default,
// unknown flags are a sticky parse error).
func parseHealFlags(args []string) (healFlags, []string, error) {
	var f healFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--force":
			p.SetBool(&f.force, "--force", inline, hasInline)
		case "--yes":
			p.SetBool(&f.yes, "--yes", inline, hasInline)
		case "--llm":
			p.SetStr(&f.llm, inline, hasInline)
		case "--task":
			p.SetStr(&f.task, inline, hasInline)
		case "--file":
			p.SetStr(&f.file, inline, hasInline)
		case "--max":
			p.SetInt(&f.max, "--max", inline, hasInline)
		case "--timeout":
			p.SetTimeout(&f.timeout, &f.timeoutDur, &f.timeoutSet, "--timeout", inline, hasInline)
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--help", "-h":
			// consumed before dispatch by the CLI; never read here
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

func parseHealFlagsOrDie(rest []string) (healFlags, []string) {
	f, args, err := parseHealFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	return f, args
}

// stdinIsTerminal reports whether stdin is an interactive terminal (a char
// device), so confirmation prompts are skipped for pipes, scripts and CI.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// healForceGateConfirmed reads one line from r and reports whether the
// operator explicitly confirmed the HIGH-risk repair gate override with y/Y.
// Anything else — an empty line, EOF, or any other input — declines. The
// prompt itself is printed by the caller; this function is factored out so
// tests can exercise the gate decision without running a real heal.
func healForceGateConfirmed(r io.Reader) bool {
	var answer string
	// Fscanln's error is ignored deliberately: an empty line or EOF leaves
	// answer == "" (declines) while "y" + EOF still yields "y" (accepts).
	_, _ = fmt.Fscanln(r, &answer)
	return answer == "y" || answer == "Y"
}

// cliPlaybookStore adapts incident.PlaybookStore to Playbook so the
// heal loop consults and records playbooks without importing incident. Steps
// are heal's deterministic "path|old|new" text format (base64 contents).
// (cmd/kern's healPlaybookStore, moved in-package.)
type cliPlaybookStore struct {
	root  string
	store *incident.PlaybookStore
}

// Lookup returns the recorded replacements for an exact signature.
func (h *cliPlaybookStore) Lookup(signature string) ([]Replacement, bool) {
	pb, ok := h.store.FindBySignature(signature)
	if !ok {
		return nil, false
	}
	reps := DecodeReplacements(pb.Steps)
	if len(reps) == 0 {
		return nil, false
	}
	// Poison-scope limiter (deep-dive C4): a recorded fix may replay only
	// against the exact content it was recorded from — Apply overwrites files
	// wholesale. Any drift is reported as a MISS so the heal loop escalates
	// to the full LLM round instead of replacing current content blindly.
	if !StepsApplyToCurrent(h.root, pb.Steps) {
		return nil, false
	}
	return reps, true
}

// Record stores replacements under a signature (upsert by signature).
func (h *cliPlaybookStore) Record(signature string, reps []Replacement) error {
	return h.store.UpsertBySignature(signature, EncodeReplacements(h.root, reps))
}

// ---- command body (moved verbatim from cmd/kern/cmd_exec.go) ----

func runHeal(rest []string) {
	f, args := parseHealFlagsOrDie(rest)
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	// The HIGH-risk repair gate exists to stop accidental YOLO repairs;
	// --force is one fat-finger away from bypassing it. Fail closed: --force
	// without --yes requires an explicit interactive y/Y confirmation, and a
	// non-interactive stdin (scripts, CI, MCP) refuses outright — never hang
	// a pipeline on a prompt, never silently proceed.
	if f.force && !f.yes {
		if !stdinIsTerminal() {
			fatal("refusing --force without --yes in non-interactive mode")
		}
		fmt.Print("HIGH-risk repair gate override requested\noverride? [y/N] ")
		if !healForceGateConfirmed(os.Stdin) {
			fatal("aborted — re-run with --yes to skip this prompt")
		}
	}
	task := f.task
	if task == "" {
		task = "Fix the failing build/test/syntax errors in this project."
	}
	iters := f.max
	if iters == 0 {
		iters = 3
	}
	// Validate local arguments before the LLM provider probe (QA F3): a
	// missing root or --file must fail as a usage error up front, not
	// surface behind a dead-provider error after the reachability check.
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		fatalUsage("heal: root %q is not a directory", root)
	}
	if f.file != "" {
		if _, err := os.Stat(f.file); err != nil {
			fatalUsage("heal: --file %q not found", f.file)
		}
	}
	// Pre-flight the explicit --llm Ollama model so a dead Ollama fails fast
	// (QA F3 note: local argument validation above still precedes it). The
	// default provider-chain pre-flight moved INTO heal.RunFile (F-ES1/
	// F-H1): it now runs after a playbook miss, so a recorded fix replays
	// with no provider up, and both the CLI and the MCP door fail fast on
	// a dead chain with the same actionable one-liner.
	if f.llm != "" {
		c := llm.New("")
		if !c.Available() {
			fatal("heal: --llm %s selects an Ollama model but Ollama is not reachable at %s (start ollama or drop --llm to use the provider chain)", f.llm, c.Base)
		}
	}
	if f.file != "" {
		fmt.Printf("kern: heal %s (cmd=%s, rounds=%d, file=%s)\n", root, f.llm, iters, f.file)
	} else {
		fmt.Printf("kern: heal %s (cmd=%s, rounds=%d)\n", root, f.llm, iters)
	}
	var res *Result
	// Cancel the agentic loop on SIGINT/SIGTERM so the spawned `opencode run`
	// child (exec.CommandContext in internal/llm/localcli.go) is killed too —
	// otherwise the grandchildren survive the interrupt and burn CPU.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Consult recorded heal playbooks first (same <root>/.kern/playbooks.json
	// store the incident command uses): a matching recorded fix is replayed and
	// verified without any LLM round; a miss or a failed verification escalates
	// to the full loop below. NewPlaybookStore never fails, but a nil pb keeps
	// the call exactly today's behavior regardless.
	var pb Playbook
	if store := incident.NewPlaybookStore(root); store != nil {
		pb = &cliPlaybookStore{root: root, store: store}
	}
	if f.file != "" {
		res = RunFileWithPlaybook(ctx, root, task, f.llm, f.file, iters, cliflags.TimeoutSeconds(f.timeoutSet, f.timeout), f.force, pb)
	} else {
		res = RunWithPlaybook(ctx, root, task, f.llm, iters, cliflags.TimeoutSeconds(f.timeoutSet, f.timeout), f.force, pb)
	}
	if res.Err != nil {
		fatal("%v", res.Err)
	}
	if len(res.Unvalidated) > 0 {
		if res.LastOutput != "" {
			fmt.Print(res.LastOutput)
		}
		fatal("heal: unable to validate: %s (not OK — install the toolchain or scope with --file)", strings.Join(res.Unvalidated, ", "))
	}
	if res.Validated {
		if res.UsedPlaybook {
			fmt.Printf("kern: validated OK via recorded playbook sig=%s (no LLM rounds)\n", res.PlaybookSig)
		} else {
			fmt.Printf("kern: validated OK after %d correction round(s)\n", res.Iterations)
		}
		if len(res.Changes) > 0 {
			fmt.Printf("kern: changed files (review and apply in your tree):\n")
			for _, c := range res.Changes {
				fmt.Printf("  - %s\n", c)
			}
		}
		if res.Diff != "" {
			fmt.Println(res.Diff)
		}
		return
	}
	fmt.Print(res.LastOutput)
	fatal("heal: still failing after %d round(s)", res.Iterations)

}
