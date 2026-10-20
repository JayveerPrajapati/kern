package topology

// Violation is one rejected boundary crossing, with the rule that rejected it.
// It lives in topology (not the guard package) because topology's own
// analyzers — CheckPurity (purity.go) — consume it, intel re-exports it as an
// alias (its SARIF renderer, sarif.go, still consumes it), and guard
// re-exports it as an alias so consumers can keep a single guard import.
type Violation struct {
	CallerFile string `json:"caller_file"`
	CalleeFile string `json:"callee_file"`
	Symbol     string `json:"symbol,omitempty"`
	Line       int    `json:"line,omitempty"`
	RuleFrom   string `json:"rule_from"`
	RuleTo     string `json:"rule_to"`
}
