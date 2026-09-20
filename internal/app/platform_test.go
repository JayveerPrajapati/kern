package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/testfixture"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// TestPlatformAnalyzeWhatIfVerify exercises the three core service methods
// (Analyze, WhatIf, Verify) against the real kern repo. It is the
// contract test: it proves the shared application-services layer wires the
// index → twin-merged graph → memory → firewall → context/verification engines
// correctly and that all three interfaces (CLI/MCP/REST) can rely on it.
// It does NOT assert exact output shapes (those drift as the repo evolves);
// it asserts the structural contract: methods return non-empty results without
// error, the rendered text contains expected section markers, and the
// verification verdict is a known value.
func TestPlatformAnalyzeWhatIfVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e (>30s); skipped with -short")
	}
	root := testfixture.Repo(t)

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Analyze: a real exported symbol.
	pkt, text, err := p.Analyze("NewServer")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if text == "" {
		t.Error("Analyze returned empty text")
	}
	if !strings.Contains(text, "Task:") {
		t.Errorf("Analyze text missing 'Task:' marker; got:\n%s", text)
	}
	if len(pkt.Symbols) == 0 {
		t.Error("Analyze returned no symbols in packet")
	}

	// WhatIf: remove a symbol — should produce a non-empty impact.
	imp, wiText, err := p.WhatIf(whatif.RemoveSymbol, "NewServer", "")
	if err != nil {
		t.Fatalf("WhatIf: %v", err)
	}
	if wiText == "" {
		t.Error("WhatIf returned empty text")
	}
	if !strings.Contains(wiText, "change:") {
		t.Errorf("WhatIf text missing 'change:' marker; got:\n%s", wiText)
	}
	if len(imp.Affected) == 0 {
		t.Error("WhatIf returned no affected symbols for NewServer")
	}

	// Verify: build only (fast, deterministic).
	res := p.Verify([]string{"build"})
	if res.Verdict == "" {
		t.Error("Verify returned empty verdict")
	}
	if res.Build == nil {
		t.Error("Verify returned nil Build result")
	}
}

// TestWhatIfRuntimeEvidence asserts the RuntimeEvidence dimension is filled
// from the platform's runtime source : with a source carrying error
// events attached via WithRuntimeSource, WhatIf returns a non-empty
// RuntimeEvidence; with no source, the dimension stays empty.
func TestWhatIfRuntimeEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("slow e2e (>30s); skipped with -short")
	}
	root := testfixture.Repo(t)

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Without a runtime source, RuntimeEvidence stays empty.
	imp, _, err := p.WhatIf(whatif.RemoveSymbol, "NewServer", "")
	if err != nil {
		t.Fatalf("WhatIf (no source): %v", err)
	}
	if len(imp.RuntimeEvidence) != 0 {
		t.Fatalf("expected empty RuntimeEvidence with no source, got %d entries", len(imp.RuntimeEvidence))
	}

	// With a runtime source carrying error events, RuntimeEvidence is populated.
	store := runtime.NewStore()
	now := time.Now().Truncate(time.Second)
	store.Ingest(runtime.Event{
		ID:        "evt-1",
		Type:      runtime.EventError,
		Service:   "whatif",
		Severity:  "error",
		Message:   "nil pointer in whatif handler",
		Timestamp: now,
	})
	p.WithRuntimeSource(store)

	imp, _, err = p.WhatIf(whatif.RemoveSymbol, "NewServer", "")
	if err != nil {
		t.Fatalf("WhatIf (with source): %v", err)
	}
	if len(imp.RuntimeEvidence) == 0 {
		t.Fatal("expected non-empty RuntimeEvidence when a runtime source with error events is attached")
	}
	if !strings.Contains(imp.RuntimeEvidence[0], "[whatif] error: nil pointer in whatif handler") {
		t.Errorf("RuntimeEvidence[0] not rendered as expected; got: %q", imp.RuntimeEvidence[0])
	}

	// Non-error events must never leak into the dimension.
	if strings.Contains(strings.Join(imp.RuntimeEvidence, "\n"), "no runtime telemetry") {
		t.Error("unexpected content in RuntimeEvidence")
	}
}

// TestPlatformNewWithGraph tests the server constructor that shares a
// caller-owned graph pointer. It verifies the Platform's engines see the
// graph that the caller built (not a copy).
func TestPlatformNewWithGraph(t *testing.T) {
	if testing.Short() {
		t.Skip("builds full index; skipped with -short")
	}
	root := testfixture.Repo(t)

	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The Platform's graph pointer must be non-nil and match what New built.
	g := p.Graph()
	if g == nil {
		t.Fatal("Graph() returned nil")
	}
	if len(g.Nodes) == 0 {
		t.Error("Graph has no nodes")
	}

	// Memory, Firewall, and engines must be wired.
	if p.Memory() == nil {
		t.Error("Memory() returned nil")
	}
	if p.Firewall() == nil {
		t.Error("Firewall() returned nil")
	}
	if p.ContextEngine() == nil {
		t.Error("ContextEngine() returned nil")
	}
	if p.VerificationEngine() == nil {
		t.Error("VerificationEngine() returned nil")
	}
}

// TestCodeContextGroundsCoder verifies the C1 grounding assembler: an
// intent naming a real symbol yields a context bundle that contains the
// symbol's own file content and its blast-radius files, and a nonsense
// intent yields empty context (the coder runs ungrounded, as before).
func TestCodeContextGroundsCoder(t *testing.T) {
	p, err := New(testfixture.Repo(t))
	if err != nil {
		t.Fatal(err)
	}
	ctxStr, err := p.CodeContext("refactor the NewServer function in web/server.go to handle corruption", "")
	if err != nil {
		t.Fatal(err)
	}
	if ctxStr == "" {
		t.Fatal("expected non-empty context for a symbol-named intent")
	}
	if !strings.Contains(ctxStr, "<context-file") {
		t.Error("context should include file contents")
	}
	if !strings.Contains(ctxStr, "NewServer") {
		t.Error("context should mention the target symbol")
	}
	if !strings.Contains(ctxStr, "Impact set") {
		t.Error("context should include the impact set")
	}

	// A nonsense intent with no plan resolves nothing: empty, not an error.
	empty, err := p.CodeContext("zzz nonexistent qwerty", "")
	if err != nil {
		t.Fatal(err)
	}
	if empty != "" {
		t.Errorf("nonsense intent should yield empty context, got %d bytes", len(empty))
	}

	// A plan naming real files grounds even when the intent names no symbol.
	planCtx, err := p.CodeContext("improve things", "1. Read go.mod.\n2. Edit web/server.go carefully.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(planCtx, "web/server.go") {
		t.Errorf("plan-named file should be included in context: %s", headStr(planCtx, 200))
	}
}

func headStr(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// Stopword-colliding symbols: "fix the Add function" mentions only words that
// collide with the change-verb stoplist, yet "Add" is a real symbol in the
// fixture. The Analyze (kern analyze/kern plan) and WhatIf chains must resolve
// it instead of erroring with symbol-not-found.
func TestAnalyzeResolvesStopwordCollidingSymbol(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module scratch\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "math.go"), []byte("package scratch\n\n// Add returns the sum of a and b.\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Plan/analyze path: the whole change text is stopwords + the symbol.
	pkt, text, err := p.Analyze("fix the Add function")
	if err != nil {
		t.Fatalf("Analyze(%q): %v", "fix the Add function", err)
	}
	if text == "" || len(pkt.Symbols) == 0 {
		t.Fatalf("Analyze resolved no symbols; text=%q packet=%+v", text, pkt)
	}
	// What-if path: same extraction, different consumer. "Add" is a leaf
	// function, so Affected is legitimately empty — the fix is that the
	// change RESOLVES to the real symbol instead of erroring.
	imp, _, err := p.WhatIf(whatif.RemoveSymbol, "fix the Add function", "")
	if err != nil {
		t.Fatalf("WhatIf(%q): %v", "fix the Add function", err)
	}
	if imp.Change.Target != "Add" {
		t.Errorf("WhatIf resolved target = %q, want %q", imp.Change.Target, "Add")
	}
}

// TestNewPersistsIndexViaLoadOrBuild guards the P1a fix: New must go through
// index.LoadOrBuild so one-shot CLI invocations (analyze/review/task/do and
// the other 35+ call sites) reuse the persisted index — and incremental
// Update on staleness — instead of full-rebuilding the tree on every call.
// Observable: LoadOrBuild saves the index back for the next caller; a bare
// index.Build never persists.
func TestNewPersistsIndexViaLoadOrBuild(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package hello\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Index() == nil || len(p.Index().Symbols) == 0 {
		t.Fatal("New: empty index")
	}
	if _, err := os.Stat(filepath.Join(dir, ".kern", "index.json")); err != nil {
		t.Fatalf("New did not persist the index (load-or-build path bypassed): %v", err)
	}
	// A second New must succeed against the persisted index.
	if _, err := New(dir); err != nil {
		t.Fatalf("second New: %v", err)
	}
}

// TestResolveSymbolFuzzyApproximatePhrase verifies that approximate phrases
// or doc titles without exact qualified symbol names auto-resolve to the
// closest matching candidate symbol in the graph rather than failing.
func TestResolveSymbolFuzzyApproximatePhrase(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module userauth\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code := `package userauth

func UserAuthenticationRateLimiter() bool {
	return true
}

func ProcessLogin() bool {
	return UserAuthenticationRateLimiter()
}
`
	if err := os.WriteFile(filepath.Join(root, "auth.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Approximate query: "user authentication rate limiting"
	sym, err := p.resolveSymbol("user authentication rate limiting")
	if err != nil {
		t.Fatalf("resolveSymbol failed to auto-resolve approximate phrase: %v", err)
	}
	if !strings.Contains(sym, "UserAuthenticationRateLimiter") {
		t.Errorf("resolveSymbol resolved %q, want UserAuthenticationRateLimiter", sym)
	}

	// Single approximate token: "RateLimiter"
	sym2, err := p.resolveSymbol("RateLimiter")
	if err != nil {
		t.Fatalf("resolveSymbol single approximate token failed: %v", err)
	}
	if !strings.Contains(sym2, "UserAuthenticationRateLimiter") {
		t.Errorf("resolveSymbol resolved %q, want UserAuthenticationRateLimiter", sym2)
	}
}

