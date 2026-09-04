// Package metrics implements local-only observability for Blueprint.
// It tracks validation counts, latencies, and outcomes in a local JSON
// file (.blueprint/metrics.json). No cloud telemetry.
package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Metrics holds accumulated validation counts, latencies, and outcomes.
// Thread-safe: all methods acquire a mutex.
type Metrics struct {
	mu sync.Mutex `json:"-"`

	// Counters
	ValidationCount int `json:"validation_count"`
	BlockedCount    int `json:"blocked_count"`
	WarningCount    int `json:"warning_count"`
	ErrorCount      int `json:"error_count"`
	PassCount       int `json:"pass_count"`

	// Latency (nanoseconds, converted to ms on output)
	ValidationLatencies []int64            `json:"validation_latencies_ns"`
	PerCheckLatencies   map[string][]int64 `json:"per_check_latencies_ns"`
	SandboxLatencies    []int64            `json:"sandbox_latencies_ns"`

	// Repair loop
	RepairAttempts  int `json:"repair_attempts"`
	RepairSuccesses int `json:"repair_successes"`

	// Overrides (false-positive suppressions)
	FalsePositiveOverrides int `json:"false_positive_overrides"`

	// Metadata
	LastUpdated time.Time `json:"last_updated"`
}

// New returns an initialized empty Metrics.
func New() *Metrics {
	return &Metrics{
		PerCheckLatencies: make(map[string][]int64),
	}
}

// Load reads metrics from the given path. Returns a new empty Metrics if the
// file doesn't exist (first run).
func Load(path string) (*Metrics, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, err
	}
	var m Metrics
	if err := json.Unmarshal(data, &m); err != nil {
		return New(), nil // corrupt file — start fresh
	}
	if m.PerCheckLatencies == nil {
		m.PerCheckLatencies = make(map[string][]int64)
	}
	return &m, nil
}

// Save writes metrics to the given path (creating parent dirs). The write is
// atomic: content is marshalled to a temp file in the same directory and then
// renamed over the target, so a concurrent reader or process never observes a
// half-written metrics file.
func (m *Metrics) Save(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LastUpdated = time.Now()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".metrics-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// maxLatencySamples caps each latency slice retained in metrics. It acts as a
// ring-buffer-style bound so a long-lived watcher daemon can't balloon
// metrics.json or degrade ComputeStats/percentiles over time.
const maxLatencySamples = 1000

// capLatencies trims a latency slice to the most recent maxLatencySamples
// entries. The tail is copied so the backing array doesn't retain the full
// history.
func capLatencies(s []int64) []int64 {
	if len(s) > maxLatencySamples {
		return append([]int64(nil), s[len(s)-maxLatencySamples:]...)
	}
	return s
}

// RecordValidation increments the outcome counter and appends the duration.
func (m *Metrics) RecordValidation(status string, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ValidationCount++
	m.ValidationLatencies = capLatencies(append(m.ValidationLatencies, int64(duration)))
	switch status {
	case "BLOCK", "block":
		m.BlockedCount++
	case "WARN", "warn":
		m.WarningCount++
	case "ERROR", "error":
		m.ErrorCount++
	case "PASS", "pass":
		m.PassCount++
	}
}

// RecordCheckLatency appends duration to the named check's latency history.
func (m *Metrics) RecordCheckLatency(checkName string, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PerCheckLatencies[checkName] = capLatencies(append(m.PerCheckLatencies[checkName], int64(duration)))
}

// RecordSandboxLatency appends duration to the sandbox latency history.
func (m *Metrics) RecordSandboxLatency(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SandboxLatencies = capLatencies(append(m.SandboxLatencies, int64(duration)))
}

// RecordRepairAttempt increments the repair counter; success also increments successes.
func (m *Metrics) RecordRepairAttempt(success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RepairAttempts++
	if success {
		m.RepairSuccesses++
	}
}

// RecordOverride increments the false-positive suppression counter.
func (m *Metrics) RecordOverride() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.FalsePositiveOverrides++
}

// Stats holds computed percentile and rate statistics from accumulated metrics.
type Stats struct {
	ValidationCount        int                `json:"validation_count"`
	BlockedCount           int                `json:"blocked_count"`
	WarningCount           int                `json:"warning_count"`
	ErrorCount             int                `json:"error_count"`
	PassCount              int                `json:"pass_count"`
	ValidationP50Ms        float64            `json:"validation_p50_ms"`
	ValidationP95Ms        float64            `json:"validation_p95_ms"`
	SandboxP50Ms           float64            `json:"sandbox_p50_ms"`
	SandboxP95Ms           float64            `json:"sandbox_p95_ms"`
	PerCheckP50Ms          map[string]float64 `json:"per_check_p50_ms"`
	PerCheckP95Ms          map[string]float64 `json:"per_check_p95_ms"`
	RepairSuccessRate      float64            `json:"repair_success_rate"`
	FalsePositiveOverrides int                `json:"false_positive_overrides"`
}

// ComputeStats computes percentiles and rates from accumulated samples.
func (m *Metrics) ComputeStats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := Stats{
		ValidationCount:        m.ValidationCount,
		BlockedCount:           m.BlockedCount,
		WarningCount:           m.WarningCount,
		ErrorCount:             m.ErrorCount,
		PassCount:              m.PassCount,
		FalsePositiveOverrides: m.FalsePositiveOverrides,
		PerCheckP50Ms:          make(map[string]float64),
		PerCheckP95Ms:          make(map[string]float64),
	}

	s.ValidationP50Ms = percentileMs(m.ValidationLatencies, 50)
	s.ValidationP95Ms = percentileMs(m.ValidationLatencies, 95)
	s.SandboxP50Ms = percentileMs(m.SandboxLatencies, 50)
	s.SandboxP95Ms = percentileMs(m.SandboxLatencies, 95)

	for name, latencies := range m.PerCheckLatencies {
		s.PerCheckP50Ms[name] = percentileMs(latencies, 50)
		s.PerCheckP95Ms[name] = percentileMs(latencies, 95)
	}

	if m.RepairAttempts > 0 {
		s.RepairSuccessRate = float64(m.RepairSuccesses) / float64(m.RepairAttempts)
	}

	return s
}

// percentileMs computes the p-th percentile of latencies (in nanoseconds)
// and returns it in milliseconds.
func percentileMs(latencies []int64, p float64) float64 {
	if len(latencies) == 0 {
		return 0
	}
	// Copy and sort.
	sorted := make([]int64, len(latencies))
	copy(sorted, latencies)
	sortInt64s(sorted)

	idx := int(float64(len(sorted)-1) * p / 100)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return float64(sorted[idx]) / 1e6 // ns → ms
}

// sortInt64s sorts a slice of int64 in place (ascending).
func sortInt64s(s []int64) {
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
}

// DefaultPath returns the default metrics file path for a repo root.
func DefaultPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".blueprint", "metrics.json")
}
