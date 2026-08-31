package mcp

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// provenanceSchemaVersion is the version of the P1.2 provenance contract
// (evidence on the context-serving path). It is the schema shared with the
// blueprintIO spec: both repos implement the same JSON shape.
const provenanceSchemaVersion = 1

// ProvenanceMode identifies whether a response was filtered by authorization
// ("governed") or returned unfiltered ("raw").
type ProvenanceMode string

const (
	// ProvenanceModeGoverned marks a response computed under an agent's
	// authorized scope: results are filtered to the allowed set and the
	// authorizing rule is attached as proof.
	ProvenanceModeGoverned ProvenanceMode = "governed"
	// ProvenanceModeRaw marks an ungoverned response: full results with
	// index-identity-only provenance. No authorizing rule is attached.
	ProvenanceModeRaw ProvenanceMode = "raw"
)

// AuthorizingRule is the proof of the authorization decision that governed a
// response. It is absent in raw mode.
type AuthorizingRule struct {
	PolicySource string `json:"policy_source"` // "task-scope" | "permissive-default"
	Policy       string `json:"policy"`        // "deny-unlisted" | "permissive-default" | deny policy id
	Fingerprint  string `json:"fingerprint"`   // sha256 hex over the decision + allowed symbol set
	DecidedAt    string `json:"decided_at"`    // RFC3339 UTC
}

// IndexProvenance is the index-identity portion of a provenance stamp.
type IndexProvenance struct {
	TreeOID          string `json:"tree_oid"`
	ContentRoot      string `json:"content_root"`
	GitCommit        string `json:"git_commit"`
	BuiltAt          string `json:"built_at"` // RFC3339 UTC
	FreshnessVerdict string `json:"freshness_verdict"`
}

// SymbolProvenance is one symbol the response actually returned.
type SymbolProvenance struct {
	Name      string `json:"name"`
	Qualified string `json:"qualified"`
	File      string `json:"file"`
	Line      int    `json:"line"`
}

// Provenance is the structured evidence attached to a retrieval response's
// MCP result envelope. Invariant: Symbols MUST equal the set of symbols
// actually returned by the tool — in governed mode that is the filtered
// subset of the authorized scope, and the authorizing fingerprint covers
// exactly that set. Denied symbols are never listed (they would leak their
// existence); the denial stays in the auditable proof only.
type Provenance struct {
	SchemaVersion   int                `json:"schema_version"`
	Mode            ProvenanceMode     `json:"mode"`
	AuthorizingRule *AuthorizingRule   `json:"authorizing_rule,omitempty"`
	Index           IndexProvenance    `json:"index"`
	Symbols         []SymbolProvenance `json:"symbols"`
}

// indexProvenance builds the index-identity portion from a live index. It is
// the single source of truth for index evidence: the freshness verdict comes
// from the real content-addressed proof (never a hard-coded literal), and the
// commit stamp prefers the identity recorded at build time, falling back to
// the live HEAD.
func (s *Server) indexProvenance(ix *index.Index) IndexProvenance {
	p := IndexProvenance{FreshnessVerdict: string(index.FreshnessUnknown)}
	if ix == nil {
		return p
	}
	if ix.Identity != nil {
		p.TreeOID = ix.Identity.TreeOID
		p.ContentRoot = ix.Identity.ContentRoot
		p.GitCommit = ix.Identity.GitCommit
		p.BuiltAt = ix.Identity.BuiltAt.UTC().Format(time.RFC3339)
	}
	if p.GitCommit == "" {
		p.GitCommit = s.commit(ix.Root)
	}
	p.FreshnessVerdict = string(ix.FreshnessProof(ix.Root).Verdict)
	return p
}

// rawProvenance builds index-identity-only provenance for ungoverned
// responses: retrieval calls without agent_id, and non-retrieval tools that
// loaded an index.
func (s *Server) rawProvenance(ix *index.Index, symbols []SymbolProvenance) *Provenance {
	if symbols == nil {
		symbols = []SymbolProvenance{}
	}
	return &Provenance{
		SchemaVersion: provenanceSchemaVersion,
		Mode:          ProvenanceModeRaw,
		Index:         s.indexProvenance(ix),
		Symbols:       symbols,
	}
}

// governedProvenance builds provenance for a governed response from the
// authorization result. policySource is "task-scope" when the request carried
// an explicit scope, otherwise "permissive-default". On denial the rule is
// still populated from the proof so the denial is auditable; the symbol set
// is empty because nothing was returned.
func (s *Server) governedProvenance(ix *index.Index, policySource string, proof governance.AuthorizationProof, symbols []SymbolProvenance) *Provenance {
	if symbols == nil {
		symbols = []SymbolProvenance{}
	}
	policy := "permissive-default"
	if policySource == policySourceTaskScope || policySource == policySourceDefaultScoped {
		policy = "deny-unlisted"
	}
	rule := &AuthorizingRule{
		PolicySource: policySource,
		Policy:       policy,
		Fingerprint:  proof.Fingerprint,
		DecidedAt:    proof.DecidedAt.UTC().Format(time.RFC3339),
	}
	if !proof.Decision.Allowed && proof.Decision.Deny != nil {
		// Denial: the rule carries the exact policy id that denied the
		// request (e.g. "governance.authentication", "firewall.permission").
		rule.Policy = proof.Decision.Deny.Policy
	}
	return &Provenance{
		SchemaVersion:   provenanceSchemaVersion,
		Mode:            ProvenanceModeGoverned,
		AuthorizingRule: rule,
		Index:           s.indexProvenance(ix),
		Symbols:         symbols,
	}
}

// provenanceSummary renders the compact one-line index stamp appended to the
// content text. It is derived from the same index-identity source as the
// structured provenance field — the verdict and commit come from the
// structured field, never a second computation — plus index stats (symbol,
// edge and package counts, build age) that are not part of the wire schema.
// String-parsing clients can keep relying on this line; structured clients
// use the provenance field.
func (s *Server) provenanceSummary(ix *index.Index, p *Provenance) string {
	if ix == nil {
		return ""
	}
	var edges int
	for _, callees := range ix.Calls {
		edges += len(callees)
	}
	age := time.Since(ix.UpdatedAt)
	if age < 0 {
		age = 0
	}
	age = age.Round(time.Second)
	verdict := p.Index.FreshnessVerdict
	if verdict == "" {
		verdict = string(index.FreshnessUnknown)
	}
	return fmt.Sprintf("[kern] index: %d symbols, %d call edges, %d packages · built %s ago · %s · commit %s",
		len(ix.Symbols), edges, len(ix.Pkgs), age, verdict, p.Index.GitCommit)
}

// stampProvenance records the structured provenance on the per-call scope so
// toolCallResponse attaches it to the result envelope (and drives the text
// summary). Handlers call this; only the last stamp per call wins.
func (s *Server) stampProvenance(ctx context.Context, p *Provenance) {
	if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok && p != nil {
		scope.prov = p
	}
}

// symbolProvenances resolves a list of (possibly simple or qualified) names
// to provenance records. Unresolvable names (foreign/external callees) are
// kept with their name only — they are external dependencies, not a leak,
// mirroring the authz edge filter's keep-unresolved-callees rule. The output
// is deduplicated by qualified name and sorted for determinism.
func symbolProvenances(ix *index.Index, names []string) []SymbolProvenance {
	seen := map[string]bool{}
	var out []SymbolProvenance
	for _, n := range names {
		if n == "" {
			continue
		}
		p := SymbolProvenance{Name: simpleName(n), Qualified: n}
		if d, ok := ix.ResolveName(n); ok {
			p.Name = d.Name
			p.Qualified = d.FullName()
			p.File = d.File
			p.Line = d.Line
		}
		if seen[p.Qualified] {
			continue
		}
		seen[p.Qualified] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Qualified < out[j].Qualified })
	return out
}
