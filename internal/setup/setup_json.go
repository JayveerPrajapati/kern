package setup

import (
	"encoding/json"
	"fmt"
)

// setup_json.go — the machine-readable output of `kern setup --json`
// (pairs with --check for an inventory and --dry-run for a would-write
// preview). One JSON document replaces the human rendering so scripts and
// CI can consume the wiring state without parsing terminal boxes. The
// fields mirror what the human table prints: per-surface status rows
// (agent, installed, skipped, note), the detected-agent list, the derived
// summary line, and the run scope.

// setupJSONStatus is one wiring-status row (the same data the human table
// prints, including the presence annotation for undetected agents).
type setupJSONStatus struct {
	Agent     string `json:"agent"`
	Installed bool   `json:"installed"`
	Skipped   bool   `json:"skipped,omitempty"`
	Note      string `json:"note,omitempty"`
}

// setupJSON is the full document emitted by `kern setup --json`.
type setupJSON struct {
	Scope    string            `json:"scope"`
	Root     string            `json:"root"`
	DryRun   bool              `json:"dryRun,omitempty"`
	Detected []string          `json:"detected"`
	Statuses []setupJSONStatus `json:"statuses"`
	Summary  string            `json:"summary"`
}

// emitSetupJSON marshals the status list (with presence annotations already
// applied by the caller) into one indented JSON document on stdout.
func emitSetupJSON(f setupFlags, root, scope string, statuses []Status) {
	detected := DetectAgents(root)
	if detected == nil {
		detected = []string{}
	}
	out := setupJSON{
		Scope:    scope,
		Root:     root,
		DryRun:   f.dryRun,
		Detected: detected,
		Statuses: make([]setupJSONStatus, 0, len(statuses)),
		Summary:  CheckSummary(root, statuses),
	}
	for _, s := range statuses {
		out.Statuses = append(out.Statuses, setupJSONStatus{
			Agent:     s.Agent,
			Installed: s.Installed,
			Skipped:   s.Skipped,
			Note:      s.Note,
		})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fatal("setup: --json marshal: %v", err)
	}
	fmt.Println(string(b))
}
