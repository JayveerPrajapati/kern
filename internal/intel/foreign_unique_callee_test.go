package intel

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// foreignUniqueCalleeIndex models the parity regression (Wave 1): the Go
// stdlib call http.DefaultClient.Do(req) is recorded with the raw callee key
// "http.DefaultClient.Do" whose bare name "Do" is UNIQUE in the project
// (internal/mcp/highlevel). The graph's unique-simple-name resolution
// (resolveNodeID case 1 — "resolves ANY qualifier for a unique name") used to
// attribute every caller of that key to the project's Do, so impact listed
// the test caller while explore (CallersFor, which skips keys whose first
// qualifier is a foreign import base) did not — TestImpactExploreCallerParityBroad
// broke once the sample began to include highlevel.Do.
func foreignUniqueCalleeIndex() *index.Index {
	return &index.Index{
		Root: "/fu",
		Symbols: []index.Symbol{
			sym("func", "Do", "mcp/highlevel.go", 1),
			sym("func", "TestV1EventsStreamSSE", "web/sse_test.go", 1),
		},
		Calls: map[string][]index.CallEdge{
			"TestV1EventsStreamSSE": {{Target: "http.DefaultClient.Do", Confidence: index.ConfidenceHigh}},
		},
		Callers: map[string][]string{
			"http.DefaultClient.Do": {"TestV1EventsStreamSSE"},
		},
		Pkgs: map[string]*index.Pkg{
			"mcp": {Name: "mcp", Path: "mcp", Imports: []index.ImportEdge{}, Files: []string{"mcp/highlevel.go"}, Lang: "go"},
			"web": {Name: "web", Path: "web", Imports: []index.ImportEdge{{Path: "net/http", Confidence: index.ConfidenceHigh}}, Files: []string{"web/sse_test.go"}, Lang: "go"},
		},
		UpdatedAt: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC),
	}
}

// TestDirectCallersNamesForeignUniqueCalleeNotAttributed pins the fix: a
// foreign-qualified callee ("http.DefaultClient.Do") must never attribute its
// callers to a local symbol whose bare name happens to be unique — the stdlib
// http client method is not the project's Do.
func TestDirectCallersNamesForeignUniqueCalleeNotAttributed(t *testing.T) {
	g := FromIndex(foreignUniqueCalleeIndex())
	got := g.DirectCallersNames("mcp.Do", false)
	if len(got) != 0 {
		t.Fatalf("DirectCallersNames(mcp.Do) = %v; foreign callee http.DefaultClient.Do must not attribute the test caller", got)
	}
	// The same edge's callee rendering must stay raw, not resolve to the
	// project Do (WhatDoesXDependOnNames parity: foreign callees survive).
	dep := g.DirectDependsOnNames("web.TestV1EventsStreamSSE", false)
	if len(dep) != 1 || dep[0] != "http.DefaultClient.Do" {
		t.Fatalf("DirectDependsOnNames(TestV1EventsStreamSSE) = %v; want the raw foreign callee", dep)
	}
}

// bareGoMethodCalleeIndex models the second parity regression: a Go method
// body calls a LOCAL CLOSURE named "add"; the extractor records the bare
// callee key "add", which collides with the receiver-qualified method
// PlaybookStore.add in an imported package. Under Go scoping a bare reference
// never names a method, so the caller-anchored import-based fallback must not
// attribute the closure call to the method (explore's CallersFor never does).
func bareGoMethodCalleeIndex() *index.Index {
	return &index.Index{
		Root: "/bm",
		Symbols: []index.Symbol{
			{Kind: "method", Name: "add", Receiver: "PlaybookStore", File: "incident/playbook.go", Line: 1, Lang: "go"},
			{Kind: "method", Name: "impactCitedFiles", Receiver: "TaskService", File: "tasklife/task_analysis.go", Line: 1, Lang: "go"},
		},
		Calls: map[string][]index.CallEdge{
			"TaskService.impactCitedFiles": {{Target: "add", Confidence: index.ConfidenceHigh}},
		},
		Callers: map[string][]string{
			"add": {"TaskService.impactCitedFiles"},
		},
		Pkgs: map[string]*index.Pkg{
			"incident": {Name: "incident", Path: "incident", Imports: []index.ImportEdge{}, Files: []string{"incident/playbook.go"}, Lang: "go"},
			"tasklife": {Name: "tasklife", Path: "tasklife", Imports: []index.ImportEdge{{Path: "example.com/mod/incident", Confidence: index.ConfidenceHigh}}, Files: []string{"tasklife/task_analysis.go"}, Lang: "go"},
		},
		UpdatedAt: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC),
	}
}

// TestDirectCallersNamesBareGoClosureNotAttributedToMethod pins the fix: the
// bare "add" call inside TaskService.impactCitedFiles is a local closure, not
// a call to the imported package's PlaybookStore.add — Go scoping never binds
// a bare reference to a method.
func TestDirectCallersNamesBareGoClosureNotAttributedToMethod(t *testing.T) {
	g := FromIndex(bareGoMethodCalleeIndex())
	got := g.DirectCallersNames("incident.PlaybookStore.add", false)
	if len(got) != 0 {
		t.Fatalf("DirectCallersNames(PlaybookStore.add) = %v; bare closure call must not attribute a Go method caller", got)
	}
}
