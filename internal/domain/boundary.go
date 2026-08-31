package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// TaskBoundary defines the allowed and denied file paths for a task. Strict
// Plan task-scoped boundaries.
type TaskBoundary struct {
	TaskID       string   `json:"task_id"`
	AllowedPaths []string `json:"allowed_paths"` // paths the task may read/write
	DeniedPaths  []string `json:"denied_paths"`  // paths the task must not touch
	AllowedEnvs  []string `json:"allowed_envs"`  // environments the task may operate in
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

// SafetyBudget tracks resource limits for a task. .
// Exceeding any limit should cause the system to PAUSE (not proceed).
type SafetyBudget struct {
	MaxFiles         int           `json:"max_files"`
	MaxServices      int           `json:"max_services"`
	MaxRisk          RiskLevel     `json:"max_risk"`
	MaxToolCalls     int           `json:"max_tool_calls"`
	MaxExternalCalls int           `json:"max_external_calls"`
	MaxTokens        int           `json:"max_tokens"`
	MaxCost          float64       `json:"max_cost"`
	MaxRuntime       time.Duration `json:"max_runtime"`
	AllowedEnvs      []string      `json:"allowed_envs"`

	// Full budget dimensions.
	// MaxToolCallsByKind caps tool-call volume per tool kind (e.g. "exec"),
	// independently of the total MaxToolCalls counter. A kind with a positive
	// limit is enforced by Exceeded().
	MaxToolCallsByKind map[string]int `json:"max_tool_calls_by_kind,omitempty"`
	// CurrentEnv is the environment the task operates in (e.g. "production").
	// When non-empty and not in AllowedEnvs, Exceeded() reports an exceeded env.
	CurrentEnv string `json:"current_env,omitempty"`

	// Current usage (tracked at runtime).
	filesUsed         int
	toolCallsUsed     int
	externalCallsUsed int
	tokensUsed        int
	costUsed          float64
	runtimeStart      time.Time
	toolCallsByKind   map[string]int
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

// TrackToolCallKind increments the per-tool-kind counter for the given kind.
// It is the per-kind counterpart to TrackToolCall: callers can cap e.g. "exec"
// calls independently of the total tool-call budget.
func (b *SafetyBudget) TrackToolCallKind(kind string) {
	if kind == "" {
		return
	}
	if b.toolCallsByKind == nil {
		b.toolCallsByKind = map[string]int{}
	}
	b.toolCallsByKind[kind]++
}

// TrackEnv sets the environment the task operates in. Exceeded() then reports
// an exceeded env when CurrentEnv is non-empty and not in AllowedEnvs.
func (b *SafetyBudget) TrackEnv(env string) {
	b.CurrentEnv = env
}

// ToolCallsUsed reports the total number of tracked tool calls.
func (b *SafetyBudget) ToolCallsUsed() int {
	return b.toolCallsUsed
}

// Reset zeroes all runtime usage counters so the budget can be reused for a
// new run. It clears the total and per-kind call counts, file/token/cost/runtime
// usage and the current environment, but preserves the configured limits.
func (b *SafetyBudget) Reset() {
	b.filesUsed = 0
	b.toolCallsUsed = 0
	b.externalCallsUsed = 0
	b.tokensUsed = 0
	b.costUsed = 0
	b.runtimeStart = time.Time{}
	b.toolCallsByKind = nil
	b.CurrentEnv = ""
}

// Exceeded reports whether any budget limit has been exceeded, and returns a
// description of the first limit that was exceeded.
func (b *SafetyBudget) Exceeded() (bool, string) {
	if b.MaxToolCalls > 0 && b.toolCallsUsed >= b.MaxToolCalls {
		return true, "max_tool_calls exceeded"
	}
	if len(b.MaxToolCallsByKind) > 0 {
		kinds := make([]string, 0, len(b.MaxToolCallsByKind))
		for k := range b.MaxToolCallsByKind {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			if limit := b.MaxToolCallsByKind[kind]; limit > 0 && b.toolCallsByKind[kind] >= limit {
				return true, "max_tool_calls_by_kind exceeded: " + kind
			}
		}
	}
	if b.CurrentEnv != "" && len(b.AllowedEnvs) > 0 {
		allowed := false
		for _, e := range b.AllowedEnvs {
			if e == b.CurrentEnv {
				allowed = true
				break
			}
		}
		if !allowed {
			return true, "env not allowed: " + b.CurrentEnv
		}
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

// DenyReason is the explain-deny object returned by a gateway when a governed
// action is denied. : it carries structured, actionable
// detail about WHY the action was blocked so callers and the UI can explain the
// decision to a human without re-deriving it from an error string.
type DenyReason struct {
	// Stage is the enforcement stage that denied the action:
	// "boundary", "firewall", "budget", "precheck", "unknown".
	Stage string `json:"stage"`
	// AgentID / TaskID / Resource / Action identify the denied request.
	AgentID  string `json:"agent_id"`
	TaskID   string `json:"task_id"`
	Resource string `json:"resource"`
	Action   string `json:"action"`
	// Reason is a human-readable summary of why the action was denied.
	Reason string `json:"reason"`
	// Risk is the assessed risk of the denied action (zero when not assessed).
	Risk Risk `json:"risk"`
	// RequiredApproval is set when the action was denied because it needs
	// human approval (the approval id is available to be approved).
	RequiredApproval *Approval `json:"required_approval,omitempty"`
	// Policy is the policy rule that denied the action (e.g.
	// "scope.env", "firewall.permission", "safety_budget"). It lets callers
	// programmatically attribute a denial to a specific enforcement rule
	// rather than re-parsing the human-readable Reason.
	Policy string `json:"policy,omitempty"`
	// SafeAlternative is a suggested safe alternative action the caller can
	// take instead of the denied one, e.g. "narrow the change to an allowed
	// path" or "request an explicit approval".
	SafeAlternative string `json:"safe_alternative,omitempty"`
}

// GatewayDecision is the outcome of a gateway evaluation. :
// it unifies the ALLOW / DENY / PAUSE / APPROVAL_REQUIRED outcomes with the
// explain-deny object so dry runs and live calls share one result shape.
type GatewayDecision string

const (
	DecisionAllowed  GatewayDecision = "ALLOW"
	DecisionDenied   GatewayDecision = "DENY"
	DecisionPaused   GatewayDecision = "PAUSE"
	DecisionApproval GatewayDecision = "APPROVAL_REQUIRED"
)

// GatewayResult is the unified result of a governed tool-call evaluation. It is
// returned by both the live Evaluate and the DryRun path so the two stay
// consistent.
type GatewayResult struct {
	Decision GatewayDecision `json:"decision"`
	Allowed  bool            `json:"allowed"`
	Risk     Risk            `json:"risk"`
	Approval *Approval       `json:"approval,omitempty"`
	Deny     *DenyReason     `json:"deny,omitempty"`
	// Budget tracks the task's resource usage after the call (P7.3 unified
	// scoping exposes the budget alongside the decision).
	Budget *SafetyBudget `json:"budget,omitempty"`
}

// TaskScope unifies the three dimensions of task scoping: which paths,
// which services, and which environments a task may touch,
// plus its artifact/output scope. It is the single authoritative scope a
// governed task carries, replacing the need to thread boundary + env + artifact
// lists separately.
type TaskScope struct {
	TaskID      string   `json:"task_id"`
	Paths       []string `json:"paths"`        // allowed file paths (empty = all)
	DeniedPaths []string `json:"denied_paths"` // denied file paths
	Services    []string `json:"services"`     // allowed services (empty = all)
	Envs        []string `json:"envs"`         // allowed environments
	Artifacts   []string `json:"artifacts"`    // allowed artifact kinds
}

// CheckPath reports whether a path is within the unified task scope. It applies
// the same prefix matching as TaskBoundary.CheckPath, honoring DeniedPaths
// first, then the Paths allowlist.
func (s TaskScope) CheckPath(path string) bool {
	return (TaskBoundary{AllowedPaths: s.Paths, DeniedPaths: s.DeniedPaths}).CheckPath(path)
}

// CheckEnv reports whether an environment is in the unified scope. Empty Envs
// allows everything (backward-compatible).
func (s TaskScope) CheckEnv(env string) bool {
	if len(s.Envs) == 0 {
		return true
	}
	for _, e := range s.Envs {
		if e == env {
			return true
		}
	}
	return false
}

// CheckService reports whether a service is within the task's service scope.
// Empty Services allows every service (all allowed); otherwise the service must
// exactly match one of the allowed Services entries.
func (s TaskScope) CheckService(service string) bool {
	if len(s.Services) == 0 {
		return true
	}
	for _, svc := range s.Services {
		if svc == service {
			return true
		}
	}
	return false
}

// CheckArtifact reports whether an artifact kind is within the task's artifact
// scope. Empty Artifacts allows every artifact kind (all allowed); otherwise
// the kind must exactly match one of the allowed Artifacts entries.
func (s TaskScope) CheckArtifact(kind string) bool {
	if len(s.Artifacts) == 0 {
		return true
	}
	for _, a := range s.Artifacts {
		if a == kind {
			return true
		}
	}
	return false
}

// ValidatePatch checks every file path touched by a unified diff against the
// task scope ( task boundary enforced on execution). It extracts the
// a/... and b/... paths from each "diff --git"/"---"/"+++" header and requires
// each one to pass CheckPath. A patch that touches an out-of-scope path (a
// denied path, or a path outside the allowed set) is rejected BEFORE it is
// applied, so a controlled action cannot bypass task-scoped governance.
// An empty scope (no Paths/DeniedPaths) allows everything (backward-compatible).
func (s TaskScope) ValidatePatch(patch string) error {
	paths := patchTouchedPaths(patch)
	if len(s.Paths) == 0 && len(s.DeniedPaths) == 0 {
		return nil // unrestricted scope: nothing to enforce
	}
	for _, p := range paths {
		if !s.CheckPath(p) {
			return fmt.Errorf("task scope %s: patch touches %q, which is outside the allowed boundary (allowed=%v denied=%v)",
				s.TaskID, p, s.Paths, s.DeniedPaths)
		}
	}
	return nil
}

// patchTouchedPaths returns the distinct file paths referenced by a unified
// diff, with the a/ b/ +++/ --- markers stripped and leading slashes removed.
func patchTouchedPaths(patch string) []string {
	seen := map[string]bool{}
	var out []string
	for _, ln := range strings.Split(patch, "\n") {
		trimmed := strings.TrimPrefix(ln, "\t")
		var p string
		switch {
		case strings.HasPrefix(trimmed, "diff --git "):
			// "diff --git a/x b/x" → the second token's b/ path.
			parts := strings.Fields(trimmed)
			if len(parts) >= 4 {
				p = strings.TrimPrefix(parts[3], "b/")
			}
		case strings.HasPrefix(trimmed, "+++ "):
			p = strings.TrimPrefix(trimmed[4:], "b/")
		case strings.HasPrefix(trimmed, "--- "):
			p = strings.TrimPrefix(trimmed[4:], "a/")
		}
		if p == "" || p == "/dev/null" {
			continue
		}
		p = strings.TrimPrefix(p, "/")
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
