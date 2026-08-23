// Phase 13 — Correlation contract + change fingerprint.
//
// These are additive, deterministic primitives over the runtime correlation
// layer. They give every correlation a typed FACTUAL/INFERRED/UNKNOWN contract
// (13.2) and every change a stable, comparable fingerprint (13.4), and they
// link trace/event evidence into the correlation chain (13.1).
package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// CorrelationLinkConfidence pairs a correlation link stage with its confidence
// classification (13.2). A chain link is FACTUAL when it is backed by direct
// runtime evidence, INFERRED when derived from other signals, and UNKNOWN when
// it cannot be verified.
type CorrelationLinkConfidence struct {
	Stage      string                          // "service","deployment","commit","symbol","task","pr","agent","trace","event"
	ID         string                          // the linked id
	Confidence domain.CorrelationConfidence    `json:"confidence"`
}

// CorrelationContract is the typed confidence contract for a full correlation
// (13.2): the overall confidence plus per-link classification. The contract is
// deterministic: links backed by the correlation's own evidence are FACTUAL,
// links present but not directly evidenced are INFERRED, and a correlation with
// no evidence at all is UNKNOWN overall.
type CorrelationContract struct {
	Overall    domain.CorrelationConfidence  `json:"overall"`
	Links      []CorrelationLinkConfidence   `json:"links"`
	EvidenceCount int                       `json:"evidence_count"`
	// Phase 13.2 contract metadata: which runtime source produced it, the
	// relationship classification (alias of Overall, explicit per spec), when it
	// was computed, and how it was produced.
	Source      string                      `json:"source,omitempty"`        // e.g. "runtime"
	Relationship domain.CorrelationConfidence `json:"relationship"`          // FACTUAL/INFERRED/UNKNOWN
	Timestamp   time.Time                  `json:"timestamp,omitempty"`      // when computed (UTC)
	Provenance  string                     `json:"provenance,omitempty"`     // e.g. "runtime:correlate"
}

// Contract derives the confidence contract for a correlation. The overall
// confidence is UNKNOWN when there is no runtime evidence; otherwise it is the
// aggregate of the per-link classes (FACTUAL if any link is evidenced,
// otherwise INFERRED).
func (c *Correlation) Contract() CorrelationContract {
	contract := CorrelationContract{EvidenceCount: len(c.ErrorEvents) + len(c.LogEvents) + len(c.TraceSpans) + len(c.MetricEvents)}
	if contract.EvidenceCount == 0 {
		contract.Overall = domain.CorrelationUnknown
		contract.Links = c.confidenceLinks()
		populateContractMeta(&contract)
		return contract
	}
	contract.Links = c.confidenceLinks()
	overall := domain.CorrelationInferred
	for _, l := range contract.Links {
		if l.Confidence == domain.CorrelationFactual {
			overall = domain.CorrelationFactual
			break
		}
	}
	contract.Overall = overall
	populateContractMeta(&contract)
	return contract
}

// populateContractMeta fills the Phase 13.2 contract metadata: source, explicit
// relationship classification, compute timestamp and provenance. The Correlation
// value does not carry its own source name, so a stable "runtime" constant is
// used (the same adapter family all correlations are produced from).
func populateContractMeta(contract *CorrelationContract) {
	contract.Source = "runtime"
	contract.Relationship = contract.Overall
	contract.Timestamp = time.Now().UTC()
	contract.Provenance = "runtime:correlate"
}

// confidenceLinks classifies each deployment/commit link of the correlation.
func (c *Correlation) confidenceLinks() []CorrelationLinkConfidence {
	var out []CorrelationLinkConfidence
	if c.AffectedService != "" {
		out = append(out, CorrelationLinkConfidence{Stage: "service", ID: c.AffectedService, Confidence: correlationServiceConfidence(c)})
	}
	for _, d := range c.Deployments {
		id := d.Version
		if id == "" {
			id = d.CommitSHA
		}
		out = append(out, CorrelationLinkConfidence{Stage: "deployment", ID: id, Confidence: correlationDeployConfidence(c)})
	}
	for _, rc := range c.RecentCommits {
		sha := rc.SHA
		if len(sha) > 8 {
			sha = sha[:8]
		}
		out = append(out, CorrelationLinkConfidence{Stage: "commit", ID: sha, Confidence: correlationCommitConfidence(c)})
	}
	return out
}

// correlationServiceConfidence: the service is FACTUAL when error events were
// observed on it, else INFERRED (resolved by alert declaration), else UNKNOWN.
func correlationServiceConfidence(c *Correlation) domain.CorrelationConfidence {
	if len(c.ErrorEvents) > 0 {
		return domain.CorrelationFactual
	}
	if c.AffectedService != "" {
		return domain.CorrelationInferred
	}
	return domain.CorrelationUnknown
}

// correlationDeployConfidence: a deployment is FACTUAL when it predates error
// events, else INFERRED.
func correlationDeployConfidence(c *Correlation) domain.CorrelationConfidence {
	if len(c.ErrorEvents) > 0 {
		return domain.CorrelationFactual
	}
	return domain.CorrelationInferred
}

// correlationCommitConfidence: a commit is INFERRED when it was authored into a
// deployment context, else UNKNOWN.
func correlationCommitConfidence(c *Correlation) domain.CorrelationConfidence {
	if len(c.RecentCommits) > 0 {
		return domain.CorrelationInferred
	}
	return domain.CorrelationUnknown
}

// ChangeFingerprint is a stable, comparable digest of a change (13.4). It lets
// the system detect whether two changes are the same even when their free-text
// descriptions differ, and to group recurring changes (e.g. "same 500 on the
// same symbol").
//
// The fingerprint enumerates the full dimension set (13.4): beyond kind/target
// it also captures the files touched, symbols, services, APIs, database/event/
// risk/test signals, and the agent/model/task that produced the change. Any
// dimension value is included in the canonical form (and therefore the hash),
// so two changes that differ on any dimension fingerprint differently.
type ChangeFingerprint struct {
	Kind      string   `json:"kind"`
	Target    string   `json:"target"`
	NewTarget string   `json:"new_target,omitempty"`
	Files     []string `json:"files,omitempty"`     // files touched by the change
	Symbols   []string `json:"symbols,omitempty"`   // symbols (funcs/types) affected
	Services  []string `json:"services,omitempty"`  // services affected
	APIs      []string `json:"apis,omitempty"`      // public APIs affected
	Database  []string `json:"database,omitempty"`  // schemas/collections/tables touched
	Events    []string `json:"events,omitempty"`    // domain events emitted
	Risk      []string `json:"risk,omitempty"`      // risk descriptors
	Tests     []string `json:"tests,omitempty"`     // tests added/changed
	Agent     []string `json:"agent,omitempty"`     // agent(s) that produced the change
	Model     []string `json:"model,omitempty"`     // model(s) used
	Task      []string `json:"task,omitempty"`      // task(s) the change belongs to
	Hash      string   `json:"hash"`
	Canonical string   `json:"canonical"` // normalized canonical form
}

// Fingerprint builds the change fingerprint for a change from its kind, target
// and new target only. The hash is a SHA-256 over the normalized (lowercased,
// trimmed, sorted) canonical form, so changes that differ only in
// casing/whitespace fingerprint identically. Prefer FingerprintChange when the
// full dimension set is available.
func Fingerprint(kind, target, newTarget string) ChangeFingerprint {
	return FingerprintChange(ChangeFingerprint{Kind: kind, Target: target, NewTarget: newTarget})
}

// FingerprintChange builds the change fingerprint over the full dimension set
// (13.4). Every non-empty dimension value feeds the canonical form, so two
// changes are equal only when they agree on all dimensions (after
// normalization).
func FingerprintChange(fp ChangeFingerprint) ChangeFingerprint {
	fp.Canonical = canonicalizeChange(fp)
	sum := sha256.Sum256([]byte(fp.Canonical))
	fp.Hash = hex.EncodeToString(sum[:])[:16]
	return fp
}

// canonicalizeChange normalizes a change (all dimensions) into a canonical
// comparable form: every value is lowercased, trimmed, and the whole set is
// sorted before joining, so ordering and casing do not affect the digest.
func canonicalizeChange(fp ChangeFingerprint) string {
	var parts []string
	add := func(vals []string) {
		for _, v := range vals {
			if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
				parts = append(parts, v)
			}
		}
	}
	add([]string{fp.Kind, fp.Target, fp.NewTarget})
	add(fp.Files)
	add(fp.Symbols)
	add(fp.Services)
	add(fp.APIs)
	add(fp.Database)
	add(fp.Events)
	add(fp.Risk)
	add(fp.Tests)
	add(fp.Agent)
	add(fp.Model)
	add(fp.Task)
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

// TraceLink is the (13.1) link that ties a correlation chain to the trace and
// event evidence that produced it.
type TraceLink struct {
	TraceID string        `json:"trace_id"`
	EventID string        `json:"event_id"`
	Stage   string        `json:"stage"` // the chain stage this evidence backs
}

// TraceEventsFromCorrelation extracts the (trace, event) evidence links from a
// correlation's trace spans and error events.
func TraceEventsFromCorrelation(c *Correlation) []TraceLink {
	var out []TraceLink
	for _, e := range c.TraceSpans {
		if e.TraceID != "" {
			out = append(out, TraceLink{TraceID: e.TraceID, EventID: e.ID, Stage: "trace"})
		}
	}
	for _, e := range c.ErrorEvents {
		if e.TraceID != "" {
			out = append(out, TraceLink{TraceID: e.TraceID, EventID: e.ID, Stage: "error"})
		}
	}
	return out
}

var _ = time.Now // keep time import (helpers may use it)