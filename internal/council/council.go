// Package council normalizes review results into consensus and divergence
// reports (blueprint KERN-P2-002) without treating majority vote as truth.
//
// Inputs are review packs (internal/reviewpack): each pack is one reviewer's
// immutable evidence artifact. The council matches claims across packs by
// canonical statement key and reports:
//
//	consensus           — claims shared by two or more packs (with the agreeing set)
//	divergence          — same statement classified differently across packs
//	minority positions  — claims held by exactly one pack
//	supporting evidence — evidence counts behind consensus claims
//	unsupported claims  — claims with no supporting evidence anywhere
//	assumptions         — claims classified inferred/reported/stale or HYPOTHESIS
//	decision drivers    — consensus claims ranked by evidence + agreement
//	next verification   — deterministic verification actions that would
//	                      resolve divergence and fill evidence gaps
//
// Everything is deterministic: identical inputs always yield identical
// reports. No LLM, no majority-winner selection — agreement is reported with
// its exact scope, and disagreement is surfaced, not suppressed.
package council

import (
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/reviewpack"
)

// maxDrivers caps decision drivers and next-verification entries.
const maxDrivers = 8

// Review is one reviewer's normalized claim set. Reviewers are identified by
// pack content hash when packs are given.
type Review struct {
	Reviewer    string                // pack content hash (short)
	Claims      []reviewpack.ClaimRef // observed claims
	Assumptions []reviewpack.ClaimRef // unverified assumptions
}

// FromPack converts a review pack into a normalized review. The reviewer id
// is the pack's content hash (short form) — the immutable pack identity.
func FromPack(p *reviewpack.ReviewPack) Review {
	return Review{
		Reviewer:    short(p.ContentHash),
		Claims:      p.Claims,
		Assumptions: p.Assumptions,
	}
}

// Consensus is one agreement: a claim key shared by >= 2 packs.
type Consensus struct {
	Statement string   // original statement text from the first agreeing pack
	Type      string   // claim type
	Status    string   // status in the first agreeing pack
	Packs     []string // reviewers (content-hash shorts) that share the claim
	Evidence  int      // total evidence count across agreeing packs
}

// Divergence is a statement classified differently across packs.
type Divergence struct {
	Statement string   // the contested statement
	Packs     []string // reviewers involved
	Kinds     []string // distinct "type/status" classifications observed
}

// Minority is a claim held by exactly one pack.
type Minority struct {
	Statement string
	Type      string
	Status    string
	Pack      string
	Evidence  int
}

// Unsupported is a claim with no evidence in any pack.
type Unsupported struct {
	Statement string
	Type      string
	Status    string
	Packs     []string
}

// Assumption is a claim classified as unverified (inferred/reported/stale or
// HYPOTHESIS), aggregated across packs.
type Assumption struct {
	Statement string
	Type      string
	Status    string
	Packs     []string
}

// Driver is a decision driver: a consensus claim ranked by evidence and
// agreement.
type Driver struct {
	Statement string
	Packs     int
	Evidence  int
}

// NextVerification is one deterministic verification action.
type NextVerification struct {
	Action string // "verify" or "obtain evidence"
	Target string // the statement to verify
	Why    string // reason: divergence or unsupported
}

// Report is the normalized council output (KERN-P2-002 output fields).
type Report struct {
	SchemaVersion    int                `json:"schema_version"`
	Packs            []string           `json:"packs"` // reviewers analyzed, sorted
	Consensus        []Consensus        `json:"consensus"`
	Divergence       []Divergence       `json:"divergence"`
	Minority         []Minority         `json:"minority_positions"`
	Supporting       []Supporting       `json:"supporting_evidence"`
	Unsupported      []Unsupported      `json:"unsupported_claims"`
	Assumptions      []Assumption       `json:"assumptions"`
	Drivers          []Driver           `json:"decision_drivers"`
	NextVerification []NextVerification `json:"next_verification"`
}

// Supporting is the evidence behind one consensus claim.
type Supporting struct {
	Statement string
	Evidence  int
	Packs     int
}

// SchemaVersion of the council report artifact.
const SchemaVersion = 1

// occurrence is one claim observation: which review held it and how it was
// classified there.
type occurrence struct {
	review   int
	typ      string
	status   string
	evidence int
}

// Normalize reduces N review packs into one consensus/divergence report.
// With fewer than 2 packs, consensus is empty and every claim is a minority
// position (the report still surfaces assumptions, unsupported claims and
// next verification).
func Normalize(packs []*reviewpack.ReviewPack) *Report {
	r := &Report{SchemaVersion: SchemaVersion}
	reviews := make([]Review, 0, len(packs))
	for _, p := range packs {
		if p == nil {
			continue
		}
		reviews = append(reviews, FromPack(p))
		r.Packs = append(r.Packs, short(p.ContentHash))
	}
	sort.Strings(r.Packs)

	// Claim index: canonical key -> occurrences across reviews.
	index := make(map[string][]occurrence)
	order := make([]string, 0) // first-seen statement order (stable)
	seen := make(map[string]bool)
	for i, rv := range reviews {
		for _, c := range rv.Claims {
			key := keyOf(c.Statement)
			if !seen[key] {
				seen[key] = true
				order = append(order, key)
			}
			index[key] = append(index[key], occurrence{i, c.Type, c.Status, c.Evidence})
		}
		for _, c := range rv.Assumptions {
			key := keyOf(c.Statement)
			if !seen[key] {
				seen[key] = true
				order = append(order, key)
			}
			index[key] = append(index[key], occurrence{i, c.Type, c.Status, c.Evidence})
		}
	}

	// Canonical statement text per key: first pack's original wording.
	text := make(map[string]string, len(order))
	for _, p := range packs {
		if p == nil {
			continue
		}
		for _, c := range p.Claims {
			if _, ok := text[keyOf(c.Statement)]; !ok {
				text[keyOf(c.Statement)] = c.Statement
			}
		}
		for _, c := range p.Assumptions {
			if _, ok := text[keyOf(c.Statement)]; !ok {
				text[keyOf(c.Statement)] = c.Statement
			}
		}
	}

	// Classify each claim key.
	for _, key := range order {
		occs := index[key]
		stmt := text[key]
		packSet := packSetOf(reviews, occs)
		totalEv := 0
		typ, status := occs[0].typ, occs[0].status
		kinds := distinctKinds(occs)

		// Assumptions: unverified classifications — any occurrence that
		// classifies the claim as inferred/reported/stale or HYPOTHESIS/
		// INFERENCE makes it an assumption for the report.
		if anyUnverified(occs) {
			typ, status := firstUnverified(occs)
			r.Assumptions = append(r.Assumptions, Assumption{
				Statement: stmt, Type: typ, Status: status, Packs: sortedKeys(packSet),
			})
		}

		if len(packSet) >= 2 {
			// Consensus candidate — but is it classified consistently?
			if len(kinds) > 1 {
				r.Divergence = append(r.Divergence, Divergence{
					Statement: stmt, Packs: sortedKeys(packSet), Kinds: kinds,
				})
			} else {
				for _, o := range occs {
					totalEv += o.evidence
				}
				r.Consensus = append(r.Consensus, Consensus{
					Statement: stmt, Type: typ, Status: status,
					Packs: sortedKeys(packSet), Evidence: totalEv,
				})
				r.Supporting = append(r.Supporting, Supporting{
					Statement: stmt, Evidence: totalEv, Packs: len(packSet),
				})
			}
		} else {
			// Minority position: exactly one pack holds it.
			pack := sortedKeys(packSet)[0]
			r.Minority = append(r.Minority, Minority{
				Statement: stmt, Type: typ, Status: status, Pack: pack, Evidence: occs[0].evidence,
			})
		}

		// Unsupported: no evidence in any occurrence.
		anyEvidence := false
		for _, o := range occs {
			if o.evidence > 0 {
				anyEvidence = true
				break
			}
		}
		if !anyEvidence {
			r.Unsupported = append(r.Unsupported, Unsupported{
				Statement: stmt, Type: typ, Status: status, Packs: sortedKeys(packSet),
			})
		}
	}

	// Decision drivers: consensus claims ranked by evidence then agreement.
	for _, c := range r.Consensus {
		r.Drivers = append(r.Drivers, Driver{Statement: c.Statement, Packs: len(c.Packs), Evidence: c.Evidence})
	}
	sort.SliceStable(r.Drivers, func(i, j int) bool {
		if r.Drivers[i].Evidence != r.Drivers[j].Evidence {
			return r.Drivers[i].Evidence > r.Drivers[j].Evidence
		}
		return r.Drivers[i].Packs > r.Drivers[j].Packs
	})
	if len(r.Drivers) > maxDrivers {
		r.Drivers = r.Drivers[:maxDrivers]
	}

	// Next verification: resolve divergence + fill evidence gaps.
	for _, d := range r.Divergence {
		r.NextVerification = append(r.NextVerification, NextVerification{
			Action: "verify", Target: d.Statement,
			Why: "divergent classification across packs " + strings.Join(d.Packs, ", "),
		})
	}
	for _, u := range r.Unsupported {
		r.NextVerification = append(r.NextVerification, NextVerification{
			Action: "obtain evidence", Target: u.Statement,
			Why: "no supporting evidence in any pack",
		})
	}
	if len(r.NextVerification) > maxDrivers {
		r.NextVerification = r.NextVerification[:maxDrivers]
	}
	return r
}

// keyOf canonicalizes a statement for cross-pack matching: lowercased,
// trimmed, whitespace-collapsed.
func keyOf(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

// isUnverified reports whether a type/status pair is an unverified
// assumption classification.
func isUnverified(typ, status string) bool {
	switch status {
	case "inferred", "reported", "stale":
		return true
	}
	return typ == "HYPOTHESIS" || typ == "INFERENCE"
}

// anyUnverified reports whether any occurrence classifies the claim as an
// unverified assumption.
func anyUnverified(occs []occurrence) bool {
	for _, o := range occs {
		if isUnverified(o.typ, o.status) {
			return true
		}
	}
	return false
}

// firstUnverified returns the type/status of the first occurrence that is
// classified as an unverified assumption.
func firstUnverified(occs []occurrence) (typ, status string) {
	for _, o := range occs {
		if isUnverified(o.typ, o.status) {
			return o.typ, o.status
		}
	}
	return occs[0].typ, occs[0].status
}

// packSetOf returns the set of reviewer indexes holding occurrences.
func packSetOf(reviews []Review, occs []occurrence) map[string]bool {
	set := make(map[string]bool)
	for _, o := range occs {
		set[reviews[o.review].Reviewer] = true
	}
	return set
}

// distinctKinds returns the distinct "type/status" classifications.
func distinctKinds(occs []occurrence) []string {
	seen := make(map[string]bool)
	var out []string
	for _, o := range occs {
		k := o.typ + "/" + o.status
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func short(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}
