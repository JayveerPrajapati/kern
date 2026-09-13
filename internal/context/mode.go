package context

// Mode is a named planner-policy preset — the spec's "Modes alter planner
// policy." A mode selects the task-policy family (which evidence classes get
// what weight), an optional review lens to re-rank the packet's facts, an
// optional disclosure-level override, and an optional token-budget override.
// Everything resolves deterministically — no LLM.
type Mode struct {
	Name           string   `json:"name"`
	TaskType       TaskType `json:"task_type"`
	Lens           string   `json:"lens,omitempty"`            // review lens name ("" = none)
	RetrievalLevel string   `json:"retrieval_level,omitempty"` // l1|l2|l3 override ("" = policy default)
	Budget         int      `json:"budget,omitempty"`          // token budget override (0 = policy default)
}

// Built-in mode names (spec's five context modes).
const (
	ModeFix          = "fix"
	ModeReview       = "review"
	ModeArchitecture = "architecture"
	ModeIncident     = "incident"
	ModeExplain      = "explain"
)

// DefaultModes returns the five built-in modes in deterministic order:
//   - fix:          fix_bug policy — failing tests/logs/stack traces/callers.
//   - review:       security_review policy, security lens — risks surface first.
//   - architecture: refactor policy, architecture lens — graph/module/deps.
//   - incident:     fix_bug policy with the runtime-heavy performance lens and
//     deepest disclosure (l3) — timelines/evidence from source.
//   - explain:      documentation policy, shallow disclosure (l1) — docs and
//     source relationships without full source.
func DefaultModes() []Mode {
	return []Mode{
		{Name: ModeFix, TaskType: TaskFixBug, Lens: "balanced"},
		{Name: ModeReview, TaskType: TaskSecurityReview, Lens: "security"},
		{Name: ModeArchitecture, TaskType: TaskRefactor, Lens: "architecture"},
		{Name: ModeIncident, TaskType: TaskFixBug, Lens: "performance", RetrievalLevel: "l3"},
		{Name: ModeExplain, TaskType: TaskDocumentation, Lens: "balanced", RetrievalLevel: "l1"},
	}
}

// ModeFor returns the mode registered under name (case-sensitive), and false
// for unknown names.
func ModeFor(name string) (Mode, bool) {
	for _, m := range DefaultModes() {
		if m.Name == name {
			return m, true
		}
	}
	return Mode{}, false
}

// ModeNames returns the built-in mode names in deterministic order.
func ModeNames() []string {
	modes := DefaultModes()
	names := make([]string, len(modes))
	for i, m := range modes {
		names[i] = m.Name
	}
	return names
}
