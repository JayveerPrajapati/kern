// Package metrics provides a unified, thread-safe metrics recorder for kern,
// consolidating performance, self-observability, AI governance, and product
// success metrics into a single layer. All metrics are local — nothing is
// transmitted. A nil *Recorder is safe to call (all methods are no-ops).
package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Recorder collects timing and count metrics across kern operations.
// It is thread-safe. A nil Recorder is safe to call (all methods are
// no-ops), so callers can use `var r *Recorder` without nil checks.
type Recorder struct {
	mu sync.Mutex

	// Performance metrics
	indexBuilds      []time.Duration
	graphQueries     []time.Duration
	contextRetrieval []time.Duration
	memoryRecall     []time.Duration
	policyEval       []time.Duration
	toolCalls        []time.Duration
	verification     []time.Duration
	cacheHits        int64
	cacheMisses      int64

	// Self-observability metrics
	requestCount  int64
	toolCallCount int64
	agentRunCount int64
	llmLatency    []time.Duration
	indexingCount int64
	approvalCount int64
	sandboxOps    int64
	incidentCount int64
	errorCount    int64

	// Product success metrics
	tokenBefore        int64
	tokenAfter         int64
	analysisCount      int64
	falsePositives     int64
	impactPredictions  int64
	correctPredictions int64
}

// New creates a new empty Recorder.
func New() *Recorder {
	return &Recorder{}
}

// defaultRecorder is the process-wide shared Recorder. It is lazy-initialized
// on first use. Callers that need their own isolated Recorder can use New()
// instead. All Record* methods on the Default are safe to call from any
// goroutine; a nil Default (before first use) is handled by the nil-safe
// receiver methods.
var (
	defaultOnce sync.Once
	defaultRec  *Recorder
)

// Default returns the process-wide shared Recorder. The same instance is
// returned on every call, so metrics recorded from different subsystems
// (index, context, governance, mcp, web) aggregate into one snapshot. Use
// New() for an isolated Recorder (e.g., in tests).
func Default() *Recorder {
	defaultOnce.Do(func() {
		defaultRec = New()
	})
	return defaultRec
}

// --- Performance metrics ---

// RecordIndexBuild records an index build duration.
func (r *Recorder) RecordIndexBuild(d time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.indexBuilds = append(r.indexBuilds, d)
}

// RecordGraphQuery records a graph query duration.
func (r *Recorder) RecordGraphQuery(d time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.graphQueries = append(r.graphQueries, d)
}

// RecordContextRetrieval records a context retrieval duration.
func (r *Recorder) RecordContextRetrieval(d time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.contextRetrieval = append(r.contextRetrieval, d)
}

// RecordMemoryRecall records a memory recall duration.
func (r *Recorder) RecordMemoryRecall(d time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.memoryRecall = append(r.memoryRecall, d)
}

// RecordPolicyEval records a policy evaluation duration.
func (r *Recorder) RecordPolicyEval(d time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policyEval = append(r.policyEval, d)
}

// RecordToolCall records a tool call duration.
func (r *Recorder) RecordToolCall(d time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toolCalls = append(r.toolCalls, d)
	r.toolCallCount++
}

// RecordVerification records a verification duration.
func (r *Recorder) RecordVerification(d time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.verification = append(r.verification, d)
}

// RecordCacheHit records a cache hit.
func (r *Recorder) RecordCacheHit() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cacheHits++
}

// RecordCacheMiss records a cache miss.
func (r *Recorder) RecordCacheMiss() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cacheMisses++
}

// --- Self-observability metrics ---

// RecordRequest records an incoming request.
func (r *Recorder) RecordRequest() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requestCount++
}

// RecordAgentRun records an agent run.
func (r *Recorder) RecordAgentRun() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agentRunCount++
}

// RecordLLMLatency records an LLM call duration.
func (r *Recorder) RecordLLMLatency(d time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.llmLatency = append(r.llmLatency, d)
}

// RecordIndexing records an indexing operation.
func (r *Recorder) RecordIndexing() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.indexingCount++
}

// RecordApproval records an approval decision.
func (r *Recorder) RecordApproval() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.approvalCount++
}

// RecordSandboxOp records a sandbox operation.
func (r *Recorder) RecordSandboxOp() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sandboxOps++
}

// RecordIncident records an incident.
func (r *Recorder) RecordIncident() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.incidentCount++
}

// RecordError records an error.
func (r *Recorder) RecordError() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errorCount++
}

// --- Product success metrics ---

// RecordTokenUsage records before/after token counts (for reduction metric).
func (r *Recorder) RecordTokenUsage(before, after int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokenBefore += before
	r.tokenAfter += after
}

// RecordAnalysis records a completed analysis.
func (r *Recorder) RecordAnalysis() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.analysisCount++
}

// RecordFalsePositive records a false positive finding.
func (r *Recorder) RecordFalsePositive() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.falsePositives++
}

// RecordImpactPrediction records an impact prediction and whether it was correct.
func (r *Recorder) RecordImpactPrediction(correct bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.impactPredictions++
	if correct {
		r.correctPredictions++
	}
}

// --- Snapshot ---

// Snapshot captures all metrics at a point in time.
type Snapshot struct {
	Timestamp time.Time

	// Performance — 10 metrics
	IndexBuildAvgMs       float64
	GraphQueryAvgMs       float64
	ContextRetrievalAvgMs float64
	MemoryRecallAvgMs     float64
	PolicyEvalAvgMs       float64
	ToolCallAvgMs         float64
	VerificationAvgMs     float64
	CacheHitRate          float64
	IndexBuildCount       int
	TotalIndexTimeMs      float64

	// Self-observability — 14 dimensions
	RequestCount    int64
	ToolCallCount   int64
	AgentRunCount   int64
	LLMLatencyAvgMs float64
	IndexingCount   int64
	ApprovalCount   int64
	SandboxOps      int64
	IncidentCount   int64
	ErrorCount      int64
	CacheHits       int64
	CacheMisses     int64

	// Product success — key metrics
	TokenReductionPct float64
	AnalysisCount     int64
	FalsePositiveRate float64
	ImpactAccuracyPct float64

	// Governance — populated from external sources via SnapshotWithGovernance
	AgentCount      int
	TaskCount       int
	BlocksCount     int
	OverridesCount  int
	ViolationsCount int
	AvgConfidence   float64
}

// Snapshot returns a point-in-time view of all recorded metrics.
func (r *Recorder) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{Timestamp: time.Now()}
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	s := Snapshot{Timestamp: time.Now()}

	// Performance
	s.IndexBuildAvgMs = avgMs(r.indexBuilds)
	s.GraphQueryAvgMs = avgMs(r.graphQueries)
	s.ContextRetrievalAvgMs = avgMs(r.contextRetrieval)
	s.MemoryRecallAvgMs = avgMs(r.memoryRecall)
	s.PolicyEvalAvgMs = avgMs(r.policyEval)
	s.ToolCallAvgMs = avgMs(r.toolCalls)
	s.VerificationAvgMs = avgMs(r.verification)
	s.IndexBuildCount = len(r.indexBuilds)
	s.TotalIndexTimeMs = totalMs(r.indexBuilds)
	total := r.cacheHits + r.cacheMisses
	if total > 0 {
		s.CacheHitRate = float64(r.cacheHits) / float64(total)
	}

	// Self-observability
	s.RequestCount = r.requestCount
	s.ToolCallCount = r.toolCallCount
	s.AgentRunCount = r.agentRunCount
	s.LLMLatencyAvgMs = avgMs(r.llmLatency)
	s.IndexingCount = r.indexingCount
	s.ApprovalCount = r.approvalCount
	s.SandboxOps = r.sandboxOps
	s.IncidentCount = r.incidentCount
	s.ErrorCount = r.errorCount
	s.CacheHits = r.cacheHits
	s.CacheMisses = r.cacheMisses

	// Product success
	if r.tokenBefore > 0 {
		s.TokenReductionPct = (1 - float64(r.tokenAfter)/float64(r.tokenBefore)) * 100
	}
	s.AnalysisCount = r.analysisCount
	if s.AnalysisCount > 0 {
		s.FalsePositiveRate = float64(r.falsePositives) / float64(s.AnalysisCount)
	}
	if r.impactPredictions > 0 {
		s.ImpactAccuracyPct = float64(r.correctPredictions) / float64(r.impactPredictions) * 100
	}

	return s
}

// avgMs returns the average duration in milliseconds.
func avgMs(ds []time.Duration) float64 {
	if len(ds) == 0 {
		return 0
	}
	var total time.Duration
	for _, d := range ds {
		total += d
	}
	return float64(total.Milliseconds()) / float64(len(ds))
}

// totalMs returns the total duration in milliseconds.
func totalMs(ds []time.Duration) float64 {
	var total time.Duration
	for _, d := range ds {
		total += d
	}
	return float64(total.Milliseconds())
}

// Render returns a human-readable multi-line report of all recorded metrics.
// Used by `kern stats --performance` (CLI) and the web console's performance
// view. Empty/zero metrics are included (so callers see the full surface).
func (r *Recorder) Render() string {
	if r == nil {
		return ""
	}
	s := r.Snapshot()
	var b strings.Builder
	fmt.Fprintf(&b, "kern performance + observability metrics (as of %s)\n\n", s.Timestamp.Format(time.RFC3339))

	fmt.Fprintf(&b, "performance (F-46):\n")
	fmt.Fprintf(&b, "  index builds       : %d (avg %.1fms, total %.0fms)\n", s.IndexBuildCount, s.IndexBuildAvgMs, s.TotalIndexTimeMs)
	fmt.Fprintf(&b, "  graph queries      : avg %.1fms\n", s.GraphQueryAvgMs)
	fmt.Fprintf(&b, "  context retrieval  : avg %.1fms\n", s.ContextRetrievalAvgMs)
	fmt.Fprintf(&b, "  memory recall      : avg %.1fms\n", s.MemoryRecallAvgMs)
	fmt.Fprintf(&b, "  policy eval        : avg %.1fms\n", s.PolicyEvalAvgMs)
	fmt.Fprintf(&b, "  tool calls         : avg %.1fms\n", s.ToolCallAvgMs)
	fmt.Fprintf(&b, "  verification       : avg %.1fms\n", s.VerificationAvgMs)
	fmt.Fprintf(&b, "  cache hit rate     : %.1f%%\n", s.CacheHitRate*100)

	fmt.Fprintf(&b, "\nself-observability (F-47):\n")
	fmt.Fprintf(&b, "  requests           : %d\n", s.RequestCount)
	fmt.Fprintf(&b, "  tool calls         : %d\n", s.ToolCallCount)
	fmt.Fprintf(&b, "  agent runs         : %d\n", s.AgentRunCount)
	fmt.Fprintf(&b, "  LLM latency        : avg %.1fms\n", s.LLMLatencyAvgMs)
	fmt.Fprintf(&b, "  indexing ops       : %d\n", s.IndexingCount)
	fmt.Fprintf(&b, "  approvals          : %d\n", s.ApprovalCount)
	fmt.Fprintf(&b, "  sandbox ops        : %d\n", s.SandboxOps)
	fmt.Fprintf(&b, "  incidents          : %d\n", s.IncidentCount)
	fmt.Fprintf(&b, "  errors             : %d\n", s.ErrorCount)

	fmt.Fprintf(&b, "\nproduct success (F-56):\n")
	fmt.Fprintf(&b, "  token reduction    : %.1f%%\n", s.TokenReductionPct)
	fmt.Fprintf(&b, "  analyses           : %d\n", s.AnalysisCount)
	fmt.Fprintf(&b, "  false positive rate: %.1f%%\n", s.FalsePositiveRate*100)
	fmt.Fprintf(&b, "  impact accuracy    : %.1f%%\n", s.ImpactAccuracyPct)

	fmt.Fprintf(&b, "\ngovernance (F-41):\n")
	fmt.Fprintf(&b, "  agents             : %d\n", s.AgentCount)
	fmt.Fprintf(&b, "  tasks              : %d\n", s.TaskCount)
	fmt.Fprintf(&b, "  blocks             : %d\n", s.BlocksCount)
	fmt.Fprintf(&b, "  overrides          : %d\n", s.OverridesCount)
	fmt.Fprintf(&b, "  arch violations    : %d\n", s.ViolationsCount)
	fmt.Fprintf(&b, "  avg confidence     : %.2f\n", s.AvgConfidence)

	return strings.TrimSuffix(b.String(), "\n")
}

// Reset clears all recorded metrics. Useful in tests and for a CLI
// `--reset` flag that starts a fresh measurement window.
func (r *Recorder) Reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.indexBuilds = nil
	r.graphQueries = nil
	r.contextRetrieval = nil
	r.memoryRecall = nil
	r.policyEval = nil
	r.toolCalls = nil
	r.verification = nil
	r.cacheHits = 0
	r.cacheMisses = 0
	r.requestCount = 0
	r.toolCallCount = 0
	r.agentRunCount = 0
	r.llmLatency = nil
	r.indexingCount = 0
	r.approvalCount = 0
	r.sandboxOps = 0
	r.incidentCount = 0
	r.errorCount = 0
	r.tokenBefore = 0
	r.tokenAfter = 0
	r.analysisCount = 0
	r.falsePositives = 0
	r.impactPredictions = 0
	r.correctPredictions = 0
}

// --- Report ---

// Report returns a human-readable text summary of all metrics.
func (s Snapshot) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Kern Metrics Report (%s)\n", s.Timestamp.Format(time.RFC3339))
	fmt.Fprintf(&b, "=========================================\n\n")

	fmt.Fprintf(&b, "Performance (F-46):\n")
	fmt.Fprintf(&b, "  Index build:      %d calls, avg %.1fms, total %.1fms\n", s.IndexBuildCount, s.IndexBuildAvgMs, s.TotalIndexTimeMs)
	fmt.Fprintf(&b, "  Graph queries:    avg %.1fms\n", s.GraphQueryAvgMs)
	fmt.Fprintf(&b, "  Context retrieval: avg %.1fms\n", s.ContextRetrievalAvgMs)
	fmt.Fprintf(&b, "  Memory recall:    avg %.1fms\n", s.MemoryRecallAvgMs)
	fmt.Fprintf(&b, "  Policy eval:      avg %.1fms\n", s.PolicyEvalAvgMs)
	fmt.Fprintf(&b, "  Tool calls:       avg %.1fms\n", s.ToolCallAvgMs)
	fmt.Fprintf(&b, "  Verification:     avg %.1fms\n", s.VerificationAvgMs)
	fmt.Fprintf(&b, "  Cache hit rate:   %.1f%%\n\n", s.CacheHitRate*100)

	fmt.Fprintf(&b, "Self-Observability (F-47):\n")
	fmt.Fprintf(&b, "  Requests:         %d\n", s.RequestCount)
	fmt.Fprintf(&b, "  Tool calls:       %d\n", s.ToolCallCount)
	fmt.Fprintf(&b, "  Agent runs:       %d\n", s.AgentRunCount)
	fmt.Fprintf(&b, "  LLM latency:      avg %.1fms\n", s.LLMLatencyAvgMs)
	fmt.Fprintf(&b, "  Indexing ops:     %d\n", s.IndexingCount)
	fmt.Fprintf(&b, "  Approvals:        %d\n", s.ApprovalCount)
	fmt.Fprintf(&b, "  Sandbox ops:      %d\n", s.SandboxOps)
	fmt.Fprintf(&b, "  Incidents:        %d\n", s.IncidentCount)
	fmt.Fprintf(&b, "  Errors:           %d\n\n", s.ErrorCount)

	fmt.Fprintf(&b, "Product Success (F-56):\n")
	fmt.Fprintf(&b, "  Token reduction:  %.1f%%\n", s.TokenReductionPct)
	fmt.Fprintf(&b, "  Analyses:         %d\n", s.AnalysisCount)
	fmt.Fprintf(&b, "  False positive:   %.1f%%\n", s.FalsePositiveRate*100)
	fmt.Fprintf(&b, "  Impact accuracy:  %.1f%%\n\n", s.ImpactAccuracyPct)

	fmt.Fprintf(&b, "Governance (F-41):\n")
	fmt.Fprintf(&b, "  Agents:           %d\n", s.AgentCount)
	fmt.Fprintf(&b, "  Tasks:            %d\n", s.TaskCount)
	fmt.Fprintf(&b, "  Blocks:           %d\n", s.BlocksCount)
	fmt.Fprintf(&b, "  Overrides:        %d\n", s.OverridesCount)
	fmt.Fprintf(&b, "  Violations:       %d\n", s.ViolationsCount)
	fmt.Fprintf(&b, "  Avg confidence:   %.2f\n", s.AvgConfidence)

	return b.String()
}

// --- Governance metrics from external sources ---

// GovernanceData holds raw governance data collected from external subsystems.
// Callers populate this from governance.AuditLog, agent.Registry, incident.Store,
// architecture.ValidateProject, etc., then pass it to SnapshotWithGovernance.
type GovernanceData struct {
	AgentCount      int
	TaskCount       int
	BlocksCount     int
	OverridesCount  int
	ViolationsCount int
	AvgConfidence   float64
}

// SnapshotWithGovernance returns a Snapshot enriched with governance data
// from external subsystems. The base Snapshot comes from the Recorder;
// governance fields are filled from the provided GovernanceData.
func (r *Recorder) SnapshotWithGovernance(gd GovernanceData) Snapshot {
	s := r.Snapshot()
	s.AgentCount = gd.AgentCount
	s.TaskCount = gd.TaskCount
	s.BlocksCount = gd.BlocksCount
	s.OverridesCount = gd.OverridesCount
	s.ViolationsCount = gd.ViolationsCount
	s.AvgConfidence = gd.AvgConfidence
	return s
}

// SortedKeys is a helper for deterministic map iteration in reports.
func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- Disk persistence (cross-process CLI metrics) ---
//
// The CLI runs as a fresh process per invocation, so in-memory metrics are
// lost on exit. Save/Load persist a Snapshot to disk so `kern stats
// performance` can show accumulated metrics across invocations. The MCP
// server and kern-server (long-lived processes) don't need this — their
// Default() singleton lives for the process lifetime.

// persistedSnapshot wraps a Snapshot with a format version for forward-compat.
type persistedSnapshot struct {
	Format   int      `json:"format"`
	Snapshot Snapshot `json:"snapshot"`
}

// Save writes the current Snapshot to the given path as JSON. It is best-effort:
// a write failure is returned but callers may ignore it (metrics are
// non-critical). The parent directory must exist.
func (r *Recorder) Save(path string) error {
	if r == nil {
		return nil
	}
	s := r.Snapshot()
	data, err := json.Marshal(persistedSnapshot{Format: 1, Snapshot: s})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Load reads a previously-saved Snapshot from the given path and merges its
// counters/durations into this Recorder. A missing file is a no-op (returns
// nil). This allows the CLI to accumulate metrics across process invocations:
// each invocation loads the prior state, records its own metrics, then saves.
//
// Merging strategy: duration-based metrics are converted back to a single
// synthetic duration entry (avg * count) so the next Snapshot recomputes
// approximately the same average. Counters are added directly.
func (r *Recorder) Load(path string) error {
	if r == nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no prior state — fine
		}
		return err
	}
	var ps persistedSnapshot
	if err := json.Unmarshal(data, &ps); err != nil {
		return err // corrupt — ignore, start fresh
	}
	s := ps.Snapshot
	r.mu.Lock()
	defer r.mu.Unlock()

	// Merge durations: convert avg*count back to one synthetic entry.
	if s.IndexBuildCount > 0 {
		r.indexBuilds = append(r.indexBuilds, time.Duration(s.IndexBuildAvgMs*float64(s.IndexBuildCount))*time.Millisecond)
	}
	r.graphQueries = append(r.graphQueries, time.Duration(s.GraphQueryAvgMs)*time.Millisecond)
	r.contextRetrieval = append(r.contextRetrieval, time.Duration(s.ContextRetrievalAvgMs)*time.Millisecond)
	r.memoryRecall = append(r.memoryRecall, time.Duration(s.MemoryRecallAvgMs)*time.Millisecond)
	r.policyEval = append(r.policyEval, time.Duration(s.PolicyEvalAvgMs)*time.Millisecond)
	r.toolCalls = append(r.toolCalls, time.Duration(s.ToolCallAvgMs)*time.Millisecond)
	r.verification = append(r.verification, time.Duration(s.VerificationAvgMs)*time.Millisecond)
	r.llmLatency = append(r.llmLatency, time.Duration(s.LLMLatencyAvgMs)*time.Millisecond)

	// Merge counters.
	r.cacheHits += s.CacheHits
	r.cacheMisses += s.CacheMisses
	r.requestCount += s.RequestCount
	r.toolCallCount += s.ToolCallCount
	r.agentRunCount += s.AgentRunCount
	r.indexingCount += s.IndexingCount
	r.approvalCount += s.ApprovalCount
	r.sandboxOps += s.SandboxOps
	r.incidentCount += s.IncidentCount
	r.errorCount += s.ErrorCount

	// Merge product-success metrics. Token usage: add the implied before/after
	// from the reduction percentage (best-effort approximation).
	if s.TokenReductionPct > 0 {
		// reduction = (1 - after/before) * 100 → after = before * (1 - pct/100)
		// We don't know the absolute before; use the analysisCount as a proxy
		// weight so subsequent snapshots carry forward proportional weight.
		r.analysisCount += s.AnalysisCount
		r.falsePositives += int64(s.FalsePositiveRate * float64(s.AnalysisCount))
		r.impactPredictions += int64(s.ImpactAccuracyPct / 100 * float64(s.AnalysisCount))
		// correctPredictions is already baked into ImpactAccuracyPct; skip
	} else {
		r.analysisCount += s.AnalysisCount
	}

	return nil
}
