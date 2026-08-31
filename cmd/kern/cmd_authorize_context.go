package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// arrayFlag is a repeatable string flag value (flag.Value), used for
// repeatable options like -deny-path.
type arrayFlag []string

func (a *arrayFlag) String() string { return strings.Join(*a, ",") }

func (a *arrayFlag) Set(v string) error {
	*a = append(*a, v)
	return nil
}

// runAuthorizeContext implements `kern authorize-context`: compute the context
// an agent may legally read for a task. Exit codes: 0 = allowed, 2 = denied
// (proof still printed), 1 = error.
func runAuthorizeContext(rest []string) {
	fs := flag.NewFlagSet("authorize-context", flag.ContinueOnError)
	var (
		agentID   = fs.String("agent", "", "agent ID to authorize (required)")
		task      = fs.String("task", "", "task ID the authorization is scoped to (required)")
		root      = fs.String("root", ".", "project root")
		symbol    = fs.String("symbol", "", "optional substring filter applied to allowed symbols")
		denyPaths arrayFlag
		jsonOut   = fs.Bool("json", true, "emit JSON (default true)")
	)
	fs.Var(&denyPaths, "deny-path", "path prefix denied by the task scope (repeatable)")
	if err := fs.Parse(rest); err != nil {
		fatalUsage("authorize-context: %v", err)
	}
	if *agentID == "" || *task == "" {
		fatalUsage("authorize-context: -agent and -task are required")
	}

	ix, err := loadOrBuild(*root)
	if err != nil {
		fatal("%v", err)
	}

	// Build a per-call firewall and register the resolved agent into it.
	fw := governance.NewFirewall()
	if agent, aerr := governance.GetAgent(*agentID); aerr == nil {
		fw = fw.WithAgents(agent)
	}

	// Build the task scope only when denial paths are given; otherwise the
	// permissive default applies.
	var scope *domain.TaskScope
	if len(denyPaths) > 0 {
		scope = &domain.TaskScope{TaskID: *task, DeniedPaths: denyPaths}
	}

	req := governance.Request{
		Task:         *task,
		AgentID:      *agentID,
		Scope:        scope,
		Root:         *root,
		SymbolFilter: *symbol,
	}
	resp, err := governance.AuthorizeContext(req, ix, fw)
	if err != nil && err != governance.ErrUnauthorized {
		fatal("%v", err)
	}

	if *jsonOut {
		printJSON(resp)
	} else {
		printAuthorizeContextText(resp)
	}

	if resp.Proof.Decision.Allowed {
		panic(exitError{code: 0})
	}
	panic(exitError{code: 2})
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
