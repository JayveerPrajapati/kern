package setup

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/mcp/toolsurface"
	"github.com/JayveerPrajapati/kern/internal/mcpcatalog"
)

// cli.go implements the `kern setup` command family (wire/check/verify/
// remove + the render surface). It was extracted from cmd/kern (workstream
// 3.6-A — setup family extraction) following the internal/approvalcli
// precedent: this file owns the family's flag parsing and CLI body, and
// exports a Run* entrypoint that returns the process exit code (int) to the
// cmd/kern dispatch table.
//
// Exit-code contract (pinned by the moved cmd/kern tests, must survive
// EXACTLY):
//
//	usage errors  -> 2 (no stack trace)
//	runtime errors -> 1
//
// The command bodies keep the house fatal style (panic with the exitError
// sentinel); RunSetup recovers the sentinel and converts it back into the
// exit code, so cmd/kern's main() never sees a foreign panic and the
// dispatch table always receives a plain int.

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

// setupFlags holds the parsed `kern setup` flags the family reads — the
// cmd/kern unified flags struct's subset for this command family. The parser
// below replicates parseFlags' documented semantics for exactly these flags
// (single-dash long forms normalize to double-dash, --flag=value and
// --flag value both work, bool flags accept --flag/--flag=true/--flag=false,
// a trailing value-less flag is accept-and-default, and unknown flags are a
// sticky parse error).
type setupFlags struct {
	root        string
	check       bool
	verify      bool
	remove      bool
	agents      string
	globalOnly  bool
	global      bool
	globalRules bool
	detect      bool
	allAgents   bool
	agentsMD    string
	dryRun      bool
	plain       bool
	yes         bool
	json        bool
}

// parseSetupFlags parses the setup family's flags from rest, replicating the
// unified cmd/kern parser's semantics for the flags runSetup reads, via the
// shared cliflags.Parser (single-dash long forms normalize, --flag=value and
// --flag value both work, a trailing value-less flag is accept-and-default,
// unknown flags are a sticky parse error). It returns the parsed flags, the
// positional args, and the first parse error (sticky).
func parseSetupFlags(args []string) (setupFlags, []string, error) {
	var f setupFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--check":
			p.SetBool(&f.check, "--check", inline, hasInline)
		case "--verify":
			p.SetBool(&f.verify, "--verify", inline, hasInline)
		case "--remove":
			p.SetBool(&f.remove, "--remove", inline, hasInline)
		case "--agents":
			p.SetStr(&f.agents, inline, hasInline)
		case "--global-only":
			p.SetBool(&f.globalOnly, "--global-only", inline, hasInline)
		case "--global":
			p.SetBool(&f.global, "--global", inline, hasInline)
		case "--global-rules":
			p.SetBool(&f.globalRules, "--global-rules", inline, hasInline)
		case "--detect":
			p.SetBool(&f.detect, "--detect", inline, hasInline)
		case "--all-agents":
			p.SetBool(&f.allAgents, "--all-agents", inline, hasInline)
		case "--agents-md":
			p.SetStr(&f.agentsMD, inline, hasInline)
		case "--dry-run":
			p.SetBool(&f.dryRun, "--dry-run", inline, hasInline)
		case "--plain":
			p.SetBool(&f.plain, "--plain", inline, hasInline)
		case "--yes":
			p.SetBool(&f.yes, "--yes", inline, hasInline)
		case "--json":
			p.SetBool(&f.json, "--json", inline, hasInline)
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

// parseSetupFlagsOrDie parses the setup family's flags, exiting with a usage
// error (exit code 2) on failure — the flag-parsing preamble the moved
// cmd/kern body repeated.
func parseSetupFlagsOrDie(rest []string) (setupFlags, []string) {
	f, args, err := parseSetupFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	return f, args
}

// setupRoot returns the command's --root value, defaulting to the current
// directory — the root-resolution preamble the moved cmd/kern body repeated.
func setupRoot(f setupFlags) string {
	if f.root != "" {
		return f.root
	}
	return "."
}

// RunSetup executes `kern setup [flags]` — the agent-wiring command family:
// wire, --check, --verify, --remove, --dry-run and the interactive plan
// surface. It returns the process exit code (0 ok, 1 runtime failure, 2
// usage error).
func RunSetup(rest []string) int {
	return run(func() int { runSetup(rest); return 0 })
}

func runSetup(rest []string) {
	f, pos := parseSetupFlagsOrDie(rest)
	// A positional here is almost certainly a user guessing a subcommand
	// that does not exist ("kern setup status" reads like a read-only
	// query). Silently ignoring it would RUN the real wiring — mutating
	// hooks, plugin copies and managed files under a query-shaped intent.
	// Fail fast with the read-only surface named instead.
	if len(pos) > 0 {
		fatalUsage("setup: unexpected argument %q — usage: kern setup [flags]; the read-only wiring check is `kern setup --check`", pos[0])
	}
	root := setupRoot(f)
	// Presence-aware display: surfaces may be wired for agents that are not
	// installed on this system (--global / --all-agents pre-wire every
	// supported agent so a later install needs no re-run). Annotate those
	// rows honestly at render time; what setup writes is unchanged.
	present := detectedSet(root)
	if f.check {
		results := Check(root)
		for i := range results {
			results[i].Note += agentPresenceNote(results[i].Agent, present)
		}
		if f.json {
			emitSetupJSON(f, root, "check (read-only inventory)", results)
			return
		}
		for _, s := range results {
			mark := "-"
			if s.Installed {
				mark = "x"
			}
			fmt.Printf("[%s] %-22s %s\n", mark, s.Agent, s.Note)
		}
		// L12: the raw table is not mappable to any documented number; the
		// summary derives its counts from the adapter registry + the table
		// itself at runtime, never from literals.
		fmt.Println(CheckSummary(root, results))
		// F4: one honest discoverability hint for the wired surface.
		fmt.Println(mcpSurfaceTip())
		return
	}
	if f.verify {
		if err := verifyMCP(root); err != nil {
			fatal("[FAIL] mcp binary not reachable: %v", err)
		}
		fmt.Println("[ok] mcp binary responds to initialize")
		return
	}
	if f.remove {
		runSetupRemove(f, root)
		return
	}
	var agents []string
	if f.agents != "" {
		agents = strings.Split(f.agents, ",")
	}
	var detected []string
	if f.globalOnly {
		f.global = true
	}
	defaulted, derr := applySetupDefaults(&f)
	if derr != nil {
		fatalUsage("%v", derr)
	}
	if f.detect && len(f.agents) == 0 {
		detected = DetectAgents(root)
	}
	if f.agentsMD != "" && f.agentsMD != "thin" && f.agentsMD != "full" {
		fatalUsage("setup: --agents-md must be thin or full, got %q", f.agentsMD)
	}
	failed := 0
	// The --global flag gates ALL user-global writes (home-scoped hooks,
	// home/global MCP adapters, global git ignore): without it, Wire touches
	// only project-scope files.
	opts := WireOptions{AgentsMD: f.agentsMD, DryRun: f.dryRun, GlobalOnly: f.globalOnly}
	ui := NewTermStyle(os.Stdout, f.plain)
	scope := "this project only"
	switch {
	case f.globalOnly:
		scope = "your user-level config only - no file is written in any repo"
	case f.global:
		scope = "this project + your user-level config (--global)"
	}
	var statuses []Status
	if ui.Rich && !f.json {
		// Interactive terminal: show the plan first (a dry-run preview), then
		// ask before writing. Non-interactive callers (install scripts, CI)
		// and --json callers take the plain path below and are never prompted.
		renderSetupHeader(ui, root)
		renderDetected(ui, detected, f.detect, defaulted)
		preview := WireWith(root, agents, f.detect, f.global, WireOptions{AgentsMD: f.agentsMD, DryRun: true, GlobalOnly: f.globalOnly})
		renderPlan(ui, root, preview, f.dryRun, scope)
		if !f.dryRun {
			if !f.yes && IsTTY(os.Stdin) && !confirmPrompt(ui, os.Stdin, "Write these files?") {
				fmt.Println("Cancelled. Nothing was written.")
				return
			}
			fmt.Print(ui.Heading(3, 3, "Writing"))
			statuses = WireWith(root, agents, f.detect, f.global, opts)
		}
	} else {
		statuses = WireWith(root, agents, f.detect, f.global, opts)
	}
	if f.json {
		// Machine-readable mode: one JSON document replaces the human
		// rendering. The additive global passes (global-rules blocks, the
		// global opencode plugin) fold into the same document; a dry-run
		// JSON never calls a write pass (mirrors the human dry-run lines).
		extra := statuses
		if f.globalRules {
			if f.dryRun {
				extra = append(extra, Status{Agent: "global rules", Note: "[dry-run] would manage the kern block in each host's global instructions (~/.claude/CLAUDE.md, ~/.codex/AGENTS.md, ~/.config/opencode/AGENTS.md, ~/.cursor/rules/kern.mdc, …) — nothing written"})
			} else {
				extra = append(extra, WireGlobalRules()...)
			}
		}
		if f.global {
			if f.dryRun {
				extra = append(extra, Status{Agent: "global (opencode plugin + instructions)", Note: "[dry-run] would write user-global instruction files (AGENTS.md, CLAUDE.md) and the global opencode plugin — nothing written"})
			} else {
				extra = append(extra, WireGlobal(agents)...)
			}
		}
		for i := range extra {
			extra[i].Note += agentPresenceNote(extra[i].Agent, present)
		}
		emitSetupJSON(f, root, scope, extra)
		return
	}
	shown := 0
	for _, s := range statuses {
		mark := "ok"
		if s.Skipped {
			mark = "--"
		} else if !s.Installed {
			mark = "!!"
			failed++
		}
		// Honest presence label: pre-wired surfaces for agents that are not
		// installed get annotated before the quiet filter decides visibility.
		s.Note += agentPresenceNote(s.Agent, present)
		// Quiet-by-default: only writes and failures print. No-op lines
		// (already present / skipped) hide — the user cares about what
		// changed, not the full inventory (--check lists everything).
		// A dry-run IS the preview: hiding its would-lines (whose
		// already-present suffix the quiet filter would swallow) would
		// leave `kern setup --dry-run` printing nothing at all, so every
		// dry-run status prints.
		if f.dryRun || !statusQuietHidden(mark, s.Note) {
			PrintSetupStatus(ui, mark, s)
			shown++
		}
	}
	if f.globalRules {
		if f.dryRun {
			// F10: a dry-run must not touch user-global files either.
			fmt.Println("[dry-run] --global-rules: would manage the kern block in each host's global instructions (~/.claude/CLAUDE.md, ~/.codex/AGENTS.md, ~/.config/opencode/AGENTS.md, ~/.cursor/rules/kern.mdc, …) — nothing written")
		} else {
			// --global-rules manages the kern agent-usage policy in each host's
			// GLOBAL instructions slot (~/.claude/CLAUDE.md, ~/.codex/AGENTS.md,
			// ~/.config/opencode/AGENTS.md): replace the marker-delimited block,
			// preserve user content outside it, create missing files.
			for _, s := range WireGlobalRules() {
				mark := "ok"
				if s.Skipped {
					mark = "--"
				} else if !s.Installed {
					mark = "!!"
					failed++
				}
				if !statusQuietHidden(mark, s.Note) {
					PrintSetupStatus(ui, mark, s)
					shown++
				}
			}
		}
	}
	if f.global {
		if f.dryRun {
			fmt.Println("[dry-run] --global: would write user-global instruction files (AGENTS.md, CLAUDE.md) and the global opencode plugin — nothing written")
		} else {
			// Global pre-wiring targets ALL agents (or the explicit --agents
			// list), never just the detected subset: an agent installed later
			// is already wired with no re-run. WireGlobal adds the global
			// instruction files (AGENTS.md, CLAUDE.md) and the opencode plugin.
			for _, s := range WireGlobal(agents) {
				mark := "ok"
				if s.Skipped {
					mark = "--"
				} else if !s.Installed {
					mark = "!!"
					failed++
				}
				// Global pre-wiring targets every supported agent, installed
				// or not — annotate the absent ones so the output never
				// implies an agent is in use.
				s.Note += agentPresenceNote(s.Agent, present)
				if !statusQuietHidden(mark, s.Note) {
					PrintSetupStatus(ui, mark, s)
					shown++
				}
			}
		}
	}
	// All-clean quiet runs must not look like a broken silent no-op: one
	// line says nothing needed writing. (Dry-run prints its own preview
	// trailer below; failures already printed their [!!] lines.)
	if failed == 0 && shown == 0 && !f.dryRun && len(statuses) > 0 {
		fmt.Println("all wiring already current — nothing to write (kern setup --check lists the full inventory)")
	}
	switch {
	case ui.Rich && f.dryRun:
		fmt.Println("\n" + ui.Dim("Dry-run preview only — nothing was written. Re-run without --dry-run to wire."))
	case ui.Rich:
		// the next-steps box below replaces the legacy trailer
	case len(f.agents) == 0 && !f.detect:
		if f.dryRun {
			fmt.Println("\nDry-run preview only — nothing was written. Re-run without --dry-run to wire.")
		} else {
			fmt.Println("\nWired all agents. Use the default (detected agents only) or --agents to target specific ones.")
		}
	case f.detect && len(f.agents) == 0 && defaulted:
		fmt.Printf("\nWired detected agents: %v (default). Use --all-agents to wire every supported agent, or --agents to target specific ones.\n", detected)
	case f.detect && len(f.agents) == 0:
		fmt.Printf("\nDetected agents: %v\n", detected)
	}
	if !f.dryRun {
		// F4: after a successful wire, one honest hint about the delivered
		// surface (the default surface is deliberate, full is opt-in).
		if ui.Rich {
			renderNextSteps(ui, defaulted)
			fmt.Println(ui.Dim(mcpSurfaceTip()))
		} else {
			fmt.Println(mcpSurfaceTip())
			fmt.Println("Restart your agent (opencode reload / claude) to pick up the MCP servers and kern-first instructions.")
		}
	}
	// Fail closed: a partial wiring (some agents errored) must not look like a
	// clean success. Exit non-zero so install scripts and CI can detect it.
	if failed > 0 {
		fatal("setup: %d agent(s) failed to wire — fix the errors above and rerun", failed)
	}
}

// mcpSurfaceTip is the ONE discoverability hint printed after a successful
// wire run and in setup --check (F4): wired agents see the default MCP
// surface, and the full catalog is opt-in via KERN_MCP_FULL. The counts are
// derived from the toolsurface registry and the tool catalog — never
// literals — so the tip tracks the code instead of drifting like the old
// "22-tool" banner did.
func mcpSurfaceTip() string {
	return fmt.Sprintf("tip: wired agents see the %d-tool default MCP surface; set KERN_MCP_FULL=1 to advertise all %d (see docs/mcp/protocol.md)",
		len(toolsurface.Default), len(catalog.ToolNames()))
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
