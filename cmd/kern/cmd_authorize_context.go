package main

import (
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// runAuthorizeContext implements `kern authorize-context`: compute the context
// an agent may legally read for a task. Exit codes: 0 = allowed, 2 = denied
// (proof still printed), 1 = error.
func runAuthorizeContext(rest []string) int {
	f, _, err := parseFlags(rest)
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
	return 2
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
