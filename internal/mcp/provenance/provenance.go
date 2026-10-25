// Package provenance owns the P1.2 provenance contract for retrieval
// responses: the structured evidence attached to a tool result envelope
// (index identity, authorizing rule, returned-symbol set) and the compact
// text summary derived from it.
//
// It is a Server-independent leaf: the only dynamic input beyond the index
// itself is a commit-resolution func (the caller injects its cached
// git-rev-parse helper). internal/mcp keeps thin *Server wrapper methods
// over these functions so handler call sites stay unchanged while the
// handler families extract.
package provenance

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// SchemaVersion is the version of the P1.2 provenance contract (evidence on
// the context-serving path). It is the schema shared with the blueprintIO
// spec: both repos implement the same JSON shape.
const SchemaVersion = 1

// Policy source labels for governed responses. Canonical home: govern.go
// aliases these so the governance layer and the provenance stamps can never
// drift apart.
const (
	PolicySourceTaskScope     = "task-scope"
	PolicySourcePermissive    = "permissive-default"
	PolicySourceDefaultScoped = "default-scoped"
)

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

// maxResponseSymbols caps the structured Symbols listing. A broad search
// returns hundreds of symbols; one entry is ~106 bytes of JSON, so an
// uncapped listing dwarfs the content text it attests (measured: 615
// entries = 65KB structured on a 16.7KB text, deep-verify L1 F1-2). The
// content text always carries the complete per-symbol file:line listing —
// the structured set is machine-readable redundancy, capped at roughly
// the text budget.
const maxResponseSymbols = 128

// SymbolProvenance is one symbol the response actually returned.
type SymbolProvenance struct {
	Name      string `json:"name"`
	Qualified string `json:"qualified"`
	File      string `json:"file"`
	Line      int    `json:"line"`
}

// Provenance is the structured evidence attached to a retrieval response's
// MCP result envelope. Invariant: Symbols equals the set of symbols actually
// returned by the tool, up to maxResponseSymbols — in governed mode that is
// the filtered subset of the authorized scope, and the authorizing
// fingerprint (computed from the authorization decision, not this list)
// covers exactly the authorized set. Denied symbols are never listed (they
// would leak their existence); the denial stays in the auditable proof
// only. When the returned set exceeds maxResponseSymbols, Symbols carries
// the first maxResponseSymbols entries and SymbolsTruncated is set; the
// content text remains the complete listing. Independently of the count cap,
// the MCP server trims the listing to the call's output budget — the same
// cap that bounds the content text.
type Provenance struct {
	SchemaVersion    int                `json:"schema_version"`
	Mode             ProvenanceMode     `json:"mode"`
	AuthorizingRule  *AuthorizingRule   `json:"authorizing_rule,omitempty"`
	Index            IndexProvenance    `json:"index"`
	Symbols          []SymbolProvenance `json:"symbols"`
	SymbolsTruncated bool               `json:"symbols_truncated,omitempty"`
}

// IndexIdentity builds the index-identity portion of a stamp. commit resolves
// a root to a short git hash; it is only consulted when the index carries no
// recorded identity (callers typically inject a cached rev-parse helper).
func IndexIdentity(ix *index.Index, commit func(string) string) IndexProvenance {
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
		p.GitCommit = commit(ix.Root)
	}
	p.FreshnessVerdict = string(ix.FreshnessProof(ix.Root).Verdict)
	return p
}

// capSymbols bounds the structured listing to maxResponseSymbols. A returned
// set larger than the cap keeps the first entries (highest-ranked for
// search-style results) and reports truncation; the content text remains the
// complete listing.
func capSymbols(symbols []SymbolProvenance) ([]SymbolProvenance, bool) {
	if len(symbols) <= maxResponseSymbols {
		return symbols, false
	}
	return symbols[:maxResponseSymbols], true
}

// Raw builds index-identity-only provenance for ungoverned responses:
// retrieval calls without agent_id, and non-retrieval tools that loaded an
// index.
func Raw(ix *index.Index, commit func(string) string, symbols []SymbolProvenance) *Provenance {
	if symbols == nil {
		symbols = []SymbolProvenance{}
	}
	symbols, truncated := capSymbols(symbols)
	return &Provenance{
		SchemaVersion:    SchemaVersion,
		Mode:             ProvenanceModeRaw,
		Index:            IndexIdentity(ix, commit),
		Symbols:          symbols,
		SymbolsTruncated: truncated,
	}
}

// Governed builds provenance for a governed response from the authorization
// result. policySource is PolicySourceTaskScope when the request carried an
// explicit scope, otherwise PolicySourceDefaultScoped. On denial the rule is
// still populated from the proof so the denial is auditable; the symbol set
// is empty because nothing was returned.
func Governed(ix *index.Index, commit func(string) string, policySource string, proof governance.AuthorizationProof, symbols []SymbolProvenance) *Provenance {
	if symbols == nil {
		symbols = []SymbolProvenance{}
	}
	symbols, truncated := capSymbols(symbols)
	policy := "permissive-default"
	if policySource == PolicySourceTaskScope || policySource == PolicySourceDefaultScoped {
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
		SchemaVersion:    SchemaVersion,
		Mode:             ProvenanceModeGoverned,
		AuthorizingRule:  rule,
		Index:            IndexIdentity(ix, commit),
		Symbols:          symbols,
		SymbolsTruncated: truncated,
	}
}

// Summary renders the compact one-line index stamp appended to the content
// text: the verdict/commit from the structured provenance field plus index
// stats. It prints ONLY when the index state is unusual (QA 2026-10-19): a
// fully-fresh git worktree whose recorded commit equals HEAD renders nothing;
// STALE/unknown verdicts, HEAD skew, and non-git roots print. Self-heal: a
// fresh verdict with a different HEAD is a content-equal commit bump — the
// recorded label is persisted to the new HEAD (index.RefreshCommitLabel) so
// the skew warning stops after this call.
func Summary(ix *index.Index, p *Provenance) string {
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
	// The verdict is tree-based; surface HEAD whenever it differs from the
	// recorded commit so commit skew is never hidden behind "fresh".
	head := headCommitCached(ix.Root)
	if head != "" && head == p.Index.GitCommit && verdict == string(index.FreshnessFresh) {
		return "" // fully fresh and in sync: nothing unusual to print
	}
	suffix := ""
	if head != "" && head != p.Index.GitCommit {
		if verdict == string(index.FreshnessFresh) {
			// Content-equal commit bump: persist the new HEAD as the recorded
			// label (provenance, never a rebuild) so the warning self-heals.
			refreshed := ""
			if changed, err := index.RefreshCommitLabel(ix.Root, ix); err == nil && changed {
				refreshed = "; label refreshed"
			}
			suffix = fmt.Sprintf(" · HEAD %s (commit differs; indexed content still matches%s)", head, refreshed)
		} else {
			suffix = fmt.Sprintf(" · HEAD %s (index behind HEAD)", head)
		}
	}
	return fmt.Sprintf("[kern] index: %d symbols, %d call edges, %d packages · built %s ago · %s · commit %s%s",
		len(ix.Symbols), edges, len(ix.Pkgs), age, verdict, p.Index.GitCommit, suffix)
}

// IndexStamp renders the compact one-line index stamp for surfaces that
// have an index but no Provenance envelope (the CLI query commands). It is
// byte-identical to the MCP provenance summary — same age/verdict/commit/
// HEAD-skew fields — so the CLI and MCP footers stay consistent.
func IndexStamp(ix *index.Index) string {
	if ix == nil {
		return ""
	}
	return Summary(ix, Raw(ix, headCommitCached, nil))
}

// headCommitCache memoizes the current HEAD short sha per root for a few
// seconds: Summary runs on every tool response and a per-call git exec would
// be measurable. A failed lookup (not a git repo, no commits) returns "" and
// is cached too — absence must not turn every response into a subprocess.
var (
	headCommitMu    sync.Mutex
	headCommitEntry = map[string]headCommitState{}
)

type headCommitState struct {
	sha string
	at  time.Time
}

const headCommitTTL = 5 * time.Second

func headCommitCached(root string) string {
	if root == "" {
		return ""
	}
	headCommitMu.Lock()
	st, ok := headCommitEntry[root]
	if ok && time.Since(st.at) < headCommitTTL {
		headCommitMu.Unlock()
		return st.sha
	}
	headCommitMu.Unlock()
	sha := headCommitUncached(root)
	headCommitMu.Lock()
	headCommitEntry[root] = headCommitState{sha: sha, at: time.Now()}
	headCommitMu.Unlock()
	return sha
}

func headCommitUncached(root string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--verify", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// SimpleName strips the package qualifier from a name, mirroring intel's
// display convention ("pkg.Func" → "Func").
func SimpleName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// SymbolProvenances resolves a list of (possibly simple or qualified) names
// to provenance records. Unresolvable names (foreign/external callees) are
// kept with their name only — mirroring the authz edge filter's
// keep-unresolved-callees rule. The output is deduplicated by qualified name
// and sorted for determinism.
func SymbolProvenances(ix *index.Index, names []string) []SymbolProvenance {
	seen := map[string]bool{}
	var out []SymbolProvenance
	for _, n := range names {
		if n == "" {
			continue
		}
		p := SymbolProvenance{Name: SimpleName(n), Qualified: n}
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
