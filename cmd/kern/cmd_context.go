package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/doctor"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/pack"
	"github.com/JayveerPrajapati/kern/internal/prompt"
	"github.com/JayveerPrajapati/kern/internal/relay"
	"github.com/JayveerPrajapati/kern/internal/swap"
)

// kernJSONContractVersion is the schema version for machine-readable JSON output.
const kernJSONContractVersion = 2

// AuthzVerdictSchemaVersion is the schema version for authorization verdicts.
const AuthzVerdictSchemaVersion = 1

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
	tier := code.TierFull
	if f.fold {
		tier = code.TierFolded
	} else if f.tier != "" {
		t, terr := code.ParseTier(f.tier)
		if terr != nil {
			fatalUsage("%v", terr)
		}
		tier = t
	}
	b, err := pack.Build(root, pack.Options{
		MaxTokens:        f.maxTokens,
		SkipInstructions: f.noinstructions,
		Tier:             tier,
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
	findings := doctor.Run(root)
	if f.json {
		printJSON(findings)
		return
	}
	fmt.Println(doctor.Render(root, findings))

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
		emitLockEvent(root, string(eventbus.LockContended), scope, map[string]any{"holder_pid": pid})
		fatal("lock %q is held (pid %d): %v", scope, pid, err)
	}
	defer lk.Release()
	emitLockEvent(root, string(eventbus.LockAcquired), scope, map[string]any{"pid": os.Getpid()})
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
	emitLockEvent(root, string(eventbus.LockReleased), args[0], nil)
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
	// P0.4: the authz gate needs both halves of the agent identity — an
	// agent-id without a task description is a usage error.
	if f.agentID != "" && f.task == "" {
		fatalUsage("--agent-id requires --task")
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
					// symbols are gone from the index too .
					continue
				}
				fatal("file not found: %s", p)
			}
		}

		// P0.4 authz gate: when both --agent-id and --task are present, run
		// AuthorizeContext BEFORE the boundary check and surface the verdict in
		// the JSON output. A denied verdict is a blocking gate: exit 2 without
		// proceeding to the boundary check.
		var authzVerdict map[string]any
		authzDenied := false
		if f.agentID != "" && f.task != "" {
			authzVerdict, authzDenied = guardAuthzVerdict(f.agentID, f.task, ix, files)
		}
		if authzDenied {
			if f.json {
				printJSON(map[string]any{
					"schema_version":  kernJSONContractVersion,
					"violations":      []intel.Violation{}, // boundary check skipped
					"freshness_proof": ix.FreshnessProof(root),
					"authz_verdict":   authzVerdict,
				})
			} else {
				fmt.Fprintf(os.Stderr, "kern: guard: authz denied for agent %q task %q\n", f.agentID, f.task)
			}
			panic(exitError{code: 2})
		}

		strict := f.precision == "strict"
		violations, skipped := intel.CheckBoundariesPrecise(ix, b, files, strict)
		// @pure mutability assertions are opt-in via "pure": true in
		// .kern/boundaries.json. A nil ruleset (missing file) has no Pure flag,
		// so the check is naturally skipped when the guard is not configured.
		if b != nil && b.Pure {
			violations = append(violations, intel.CheckPurity(ix, files)...)
		}
		switch {
		case f.sarif:
			fmt.Println(intel.RenderViolationsSARIF(violations, version))
		case f.json:
			out := map[string]any{
				"schema_version":  kernJSONContractVersion,
				"violations":      violations,
				"freshness_proof": ix.FreshnessProof(root),
			}
			// Only surface skipped edges when strict mode actually skipped
			// some, so default-mode JSON output is unchanged.
			if len(skipped) > 0 {
				out["skipped_edges"] = skipped
			}
			// The authz_verdict is emitted only when --agent-id/--task were
			// supplied (backward compat: old callers see no new key).
			if authzVerdict != nil {
				out["authz_verdict"] = authzVerdict
			}
			printJSON(out)
		default:
			fmt.Println(intel.RenderViolations(violations))
			if len(skipped) > 0 {
				// A missing boundaries file is not a silent pass: make the gap
				// visible as a clear WARN (a warning, never a violation — the
				// exit code stays driven by violations alone).
				if n := skipped["boundaries-not-configured"]; n > 0 {
					fmt.Printf("WARN: no boundary rules configured (.kern/boundaries.json not found) — architecture guard NOT enforced; %d files unchecked\n", n)
				}
				langs := make([]string, 0, len(skipped))
				total := 0
				for l, n := range skipped {
					if l == "boundaries-not-configured" {
						continue
					}
					if l != "" {
						langs = append(langs, l)
					}
					total += n
				}
				sort.Strings(langs)
				if total > 0 {
					fmt.Printf("skipped %d heuristic edges across {%s}; use --precision default to trust them\n", total, strings.Join(langs, ","))
				}
			}
		}
		// Publish guard outcomes as ArchitectureViolation / ArchitectureWarning
		// events on a persisted bus rooted at root, so other processes (e.g.
		// kern-server webhook delivery) can see guard results. Best-effort
		// side effect: output and exit behavior are unchanged, and the events
		// are persisted even when the check REJECTs below.
		publishGuardEvents(root, violations, skipped["boundaries-not-configured"] > 0)
		if f.threshold >= 0 && len(violations) > f.threshold {
			panic(exitError{code: 2})
		}
	default:
		fatalUsage("usage: kern guard <check|init> [root] [--file f1,f2] [--range a..b] [--json|--sarif] [--threshold N] [--precision default|strict] [--agent-id ID --task DESC]")
	}

}

// publishGuardEvents publishes guard outcomes as ArchitectureViolation /
// ArchitectureWarning events: appended to the persisted bus at
// <root>/.kern/events.jsonl (replayed by kern-server on start) AND, when a
// relay owns <root>/.kern/events.sock, emitted live so `kern events watch`
// subscribers see guard results immediately instead of at the next replay.
// Publishing is best-effort and deterministic (event order = violation order);
// it never fails the guard or alters its output or exit behavior.
func publishGuardEvents(root string, violations []intel.Violation, warnNotConfigured bool) {
	relay.PublishPersisted(root, intel.GuardEvents(violations, warnNotConfigured))
}

// The CLI guard's default agent identity and the KERN_MCP_PERMISSIVE escape
// hatch live in internal/governance (DefaultAgentID, PermissiveMode,
// EnsureDefaultAgent) — shared with the MCP server so both surfaces govern
// identically.

// guardAuthzVerdict runs authorization for guard check and returns the verdict.
func guardAuthzVerdict(agentID, task string, ix *index.Index, files []string) (map[string]any, bool) {
	if agentID == governance.DefaultAgentID {
		governance.EnsureDefaultAgent()
	}

	agent, aerr := governance.GetAgent(agentID)
	if aerr != nil {
		if governance.PermissiveMode() {
			return map[string]any{
				"schema_version": AuthzVerdictSchemaVersion,
				"agent_id":       agentID,
				"task":           task,
				"decision":       "unknown",
				"policy_source":  "permissive-default",
				"denied_files":   []string{},
				"fingerprint":    "",
				"decided_at":     time.Now().UTC(),
			}, false
		}
		return map[string]any{
			"schema_version": AuthzVerdictSchemaVersion,
			"agent_id":       agentID,
			"task":           task,
			"decision":       "denied",
			"policy_source":  "default-scoped",
			"denied_files":   files,
			"fingerprint":    "",
			"decided_at":     time.Now().UTC(),
		}, true
	}

	fw := governance.NewFirewall().WithAgents(agent)
	req := governance.Request{
		Task:    task,
		AgentID: agentID,
		Root:    ix.Root,
		// Scope nil: the cwd-scoped default scope (effectiveScope) applies.
	}
	resp, err := governance.AuthorizeContext(req, ix, fw)
	if err != nil && err != governance.ErrUnauthorized {
		fatal("%v", err)
	}

	decision := "denied"
	if resp.Proof.Decision.Allowed {
		decision = "allowed"
	}

	return map[string]any{
		"schema_version": AuthzVerdictSchemaVersion,
		"agent_id":       agentID,
		"task":           task,
		"decision":       decision,
		"policy_source":  resp.Scope.PolicySource,
		"denied_files":   deniedFilesForRequest(resp.Scope.Denied, files),
		"fingerprint":    resp.Proof.Fingerprint,
		"decided_at":     resp.Proof.DecidedAt,
	}, decision == "denied"
}

// deniedFilesForRequest returns files that were denied by authorization.
func deniedFilesForRequest(denied []governance.DeniedSymbol, files []string) []string {
	if len(denied) == 0 || len(files) == 0 {
		return []string{}
	}
	deniedSet := make(map[string]bool, len(denied))
	for _, d := range denied {
		deniedSet[d.Symbol.File] = true
	}
	var out []string
	for _, f := range files {
		if deniedSet[f] {
			out = append(out, f)
		}
	}
	return out
}
