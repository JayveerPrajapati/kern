package metrics

import (
	"bufio"
	"encoding/json"
	"os"
)

// AdoptionEvent is one line of .kern/adoption.log, appended by the opencode
// plugin's shadow tools (read/glob/grep/bash) when KERN_ADOPTION_LOG=1 is set.
// The log is append-only, one JSON object per line:
//
//	{"ts":1730000000000,"tool":"read","routed":true}
type AdoptionEvent struct {
	Ts     int64  `json:"ts"`
	Tool   string `json:"tool"`
	Routed bool   `json:"routed"`
}

// AdoptionCounts is the aggregate of an adoption log: how many shadow-tool
// calls were observed in total and how many were served by kern.
type AdoptionCounts struct {
	Routed int64
	Total  int64
}

// Pct returns the kern-routed share as a percentage in [0, 100]. It returns 0
// when no calls were observed (avoiding a NaN).
func (c AdoptionCounts) Pct() float64 {
	if c.Total <= 0 {
		return 0
	}
	return float64(c.Routed) / float64(c.Total) * 100
}

// AggregateAdoptionLog reads an append-only adoption log (one JSON event per
// line, as written by the opencode plugin) and returns the routed/total
// counts. Blank and malformed lines are skipped so a partially written line
// (e.g. a crash mid-append) never poisons the aggregate. A missing file is a
// no-op returning zero counts and a nil error, mirroring Recorder.Load.
func AggregateAdoptionLog(path string) (AdoptionCounts, error) {
	var counts AdoptionCounts
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return counts, nil
		}
		return counts, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := trimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev AdoptionEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // malformed line — best-effort aggregate
		}
		counts.Total++
		if ev.Routed {
			counts.Routed++
		}
	}
	if err := sc.Err(); err != nil {
		return counts, err
	}
	return counts, nil
}

// trimSpace is a tiny []byte equivalent of strings.TrimSpace (kept local to
// avoid importing strings for one call).
func trimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && (b[start] == ' ' || b[start] == '\t' || b[start] == '\r' || b[start] == '\n') {
		start++
	}
	end := len(b)
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\r' || b[end-1] == '\n') {
		end--
	}
	return b[start:end]
}

// IngestAdoptionLog aggregates the adoption log at path and folds the counts
// into r via RecordKernAdoption, so `kern stats performance` reflects them.
// It is the caller's job to ingest a given log window only once per persisted
// measurement window (the CLI's Load/Save accumulation would otherwise
// double-count across invocations) — hence it is not wired into the default
// stats path automatically. A missing file is a no-op.
func (r *Recorder) IngestAdoptionLog(path string) error {
	counts, err := AggregateAdoptionLog(path)
	if err != nil {
		return err
	}
	if r == nil {
		return nil
	}
	for i := int64(0); i < counts.Routed; i++ {
		r.RecordKernAdoption(true)
	}
	for i := counts.Routed; i < counts.Total; i++ {
		r.RecordKernAdoption(false)
	}
	return nil
}
