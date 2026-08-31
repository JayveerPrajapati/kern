// Package authz implements the authorized-context primitive (P0.1): given an
// agent identity, a task scope and the project index, it computes the exact
// set of symbols and call edges the agent is permitted to read, together with
// an auditable authorization proof. It is the product spine every governed
// retrieval tool hangs off.
package governance

import (
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Request is the input to an authorization decision. The agent may be resolved
// by ID (via the identity registry) or supplied directly.
type Request struct {
	Task          string            `json:"task"`
	AgentID       string            `json:"agent_id"`
	AgentIdentity *AgentIdentity    `json:"-"`
	Scope         *domain.TaskScope `json:"scope,omitempty"`
	Root          string            `json:"root"`
	SymbolFilter  string            `json:"symbol_filter,omitempty"`
}

// SymbolRef is a symbol as it appears in an authorized scope.
type SymbolRef struct {
	Name      string `json:"name"`
	Qualified string `json:"qualified,omitempty"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Pkg       string `json:"pkg,omitempty"`
}

// EdgeRef is one call edge in an authorized scope. Edges to callees that are
// unresolved in the index are kept: they are external dependencies, not a
// leak.
type EdgeRef struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// DeniedSymbol records a symbol the scope excluded, with the stage that
// denied it ("path") and a human-readable reason.
type DeniedSymbol struct {
	Symbol SymbolRef `json:"symbol"`
	Stage  string    `json:"stage"`
	Reason string    `json:"reason"`
}

// AuthorizedScope is the permitted context: the symbols and edges the agent
// may read, plus everything that was denied and why.
type AuthorizedScope struct {
	Symbols      []SymbolRef    `json:"symbols"`
	Edges        []EdgeRef      `json:"edges"`
	Denied       []DeniedSymbol `json:"denied,omitempty"`
	PolicySource string         `json:"policy_source"`
}

// AgentSummary is the identity portion of the proof.
type AgentSummary struct {
	ID          string `json:"id"`
	Permissions int    `json:"permission_count"`
}

// AuthorizationProof is the auditable evidence of a decision: what was
// decided, for whom, under which scope, and a cryptographic fingerprint that
// pins the result to the exact index + policy inputs that produced it.
type AuthorizationProof struct {
	Decision       domain.GatewayResult `json:"decision"`
	Agent          AgentSummary         `json:"agent"`
	TaskScope      domain.TaskScope     `json:"task_scope"`
	Fingerprint    string               `json:"fingerprint"`
	IndexFreshness string               `json:"index_freshness"`
	IndexVersion   string               `json:"index_version"`
	DecidedAt      time.Time            `json:"decided_at"`
}

// Response is the complete result of an authorization decision: the permitted
// scope plus the proof. On denial the scope is empty (zero symbols) and the
// proof carries the deny reason.
type Response struct {
	Scope AuthorizedScope    `json:"scope"`
	Proof AuthorizationProof `json:"proof"`
}
