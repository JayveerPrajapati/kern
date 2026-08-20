package intel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestNearExpandsBothDirections(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := Near(ix, "Public", 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if nodes[0].Symbol != "Public" || nodes[0].Depth != 0 {
		t.Errorf("root must be Public at depth 0, got %+v", nodes[0])
	}
	names := map[string]bool{}
	for _, n := range nodes {
		names[n.Symbol] = true
	}
	// One hop out, both directions: Caller calls Public; Public calls inner.
	if !names["Caller"] || !names["inner"] {
		t.Errorf("expected Caller (caller) and inner (callee) at depth 1, got %v", names)
	}
	for _, n := range nodes {
		if n.Symbol == "Caller" && n.Dir != "caller" {
			t.Errorf("Caller should be marked a caller of Public, got %s", n.Dir)
		}
		if n.Symbol == "inner" && n.Dir != "callee" {
			t.Errorf("inner should be marked a callee of Public, got %s", n.Dir)
		}
	}
}

func TestNearDepthAndCap(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Depth 0 stops at the root.
	nodes, err := Near(ix, "Public", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Errorf("depth 0 must return only the root, got %d nodes", len(nodes))
	}
	// Cap the expansion.
	nodes, err = Near(ix, "Public", 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) > 2 {
		t.Errorf("maxNodes=2 violated, got %d nodes", len(nodes))
	}
	// Unknown symbols error out.
	if _, err := Near(ix, "Nope", 1, 100); err == nil {
		t.Error("expected an error for an unknown symbol")
	}
}

func TestRenderNearIsTree(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := Near(ix, "Public", 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := RenderNear(ix, nodes)
	if !strings.Contains(out, "Public") || !strings.Contains(out, "lib/lib.go") {
		t.Errorf("render should carry the root and file:line, got:\n%s", out)
	}
	if !strings.Contains(out, "↑") || !strings.Contains(out, "↓") {
		t.Errorf("render should show caller/callee arrows, got:\n%s", out)
	}
}

func TestProbeResolvesTaskSymbols(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
		"lib/lib_test.go":  srcTest,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := Probe(ix, "Investigate why Caller calling Public returns empty", 4000)
	if len(r.Anchors) < 2 {
		t.Fatalf("expected anchors for Caller and Public, got %+v", r.Anchors)
	}
	var caller, pub *ProbeAnchor
	for i := range r.Anchors {
		switch r.Anchors[i].Resolved {
		case "Caller":
			caller = &r.Anchors[i]
		case "Public":
			pub = &r.Anchors[i]
		}
	}
	if caller == nil || pub == nil {
		t.Fatalf("expected Caller and Public anchors, got %+v", r.Anchors)
	}
	if !contains(pub.Callers, "Caller") {
		t.Errorf("Public callers should include Caller, got %v", pub.Callers)
	}
	if !contains(pub.Callees, "inner") {
		t.Errorf("Public callees should include inner, got %v", pub.Callees)
	}
	if !contains(pub.Tests, "TestPublic") {
		t.Errorf("Public tests should include TestPublic, got %v", pub.Tests)
	}
}

func TestProbeBudgetCap(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := Probe(ix, "Caller Public inner Deep", 5)
	if !r.Truncated {
		t.Errorf("tiny budget should mark the bundle truncated, got tokens=%d", r.Tokens)
	}
	fitted := FitProbe(RenderProbe(r), r.MaxTokens)
	if len(fitted) > len(RenderProbe(r)) {
		t.Error("FitProbe must not grow the bundle")
	}
	// W2-24: the report payload itself (what --json serializes) must fit the
	// budget, not just the text view. A 5-token budget sits below the report
	// skeleton (task line + anchor headers), so use a reachable budget.
	r2 := Probe(ix, "Caller Public inner Deep", 120)
	if _, err := json.Marshal(r2); err != nil {
		t.Fatalf("report must stay JSON-serializable after trimming: %v", err)
	}
	if r2.Tokens > 120 {
		t.Errorf("report payload must be trimmed to the budget, tokens=%d > 120", r2.Tokens)
	}
}

func TestProbeNoAnchors(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go": srcLib,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := Probe(ix, "this task mentions nothing relevant", 4000)
	if len(r.Anchors) != 0 {
		t.Errorf("expected no anchors, got %+v", r.Anchors)
	}
}

// Probe fuzzy fallback regression: when a natural-language task contains no
// exact identifiers, the probe should still resolve via keyword matching
// against symbol names. "decommission a network service" should match
// symbols containing "network" or "service" in their names.
func TestProbeFuzzyKeywordFallback(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "decommissionNetworkService", File: "net.go", Line: 10},
			{Kind: "func", Name: "NetworkClient", File: "client.go", Line: 20},
			{Kind: "func", Name: "ServiceRegistry", File: "registry.go", Line: 30},
			{Kind: "func", Name: "unrelated", File: "other.go", Line: 40},
		},
		Calls:   map[string][]string{},
		Callers: map[string][]string{},
	}
	r := Probe(ix, "decommission a network service", 4000)
	if len(r.Anchors) == 0 {
		t.Fatal("expected fuzzy-matched anchors for natural-language task, got none")
	}
	// At least one anchor should relate to network/service.
	found := false
	for _, a := range r.Anchors {
		name := strings.ToLower(a.Name)
		if strings.Contains(name, "network") || strings.Contains(name, "service") || strings.Contains(name, "decommission") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one network/service anchor, got %+v", r.Anchors)
	}
}

func TestTraceOverlaysHotSymbols(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
		"lib/lib_test.go":  srcTest,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	trace := "github.com/acme/app/lib.Public(0x1)\n\t/path/lib.go:29 +0x12\nPublic(\nCaller(\n"
	r := Trace(ix, trace, "test.trace", 0)
	if r.Frames != 4 {
		t.Errorf("expected 4 frames, got %d", r.Frames)
	}
	if len(r.Hot) < 2 {
		t.Fatalf("expected Public and Caller to resolve, got %+v", r.Hot)
	}
	top := r.Hot[0]
	if top.Symbol != "Public" {
		t.Errorf("Public should be hottest, got %s", top.Symbol)
	}
	if top.Hits != 2 {
		t.Errorf("Public should have 2 hits (qualified + call site), got %d", top.Hits)
	}
	if !top.Tested {
		t.Error("Public is exercised by TestPublic and should be marked tested")
	}
	if top.Blast < 1 {
		t.Errorf("Public should have a non-zero blast radius, got %d", top.Blast)
	}
}

func TestTraceUnresolved(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go": srcLib,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := Trace(ix, "fmt.Println(1)\nos.Exit(0)\n", "", 0)
	if len(r.Hot) != 0 {
		t.Errorf("stdlib-only trace should resolve nothing, got %+v", r.Hot)
	}
}

func TestGuardRejectsForbiddenEdge(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := &Boundaries{Rules: []BoundaryRule{{From: "client", To: "lib", Action: "forbid"}}}
	violations := CheckBoundaries(ix, b, []string{"client/client.go"})
	// Caller calls lib.Public and lib.UntestedHot, both crossing the same
	// client->lib boundary, so they collapse into one evidence-carrying
	// violation for the file pair.
	if len(violations) != 1 {
		t.Fatalf("expected 1 collapsed violation, got %+v", violations)
	}
	v := violations[0]
	if v.CallerFile != "client/client.go" || v.CalleeFile != "lib/lib.go" {
		t.Errorf("wrong edge evidence: %+v", v)
	}
	if v.Symbol != "Public" {
		t.Errorf("expected Public as the offending symbol, got %s", v.Symbol)
	}
	if !strings.Contains(RenderViolations(violations), "REJECT") {
		t.Error("render should say REJECT")
	}
}

func TestGuardAllowOverridesForbid(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := &Boundaries{Rules: []BoundaryRule{
		{From: "client", To: "lib", Action: "forbid"},
		{From: "client", To: "lib", Action: "allow"},
	}}
	if v := CheckBoundaries(ix, b, []string{"client/client.go"}); len(v) != 0 {
		t.Errorf("allow rule must override forbid, got %+v", v)
	}
}

func TestGuardImportLevelAndPass(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
		"client/imports.go": `package client

import "lib"

func Touch() {}`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// No rules -> PASS.
	if v := CheckBoundaries(ix, &Boundaries{}, []string{"client/client.go"}); len(v) != 0 {
		t.Errorf("no rules must pass, got %+v", v)
	}
	// A file that only imports the forbidden package (no call) is still caught.
	b := &Boundaries{Rules: []BoundaryRule{{From: "client", To: "lib", Action: "forbid"}}}
	v := CheckBoundaries(ix, b, []string{"client/imports.go"})
	if len(v) != 1 {
		t.Fatalf("import-level crossing should be flagged, got %+v", v)
	}
	if v[0].CallerFile != "client/imports.go" {
		t.Errorf("wrong caller file: %+v", v[0])
	}
}

func TestGuardLoadAndInit(t *testing.T) {
	dir := writeTree(t, map[string]string{})
	if err := InitBoundaries(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".kern", "boundaries.json")); err != nil {
		t.Fatalf("init should write boundaries.json: %v", err)
	}
	b, err := LoadBoundaries(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Rules) == 0 {
		t.Error("starter template should contain a rule")
	}
}
