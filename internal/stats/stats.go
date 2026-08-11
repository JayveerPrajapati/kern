// Package stats records before/after token counts and cost estimates in a
// local JSONL log. Nothing is transmitted anywhere.
package stats

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

// Operation is the kind of optimization performed.
type Operation string

const (
	OpOptimizePrompt Operation = "optimize_prompt"
	OpCompactFile    Operation = "compact_file"
	OpProjectMap     Operation = "project_map"
	OpRunBuild       Operation = "run_build"
	OpOptimizeLog    Operation = "optimize_log"
)

// Entry is one recorded optimization.
type Entry struct {
	Time         time.Time `json:"time"`
	Session      string    `json:"session,omitempty"`
	Operation    Operation `json:"operation"`
	Source       string    `json:"source,omitempty"`
	Model        string    `json:"model,omitempty"`
	BeforeTokens int       `json:"before_tokens"`
	AfterTokens  int       `json:"after_tokens"`
	SavedTokens  int       `json:"saved_tokens"`
	SavedPercent float64   `json:"saved_percent"`
	CostSavedUSD float64   `json:"cost_saved_usd"`
	BeforeBytes  int       `json:"before_bytes"`
	AfterBytes   int       `json:"after_bytes"`
}

// pricesUSD maps model -> USD per 1M tokens (input). Estimates only.
var pricesUSD = map[string]float64{
	"gpt-4o":            2.50,
	"gpt-4o-mini":       0.15,
	"gpt-4.1":           2.00,
	"gpt-4.1-mini":      0.40,
	"gpt-4.1-nano":      0.10,
	"claude-sonnet-4":   3.00,
	"claude-3-5-sonnet": 3.00,
	"claude-haiku-4-5":  1.00,
	"gemini-2.5-pro":    1.25,
	"gemini-2.5-flash":  0.30,
	"llama-3.3-70b":     0.00,
	"local":             0.00,
}

// CostPerMillion returns the USD input price per 1M tokens for a model.
func CostPerMillion(model string) float64 {
	if p, ok := pricesUSD[model]; ok {
		return p
	}
	if model == "" {
		return 0
	}
	return 0
}

// DefaultModel is used when a caller doesn't specify a model.
const DefaultModel = "gpt-4o-mini"

// Recorder appends entries to the per-day JSONL log.
type Recorder struct {
	dir string
}

// NewRecorder creates a recorder rooted at ~/.cache/kern/stats.
func NewRecorder() (*Recorder, error) {
	dir := cache.Path("stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Recorder{dir: dir}, nil
}

func (r *Recorder) dayPath(t time.Time) string {
	return filepath.Join(r.dir, t.Format("2006-01-02")+".jsonl")
}

// Record writes one entry.
func (r *Recorder) Record(e Entry) error {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	} else {
		e.Time = e.Time.UTC()
	}
	e.SavedTokens = e.BeforeTokens - e.AfterTokens
	if e.BeforeTokens > 0 {
		e.SavedPercent = float64(e.SavedTokens) / float64(e.BeforeTokens) * 100
	}
	e.CostSavedUSD = float64(e.SavedTokens) / 1e6 * CostPerMillion(e.Model)
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(r.dayPath(e.Time), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// Summary aggregates entries over a time range.
type Summary struct {
	Operations  int               `json:"operations"`
	BeforeTotal int               `json:"before_tokens"`
	AfterTotal  int               `json:"after_tokens"`
	SavedTotal  int               `json:"saved_tokens"`
	SavedPct    float64           `json:"saved_percent"`
	CostSaved   float64           `json:"cost_saved_usd"`
	ByOperation map[Operation]int `json:"by_operation"`
}

// Summarize reads all log files matching the filter and aggregates.
func (r *Recorder) Summarize(days int, session string) (*Summary, error) {
	sum := &Summary{ByOperation: make(map[Operation]int)}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	if days <= 0 {
		// days<=0 means today only, not "all time": an empty range counts no
		// full days before today.
		cutoff = time.Now().UTC()
	}
	bound := cutoff.Truncate(24 * time.Hour)
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return sum, err
	}
	for _, de := range entries {
		if de.IsDir() || filepath.Ext(de.Name()) != ".jsonl" {
			continue
		}
		day, perr := time.Parse("2006-01-02", de.Name()[:len(de.Name())-len(".jsonl")])
		if perr != nil {
			continue
		}
		if day.Before(bound) {
			continue
		}
		f, ferr := os.Open(filepath.Join(r.dir, de.Name()))
		if ferr != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var e Entry
			if json.Unmarshal(sc.Bytes(), &e) != nil {
				continue
			}
			if session != "" && e.Session != session {
				continue
			}
			sum.Operations++
			sum.BeforeTotal += e.BeforeTokens
			sum.AfterTotal += e.AfterTokens
			sum.SavedTotal += e.SavedTokens
			sum.CostSaved += e.CostSavedUSD
			sum.ByOperation[e.Operation]++
		}
		f.Close()
	}
	if sum.BeforeTotal > 0 {
		sum.SavedPct = float64(sum.SavedTotal) / float64(sum.BeforeTotal) * 100
	}
	return sum, nil
}

// Entries returns the most recent n entries across all log files.
func (r *Recorder) Entries(n int) ([]Entry, error) {
	if n <= 0 {
		n = 20
	}
	var all []Entry
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil, err
	}
	for _, de := range entries {
		if de.IsDir() || filepath.Ext(de.Name()) != ".jsonl" {
			continue
		}
		f, ferr := os.Open(filepath.Join(r.dir, de.Name()))
		if ferr != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var e Entry
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				all = append(all, e)
			}
		}
		f.Close()
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Time.After(all[j].Time) })
	if len(all) > n {
		all = all[:n]
	}
	return all, nil
}
