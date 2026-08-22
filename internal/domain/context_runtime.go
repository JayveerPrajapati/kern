package domain

import "time"

// ContextClass categorizes a piece of context by its semantic role in the
// task. Strict Plan Phase 5 P0: 15 context classes.
type ContextClass string

const (
	ContextUserIntent  ContextClass = "USER_INTENT"
	ContextTaskState   ContextClass = "TASK_STATE"
	ContextFact        ContextClass = "FACT"
	ContextConstraint  ContextClass = "CONSTRAINT"
	ContextDecision    ContextClass = "DECISION"
	ContextPlan        ContextClass = "PLAN"
	ContextEvidence    ContextClass = "EVIDENCE"
	ContextSourceCode  ContextClass = "SOURCE_CODE"
	ContextToolResult  ContextClass = "TOOL_RESULT"
	ContextError       ContextClass = "ERROR"
	ContextTestResult  ContextClass = "TEST_RESULT"
	ContextMemory      ContextClass = "MEMORY"
	ContextHistory     ContextClass = "HISTORY"
	ContextHypothesis  ContextClass = "HYPOTHESIS"
	ContextArtifact    ContextClass = "ARTIFACT"
)

// ContextState is the retention state of a context item. Strict Plan Phase 5
// P0: 5 context states. The GC pipeline (P1) transitions items between states
// based on relevance, freshness, authority, and duplicate relationships.
type ContextState string

const (
	ContextActive   ContextState = "ACTIVE"   // currently relevant; in the model context
	ContextWarm     ContextState = "WARM"     // likely relevant; available for paging in
	ContextCold     ContextState = "COLD"     // unlikely relevant; available if needed
	ContextArchived ContextState = "ARCHIVED" // stored outside active context; compact reference kept
	ContextDropped  ContextState = "DROPPED"  // removed; no longer available
)

// ContextItem is a single piece of context, tagged with its class and state.
// The ContextPacket's typed fields (Facts, Files, Symbols, etc.) are the raw
// data; ContextItems add the class/state/relevance metadata the GC pipeline
// needs to decide what to keep, compress, or drop.
type ContextItem struct {
	ID        string        // stable identifier (e.g. "fact:abc", "file:main.go")
	Class     ContextClass  // semantic role
	State     ContextState  // retention state (ACTIVE by default)
	Content   string        // the context content (rendered text or JSON)
	Source    string        // where it came from (e.g. "graph", "memory", "tool")
	Relevance float64       // 0.0-1.0 relevance score (set by GC)
	Freshness time.Time     // when the data was observed/created
	Digest    string        // content digest for dedup
}

// GCAction is the action the context GC pipeline decides for an item.
// Strict Plan Phase 5 P1.
type GCAction string

const (
	GCKeep     GCAction = "KEEP"     // stay in ACTIVE
	GCCompress GCAction = "COMPRESS" // reduce to summary, stay ACTIVE
	GCDemote   GCAction = "DEMOTE"   // move to WARM/COLD
	GCArchive  GCAction = "ARCHIVE"  // move to ARCHIVED, keep compact reference
	GCDrop     GCAction = "DROP"     // remove entirely
)

// ContextSnapshot is a compact task snapshot for resume/replay. Strict Plan
// Phase 5 P1.
type ContextSnapshot struct {
	Goal      string   `json:"goal"`
	State     string   `json:"state"`
	Decisions []string `json:"decisions"`
	Constraints []string `json:"constraints"`
	Files     []string `json:"files"`
	Tests     []string `json:"tests"`
	Risks     []string `json:"risks"`
	NextAction string  `json:"next_action"`
}

// ToolResultSummary is the normalized form of a large raw tool output. Strict
// Plan Phase 5 P1: "Convert large raw tool outputs into facts, errors,
// evidence, summary, references, artifact. Store the raw output outside active
// model context."
type ToolResultSummary struct {
	Tool       string   `json:"tool"`
	Facts      []string `json:"facts"`
	Errors     []string `json:"errors"`
	Evidence   []string `json:"evidence"`
	Summary    string   `json:"summary"`
	References []string `json:"references"`
	ArtifactID string   `json:"artifact_id,omitempty"` // raw output stored as artifact
	TokenSaved int      `json:"token_saved"`
}
