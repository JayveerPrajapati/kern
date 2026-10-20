// guard.go implements the `kern guard` CLI entry point: flag parsing, the
// check/init/allow subcommand dispatch, the authz gate and the persisted
// event publishing live here; the boundary engine is internal/guard and the
// authorization is internal/governance.
package contextcli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/guard"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/relay"
	"github.com/JayveerPrajapati/kern/internal/topology"
)

// KernJSONContractVersion is the schema version for machine-readable JSON
// output — the shared constant for every kern command that emits a
// versioned JSON contract (guard, security, fingerprint). It was moved here
// with the guard family; the staying cmd/kern JSON surfaces reference it.
const KernJSONContractVersion = 2

// AuthzVerdictSchemaVersion is the schema version for authorization
// verdicts.
const AuthzVerdictSchemaVersion = 1

// guardFlags are the flags `kern guard` consumes — exactly the dispatch
// commandFlags["guard"] row (agent-id/file/force/json/precision/range/sarif/
// task/threshold).
type guardFlags struct {
	agentID   string
	file      string
	force     bool
	json      bool
	precision string
	range_    string
	sarif     bool
	task      string
	threshold int
}

// parseGuardFlags replicates the unified cmd/kern parser's semantics for the
// guard flags, via the shared cliflags.Parser. The int error message names
// "--threshold" exactly like the shared parser.
func parseGuardFlags(args []string) (guardFlags, []string, error) {
	var f guardFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--agent-id":
			p.SetStr(&f.agentID, inline, hasInline)
		case "--file":
			p.SetStr(&f.file, inline, hasInline)
		case "--force":
			p.SetBool(&f.force, "--force", inline, hasInline)
		case "--json":
			p.SetBool(&f.json, "--json", inline, hasInline)
		case "--precision":
			p.SetStr(&f.precision, inline, hasInline)
		case "--range":
			p.SetStr(&f.range_, inline, hasInline)
		case "--sarif":
			p.SetBool(&f.sarif, "--sarif", inline, hasInline)
		case "--task":
			p.SetStr(&f.task, inline, hasInline)
		case "--threshold":
			p.SetInt(&f.threshold, "--threshold", inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

// RunGuard executes `kern guard <check|init|allow> [root] [...]` and returns
// the process exit code (0 ok, 1 runtime failure, 2 usage error, 3 policy
// denial).
func RunGuard(rest []string) int {
	return run(func() int { runGuardCLI(rest); return 0 })
}

func runGuardCLI(rest []string) {
	f, args, err := parseGuardFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	// The authz gate needs both halves of the agent identity — an
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
		path := guard.DefaultBoundariesPath(root)
		if _, serr := os.Stat(path); serr == nil && !f.force {
			// guard init previously OVERWROTE an existing boundaries file
			// without warning (destroying custom rules). Refuse unless
			// --force is given.
			fmt.Printf("guard: %s already exists — leaving untouched (pass --force to overwrite)\n", path)
			return
		}
		if err := guard.InitBoundaries(root); err != nil {
			fatal("Guard: %v", err)
		}
		fmt.Printf("wrote %s (edit it to declare boundary rules)\n", path)
	case "check":
		// ReadIndexWithProof returns the freshness proof ReadIndex's staleness
		// decision was based on (or the post-update observation), so the JSON
		// output below does not re-observe the tree a second time — the old
		// explicit ix.FreshnessProof(root) recomputed the same walk/git dance
		// (~0.5-0.8s on a medium repo) purely for provenance output.
		ix, freshness, err := intel.ReadIndexWithProof(root)
		if err != nil {
			fatal("Guard: %v", err)
		}
		b, err := guard.LoadBoundaries(root)
		if err != nil {
			fatal("Guard: %v", err)
		}
		unconfigured := b == nil
		if b == nil {
			b = guard.InferBoundaries(ix)
		}
		var files []string
		if f.file != "" {
			for _, p := range strings.Split(f.file, ",") {
				if p = strings.TrimSpace(p); p != "" {
					files = append(files, p)
				}
			}
		} else {
			from, to := cliflags.SplitRange(f.range_)
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
				if errors.Is(err, fs.ErrNotExist) {
					// Deleted in the diff: nothing to check — the file's
					// symbols are gone from the index too .
					continue
				}
				fatal("file not found: %s", p)
			}
		}

		// Authz gate: when both --agent-id and --task are present, run
		// AuthorizeContext BEFORE the boundary check and surface the verdict in
		// the JSON output. A denied verdict is a blocking gate: exit 3 without
		// proceeding to the boundary check.
		var authzVerdict map[string]any
		authzDenied := false
		if f.agentID != "" && f.task != "" {
			authzVerdict, authzDenied = guardAuthzVerdict(f.agentID, f.task, ix, files)
		}
		if authzDenied {
			if f.json {
				printJSON(map[string]any{
					"schema_version":  KernJSONContractVersion,
					"violations":      []guard.Violation{}, // boundary check skipped
					"freshness_proof": freshness,
					"authz_verdict":   authzVerdict,
				})
			}
			fatalPolicy("guard: authz denied for agent %q task %q — exit 3 (denied)", f.agentID, f.task)
		}

		strict := f.precision == "strict"
		violations, skipped := guard.CheckBoundariesPrecise(ix, b, files, strict)
		// Guard gate: a changed file whose package participates in an
		// import cycle is a WARN (never a violation — the exit code stays
		// driven by boundary violations alone), surfaced in both output modes.
		cycleWarnings := intel.ImportCycleWarnings(ix, files)
		// @pure mutability assertions are opt-in via "pure": true in
		// .kern/boundaries.json. A nil ruleset (missing file) has no Pure flag,
		// so the check is naturally skipped when the guard is not configured.
		if b != nil && b.Pure {
			violations = append(violations, topology.CheckPurity(ix, files)...)
		}
		switch {
		case f.sarif:
			fmt.Println(intel.RenderViolationsSARIF(violations, version))
		case f.json:
			out := map[string]any{
				"schema_version":  KernJSONContractVersion,
				"violations":      violations,
				"freshness_proof": freshness,
			}
			// Only surface skipped edges when strict mode actually skipped
			// some, so default-mode JSON output is unchanged.
			if len(skipped) > 0 {
				out["skipped_edges"] = skipped
			}
			if len(cycleWarnings) > 0 {
				out["cycle_warnings"] = cycleWarnings
			}
			// The authz_verdict is emitted only when --agent-id/--task were
			// supplied (backward compat: old callers see no new key).
			if authzVerdict != nil {
				out["authz_verdict"] = authzVerdict
			}
			printJSON(out)
		default:
			fmt.Println(guard.RenderViolations(violations))
			for _, w := range cycleWarnings {
				fmt.Printf("WARN: %s (touched by this diff)\n", w)
			}
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
		publishGuardEvents(root, violations, unconfigured || skipped["boundaries-not-configured"] > 0)
		if f.threshold >= 0 && len(violations) > f.threshold {
			fatalPolicy("guard: %d violation(s) exceed threshold %d — exit 3 (denied); see output above", len(violations), f.threshold)
		}
	case "allow":
		// D4-D (2026-10-08): operator one-time allow for a guarded subject
		// (the read/grep/bash deny surfaces name the hash in their deny
		// message). Tty-gated: only a human at an interactive terminal can
		// mint an entry — with no controlling terminal (agent pipe, CI) the
		// command refuses fail-closed, so the registry can never be minted
		// by the very agents it gates.
		if len(args) < 2 {
			fatalUsage("usage: kern guard allow <hash> [subject-preview] — the hash comes from the deny message")
		}
		hash := args[1]
		subject := strings.Join(args[2:], " ")
		tty, terr := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if terr != nil {
			fatalPolicy("guard allow: no interactive terminal — an allow-once entry can only be minted by a human; run it in your own terminal")
		}
		defer tty.Close()
		fmt.Fprintf(tty, "kern guard allow\n  hash:    %s\n  subject: %s\nAllow this exact subject ONCE (y/N)? ", hash, subject)
		// 15s deadline (the requireInteractiveApproval precedent): a tty
		// read with no reader or a distracted operator must never hang the
		// caller — live-probed as a real 2-minute hang through the governed
		// bash path before this deadline existed.
		answerCh := make(chan string, 1)
		go func() {
			var answer string
			fmt.Fscanln(tty, &answer)
			answerCh <- answer
		}()
		select {
		case answer := <-answerCh:
			if answer != "y" && answer != "Y" && answer != "yes" {
				fatalPolicy("guard allow: not confirmed — nothing recorded")
			}
		case <-time.After(15 * time.Second):
			fatalPolicy("guard allow: timed out after 15s — nothing recorded")
		}
		if aerr := governance.AppendAllowOnce(hash, subject); aerr != nil {
			fatal("guard allow: %v", aerr)
		}
		fmt.Println("recorded — the next identical subject runs once, then the allowance is inert")
	default:
		fatalUsage("usage: kern guard <check|init|allow> [root] [--file f1,f2] [--range a..b] [--json|--sarif] [--threshold N] [--precision default|strict] [--agent-id ID --task DESC]")
	}

}

// publishGuardEvents publishes guard outcomes as ArchitectureViolation /
// ArchitectureWarning events: appended to the persisted bus at
// <root>/.kern/events.jsonl (replayed by kern-server on start) AND, when a
// relay owns <root>/.kern/events.sock, emitted live so `kern events watch`
// subscribers see guard results immediately instead of at the next replay.
// Publishing is best-effort and deterministic (event order = violation order);
// it never fails the guard or alters its output or exit behavior.
func publishGuardEvents(root string, violations []guard.Violation, warnNotConfigured bool) {
	relay.PublishPersisted(root, guard.GuardEvents(violations, warnNotConfigured))
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
		fatal("guardAuthzVerdict: %v", err)
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
