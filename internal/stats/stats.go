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
	kernctx "github.com/JayveerPrajapati/kern/internal/context"
)

// Operation is the kind of optimization performed.
type Operation string

const (
	OpOptimizePrompt Operation = "optimize_prompt"
	OpCompactFile    Operation = "compact_file"
	OpProjectMap     Operation = "project_map"
	OpRunBuild       Operation = "run_build"
	OpOptimizeLog    Operation = "optimize_log"
	// OpToolCall marks entries recorded at MCP dispatch: one per executed
	// tool call, carrying the Tool name and the returned payload size. It is
	// excluded from Summarize (the optimization-events ledger) so existing
	// surfaces ("N ops, N tokens saved") keep their meaning; the per-tool
	// ledger lives in SummarizeByTool.
	OpToolCall Operation = "tool_call"
)

// ToolForOperation maps an optimization Operation to the canonical MCP tool
// that produced it, so existing optimization entries can be attributed to a
// tool in the per-tool ledger. An empty result means the operation has no
// natural tool mapping.
func ToolForOperation(op Operation) string {
	switch op {
	case OpOptimizePrompt:
		return "kern_optimize_prompt"
	case OpCompactFile:
		return "kern_compact_file"
	case OpProjectMap:
		return "kern_project_map"
	case OpRunBuild:
		return "kern_run_build"
	case OpOptimizeLog:
		return "kern_optimize_log"
	}
	return ""
}

// Entry is one recorded optimization.
type Entry struct {
	Time      time.Time `json:"time"`
	Session   string    `json:"session,omitempty"`
	Operation Operation `json:"operation"`
	// Tool is the MCP tool that produced the entry ("" for entries recorded
	// before per-tool attribution existed). Old JSONL lines without the field
	// read back as "" — backward compatible.
	Tool string `json:"tool,omitempty"`
	// Agent is the agent identity the entry is attributed to (the MCP
	// agent_id argument, "" for entries recorded before per-agent
	// attribution existed). Old JSONL lines without the field read back as
	// "" — backward compatible.
	Agent        string  `json:"agent,omitempty"`
	Source       string  `json:"source,omitempty"`
	Model        string  `json:"model,omitempty"`
	BeforeTokens int     `json:"before_tokens"`
	AfterTokens  int     `json:"after_tokens"`
	SavedTokens  int     `json:"saved_tokens"`
	SavedPercent float64 `json:"saved_percent"`
	CostSavedUSD float64 `json:"cost_saved_usd"`
	BeforeBytes  int     `json:"before_bytes"`
	AfterBytes   int     `json:"after_bytes"`
}

// CostPerMillion returns the USD input price per 1M tokens for a model.
// It delegates to the canonical resolver in internal/context (longest-prefix
// table + KERN_MODEL_COSTS extension + the KERN_COST_PER_TOKEN/cost_per_token
// override chain), so the per-entry ledger always agrees with the aggregate
// "cost model" label instead of a divergent exact-match table. Unknown and
// empty models resolve to the flat assumed default ($10/1M).
func CostPerMillion(model string) float64 {
	return kernctx.CostPerMillionFor(model)
}

// ModelSavings returns estimated dollar savings across major frontier LLM pricing tiers.
func ModelSavings(savedTokens int) map[string]float64 {
	out := make(map[string]float64)
	if savedTokens <= 0 {
		return out
	}
	for m, price := range kernctx.ModelRates() {
		if price > 0 {
			out[m] = float64(savedTokens) / 1e6 * price
		}
	}
	return out
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
	// Savings are only derived when the caller knows the before count. A
	// tool that only knows its returned payload (per-tool dispatch entries)
	// records BeforeTokens=0 and must not show fabricated negative savings
	// (Record used to derive Saved = Before - After unconditionally).
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

// walkEntries iterates every entry in the day window (days <= 0 = today only)
// that passes the session filter, invoking fn for each. Shared by Summarize
// and SummarizeByTool so both surfaces agree on the same window semantics.
func (r *Recorder) walkEntries(days int, session string, fn func(Entry)) error {
	cutoff := time.Now().UTC().AddDate(0, 0, -(days - 1))
	if days <= 0 {
		// days<=0 means today only, not "all time": an empty range counts no
		// full days before today.
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
		_ = f.Close()
	}
	return nil
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

// Summarize reads all log files matching the filter and aggregates the
// optimization-events ledger. Per-tool dispatch entries (Operation ==
// OpToolCall) are excluded so "operations" keeps meaning the count of
// optimization events; the per-tool ledger is served by SummarizeByTool.
func (r *Recorder) Summarize(days int, session string) (*Summary, error) {
	sum := &Summary{ByOperation: make(map[Operation]int)}
	if err := r.walkEntries(days, session, func(e Entry) {
		if e.Operation == OpToolCall {
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
	if sum.BeforeTotal > 0 {
		sum.SavedPct = float64(sum.SavedTotal) / float64(sum.BeforeTotal) * 100
	}
	return sum, nil
}

// ToolStat is one tool's aggregated per-tool ledger row.
type ToolStat struct {
	Tool           string  `json:"tool"`
	Calls          int     `json:"calls"`
	TokensReturned int     `json:"tokens_returned"`
	TokensSaved    int     `json:"tokens_saved"`
	CostSaved      float64 `json:"cost_saved_usd"`
}

// SummarizeByTool aggregates every entry that carries a Tool (per-tool
// dispatch entries plus optimization entries whose Operation maps to a tool)
// over the same day window as Summarize. Tools that do not know their
// before/after contribute tokens returned only (saved = 0 — honest, never
// fabricated). Rows are sorted by tokens saved desc, then tokens returned
// desc, then tool name for a stable table.
func (r *Recorder) SummarizeByTool(days int, session string) ([]ToolStat, error) {
	byTool := make(map[string]*ToolStat)
	if err := r.walkEntries(days, session, func(e Entry) {
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

// AgentStat is one agent's aggregated per-agent ledger row.
type AgentStat struct {
	Agent          string  `json:"agent"`
	Calls          int     `json:"calls"`
	TokensReturned int     `json:"tokens_returned"`
	TokensSaved    int     `json:"tokens_saved"`
	CostSaved      float64 `json:"cost_saved_usd"`
}

// unattributedAgent is the bucket every entry without an agent attribution
// falls into, so the per-agent ledger is complete instead of silently
// dropping legacy/agent-less entries.
const unattributedAgent = "(unattributed)"

// SummarizeByAgent aggregates entries by the agent that produced them (the
// MCP agent_id argument, plus optimization entries recorded with an Agent)
// over the same day window as Summarize. Entries without an attribution
// (recorded before per-agent attribution existed, or by surfaces that never
// knew the agent) fall into the "(unattributed)" bucket. Rows are sorted by
// tokens saved desc, then tokens returned desc, then agent name for a stable
// table — the same ordering SummarizeByTool uses.
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
