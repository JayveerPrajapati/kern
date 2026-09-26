package exec

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/heal"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
)

// Heal runs the self-heal loop (validate -> fix -> repeat) over a failing
// project, consulting and recording incident playbooks for the
// deterministic known-fix fast path.
func Heal(ctx context.Context, args map[string]any) (string, error) {
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	task := mcpargs.ArgString(args, "task")
	if task == "" {
		task = "Fix the failing build/test/syntax errors in this project."
	}
	// kern_heal drives validation/build commands (arbitrary host code); it
	// must pass the governance firewall, fail closed. The task text is the
	// best command binding available (the concrete commands are
	// LLM-chosen), so an approval for one heal task never authorizes a
	// different one.
	if err := governance.CheckExecCommand(task, root); err != nil {
		return "", err
	}
	model := mcpargs.ArgString(args, "model")
	rounds := 3
	if s := mcpargs.ArgString(args, "max_rounds"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return "", fmt.Errorf("max_rounds: invalid integer %q", s)
		}
		if n > 0 {
			rounds = n
		}
	}
	timeout := 120 * time.Second
	if s := mcpargs.ArgString(args, "timeout"); s != "" {
		sec, err := strconv.Atoi(s)
		if err != nil {
			return "", fmt.Errorf("timeout: invalid integer %q", s)
		}
		if sec > 0 {
			timeout = time.Duration(sec) * time.Second
		}
	}
	res := heal.RunWithPlaybook(ctx, root, task, model, rounds, timeout, mcpargs.ArgBool(args, "force"), healPlaybook(root))
	var b strings.Builder
	if res.Validated {
		if res.UsedPlaybook {
			fmt.Fprintf(&b, "status: healed OK via recorded playbook (no LLM rounds)\n")
		} else {
			fmt.Fprintf(&b, "status: healed OK after %d round(s)\n", res.Iterations)
		}
		for _, c := range res.Changes {
			fmt.Fprintf(&b, "changed: %s\n", c)
		}
		if res.Diff != "" {
			fmt.Fprintf(&b, "diff:\n%s\n", res.Diff)
		}
		return b.String(), nil
	}
	fmt.Fprintf(&b, "status: still failing after %d round(s)\n", res.Iterations)
	if res.Err != nil {
		fmt.Fprintf(&b, "error: %v\n", res.Err)
	}
	if res.LastOutput != "" {
		fmt.Fprintf(&b, "output:\n%s\n", truncateMCP(res.LastOutput, 3000))
	}
	return b.String(), nil
}

// healPlaybook returns a heal.Playbook backed by the incident playbook store
// (<root>/.kern/playbooks.json, same store the incident command uses), or nil
// when the store cannot be constructed — a nil pb keeps heal behavior exactly
// today's (no signature computation, no playbook consult).
func healPlaybook(root string) heal.Playbook {
	store := incident.NewPlaybookStore(root)
	if store == nil {
		return nil
	}
	return &mcpHealPlaybookStore{root: root, store: store}
}

// mcpHealPlaybookStore adapts incident.PlaybookStore to heal.Playbook so the
// heal loop consults and records playbooks without importing incident. Steps
// are heal's deterministic "path|old|new" text format (base64 contents).
type mcpHealPlaybookStore struct {
	root  string
	store *incident.PlaybookStore
}

// Lookup returns the recorded replacements for an exact signature.
func (h *mcpHealPlaybookStore) Lookup(signature string) ([]heal.Replacement, bool) {
	pb, ok := h.store.FindBySignature(signature)
	if !ok {
		return nil, false
	}
	reps := heal.DecodeReplacements(pb.Steps)
	if len(reps) == 0 {
		return nil, false
	}
	return reps, true
}

// Record stores replacements under a signature (upsert by signature).
func (h *mcpHealPlaybookStore) Record(signature string, reps []heal.Replacement) error {
	return h.store.UpsertBySignature(signature, heal.EncodeReplacements(h.root, reps))
}

// truncateMCP truncates s to n bytes with a visible continuation marker.
func truncateMCP(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}
