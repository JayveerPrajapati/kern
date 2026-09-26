// Package learnclaim renders the human-readable constraint content and
// provenance summaries for learning patterns. It is a leaf: it depends only
// on internal/domain and knows nothing about the learning extractor, which
// projects its Pattern onto a PatternView before calling the renderers.
package learnclaim

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// ClaimProvenance summarizes a grouped pattern's provenance: the distinct
// contributing sources (sorted), the count, and the newest timestamp.
type ClaimProvenance struct {
	Sources []string  // distinct memory sources, sorted
	Count   int       // number of memories carrying the provenance
	Latest  time.Time // newest timestamp among the grouped memories
}

// PatternView is the minimal view of a learning.Pattern that the claim
// renderers need. learning constructs it from its Pattern so the renderers
// stay independent of the learning package (no import cycle).
type PatternView struct {
	Key            string
	Count          int
	Scopes         []string
	ClaimType      domain.ClaimType
	Incidents      []string
	Statement      string
	Recommendation string
	Provenance     ClaimProvenance
}

// RenderContent renders a PatternView as a human-readable synthesized
// constraint. For incident-backed patterns the constraint names the failure
// class (the grouping key), the recurrence count, and the incident IDs, so
// future remember/plan phases read "this class of failure recurs" from
// memory. Typed-claim patterns name the claim type, and an explicit Statement
// (e.g. a calibration verdict) is written verbatim.
func RenderContent(p PatternView) string {
	var b strings.Builder
	b.WriteString("pattern: ")
	b.WriteString(p.Key)
	b.WriteString(" recurring ")
	b.WriteString(strconv.Itoa(p.Count))
	b.WriteString(" times across ")
	b.WriteString(strconv.Itoa(len(p.Scopes)))
	b.WriteString(" scope(s)")
	if p.ClaimType != "" {
		b.WriteString("\nclaim type: ")
		b.WriteString(string(p.ClaimType))
	}
	if len(p.Incidents) > 0 {
		b.WriteString("\nfailure class recurs: incidents ")
		b.WriteString(strings.Join(p.Incidents, ", "))
	}
	if p.Statement != "" {
		b.WriteString("\n")
		b.WriteString(p.Statement)
	} else if p.Recommendation != "" {
		b.WriteString("\n")
		b.WriteString(p.Recommendation)
	}
	return b.String()
}

// RenderProvenance renders a PatternView's provenance summary as a stable
// string for the remembered constraint's Provenance field ("" when no
// sources). Sources are sorted defensively so the output is deterministic.
func RenderProvenance(p PatternView) string {
	if len(p.Provenance.Sources) == 0 {
		return ""
	}
	srcs := append([]string(nil), p.Provenance.Sources...)
	sort.Strings(srcs)
	var b strings.Builder
	b.WriteString("sources: ")
	b.WriteString(strings.Join(srcs, ", "))
	b.WriteString("; count: ")
	b.WriteString(strconv.Itoa(p.Provenance.Count))
	if !p.Provenance.Latest.IsZero() {
		b.WriteString("; latest: ")
		b.WriteString(p.Provenance.Latest.UTC().Format(time.RFC3339))
	}
	return b.String()
}
