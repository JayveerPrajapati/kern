package mcp

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// e2ePersona names the caller perspective a test is modeling. Keeping this
// explicit in the harness makes it easy to report which personas a tool has
// actually been exercised from, not just which nominal test exists.
type e2ePersona string

const (
	personaDeveloper      e2ePersona = "developer"
	personaRefactorer     e2ePersona = "refactoring engineer"
	personaSecurity       e2ePersona = "security auditor"
	personaSRE            e2ePersona = "SRE"
	personaAgent          e2ePersona = "AI agent"
	personaArchitect      e2ePersona = "architect"
	personaReleaseManager e2ePersona = "release manager"
	personaDocsWriter     e2ePersona = "documentation writer"
)

// e2eMetric captures a single end-to-end result observation. It is used by
// load tests and future reporting so the same evidence can be attached to
// every tool result rather than being reconstructed from raw strings later.
type e2eMetric struct {
	Name       string
	Persona    e2ePersona
	Duration   time.Duration
	TextBytes  int
	Provenance bool
}

// e2eLoadCase describes one load-test slice for a single tool.
type e2eLoadCase struct {
	Name        string
	Tool        string
	Persona     e2ePersona
	Requests    int
	Concurrency int
	Timeout     time.Duration
	Cold        bool
	Warm        bool
}

// e2eContract is a declarative result contract used by future per-tool tests.
// It deliberately keeps the success path and failure path in one struct so
// the expected shape of a tool is visible at the call site.
type e2eContract struct {
	Success          bool
	MustContain      []string
	MustNotContain   []string
	WantProvenance   bool
	ForbidProvenance bool
}

// e2eToolJSON returns the JSON-RPC result object, not just the text field.
func e2eToolJSON(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %#v", resp)
	}
	return result
}

// assertE2EContract validates a tool response against a declarative success
// or failure contract, including provenance expectations.
func assertE2EContract(t *testing.T, name string, resp map[string]any, contract e2eContract) {
	t.Helper()

	if contract.Success {
		assertE2EResult(t, name, resp, e2eAssertion{
			MustContain:    contract.MustContain,
			MustNotContain: contract.MustNotContain,
		})
		prov := e2eProvenance(resp)
		if contract.WantProvenance && prov == nil {
			t.Fatalf("%s: expected structured provenance", name)
		}
		if contract.ForbidProvenance && prov != nil {
			t.Fatalf("%s: unexpected structured provenance: %#v", name, prov)
		}
		return
	}

	text := assertE2EToolError(t, name, resp, contract.MustContain...)
	for _, banned := range contract.MustNotContain {
		if strings.Contains(text, banned) {
			t.Errorf("%s: error text must not contain %q", name, banned)
		}
	}
}

// assertNoGoroutineLeak compares the goroutine count after a test body with
// the count captured before it. A small tolerance keeps this stable when the
// runtime or test framework legitimately schedules one or two background
// goroutines.
func assertNoGoroutineLeak(t *testing.T, before int) {
	t.Helper()
	after := runtime.NumGoroutine()
	if after > before+2 {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
