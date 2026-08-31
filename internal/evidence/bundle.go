// Package evidence builds the exportable, certifiable evidence artifact
// (P1.1): a self-contained, tamper-evident bundle that an enterprise
// security team can take to a SOC 2 / ISO 42001 / EU AI Act review.
//
// The bundle has three pillars — an authorization proof (what an agent was
// permitted to read), a freshness proof (that the index the proof was
// derived from matches the working tree), and a lineage section (which
// symbols/edges the authorized scope covered) — plus a snapshot of the
// persisted audit chain (the authorization log) and a SHA-256 hash of the
// whole bundle for tamper evidence.
//
// Tamper-evidence is SHA-256 hash-chaining, NOT cryptographic signing.
// Key-based signing (x509/Ed25519) is a documented future item.
package evidence

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// SchemaVersion is the version of the evidence bundle schema documented in
// docs/evidence-bundle-schema.md. Bump it (and the doc) on any breaking
// change to the wire format; Verify rejects bundles with a different version.
const SchemaVersion = 1

// Bundle is the exportable evidence artifact: the three pillars plus a
// snapshot of the audit chain, sealed by a SHA-256 content hash.
type Bundle struct {
	SchemaVersion int       `json:"schema_version"` // 1
	BundleID      string    `json:"bundle_id"`      // UUID
	GeneratedAt   time.Time `json:"generated_at"`
	RepoRoot      string    `json:"repo_root"`
	TaskID        string    `json:"task_id,omitempty"`
	AgentID       string    `json:"agent_id,omitempty"`

	// The three pillars:
	Authorization *AuthorizationSection `json:"authorization,omitempty"`
	Freshness     *FreshnessSection     `json:"freshness"`
	Lineage       *LineageSection       `json:"lineage,omitempty"`

	// Audit chain snapshot (the authorization log):
	AuditTrail     []AuditEntrySnapshot `json:"audit_trail"`
	AuditChainHash string               `json:"audit_chain_hash"` // last hash in the chain

	// Tamper-evidence:
	BundleHash string `json:"bundle_hash"` // sha256 of everything above
}

// AuthorizationSection captures an authorization decision and its auditable
// proof. Reconstructed is true when the proof was re-derived at export time
// (there is no persisted per-call proof to fetch — always the case today).
type AuthorizationSection struct {
	Proof         governance.AuthorizationProof `json:"proof"`
	Scope         governance.AuthorizedScope    `json:"scope"`
	Reconstructed bool                          `json:"reconstructed"`
}

// FreshnessSection captures the freshness proof of the index the bundle was
// generated from, so a reviewer can see why the index was judged fresh or
// stale instead of trusting a literal.
type FreshnessSection struct {
	Proof        index.FreshnessProof `json:"proof"`
	IndexVersion string               `json:"index_version"`
}

// LineageSection records which symbols and call edges the authorized scope
// covered at export time. It reflects the authorized scope, not a historical
// retrieval log (which does not exist yet) — documented in the schema.
type LineageSection struct {
	Symbols []SymbolAccess `json:"symbols"`
	Edges   []EdgeAccess   `json:"edges,omitempty"`
	Task    string         `json:"task"`
}

// SymbolAccess is the lineage record of one symbol the agent was authorized
// to read.
type SymbolAccess struct {
	Name      string `json:"name"`
	Qualified string `json:"qualified"`
	File      string `json:"file"`
	Line      int    `json:"line"`
}

// EdgeAccess is the lineage record of one call edge within the authorized
// scope.
type EdgeAccess struct {
	Caller string `json:"caller"`
	Callee string `json:"callee"`
}

// AuditEntrySnapshot is a flat snapshot of one audit-chain entry, carrying
// everything a reviewer needs to verify the chain without the full AuditEntry
// struct. ValidationOutcome is included so the bundle's own hash covers what
// the audit chain's computeAuditHash does not (a documented gap).
type AuditEntrySnapshot struct {
	ID                string                        `json:"id"`
	Timestamp         time.Time                     `json:"timestamp"`
	AgentID           string                        `json:"agent_id"`
	Action            string                        `json:"action"`
	Resource          string                        `json:"resource"`
	Result            string                        `json:"result"`
	Hash              string                        `json:"hash"`
	TaskID            string                        `json:"task_id,omitempty"`
	ValidationOutcome *governance.ValidationOutcome `json:"validation_outcome,omitempty"`
}

// defaultAgentPermissions mirrors the CLI's built-in agent (cmd_context.go:
// registerDefaultAgentCLI): the default identity may read context.
var defaultAgentPermissions = []governance.Permission{{Resource: "context", Action: "read"}}

// Generate assembles an evidence bundle for root. The authorization pillar is
// re-derived at export time via governance.AuthorizeContext (there is no persisted
// per-call proof), so it always carries Reconstructed=true. A denied
// authorization is still captured in the bundle — a denial is evidence too.
// The freshness pillar comes from the index's own freshness proof; the
// lineage comes from the authorized scope; the audit trail is snapshotted
// from <root>/.kern/audit via Replay().
func Generate(root, agentID, task string, ix *index.Index) (*Bundle, error) {
	if ix == nil {
		return nil, errors.New("evidence: index is nil")
	}
	if root == "" {
		root = "."
	}
	repoRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("evidence: resolve root: %w", err)
	}

	b := &Bundle{
		SchemaVersion: SchemaVersion,
		BundleID:      newBundleID(),
		GeneratedAt:   time.Now().UTC(),
		RepoRoot:      repoRoot,
		TaskID:        task,
		AgentID:       agentID,
		AuditTrail:    []AuditEntrySnapshot{}, // stable wire form: never null
	}

	// Pillar 1: freshness proof of the index the bundle is generated from.
	fp := ix.FreshnessProof(repoRoot)
	b.Freshness = &FreshnessSection{
		Proof:        fp,
		IndexVersion: ix.UpdatedAt.UTC().Format(time.RFC3339),
	}

	// Pillar 2: authorization, re-derived at export time. Register the
	// built-in default agent (idempotent) so `--agent-id default` authorizes
	// like every other CLI path.
	if agentID == "default" {
		if _, err := governance.GetAgent("default"); err != nil {
			_ = governance.RegisterAgent(governance.NewAgent("default", "Default Agent", "default", defaultAgentPermissions))
		}
	}
	fw := governance.NewFirewall()
	if agent, aerr := governance.GetAgent(agentID); aerr == nil {
		fw = fw.WithAgents(agent)
	}
	resp, aerr := governance.AuthorizeContext(governance.Request{
		Task:    task,
		AgentID: agentID,
		Root:    repoRoot,
	}, ix, fw)
	if aerr != nil && aerr != governance.ErrUnauthorized {
		return nil, fmt.Errorf("evidence: authorize context: %w", aerr)
	}
	b.Authorization = &AuthorizationSection{
		Proof:         resp.Proof,
		Scope:         resp.Scope,
		Reconstructed: true,
	}

	// Pillar 3: lineage from the authorized scope (symbols + call edges).
	b.Lineage = lineageFromScope(resp.Scope, task)

	// Audit chain snapshot: replay the persisted trail and snapshot every
	// entry, ValidationOutcome included.
	trail, err := snapshotAuditTrail(repoRoot)
	if err != nil {
		return nil, err
	}
	b.AuditTrail = trail
	if len(trail) > 0 {
		b.AuditChainHash = trail[len(trail)-1].Hash
	}

	// Seal the bundle: SHA-256 over canonical JSON of everything above.
	b.BundleHash = computeBundleHash(b)
	return b, nil
}

// lineageFromScope maps an authorized scope to the bundle's lineage records.
func lineageFromScope(scope governance.AuthorizedScope, task string) *LineageSection {
	ls := &LineageSection{Task: task, Symbols: []SymbolAccess{}}
	for _, s := range scope.Symbols {
		ls.Symbols = append(ls.Symbols, SymbolAccess{
			Name:      s.Name,
			Qualified: s.Qualified,
			File:      s.File,
			Line:      s.Line,
		})
	}
	for _, e := range scope.Edges {
		ls.Edges = append(ls.Edges, EdgeAccess{Caller: e.From, Callee: e.To})
	}
	return ls
}

// snapshotAuditTrail replays the persisted audit chain at <root>/.kern/audit
// and returns a flat snapshot of every entry in insertion order. An empty or
// absent trail is not an error (fresh repo with no governance events yet).
func snapshotAuditTrail(root string) ([]AuditEntrySnapshot, error) {
	store := storage.NewLocal(filepath.Join(root, ".kern", "audit"))
	log := governance.NewAuditLog().WithStore(store)
	if _, err := log.Replay(); err != nil {
		return nil, fmt.Errorf("evidence: replay audit chain: %w", err)
	}
	entries := log.All()
	snaps := make([]AuditEntrySnapshot, 0, len(entries))
	for _, e := range entries {
		snap := AuditEntrySnapshot{
			ID:        e.ID,
			Timestamp: e.Timestamp,
			AgentID:   e.AgentID,
			Action:    e.Action,
			Resource:  e.Resource,
			Result:    e.Result,
			Hash:      e.Hash,
			TaskID:    e.TaskID,
		}
		if e.ValidationOutcome != nil {
			vo := *e.ValidationOutcome // deep copy: the bundle owns its snapshot
			snap.ValidationOutcome = &vo
		}
		snaps = append(snaps, snap)
	}
	return snaps, nil
}

// Verify validates the bundle's tamper-evidence seal: it checks the schema
// version and recomputes the SHA-256 bundle hash over the canonical JSON
// (with BundleHash cleared), returning an error on any mismatch. It does NOT
// re-verify the audit chain against the repo — callers that have access to
// the repo should additionally run AuditLog.VerifyChain().
func (b *Bundle) Verify() error {
	if b == nil {
		return errors.New("evidence: nil bundle")
	}
	if b.SchemaVersion != SchemaVersion {
		return fmt.Errorf("evidence: schema version %d, want %d", b.SchemaVersion, SchemaVersion)
	}
	if b.BundleHash == "" {
		return errors.New("evidence: bundle_hash is empty")
	}
	if got := computeBundleHash(b); got != b.BundleHash {
		return errors.New("evidence: bundle hash mismatch — content tampered or bundle_hash altered")
	}
	return nil
}

// Parse decodes a bundle from its JSON wire form.
func Parse(data []byte) (*Bundle, error) {
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("evidence: parse bundle: %w", err)
	}
	return &b, nil
}

// computeBundleHash is the bundle's tamper-evidence seal: SHA-256 over the
// canonical JSON of the bundle with BundleHash cleared. Marshal output is
// deterministic (the bundle contains no maps, so no map-iteration ordering),
// which makes the hash reproducible across runs and verifiers.
func computeBundleHash(b *Bundle) string {
	if b == nil {
		return ""
	}
	cpy := *b
	cpy.BundleHash = ""
	data, err := json.Marshal(&cpy)
	if err != nil {
		// Cannot happen for this type set (no unsupported values); fail the
		// seal closed on the off chance it does.
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// newBundleID returns a random RFC-4122 v4 UUID string. Generated locally
// from crypto/rand — no external dependency.
func newBundleID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is unrecoverable; fall back to a time-seeded ID
		// rather than returning a duplicate/empty bundle id.
		return fmt.Sprintf("bundle-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
