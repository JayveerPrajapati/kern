// Package stats records local token/cost savings; nothing is transmitted.
package stats

import (
	"bufio"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
)

type Operation string

const (
	OpOptimizePrompt Operation = "optimize_prompt"
	OpCompactFile    Operation = "compact_file"
	OpProjectMap     Operation = "project_map"
	OpRunBuild       Operation = "run_build"
	OpOptimizeLog    Operation = "optimize_log"
	// OpToolCall marks one MCP dispatch; SummarizeByTool owns its ledger.
	OpToolCall Operation = "tool_call"
)

// ToolForOperation maps an operation to its canonical MCP tool.
func ToolForOperation(op Operation) string {
	switch op {
	case OpOptimizePrompt:
		return "kern_optimize"
	case OpCompactFile:
		return "kern_compact_file"
	case OpProjectMap:
		return "kern_project_map"
	case OpRunBuild:
		return "kern_run_build"
	case OpOptimizeLog:
		return "kern_optimize"
	}
	return ""
}

type Entry struct {
	Time         time.Time `json:"time"`
	Session      string    `json:"session,omitempty"`
	Operation    Operation `json:"operation"`
	Tool         string    `json:"tool,omitempty"`
	Agent        string    `json:"agent,omitempty"`
	Root         string    `json:"root,omitempty"`
	Slice        bool      `json:"slice,omitempty"`
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

func CostPerMillion(model string) float64 {
	return kernctx.CostPerMillionFor(model)
}

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
	if os.Getenv("KERN_READONLY") == "1" {
		return nil // read-path telemetry suppressed for read-only sessions
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Time = e.Time.UTC()
	e.Root = absPath(e.Root)
	if e.BeforeTokens > 0 {
		e.SavedTokens = e.BeforeTokens - e.AfterTokens
		e.SavedPercent = float64(e.SavedTokens) / float64(e.BeforeTokens) * 100
		e.CostSavedUSD = float64(e.SavedTokens) / 1e6 * CostPerMillion(e.Model)
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(r.dayPath(e.Time), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write(append(data, '\n'))
	return err
}

func (r *Recorder) walkEntries(days int, session string, fn func(Entry)) error {
	cutoff := time.Now().UTC().AddDate(0, 0, -(days - 1))
	if days <= 0 {
		// days<=0 means today only, not "all time".
		cutoff = time.Now().UTC()
	}
	bound := cutoff.Truncate(24 * time.Hour)
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return err
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
		// Raise Scanner's default cap: entries can exceed 64KiB.
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			var e Entry
			if json.Unmarshal(sc.Bytes(), &e) != nil {
				continue
			}
			if session != "" && e.Session != session {
				continue
			}
			fn(e)
		}
		if serr := sc.Err(); serr != nil {
			return fmt.Errorf("stats: %s: %w", de.Name(), serr)
		}
		_ = f.Close()
	}
	return nil
}

type Summary struct {
	Operations  int               `json:"operations"`
	BeforeTotal int               `json:"before_tokens"`
	AfterTotal  int               `json:"after_tokens"`
	SavedTotal  int               `json:"saved_tokens"`
	SavedPct    float64           `json:"saved_percent"`
	CostSaved   float64           `json:"cost_saved_usd"`
	ByOperation map[Operation]int `json:"by_operation"`
	// SliceReadTokens counts re-delivered anchor output.
	SliceReadTokens int `json:"slice_read_tokens"`
	// RulesInjectionTokens: the session's rules-injection cost (rule files).
	RulesInjectionTokens int `json:"rules_injection_tokens,omitempty"`
	// Sessions counts distinct sessions in the window.
	Sessions      int     `json:"sessions,omitempty"`
	NetSavedTotal int     `json:"net_saved_tokens,omitempty"`
	NetSavedPct   float64 `json:"net_saved_percent,omitempty"`
	ToolCalls     int     `json:"tool_calls,omitempty"`
}

func (r *Recorder) Summarize(days int, session string) (*Summary, error) {
	return r.summarize(days, session, "")
}

func (r *Recorder) SummarizeByRoot(days int, session, root string) (*Summary, error) {
	return r.summarize(days, session, absPath(root))
}

func (r *Recorder) Roots(days int, session string) ([]string, error) {
	roots := make(map[string]struct{})
	err := r.walkEntries(days, session, func(e Entry) {
		if e.Root != "" {
			roots[e.Root] = struct{}{}
		}
	})
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(roots)), nil
}

func absPath(root string) string {
	if root == "" {
		return ""
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return filepath.Clean(root)
}

func (r *Recorder) summarize(days int, session, root string) (*Summary, error) {
	sum := &Summary{ByOperation: make(map[Operation]int)}
	sessions := make(map[string]struct{})
	if err := r.walkEntries(days, session, func(e Entry) {
		if root != "" && e.Root != root {
			return
		}
		sessions[e.Session] = struct{}{}
		if e.Operation == OpToolCall {
			sum.ToolCalls++
			if e.Slice {
				sum.SliceReadTokens += e.AfterTokens
			}
			return
		}
		sum.Operations++
		sum.BeforeTotal += e.BeforeTokens
		sum.AfterTotal += e.AfterTokens
		sum.SavedTotal += e.SavedTokens
		sum.CostSaved += e.CostSavedUSD
		sum.ByOperation[e.Operation]++
	}); err != nil {
		return sum, err
	}
	sum.Sessions = len(sessions)
	if sum.BeforeTotal > 0 {
		sum.SavedPct = float64(sum.SavedTotal) / float64(sum.BeforeTotal) * 100
	}
	return sum, nil
}

func (s *Summary) ApplyNet(rulesTokens int) {
	if s == nil {
		return
	}
	s.RulesInjectionTokens = rulesTokens
	s.NetSavedTotal = s.SavedTotal - rulesTokens*s.Sessions - s.SliceReadTokens
	if s.BeforeTotal > 0 {
		s.NetSavedPct = float64(s.NetSavedTotal) / float64(s.BeforeTotal) * 100
	}
}

type ToolStat struct {
	Tool           string  `json:"tool"`
	Calls          int     `json:"calls"`
	TokensReturned int     `json:"tokens_returned"`
	TokensSaved    int     `json:"tokens_saved"`
	CostSaved      float64 `json:"cost_saved_usd"`
}

func (r *Recorder) SummarizeByTool(days int, session string, roots ...string) ([]ToolStat, error) {
	root := ""
	if len(roots) > 0 {
		root = absPath(roots[0])
	}
	return r.summarizeByTool(days, session, root)
}

func (r *Recorder) summarizeByTool(days int, session, root string) ([]ToolStat, error) {
	byTool := make(map[string]*ToolStat)
	if err := r.walkEntries(days, session, func(e Entry) {
		if root != "" && e.Root != root {
			return
		}
		if e.Tool == "" {
			return
		}
		ts := byTool[e.Tool]
		if ts == nil {
			ts = &ToolStat{Tool: e.Tool}
			byTool[e.Tool] = ts
		}
		ts.Calls++
		ts.TokensReturned += e.AfterTokens
		ts.TokensSaved += e.SavedTokens
		ts.CostSaved += e.CostSavedUSD
	}); err != nil {
		return nil, err
	}
	out := make([]ToolStat, 0, len(byTool))
	for _, ts := range byTool {
		out = append(out, *ts)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TokensSaved != out[j].TokensSaved {
			return out[i].TokensSaved > out[j].TokensSaved
		}
		if out[i].TokensReturned != out[j].TokensReturned {
			return out[i].TokensReturned > out[j].TokensReturned
		}
		return out[i].Tool < out[j].Tool
	})
	return out, nil
}

type AgentStat struct {
	Agent          string  `json:"agent"`
	Calls          int     `json:"calls"`
	TokensReturned int     `json:"tokens_returned"`
	TokensSaved    int     `json:"tokens_saved"`
	CostSaved      float64 `json:"cost_saved_usd"`
}

// unattributedAgent keeps legacy entries in the agent ledger.
const unattributedAgent = "(unattributed)"

func (r *Recorder) SummarizeByAgent(days int, session string) ([]AgentStat, error) {
	byAgent := make(map[string]*AgentStat)
	if err := r.walkEntries(days, session, func(e Entry) {
		agent := e.Agent
		if agent == "" {
			agent = unattributedAgent
		}
		as := byAgent[agent]
		if as == nil {
			as = &AgentStat{Agent: agent}
			byAgent[agent] = as
		}
		as.Calls++
		as.TokensReturned += e.AfterTokens
		as.TokensSaved += e.SavedTokens
		as.CostSaved += e.CostSavedUSD
	}); err != nil {
		return nil, err
	}
	out := make([]AgentStat, 0, len(byAgent))
	for _, as := range byAgent {
		out = append(out, *as)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TokensSaved != out[j].TokensSaved {
			return out[i].TokensSaved > out[j].TokensSaved
		}
		if out[i].TokensReturned != out[j].TokensReturned {
			return out[i].TokensReturned > out[j].TokensReturned
		}
		return out[i].Agent < out[j].Agent
	})
	return out, nil
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
		_ = f.Close()
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Time.After(all[j].Time) })
	if len(all) > n {
		all = all[:n]
	}
	return all, nil
}
