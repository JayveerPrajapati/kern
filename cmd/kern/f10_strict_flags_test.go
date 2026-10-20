package main

import (
	"strings"
	"testing"

	graphcli "github.com/JayveerPrajapati/kern/internal/graphcli"
)

// F10 strict per-command flags: the shared parseFlags knows every flag in
// the system, so dispatchCommand must reject flags a command does not
// declare (commandFlags) — usage error, exit 2, matching the strict
// `kern mcp tools` FlagSet. These tests pin the contract.

// TestUnknownCommandFlagsExit2: representative commands across families
// must reject a flag that is NOT theirs with exit 2, never silently apply a
// default. `kern budget --budget 100` is the F10 smoking gun (--budget is
// context-watch's flag, budget reads --max).
func TestUnknownCommandFlagsExit2(t *testing.T) {
	cases := []struct {
		cmd  string
		rest []string
		want string // substring of the stderr error
	}{
		{"version", []string{"--json"}, "unknown flag: --json"},
		{"ast", []string{"--bogus", "pattern"}, "unknown flag: --bogus"},
		{"search", []string{"--bogus", "dispatch"}, "unknown flag: --bogus"},
		{"explore", []string{"--bogus", "sym"}, "unknown flag: --bogus"},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			var code int
			stderr := captureStderr(t, func() {
				code = dispatchCommand(tc.cmd, tc.rest)
			})
			if code != 2 {
				t.Fatalf("dispatchCommand(%q, %v) = %d, want 2 (usage error)", tc.cmd, tc.rest, code)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr, tc.want)
			}
			if !strings.Contains(stderr, "--help") {
				t.Fatalf("stderr = %q, want a pointer to 'kern %s --help'", stderr, tc.cmd)
			}
		})
	}
}

// TestCommandFlagsAcceptDocumentedFlags: the flags each command's help text
// documents (goldenUsageFlags, usage-derived) must be ACCEPTED — a strict
// registry that rejects documented flags would be a regression, not a fix.
func TestCommandFlagsAcceptDocumentedFlags(t *testing.T) {
	for name, want := range goldenUsageFlags {
		if commandRawArgs[name] {
			continue // pass-through commands are exempt by design
		}
		for _, f := range want {
			if f == "--" {
				continue // the sandbox separator; the validator stops at it
			}
			flag := strings.TrimPrefix(f, "--")
			found := false
			for _, allowed := range commandFlags[name] {
				if allowed == flag {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("command %q: documented flag %q is not in commandFlags — strict validation would reject it", name, f)
			}
		}
	}
}

// handlerRegisteredFlags pins the flags each audited command's handler
// GENUINELY registers/consumes (no "--" prefix), for the reverse drift
// direction of TestCommandFlagsAcceptDocumentedFlags: the F10 strict gate
// (unknownFlagIn) rejects any handler flag missing from commandFlags, so a
// handler flag that is not declared there exits 2 before the handler runs.
// This is the P0-1 class that broke `kern fix --file/--content`: runFix
// registered them on its FlagSet but commandFlags["fix"] did not declare
// them. The table mirrors the handler registrations below and MUST be
// updated when a handler adds or drops a flag:
//
//	guard          → internal/contextcli/guard.go            parseGuardFlags (per-command parser; the shared parseFlags fields it reads)
var handlerRegisteredFlags = map[string][]string{}

// TestHandlerFlagsPresentInCommandFlags: every flag a handler genuinely
// registers/consumes must be declared in commandFlags, or the F10 strict
// gate rejects it before the handler can run. This is the direction that
// caught P0-1 (`kern fix --file` exited 2 despite runFix registering it).
func TestHandlerFlagsPresentInCommandFlags(t *testing.T) {
	for name, want := range handlerRegisteredFlags {
		for _, f := range want {
			found := false
			for _, allowed := range commandFlags[name] {
				if allowed == f {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("command %q: handler-registered flag %q is not in commandFlags — strict validation would reject it before the handler runs (P0-1 class)", name, f)
			}
		}
	}
}

// TestRawArgsCommandsAreExempt: pass-through commands (mcp-client,
// blueprint) forward arbitrary args to their own subcommand grammars and
// must not be rejected by the strict validator.
func TestRawArgsCommandsAreExempt(t *testing.T) {
	for cmd := range commandRawArgs {
		if bad := unknownFlagIn(cmd, []string{"--anything", "--goes", "pos"}); bad != "" {
			t.Errorf("rawArgs command %q must not be validated, got unknown flag %q", cmd, bad)
		}
	}
}

// TestFlagValidationStopsAtDoubleDash: everything after a bare `--` is
// pass-through (the `kern sandbox [root] -- <command...>` convention) —
// shell flags like `-c`/`-n` there must never be misread as kern flags.
func TestFlagValidationStopsAtDoubleDash(t *testing.T) {
	if bad := unknownFlagIn("sandbox", []string{"--", "echo", "-n", "hi"}); bad != "" {
		t.Fatalf("flags after `--` must be pass-through, got unknown flag %q", bad)
	}
	if bad := unknownFlagIn("sandbox", []string{"--bogus", "--", "echo", "hi"}); bad == "" {
		t.Fatalf("flags BEFORE `--` are still validated; --bogus must be rejected")
	}
}

// TestF13AstProseRejectJunkRoot: `kern ast func run` (unquoted) used to
// silently "succeed" (exit 0) while indexing a nonexistent junk path.
// A nonexistent root positional is now a usage error (exit 2), and so is
// an excess positional count.
func TestF13AstProseRejectJunkRoot(t *testing.T) {
	if code := graphcli.RunAst([]string{"func", "no-such-dir-xyz"}); code != 2 {
		t.Fatalf("runAst exit code = %d, want 2 (usage error)", code)
	}
	expectExit(t, 2, func() {
		runProse([]string{"func", "run"})
	})
	if code := graphcli.RunAst([]string{"a", "b", "c"}); code != 2 {
		t.Fatalf("runAst exit code = %d, want 2 (usage error)", code)
	}
	expectExit(t, 2, func() {
		runProse([]string{"a", "b", "c"})
	})
}

// TestL1PreEditNoArgsExits2: a bare `kern pre-edit` is a usage error
// (exit 2, documented usage), not a runtime failure (exit 1). Wiring-level
// pin: the dispatch row routes to the internal/mcp/preedit entry point,
// which returns the usage exit code (the behavioral coverage moved with the
// family to internal/mcp/preedit/cli_test.go).
func TestL1PreEditNoArgsExits2(t *testing.T) {
	if code := dispatchCommand("pre-edit", nil); code != 2 {
		t.Fatalf("dispatchCommand(pre-edit) = %d, want 2 (usage error)", code)
	}
}

// TestL2PromptShowIsSubcommand: `kern prompt show` without a template is a
// usage error (exit 2), never "unknown template show" (exit 1) — "show" is
// a subcommand, not a template name.
func TestL2PromptShowIsSubcommand(t *testing.T) {
	expectExit(t, 2, func() {
		runPrompt([]string{"show"})
	})
}
