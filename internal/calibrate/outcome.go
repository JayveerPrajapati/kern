// Outcome matcher — Feature Batch C (calibration: prediction vs reality).
// Scores recorded predictions against caller-supplied outcome data (the app
// layer fills it from the incident store; calibrate never imports
// internal/incident). Matches persist to .kern/outcomes.jsonl (idempotent).
package calibrate

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// OutcomeSource is caller-supplied observed-outcome data (incident-derived).
type OutcomeSource struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"` // outcome kind ("impact")
	Subsystem string    `json:"subsystem"`
	Files     []string  `json:"files,omitempty"`
	TS        time.Time `json:"ts"` // when the outcome was observed
}

// Outcome is one matched (prediction, outcome) pair.
type Outcome struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Hit       bool   `json:"hit"`
	Subsystem string `json:"subsystem"` // the PREDICTION's subsystem

	// Prediction-record and outcome-source refs, populated on match. They
	// carry the provenance for typed-claim memories ("prediction log entry
	// id/timestamp + outcome source"): additive, never used by the match
	// itself.
	PredID string    `json:"pred_id,omitempty"`
	Target string    `json:"target,omitempty"`
	Change string    `json:"change,omitempty"`
	SrcID  string    `json:"src_id,omitempty"`
	PredTS time.Time `json:"pred_ts,omitempty"`
	SrcTS  time.Time `json:"src_ts,omitempty"`
}

// SubsystemConfidence is one subsystem's calibration model entry.
type SubsystemConfidence struct {
	Subsystem    string
	Hits         int
	Misses       int
	Confidence   float64
	Samples      int
	Insufficient bool // true when Samples < 5 (deterministic floor)
}

func outcomeLogPath(root string) string { return filepath.Join(root, ".kern", "outcomes.jsonl") }

func intersects(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// outcomeID deterministically identifies one (incident, prediction) match so
// re-matching the same incidents never duplicates persisted outcomes.
func outcomeID(srcID, predID string) string { return shortHash(srcID, predID) }

// MatchOutcomes scores predictions against observed outcomes. Per outcome
// source, each same-Kind prediction whose timestamp PRECEDES it is a
// candidate: HIT when its predicted files intersect the outcome's files; a
// same-subsystem candidate with no file intersection is a MISS; different
// subsystems with no intersection are uncounted. Matches persist idempotently.
func MatchOutcomes(root string, incidents []OutcomeSource) ([]Outcome, error) {
	preds, err := loadLog[Prediction](predictionLogPath(root))
	if err != nil {
		return nil, err
	}
	if len(incidents) == 0 {
		return nil, nil
	}
	srcs := append([]OutcomeSource(nil), incidents...)
	sort.SliceStable(srcs, func(i, j int) bool { // deterministic order
		if !srcs[i].TS.Equal(srcs[j].TS) {
			return srcs[i].TS.Before(srcs[j].TS)
		}
		return srcs[i].ID < srcs[j].ID
	})
	existing, err := loadLog[Outcome](outcomeLogPath(root))
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(existing))
	for _, o := range existing {
		seen[o.ID] = true
	}
	var matched []Outcome
	for _, src := range srcs {
		for _, p := range preds {
			if p.Kind != src.Kind || !p.TS.Before(src.TS) {
				continue
			}
			subMatch := p.Subsystem != "" && p.Subsystem == src.Subsystem
			fileMatch := intersects(p.PredictedFiles, src.Files)
			if !subMatch && !fileMatch {
				continue
			}
			id := outcomeID(src.ID, p.ID)
			if seen[id] {
				continue
			}
			seen[id] = true
			matched = append(matched, Outcome{
				ID:        id,
				Kind:      p.Kind,
				Hit:       fileMatch,
				Subsystem: p.Subsystem,
				PredID:    p.ID,
				Target:    p.Target,
				Change:    p.Change,
				SrcID:     src.ID,
				PredTS:    p.TS,
				SrcTS:     src.TS,
			})
		}
	}
	for _, o := range matched { // persist for root-only Model/Health reads
		line, merr := json.Marshal(o)
		if merr != nil {
			return nil, merr
		}
		if err := appendJSONLLine(outcomeLogPath(root), line, predictionLogCap); err != nil {
			return nil, err
		}
	}
	return matched, nil
}

// Model computes per-subsystem confidence from persisted outcomes.
// Sorted by Subsystem; Insufficient when Samples < 5.
func Model(root string) ([]SubsystemConfidence, error) {
	outcomes, err := loadLog[Outcome](outcomeLogPath(root))
	if err != nil {
		return nil, err
	}
	counts := map[string]*SubsystemConfidence{}
	for _, o := range outcomes {
		c := counts[o.Subsystem]
		if c == nil {
			c = &SubsystemConfidence{Subsystem: o.Subsystem}
			counts[o.Subsystem] = c
		}
		if o.Hit {
			c.Hits++
		} else {
			c.Misses++
		}
	}
	out := make([]SubsystemConfidence, 0, len(counts))
	for _, c := range counts {
		c.Samples = c.Hits + c.Misses
		if c.Samples > 0 {
			c.Confidence = float64(c.Hits) / float64(c.Samples)
		}
		c.Insufficient = c.Samples < 5
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Subsystem < out[j].Subsystem })
	return out, nil
}

// Health renders the calibration summary (counts + per-subsystem table).
func Health(root string) (string, error) {
	preds, err := loadLog[Prediction](predictionLogPath(root))
	if err != nil {
		return "", err
	}
	outcomes, err := loadLog[Outcome](outcomeLogPath(root))
	if err != nil {
		return "", err
	}
	model, err := Model(root)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "calibration: %d prediction(s) recorded, %d matched outcome(s)\n", len(preds), len(outcomes))
	if len(model) == 0 {
		b.WriteString("no calibration data yet — run kern impact / kern verify to record predictions; outcomes come from observed incidents\n")
		return b.String(), nil
	}
	fmt.Fprintf(&b, "%-16s %6s %7s %10s %8s\n", "subsystem", "hits", "misses", "confidence", "samples")
	for _, c := range model {
		note := ""
		if c.Insufficient {
			note = "  (insufficient data)"
		}
		fmt.Fprintf(&b, "%-16s %6d %7d %9.1f%% %8d%s\n", c.Subsystem, c.Hits, c.Misses, c.Confidence*100, c.Samples, note)
	}
	return b.String(), nil
}

// ConfidenceLine renders the per-subsystem confidence line for kern impact /
// what-if text. Best-effort: read errors omit the line; no samples → insufficient.
func ConfidenceLine(root, subsystem string) string {
	model, err := Model(root)
	if err != nil {
		return ""
	}
	for _, c := range model {
		if c.Subsystem == subsystem {
			if c.Samples == 0 {
				return fmt.Sprintf("confidence: %s insufficient data", subsystem)
			}
			return fmt.Sprintf("confidence: %s %.1f%% (%d samples)", subsystem, c.Confidence*100, c.Samples)
		}
	}
	return fmt.Sprintf("confidence: %s insufficient data", subsystem)
}

// AggregateConfidenceLine renders the overall confidence line for kern verify
// text (total hits/misses); insufficient data below 5 total samples.
func AggregateConfidenceLine(root string) string {
	model, err := Model(root)
	if err != nil {
		return ""
	}
	hits, misses := 0, 0
	for _, c := range model {
		hits += c.Hits
		misses += c.Misses
	}
	if hits+misses < 5 {
		return "confidence: insufficient data"
	}
	return fmt.Sprintf("confidence: %.1f%% (%d samples)", float64(hits)/float64(hits+misses)*100, hits+misses)
}

func loadLog[T any](path string) ([]T, error) {
	lines, err := readJSONLines(path)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(lines))
	for _, ln := range lines {
		var v T
		if json.Unmarshal(ln, &v) == nil {
			out = append(out, v)
		}
	}
	return out, nil
}
