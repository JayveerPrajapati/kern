// Package resilience implements a fault-injection framework for testing
// how target code handles failures (timeouts, 500s, dependency down).
// Each scenario declares which ecosystem it applies to and implements
// the Scenario interface.
package resilience

import (
	"context"
)

// CheckName is the stable identifier for the resilience check result.
const CheckName = "resilience:scenarios"

// Scenario is the fault-injection contract. Each scenario simulates a
// resilience failure to verify the target code handles it gracefully.
type Scenario interface {
	ID() string
	Applicable(info RepoInfo) bool
	Prepare(ctx context.Context) error
	Run(ctx context.Context, target Sandbox) Result
	Cleanup(ctx context.Context) error
}

// RepoInfo describes the repository being tested, for Applicable() checks.
type RepoInfo struct {
	Root       string   // absolute repo path
	Language   string   // "go", "python", "typescript", etc.
	ModulePath string   // Go module path (if Go)
	Imports    []string // key imports (e.g. "net/http", "database/sql")
	HasGoMod   bool
	HasTests   bool
	HasShell   bool // true when the repo contains .sh scripts (second ecosystem, B5)
}

// Sandbox is the execution boundary for scenarios. This is satisfied by
// blueprint's sandbox.Run (Phase 8), but abstracted so scenarios don't
// depend on the sandbox implementation details.
type Sandbox interface {
	// Run executes a command in isolation and returns the result.
	Run(ctx context.Context, repoRoot string, command []string) SandboxResult
}

// SandboxResult is the outcome of a sandboxed command.
type SandboxResult struct {
	ExitCode int
	Ok       bool
	Stdout   string
	Stderr   string
	Output   string // combined stdout+stderr for convenience
	TimedOut bool
	Duration int64 // milliseconds
}

// Result is the outcome of a Scenario.Run.
type Result struct {
	ScenarioID    string // the scenario that ran
	Passed        bool   // true = code handled the fault gracefully
	ExitCode      int    // exit code of the code under test
	Output        string // truncated output
	FaultInjected bool   // true = the fault was actually injected
	Detail        string // human-readable explanation
}

// Registry holds registered scenario providers, keyed by ID.
type Registry struct {
	scenarios map[string]Scenario
}

// NewRegistry returns an empty scenario registry.
func NewRegistry() *Registry {
	return &Registry{scenarios: make(map[string]Scenario)}
}

// Register adds a scenario to the registry. Panics on duplicate ID.
func (r *Registry) Register(s Scenario) {
	if _, exists := r.scenarios[s.ID()]; exists {
		panic("resilience: duplicate scenario ID: " + s.ID())
	}
	r.scenarios[s.ID()] = s
}

// Applicable returns scenarios applicable to the given repo.
func (r *Registry) Applicable(info RepoInfo) []Scenario {
	var result []Scenario
	for _, s := range r.scenarios {
		if s.Applicable(info) {
			result = append(result, s)
		}
	}
	return result
}

// Get returns a scenario by ID, or nil if not found.
func (r *Registry) Get(id string) Scenario {
	return r.scenarios[id]
}

// All returns every registered scenario.
func (r *Registry) All() []Scenario {
	result := make([]Scenario, 0, len(r.scenarios))
	for _, s := range r.scenarios {
		result = append(result, s)
	}
	return result
}
