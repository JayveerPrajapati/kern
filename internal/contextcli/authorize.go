// authorize.go implements the `kern authorize-context` CLI entry point: flag
// parsing and the human/JSON rendering live here; the authorization engine
// is internal/governance.
package contextcli

import (
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// authorizeFlags are the flags `kern authorize-context` consumes — exactly
// the dispatch commandFlags["authorize-context"] row (agent/deny-path/json/
// root/symbol/task). --json defaults TRUE for this command (the preserved
// FlagSet default): jsonSet records whether the flag was given at all, so an
// explicit --json=false is the only way to get the text form.
type authorizeFlags struct {
	agent     string
	task      string
	root      string
	symbol    string
	denyPaths []string
	json      bool
	jsonSet   bool
}

// parseAuthorizeFlags replicates the unified cmd/kern parser's semantics for
// the authorize-context flags, via the shared cliflags.Parser. --deny-path
// accumulates (repeatable); --json sets jsonSet whenever the flag appears
// (even --json=false), exactly like the shared parser.
func parseAuthorizeFlags(args []string) (authorizeFlags, []string, error) {
	var f authorizeFlags
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--agent":
			p.SetStr(&f.agent, inline, hasInline)
		case "--task":
			p.SetStr(&f.task, inline, hasInline)
		case "--root":
			p.SetStr(&f.root, inline, hasInline)
		case "--symbol":
			p.SetStr(&f.symbol, inline, hasInline)
		case "--deny-path":
			if hasInline {
				f.denyPaths = append(f.denyPaths, inline)
			} else if v, ok := p.Take(); ok {
				f.denyPaths = append(f.denyPaths, v)
			}
		case "--json":
			p.SetBool(&f.json, "--json", inline, hasInline)
			f.jsonSet = true
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

// RunAuthorizeContext executes `kern authorize-context -agent <id> -task
// <task> [...]` and returns the process exit code. Exit codes: 0 = allowed,
// 3 = denied (policy family; proof still printed), 1 = error, 2 = usage
// error.
func RunAuthorizeContext(rest []string) int {
	return run(func() int { return runAuthorizeContextCLI(rest) })
}

// runAuthorizeContextCLI implements `kern authorize-context`: compute the
// context an agent may legally read for a task. Exit codes: 0 = allowed,
// 3 = denied (policy family; proof still printed), 1 = error.
func runAuthorizeContextCLI(rest []string) int {
	f, _, err := parseAuthorizeFlags(rest)
	if err != nil {
		fatalUsage("authorize-context: %v", err)
	}
	agentID := f.agent
	task := f.task
	root := f.root
	symbol := f.symbol
	denyPaths := f.denyPaths
	// --json defaults TRUE for authorize-context (preserved FlagSet default);
	// the unified parser's bool default is false, so treat it as json unless
	// an explicit --json[=false] was given.
	jsonOut := f.json || !f.jsonSet
	if agentID == "" || task == "" {
		fatalUsage("authorize-context: -agent and -task are required")
	}
	// Agent identities are process-local by default; a fresh CLI process
	// starts with an empty registry, which would deny every agent — even
	// the built-in default. Ensure the default identity exists and merge
	// any agents persisted by `kern org agents register` before resolving.
	governance.EnsureDefaultAgent()
	// Fail loud: a registry that cannot be loaded would silently deny or
	// mis-scope every authorization decision below — never continue with a
	// partial registry.
	if err := governance.LoadAgents(root); err != nil {
		fatal("authorize-context: could not load agents: %v", err)
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("AuthorizeContext: %v", err)
	}

	// Build a per-call firewall and register the resolved agent into it.
	fw := governance.NewFirewall()
	if agent, aerr := governance.GetAgent(agentID); aerr == nil {
		fw = fw.WithAgents(agent)
	}

	// Build the task scope only when denial paths are given; otherwise the
	// permissive default applies.
	var scope *domain.TaskScope
	if len(denyPaths) > 0 {
		scope = &domain.TaskScope{TaskID: task, DeniedPaths: denyPaths}
	}

	req := governance.Request{
		Task:         task,
		AgentID:      agentID,
		Scope:        scope,
		Root:         root,
		SymbolFilter: symbol,
	}
	resp, err := governance.AuthorizeContext(req, ix, fw)
	if err != nil && err != governance.ErrUnauthorized {
		fatal("AuthorizeContext: %v", err)
	}

	if jsonOut {
		printJSON(resp)
	} else {
		printAuthorizeContextText(resp)
	}

	if resp.Proof.Decision.Allowed {
		return 0
	}
	return 3
}

// printAuthorizeContextText renders a human-readable summary of an
// authorization decision for `-json=false` callers.
func printAuthorizeContextText(resp governance.Response) {
	if resp.Proof.Decision.Allowed {
		fmt.Printf("ALLOWED  agent=%s task=%s symbols=%d edges=%d denied=%d fingerprint=%s\n",
			resp.Proof.Agent.ID, resp.Proof.TaskScope.TaskID,
			len(resp.Scope.Symbols), len(resp.Scope.Edges), len(resp.Scope.Denied),
			shortFingerprint(resp.Proof.Fingerprint))
		return
	}
	deny := resp.Proof.Decision.Deny
	if deny == nil {
		fmt.Printf("DENIED   agent=%s task=%s fingerprint=%s\n",
			resp.Proof.Agent.ID, resp.Proof.TaskScope.TaskID,
			shortFingerprint(resp.Proof.Fingerprint))
		return
	}
	fmt.Printf("DENIED   agent=%s task=%s stage=%s reason=%s fingerprint=%s\n",
		deny.AgentID, deny.TaskID, deny.Stage, deny.Reason,
		shortFingerprint(resp.Proof.Fingerprint))
	for _, d := range resp.Scope.Denied {
		fmt.Printf("  denied %s (%s): %s\n", d.Symbol.Qualified, d.Stage, d.Reason)
	}
}

// shortFingerprint truncates a hex fingerprint for display.
func shortFingerprint(fp string) string {
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}
