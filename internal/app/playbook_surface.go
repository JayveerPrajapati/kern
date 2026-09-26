// Heal-playbook surface (Feature Batch D): thin app-layer wrappers over the
// incident playbook store so every interface (MCP, CLI, REST) reaches the
// store through the shared application-services layer — and so interface
// packages never need to import internal/incident directly (Architecture
// Invariant 1: interfaces don't orchestrate engines directly).
package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/incident"
)

// AddPlaybook stores a heal playbook for the given project root (insert or
// replace by signature). It returns a one-line confirmation so all surfaces
// render identically.
func AddPlaybook(root string, pb incident.Playbook) (string, error) {
	if err := incident.AddPlaybook(root, pb); err != nil {
		return "", err
	}
	return fmt.Sprintf("added playbook: %s (%d steps)", pb.Signature, len(pb.Steps)), nil
}

// AddPlaybookJSON parses a playbook JSON document and stores it, returning the
// confirmation line. A malformed document is an error, never a silent skip.
func AddPlaybookJSON(root, runbookJSON string) (string, error) {
	var pb incident.Playbook
	if err := json.Unmarshal([]byte(runbookJSON), &pb); err != nil {
		return "", fmt.Errorf("invalid runbook JSON: %w", err)
	}
	return AddPlaybook(root, pb)
}

// ListPlaybooksText renders the stored heal playbooks for the project root as
// text (deterministic ordering: newest first, then signature asc).
func ListPlaybooksText(root string) (string, error) {
	list, err := incident.ListPlaybooks(root)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "no playbooks stored", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "playbooks (%d):\n", len(list))
	for _, p := range list {
		fmt.Fprintf(&b, "  %s\n", p.Signature)
		for _, s := range p.Steps {
			fmt.Fprintf(&b, "    step: %s\n", s)
		}
		if p.Source != "" {
			fmt.Fprintf(&b, "    source: %s\n", p.Source)
		}
	}
	return b.String(), nil
}
