package domain

import (
	"fmt"
	"strings"
)

// KnowledgeSource is a source of engineering knowledge that may contradict
// other sources. .
type KnowledgeSource string

const (
	SourceGraph        KnowledgeSource = "GRAPH"
	SourceTwin         KnowledgeSource = "TWIN"
	SourceMemory       KnowledgeSource = "MEMORY"
	SourceGit          KnowledgeSource = "GIT"
	SourceRuntime      KnowledgeSource = "RUNTIME"
	SourceArchitecture KnowledgeSource = "ARCHITECTURE"
	SourceTests        KnowledgeSource = "TESTS"
)

// ConflictResult is the enum result of a per-subject consistency check (Phase
// 14.2): NO_CONFLICT when all sources agree, CONFLICT when two or more sources
// contradict, STALE when a source is older than the freshness bound and so
// cannot be trusted to confirm or deny, and UNKNOWN when there is not enough
// information to decide.
type ConflictResult string

const (
	ConflictNone    ConflictResult = "NO_CONFLICT"
	ConflictPresent ConflictResult = "CONFLICT"
	ConflictStale   ConflictResult = "STALE"
	ConflictUnknown ConflictResult = "UNKNOWN"
)

// ConsistencyConflict represents a contradiction between two or more knowledge
// sources about the same subject. .
type ConsistencyConflict struct {
	Subject    string          `json:"subject"`              // what the conflict is about (symbol, service, etc.)
	ClaimA     string          `json:"claim_a"`              // the first claim
	SourceA    KnowledgeSource `json:"source_a"`             // source of the first claim
	ClaimB     string          `json:"claim_b"`              // the contradicting claim
	SourceB    KnowledgeSource `json:"source_b"`             // source of the second claim
	Resolution string          `json:"resolution,omitempty"` // how to resolve (empty = unresolved)
	// Explanation is the explanation of the conflict: WHY the two
	// claims were deemed contradictory, in human-readable form. Populated by
	// the stale-aware classifier so the decision is explainable.
	Explanation string `json:"explanation,omitempty"`
	// StaleSource records which source was determined to be stale ,
	// when the conflict is attributed to staleness.
	StaleSource string `json:"stale_source,omitempty"`
	// VersionA and VersionB record the version/fingerprint each side was
	// checked at, so the explanation can name which source is newer (Phase
	// 14.4).
	VersionA string `json:"version_a,omitempty"`
	VersionB string `json:"version_b,omitempty"`
	// SourceNewer names which of SourceA/SourceB was newer at check time
	// ; empty when neither is known to be newer.
	SourceNewer KnowledgeSource `json:"source_newer,omitempty"`
}

// Explain renders a human-readable explanation of the conflict :
// which two sources disagree, the two claims, which source is newer, and the
// next recommended check (re-validate the newer source, falling back to A).
// The output is deterministic given the conflict's fields.
func (c *ConsistencyConflict) Explain() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Subject %q is inconsistent: %s claims %q but %s claims %q",
		c.Subject, c.SourceA, c.ClaimA, c.SourceB, c.ClaimB)
	if c.SourceNewer != "" {
		fmt.Fprintf(&b, "; %s is the newer source", c.SourceNewer)
	}
	next := c.SourceNewer
	if next == "" {
		next = c.SourceA
	}
	fmt.Fprintf(&b, "; next recommended check: re-validate %s", next)
	return b.String()
}

// ConsistencyReport is the result of a cross-engine consistency check.
type ConsistencyReport struct {
	Conflicts            []ConsistencyConflict `json:"conflicts"`
	ConfidenceDowngrades map[string]float64    `json:"confidence_downgrades"` // subject → new confidence (downgraded)
	// Result is the overall consistency result of the whole report .
	Result ConflictResult `json:"result"`
	// StaleSubjects lists subjects whose evidence was deemed stale .
	StaleSubjects []string `json:"stale_subjects,omitempty"`
}

// Classification returns the conflict result of the whole report.
func (r *ConsistencyReport) Classification() ConflictResult {
	if len(r.Conflicts) > 0 {
		return ConflictPresent
	}
	if len(r.StaleSubjects) > 0 {
		return ConflictStale
	}
	if r.Result == ConflictUnknown {
		return ConflictUnknown
	}
	return ConflictNone
}
