package mcp

import (
	"context"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
)

// The provenance contract (types, construction, summaries) lives in
// internal/mcp/provenance — a Server-independent leaf. This file keeps the
// *Server method surface the handlers call: thin wrappers over the leaf
// functions plus the two pieces that genuinely need kernel state — commit
// resolution (the cached rev-parse helper) and the per-call scope stamp.
//
// The aliases keep every existing reference (handler call sites, tests,
// other packages) compiling unchanged while handler families extract.

type (
	ProvenanceMode   = provenance.ProvenanceMode
	AuthorizingRule  = provenance.AuthorizingRule
	IndexProvenance  = provenance.IndexProvenance
	SymbolProvenance = provenance.SymbolProvenance
	Provenance       = provenance.Provenance
)

const (
	ProvenanceModeGoverned = provenance.ProvenanceModeGoverned
	ProvenanceModeRaw      = provenance.ProvenanceModeRaw
)

// rawProvenance builds index-identity-only provenance for ungoverned
// responses: retrieval calls without agent_id, and non-retrieval tools that
// loaded an index.
func (s *Server) rawProvenance(ix *index.Index, symbols []SymbolProvenance) *Provenance {
	return provenance.Raw(ix, s.commit, symbols)
}

// governedProvenance builds provenance for a governed response from the
// authorization result. policySource is "task-scope" when the request carried
// an explicit scope, otherwise "permissive-default". On denial the rule is
// still populated from the proof so the denial is auditable; the symbol set
// is empty because nothing was returned.
func (s *Server) governedProvenance(ix *index.Index, policySource string, proof governance.AuthorizationProof, symbols []SymbolProvenance) *Provenance {
	return provenance.Governed(ix, s.commit, policySource, proof, symbols)
}

// provenanceSummary renders the compact one-line index stamp appended to the
// content text.
func (s *Server) provenanceSummary(ix *index.Index, p *Provenance) string {
	return provenance.Summary(ix, p)
}

// stampProvenance records the structured provenance on the per-call scope so
// toolCallResponse attaches it to the result envelope (and drives the text
// summary). Handlers call this; only the last stamp per call wins.
func (s *Server) stampProvenance(ctx context.Context, p *Provenance) {
	if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok && p != nil {
		scope.prov = p
	}
}
