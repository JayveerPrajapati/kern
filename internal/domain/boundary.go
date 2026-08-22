package domain

import "time"

// TaskBoundary defines the allowed and denied file paths for a task. Strict
// Plan Phase 7 P0: task-scoped boundaries.
type TaskBoundary struct {
	TaskID        string   `json:"task_id"`
	AllowedPaths  []string `json:"allowed_paths"`  // paths the task may read/write
	DeniedPaths   []string `json:"denied_paths"`   // paths the task must not touch
	AllowedEnvs   []string `json:"allowed_envs"`   // environments the task may operate in
}

// CheckPath reports whether a path is within the task's boundaries. A path is
// allowed if it matches an AllowedPaths prefix AND does not match any
// DeniedPaths prefix. If AllowedPaths is empty, all paths are allowed (except
// denied).
func (b TaskBoundary) CheckPath(path string) bool {
	for _, denied := range b.DeniedPaths {
		if pathMatches(path, denied) {
			return false
		}
	}
	if len(b.AllowedPaths) == 0 {
		return true // no allowlist = allow all (except denied)
	}
	for _, allowed := range b.AllowedPaths {
		if pathMatches(path, allowed) {
			return true
		}
	}
	return false
}

// pathMatches reports whether path starts with prefix (treating prefix as a
// directory prefix or exact match).
func pathMatches(path, prefix string) bool {
	if path == prefix {
		return true
	}
	if len(path) > len(prefix) && path[:len(prefix)] == prefix && (prefix[len(prefix)-1] == '/' || path[len(prefix)] == '/') {
		return true
	}
	return false
}

// SafetyBudget tracks resource limits for a task. Strict Plan Phase 7 P1.
// Exceeding any limit should cause the system to PAUSE (not proceed).
type SafetyBudget struct {
	MaxFiles        int           `json:"max_files"`
	MaxServices     int           `json:"max_services"`
	MaxRisk         RiskLevel     `json:"max_risk"`
	MaxToolCalls    int           `json:"max_tool_calls"`
	MaxExternalCalls int          `json:"max_external_calls"`
	MaxTokens       int           `json:"max_tokens"`
	MaxCost         float64       `json:"max_cost"`
	MaxRuntime      time.Duration `json:"max_runtime"`
	AllowedEnvs     []string      `json:"allowed_envs"`

	// Current usage (tracked at runtime).
	filesUsed        int
	toolCallsUsed    int
	externalCallsUsed int
	tokensUsed        int
	costUsed          float64
	runtimeStart      time.Time
}

// DefaultSafetyBudget returns a conservative default budget.
func DefaultSafetyBudget() SafetyBudget {
	return SafetyBudget{
		MaxFiles:         50,
		MaxServices:      5,
		MaxRisk:          RiskHigh,
		MaxToolCalls:     100,
		MaxExternalCalls: 10,
		MaxTokens:        500000,
		MaxCost:          10.0,
		MaxRuntime:       30 * 60 * time.Second, // 30 minutes
		AllowedEnvs:      []string{"development", "staging"},
	}
}

// TrackToolCall increments the tool-call counter.
func (b *SafetyBudget) TrackToolCall() {
	b.toolCallsUsed++
}

// TrackFile increments the file-change counter.
func (b *SafetyBudget) TrackFile() {
	b.filesUsed++
}

// TrackTokens adds to the token counter.
func (b *SafetyBudget) TrackTokens(n int) {
	b.tokensUsed += n
}

// Exceeded reports whether any budget limit has been exceeded, and returns a
// description of the first limit that was exceeded.
func (b *SafetyBudget) Exceeded() (bool, string) {
	if b.MaxToolCalls > 0 && b.toolCallsUsed >= b.MaxToolCalls {
		return true, "max_tool_calls exceeded"
	}
	if b.MaxFiles > 0 && b.filesUsed >= b.MaxFiles {
		return true, "max_files exceeded"
	}
	if b.MaxTokens > 0 && b.tokensUsed >= b.MaxTokens {
		return true, "max_tokens exceeded"
	}
	if b.MaxCost > 0 && b.costUsed >= b.MaxCost {
		return true, "max_cost exceeded"
	}
	if b.MaxRuntime > 0 && !b.runtimeStart.IsZero() {
		if time.Since(b.runtimeStart) > b.MaxRuntime {
			return true, "max_runtime exceeded"
		}
	}
	return false, ""
}

// Start begins the runtime clock.
func (b *SafetyBudget) Start() {
	b.runtimeStart = time.Now()
}
