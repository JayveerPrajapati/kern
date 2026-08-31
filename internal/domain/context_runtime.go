package domain

import "time"

// ContextClass categorizes a piece of context by its semantic role in the
// task. : 15 context classes.
type ContextClass string

const (
	ContextUserIntent ContextClass = "USER_INTENT"
	ContextTaskState  ContextClass = "TASK_STATE"
	ContextFact       ContextClass = "FACT"
	ContextConstraint ContextClass = "CONSTRAINT"
	ContextDecision   ContextClass = "DECISION"
	ContextPlan       ContextClass = "PLAN"
	ContextEvidence   ContextClass = "EVIDENCE"
	ContextSourceCode ContextClass = "SOURCE_CODE"
	ContextToolResult ContextClass = "TOOL_RESULT"
	ContextError      ContextClass = "ERROR"
	ContextTestResult ContextClass = "TEST_RESULT"
	ContextMemory     ContextClass = "MEMORY"
	ContextHistory    ContextClass = "HISTORY"
	ContextHypothesis ContextClass = "HYPOTHESIS"
	ContextArtifact   ContextClass = "ARTIFACT"
)

// ContextState is the retention state of a context item.
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
	ID        string       // stable identifier (e.g. "fact:abc", "file:main.go")
	Class     ContextClass // semantic role
	State     ContextState // retention state (ACTIVE by default)
	Content   string       // the context content (rendered text or JSON)
	Source    string       // where it came from (e.g. "graph", "memory", "tool")
	Relevance float64      // 0.0-1.0 relevance score (set by GC)
	Freshness time.Time    // when the data was observed/created
	Digest    string       // content digest for dedup
	// LastUsed is the last time the item was referenced (P5.5 GC completeness).
	LastUsed time.Time
	// Authorized is the governance decision for this item (P5.4 per-item
	// authorization). Empty when not gated.
	Authorized bool
	// DenyReason records why an unauthorized item was excluded (P5.4).
	DenyReason string
	// Repository is the repository this context item is scoped to (P5.4 scoped
	// authorization dimension). Empty = not scoped to a specific repository.
	Repository string `json:"repository,omitempty"`
	// TaskID is the task this context item belongs to (P5.4 scoped dimension).
	// Empty = not scoped to a specific task.
	TaskID string `json:"task_id,omitempty"`
	// Tenant is the tenant/team this context item is scoped to (P5.4 scoped
	// dimension). Empty = not scoped to a specific tenant.
	Tenant string `json:"tenant,omitempty"`
	// SecurityClass is the security classification of this context item (P5.4
	// scoped dimension). Empty = not classified.
	SecurityClass string `json:"security_class,omitempty"`
	// EvidenceRefs holds the item IDs that are evidence for this canonical
	// fact (P5.8 canonical-fact model). It is populated by CanonicalizeItems
	// when several items share the same content digest.
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

// ContextAuthorization carries the scoped identity used to authorize a context
// item across the five dimensions. Agent is the primary identity;
// repository/task/tenant/classification refine the scope within which that
// agent may access an item. An empty scope field means that dimension is
// unrestricted (fail-open on scope, fail-closed on entitlement via the
// firewall).
type ContextAuthorization struct {
	// Agent is the primary identity the firewall check runs as.
	Agent string
	// Repository restricts items to a single repository when set.
	Repository string
	// TaskID restricts items to a single task when set.
	TaskID string
	// Tenant restricts items to a single tenant when set.
	Tenant string
	// AllowedTenants is the allow-list of tenants the agent may access. An item
	// with a tenant not in this list is denied when the list is non-empty.
	AllowedTenants []string
	// AllowedSecurityClasses is the allow-list of security classifications the
	// agent may access. An item with a SecurityClass not in this list is denied
	// when the list is non-empty.
	AllowedSecurityClasses []string
}

// GCAction is the action the context GC pipeline decides for an item.
type GCAction string

const (
	GCKeep     GCAction = "KEEP"     // stay in ACTIVE
	GCCompress GCAction = "COMPRESS" // reduce to summary, stay ACTIVE
	GCDemote   GCAction = "DEMOTE"   // move to WARM/COLD
	GCArchive  GCAction = "ARCHIVE"  // move to ARCHIVED, keep compact reference
	GCDrop     GCAction = "DROP"     // remove entirely
)

// ContextSnapshot is a compact task snapshot for resume/replay.
type ContextSnapshot struct {
	Goal        string   `json:"goal"`
	State       string   `json:"state"`
	Decisions   []string `json:"decisions"`
	Constraints []string `json:"constraints"`
	Files       []string `json:"files"`
	Tests       []string `json:"tests"`
	Risks       []string `json:"risks"`
	NextAction  string   `json:"next_action"`
}

// ToolResultSummary is the normalized form of a large raw tool output. Strict
// Plan "Convert large raw tool outputs into facts, errors,
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

// ContextPage is one page of context items (P5.10 paging). It carries the page
// slice plus paging metadata so a consumer can page through warm/cold items
// without scanning the whole store.
type ContextPage struct {
	Items      []ContextItem `json:"items"`
	Page       int           `json:"page"`
	PageSize   int           `json:"page_size"`
	Total      int           `json:"total"`
	TotalPages int           `json:"total_pages"`
	HasNext    bool          `json:"has_next"`
}

// ContextLease is a time-bounded lease on an ACTIVE context item (P5.11
// leases). It lets the runtime reserve an item in the active window for a
// bounded duration, preventing premature eviction by the GC pipeline while the
// item is being used, and forcing renewal so stale references are released.
type ContextLease struct {
	ItemID    string    `json:"item_id"`
	Holder    string    `json:"holder"` // agent/task id holding the lease
	ExpiresAt time.Time `json:"expires_at"`
}

// FreshnessPolicy configures the freshness gating of context items (P5.9). An
// item is STALE when its age exceeds MaxAge; it is FRESH when its age is below
// FreshBelow. Items older than MaxAge are excluded from the active set unless
// ForceFresh (e.g. a specific task override) is set.
type FreshnessPolicy struct {
	// MaxAge is the maximum age an item may be to remain usable. Zero = no
	// freshness bound (keep everything regardless of age).
	MaxAge time.Duration
	// FreshBelow is the age threshold under which an item is unambiguously
	// fresh. Items between FreshBelow and MaxAge are considered "aging" and
	// may be demoted by the GC. Zero = no aging band.
	FreshBelow time.Duration
}

// Freshness is the classification of an item's age against a FreshnessPolicy.
type Freshness string

const (
	FreshnessFresh Freshness = "FRESH"
	FreshnessAging Freshness = "AGING"
	FreshnessStale Freshness = "STALE"
)

// ClassifyAge returns the freshness classification for an item observed at
// observed with respect to now under the policy.
func (p FreshnessPolicy) ClassifyAge(observed, now time.Time) Freshness {
	if p.MaxAge <= 0 {
		return FreshnessFresh // unbounded
	}
	age := now.Sub(observed)
	if age >= p.MaxAge {
		return FreshnessStale
	}
	if p.FreshBelow > 0 && age >= p.FreshBelow {
		return FreshnessAging
	}
	return FreshnessFresh
}

// ContextReplay is a record of a task snapshot for the replay engine (P5.12).
// It captures the snapshot plus the input that produced it so a later session
// can reconstruct the task context deterministically.
type ContextReplay struct {
	Snapshot ContextSnapshot `json:"snapshot"`
	Input    string          `json:"input"`    // the original task/intent text
	Source   string          `json:"source"`   // e.g. "mcp", "loop", "cli"
	Occurred time.Time       `json:"occurred"` // when the snapshot was captured
}
