package gov

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
)

// buildTestIndex builds a two-package index on a temp root: package a at the
// root (a func + a method), package b in a subdirectory (one func per Go
// directory rule). Package-level funcs resolve as bare names ("Alpha");
// methods carry receiver-qualified names ("Greeter.Greet") — matching
// index.Symbol.FullName semantics the governor filters on.
func buildTestIndex(t *testing.T) (*index.Index, string) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"a.go":   "package a\n\nfunc Alpha() {}\n\nfunc Beta() {}\n\ntype Greeter struct{}\n\nfunc (g Greeter) Greet() {}\n",
		"b/b.go": "package b\n\nfunc Gamma() {}\n",
	}
	for name, body := range files {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	return ix, root
}

func TestTaskScopeFromArgs(t *testing.T) {
	args := map[string]any{
		"scope": map[string]any{
			"paths":        []any{"src/a", "src/b"},
			"denied_paths": []any{"src/b/secret"},
			"services":     []any{"api"},
			"envs":         []any{"prod"},
			"artifacts":    []any{"bin/kern"},
		},
	}
	sc := TaskScopeFromArgs(args, "task-1")
	if sc == nil {
		t.Fatal("expected scope, got nil")
	}
	if sc.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want task-1", sc.TaskID)
	}
	if len(sc.Paths) != 2 || sc.Paths[0] != "src/a" || sc.Paths[1] != "src/b" {
		t.Errorf("Paths = %v", sc.Paths)
	}
	if len(sc.DeniedPaths) != 1 || sc.DeniedPaths[0] != "src/b/secret" {
		t.Errorf("DeniedPaths = %v", sc.DeniedPaths)
	}
	if len(sc.Services) != 1 || sc.Services[0] != "api" {
		t.Errorf("Services = %v", sc.Services)
	}
	if len(sc.Envs) != 1 || sc.Envs[0] != "prod" {
		t.Errorf("Envs = %v", sc.Envs)
	}
	if len(sc.Artifacts) != 1 || sc.Artifacts[0] != "bin/kern" {
		t.Errorf("Artifacts = %v", sc.Artifacts)
	}
}

func TestTaskScopeFromArgsMissingOrMalformed(t *testing.T) {
	if sc := TaskScopeFromArgs(map[string]any{}, "t"); sc != nil {
		t.Errorf("missing scope: want nil, got %+v", sc)
	}
	if sc := TaskScopeFromArgs(map[string]any{"scope": nil}, "t"); sc != nil {
		t.Errorf("nil scope: want nil, got %+v", sc)
	}
	if sc := TaskScopeFromArgs(map[string]any{"scope": "not-a-map"}, "t"); sc != nil {
		t.Errorf("non-map scope: want nil, got %+v", sc)
	}
	// Wrong element types are skipped, not fatal.
	sc := TaskScopeFromArgs(map[string]any{"scope": map[string]any{"paths": "src"}}, "t")
	if sc == nil {
		t.Fatal("expected scope with skipped bad field")
	}
	if len(sc.Paths) != 0 {
		t.Errorf("Paths = %v, want empty (string not []any)", sc.Paths)
	}
}

func TestGovernorNameAllowed(t *testing.T) {
	ix, _ := buildTestIndex(t)
	g := &Governor{Allowed: map[string]bool{"Alpha": true, "Greeter.Greet": true}}

	// Exact match on the qualified set.
	if !g.NameAllowed(ix, "Alpha") {
		t.Error("exact allowed name must pass")
	}
	// Method name that resolves to an allowed definition (receiver-qualified).
	if !g.NameAllowed(ix, "Greet") {
		t.Error("name resolving to allowed definition must pass")
	}
	// Resolvable but not allowed.
	if g.NameAllowed(ix, "Gamma") {
		t.Error("name resolving outside the allowed set must be denied")
	}
	if g.NameAllowed(ix, "Beta") {
		t.Error("denied name must fail")
	}
	// Unresolvable (foreign/external) names are kept, mirroring the authz
	// edge filter's keep-unresolved-callees rule.
	if !g.NameAllowed(ix, "external.Thing") {
		t.Error("unresolvable external name must be kept")
	}
}

func TestGovernorFilterQualified(t *testing.T) {
	ix, _ := buildTestIndex(t)
	g := &Governor{Allowed: map[string]bool{"Alpha": true, "Greeter.Greet": true}}
	names := []string{"Alpha", "Gamma", "external.Thing", "Beta"}

	// keepUnresolved=false: resolvable-denied names and unresolvable names
	// are both dropped (callers/blast radius are always local).
	got := g.FilterQualified(ix, names, false)
	if len(got) != 1 || got[0] != "Alpha" {
		t.Errorf("keepUnresolved=false: got %v, want [Alpha]", got)
	}

	// keepUnresolved=true: unresolvable (external) names survive.
	got = g.FilterQualified(ix, names, true)
	want := []string{"Alpha", "external.Thing"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("keepUnresolved=true: got %v, want %v", got, want)
	}

	// Method resolution: unqualified method name maps to the allowed
	// receiver-qualified definition.
	got = g.FilterQualified(ix, []string{"Greet", "Gamma"}, false)
	if len(got) != 1 || got[0] != "Greet" {
		t.Errorf("method resolution: got %v, want [Greet]", got)
	}
}

func TestGovernorFilterList(t *testing.T) {
	ix, _ := buildTestIndex(t)
	g := &Governor{Allowed: map[string]bool{"Alpha": true}}

	got := g.FilterList(ix, "Alpha, Gamma, … , , external.Thing")
	want := []string{"Alpha", "external.Thing"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestGovernorFilterGraphText pins the graph-text filtering against the
// format the intel renderer actually emits (graphctx.go): a "graph" header,
// a 2-space root dispatch line, callers/callees sections with 2-space
// adjacency rows ("  Name [CONF] — file:line"), 4-space nested dispatch under
// a callee row, and a trailing community line. The community case must flush
// surviving adjacency rows BEFORE emitting its own line — otherwise the final
// flush rewrites the community member count (leaking the filtered count) and
// dumps callee rows after it (regression guard for the community-flush bug).
func TestGovernorFilterGraphText(t *testing.T) {
	ix, _ := buildTestIndex(t)
	g := &Governor{Allowed: map[string]bool{"Alpha": true}}
	in := strings.Join([]string{
		"graph Alpha (func) — a.go:1",
		"  dispatch (INFERRED): Alpha, Gamma",
		"callers (2):",
		"  Beta [HIGH] — a.go:4",
		"  external.Thing [MED] — ext.go:1",
		"callees (1):",
		"  Gamma [LOW] — b/b.go:2",
		"    dispatch (INFERRED): Gamma, external.Nested",
		"community (2 members): Alpha, Gamma",
	}, "\n")

	out := g.FilterGraphText(ix, in)
	for _, want := range []string{"callers (1):", "callees (1):", "community (1 members): Alpha"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Beta") || strings.Contains(out, "Gamma") {
		t.Errorf("filtered symbols leaked:\n%s", out)
	}
	if !strings.Contains(out, "external.Thing") || !strings.Contains(out, "external.Nested") {
		t.Errorf("unresolvable names must be kept:\n%s", out)
	}
	// The root dispatch line keeps only the allowed member.
	if !strings.Contains(out, "dispatch (INFERRED): Alpha") {
		t.Errorf("root dispatch not filtered to allowed member:\n%s", out)
	}
	// The community line must appear last, unmangled, with its member count.
	if strings.Contains(out, "community (") && !strings.HasSuffix(strings.TrimSpace(out), "community (1 members): Alpha") {
		t.Errorf("community line mangled or not last:\n%s", out)
	}
}

func TestGovernorFilterContextFooter(t *testing.T) {
	ix, _ := buildTestIndex(t)
	g := &Governor{Allowed: map[string]bool{"Alpha": true}}
	in := strings.Join([]string{
		"// verbatim source of Alpha",
		"callers: Gamma, external.Thing",
		"calls: Beta",
		"token savings: 42%",
	}, "\n")

	out := g.FilterContextFooter(ix, in)
	if !strings.Contains(out, "callers: external.Thing") {
		t.Errorf("callers line not filtered: %s", out)
	}
	if strings.Contains(out, "Gamma") || strings.Contains(out, "Beta") {
		t.Errorf("denied names leaked: %s", out)
	}
	// Source lines and summary pass through unchanged.
	if !strings.Contains(out, "verbatim source of Alpha") || !strings.Contains(out, "42%") {
		t.Errorf("source/summary mangled: %s", out)
	}
	if got := g.FilterContextFooter(ix, ""); got != "" {
		t.Errorf("empty input: got %q, want empty", got)
	}
}

func TestGraphSymbolsFromText(t *testing.T) {
	ix, _ := buildTestIndex(t)
	text := strings.Join([]string{
		"graph Alpha (func) — a.go:1",
		"  dispatch (INFERRED): Alpha, Gamma",
		"callers (1):",
		"  Beta [HIGH] — a.go:4",
		"community (1 members): Alpha",
	}, "\n")
	syms := GraphSymbolsFromText(ix, text)
	if len(syms) == 0 {
		t.Fatal("expected symbols, got none")
	}
	byName := map[string]bool{}
	for _, s := range syms {
		byName[s.Name] = true
	}
	for _, want := range []string{"Alpha", "Beta", "Gamma"} {
		if !byName[want] {
			t.Errorf("symbol %q not extracted; got %v", want, byName)
		}
	}
}

func TestSimpleNames(t *testing.T) {
	got := SimpleNames([]string{"b.Gamma", "a.Alpha", "Alpha", "a.Alpha"})
	want := []string{"Alpha", "Gamma"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v (dedupe+sort+qualifier-strip)", got, want)
	}
}

func TestSimpleName(t *testing.T) {
	if got := SimpleName("pkg.Func"); got != "Func" {
		t.Errorf("SimpleName(pkg.Func) = %q, want Func", got)
	}
	if got := SimpleName("Bare"); got != "Bare" {
		t.Errorf("SimpleName(Bare) = %q, want Bare", got)
	}
}

// The tests below are the newGovernor unit tests (moved from the mcp layer
// when the implementation merged into gov.New). The fixture-based governor
// integration tests stay in internal/mcp (govern_test.go).

func TestNewPermissiveRawMode(t *testing.T) {
	t.Setenv("KERN_MCP_PERMISSIVE", "1")
	ix, _ := buildTestIndex(t)
	g, err := New(context.Background(), map[string]any{"root": ix.Root}, ix)
	if err != nil {
		t.Fatalf("permissive mode must not error: %v", err)
	}
	if g != nil {
		t.Fatalf("permissive mode must return a nil governor, got %+v", g)
	}
}

// TestNewDefaultScoped: with no KERN_MCP_PERMISSIVE and no agent_id, the
// governor is non-nil, policySource is "default-scoped", and the allowed set
// is confined to the project root symbols (the whole fixture project is the
// cwd scope).
func TestNewDefaultScoped(t *testing.T) {
	governance.EnsureDefaultAgent() // the mcp server does this at init; these tests call New directly
	ix, root := buildTestIndex(t)
	g, err := New(context.Background(), map[string]any{"root": root}, ix)
	if err != nil {
		t.Fatalf("default-governed call must not error: %v", err)
	}
	if g == nil {
		t.Fatal("default path must return a non-nil governor")
	}
	if g.PolicySource != provenance.PolicySourceDefaultScoped {
		t.Fatalf("expected policySource %q, got %q", provenance.PolicySourceDefaultScoped, g.PolicySource)
	}
	// The whole project root is in scope: Alpha, Beta, Gamma, Greeter.Greet.
	for _, want := range []string{"Alpha", "Beta", "Gamma", "Greeter.Greet"} {
		if !g.Allowed[want] {
			t.Errorf("default scope must allow project symbol %s (allowed=%v)", want, g.Allowed)
		}
	}
}

// TestNewExplicitAgentTaskScope: an explicit agent_id + task + scope yields
// policySource "task-scope", with the denied path excluded from the allowed
// set.
func TestNewExplicitAgentTaskScope(t *testing.T) {
	agentID := "gov-new-" + t.Name()
	if err := governance.RegisterAgent(governance.NewAgent(agentID, "Gov New Test", "tester", []governance.Permission{
		{Resource: "context", Action: "read"},
	})); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	ix, root := buildTestIndex(t)
	args := map[string]any{
		"root":     root,
		"agent_id": agentID,
		"task":     "T-gov-new",
		"scope":    map[string]any{"denied_paths": []any{"b/"}},
	}
	g, err := New(context.Background(), args, ix)
	if err != nil {
		t.Fatalf("explicit governed call must not error: %v", err)
	}
	if g == nil {
		t.Fatal("explicit governed call must return a non-nil governor")
	}
	if g.PolicySource != provenance.PolicySourceTaskScope {
		t.Fatalf("expected policySource %q, got %q", provenance.PolicySourceTaskScope, g.PolicySource)
	}
	if !g.Allowed["Alpha"] || g.Allowed["Gamma"] {
		t.Fatalf("task-scope governor allowed set wrong: Alpha must be allowed and Gamma denied (allowed=%v)", g.Allowed)
	}
}

// TestNewUnknownAgentDenied: an explicit but unregistered agent_id fails
// closed with an error; the governor is still returned alongside the error so
// the denial is auditable.
func TestNewUnknownAgentDenied(t *testing.T) {
	ix, root := buildTestIndex(t)
	g, err := New(context.Background(), map[string]any{
		"root":     root,
		"agent_id": "no-such-agent-xyz",
	}, ix)
	if err == nil {
		t.Fatal("unknown agent must fail closed with an error")
	}
	if g == nil {
		t.Fatal("the governor must still be returned alongside the error for auditability")
	}
	if g.PolicySource == "" {
		t.Fatal("denied governor must carry its policy source")
	}
}

// TestNewGovContext pins the GovContext bundle factory (F18 Wave 3a): the
// returned bundle wires the injected governor factory and stamp func into the
// three hooks, and the composed stamp paths build the governed/raw provenance
// records exactly as the mcp layer did before the extraction.
func TestNewGovContext(t *testing.T) {
	ix, _ := buildTestIndex(t)
	commit := func(string) string { return "abc1234" }

	var stamped []*provenance.Provenance
	bundle := NewGovContext(ix, commit, func() (*Governor, error) {
		return &Governor{PolicySource: provenance.PolicySourceTaskScope, Proof: governance.AuthorizationProof{}}, nil
	}, func(p *provenance.Provenance) {
		stamped = append(stamped, p)
	})

	// Shape: all three hooks wired, governor passthrough intact.
	if bundle.NewGov == nil || bundle.StampGov == nil || bundle.StampRaw == nil {
		t.Fatal("bundle must wire all three hooks")
	}
	g, err := bundle.NewGov()
	if err != nil || g == nil || g.PolicySource != provenance.PolicySourceTaskScope {
		t.Fatalf("NewGov = %+v, %v; want task-scoped governor", g, err)
	}

	// StampGov composes a governed provenance record and stamps it.
	bundle.StampGov(g, nil)
	if len(stamped) != 1 {
		t.Fatalf("StampGov must stamp exactly one record, got %d", len(stamped))
	}
	if stamped[0].Mode != provenance.ProvenanceModeGoverned {
		t.Errorf("StampGov record mode = %q, want governed", stamped[0].Mode)
	}
	if stamped[0].Index.GitCommit != "abc1234" {
		t.Errorf("StampGov record commit = %q, want abc1234", stamped[0].Index.GitCommit)
	}
	if stamped[0].AuthorizingRule == nil || stamped[0].AuthorizingRule.PolicySource != provenance.PolicySourceTaskScope {
		t.Errorf("StampGov record rule = %+v, want task-scope authorizing rule", stamped[0].AuthorizingRule)
	}

	// StampRaw composes an index-identity-only record.
	bundle.StampRaw(nil)
	if len(stamped) != 2 {
		t.Fatalf("StampRaw must stamp one more record, got %d total", len(stamped))
	}
	if stamped[1].Mode != provenance.ProvenanceModeRaw {
		t.Errorf("StampRaw record mode = %q, want raw", stamped[1].Mode)
	}
	if stamped[1].AuthorizingRule != nil {
		t.Error("raw record must carry no authorizing rule")
	}
}

// TestNewGovContextNilCallbacks pins the nil-callback guard: the factory
// refuses nil governor/stamp funcs up front instead of building a bundle that
// would panic mid-call.
func TestNewGovContextNilCallbacks(t *testing.T) {
	ix, _ := buildTestIndex(t)
	commit := func(string) string { return "" }
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: expected panic, got none", name)
			}
		}()
		fn()
	}
	mustPanic("nil governorFunc", func() {
		NewGovContext(ix, commit, nil, func(*provenance.Provenance) {})
	})
	mustPanic("nil stampFunc", func() {
		NewGovContext(ix, commit, func() (*Governor, error) { return nil, nil }, nil)
	})
}
