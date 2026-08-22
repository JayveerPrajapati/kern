package domain

// KnowledgeSource is a source of engineering knowledge that may contradict
// other sources. Strict Plan Phase 14.
type KnowledgeSource string

const (
	SourceGraph       KnowledgeSource = "GRAPH"
	SourceTwin        KnowledgeSource = "TWIN"
	SourceMemory      KnowledgeSource = "MEMORY"
	SourceGit         KnowledgeSource = "GIT"
	SourceRuntime     KnowledgeSource = "RUNTIME"
	SourceArchitecture KnowledgeSource = "ARCHITECTURE"
	SourceTests       KnowledgeSource = "TESTS"
)

// ConsistencyConflict represents a contradiction between two or more knowledge
// sources about the same subject. Strict Plan Phase 14.
type ConsistencyConflict struct {
	Subject   string          `json:"subject"`   // what the conflict is about (symbol, service, etc.)
	ClaimA    string          `json:"claim_a"`   // the first claim
	SourceA   KnowledgeSource `json:"source_a"`  // source of the first claim
	ClaimB    string          `json:"claim_b"`   // the contradicting claim
	SourceB   KnowledgeSource `json:"source_b"`  // source of the second claim
	Resolution string         `json:"resolution,omitempty"` // how to resolve (empty = unresolved)
}

// ConsistencyReport is the result of a cross-engine consistency check.
type ConsistencyReport struct {
	Conflicts       []ConsistencyConflict `json:"conflicts"`
	ConfidenceDowngrades map[string]float64 `json:"confidence_downgrades"` // subject → new confidence (downgraded)
}
