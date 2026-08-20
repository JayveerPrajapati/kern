package domain

// Intent captures the parsed user intent for a change request. It is the
// output of the deterministic intent-analysis step (keyword/category matching,
// no LLM) that precedes context retrieval.
type Intent struct {
	RawText    string   // the original change description
	Verbs      []string // detected action verbs: "add", "remove", "refactor", "fix", "update", "test"
	Targets    []string // detected target nouns: symbol names, file paths, service names
	Categories []string // "feature", "bugfix", "refactor", "test", "docs", "config"
}
