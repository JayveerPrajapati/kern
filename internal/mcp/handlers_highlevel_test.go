package mcp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/tasklife"
)

// TestHandleMeta_PhaseArg verifies kern_meta's phase arg: invalid phases are
// rejected before dispatch, valid phases are echoed as a hint in the response.
func TestHandleMeta_PhaseArg(t *testing.T) {
	t.Parallel()
	s := NewServer(strings.NewReader(""), io.Discard)

	// Invalid phase → rejected up front, before any handler runs.
	_, err := s.handleMeta(context.Background(), map[string]any{"request": "show me the savings", "phase": "bogus"})
	if err == nil || !strings.Contains(err.Error(), "phase must be one of") {
		t.Fatalf("invalid phase: got err %v, want rejection with 'phase must be one of'", err)
	}

	// Valid phase → routed normally and the hint is echoed in the response.
	out, err := s.handleMeta(context.Background(), map[string]any{"request": "show me the savings", "phase": "verify"})
	if err != nil {
		t.Fatalf("valid phase handleMeta: %v", err)
	}
	if !strings.Contains(out, "classified as: kern_stats") {
		t.Errorf("expected kern_stats classification, got: %s", out)
	}
	if !strings.Contains(out, "[phase hint: verify — set KERN_MCP_PHASE=verify to filter the advertised tool list]") {
		t.Errorf("expected phase hint in response, got: %s", out)
	}
}

// TestHandleRiskRequiresChange pins the kern_impact risk=true contract up
// front: the change argument is mandatory and is rejected before any
// platform/index work happens, so a bare server can serve the error.
func TestHandleRiskRequiresChange(t *testing.T) {
	t.Parallel()
	s := newTestServer()
	_, err := s.handleImpact(context.Background(), map[string]any{"root": ".", "risk": "true"})
	if err == nil || !strings.Contains(err.Error(), "change is required") {
		t.Fatalf("missing change: got err %v, want rejection with 'change is required'", err)
	}
}

// TestHandleRiskServesRiskAssessment drives the risk=true happy path end to
// end: handleImpact with risk=true resolves the root through the session
// index, builds the platform, and returns the rendered governance risk
// assessment prefixed with "RISK for: <change>".
func TestHandleRiskServesRiskAssessment(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	// Drain the session's background index save before TempDir cleanup so the
	// .kern persistence goroutine cannot race the RemoveAll (pre-existing
	// flake: "TempDir RemoveAll cleanup: directory not empty").
	defer s.Close()
	out, err := s.handleImpact(context.Background(), map[string]any{"root": root, "change": "Greet", "risk": "true"})
	if err != nil {
		t.Fatalf("handleImpact risk=true: %v", err)
	}
	if !strings.HasPrefix(out, "RISK for: Greet\n") {
		t.Errorf("handleImpact risk=true output = %q, want prefix %q", out, "RISK for: Greet\n")
	}
	if !strings.Contains(out, "no risks identified") && !strings.Contains(out, "factor:") {
		t.Errorf("handleImpact risk=true output = %q, want a rendered risk assessment (factors or explicit no-risk)", out)
	}
}
func TestHandleAnalyzeLens(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()
	out, err := s.handleAnalyze(context.Background(), map[string]any{"root": root, "change": "Greet", "lens": "security"})
	if err != nil {
		t.Fatalf("handleAnalyze with lens: %v", err)
	}
	if !strings.HasPrefix(out, "ANALYSIS for: Greet\n") {
		t.Errorf("output = %q, want ANALYSIS prefix", out)
	}
	// QA 6.6: an ephemeral task record must not leak its bookkeeping
	// annotation into the payload (the a-<n> ID dies with the process).
	if strings.Contains(out, "[task: ") {
		t.Errorf("default (ephemeral) output must not carry the task line, got %q", out)
	}

	if _, err := s.handleAnalyze(context.Background(), map[string]any{"root": root, "change": "Greet", "lens": "bogus"}); err == nil || !strings.Contains(err.Error(), "unknown lens") {
		t.Errorf("unknown lens: err = %v, want rejection with 'unknown lens'", err)
	}
}

// TestHandlePlanSections (B3/B5): kern_plan's end-to-end output carries the
// context-sufficiency proof and the first slice, with the served-ledger
// hook wired (nothing served yet → ready-to-execute false, 0/N served).
func TestHandlePlanSections(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()
	out, err := s.handlePlan(context.Background(), map[string]any{"root": root, "change": "Greet"})
	if err != nil {
		t.Fatalf("handlePlan: %v", err)
	}
	if !strings.HasPrefix(out, "PLAN for: Greet\n") {
		t.Fatalf("handlePlan output = %q, want PLAN prefix", out)
	}
	for _, want := range []string{
		"## Context sufficiency",
		"ready-to-execute: false",
		"served: 0/",
		"## First slice",
		"Smallest verifiable first edit:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan output must contain %q, got:\n%s", want, out)
		}
	}
}

func TestHandleAnalyzeProfile(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	base, err := s.handleAnalyze(context.Background(), map[string]any{"root": root, "change": "Greet"})
	if err != nil {
		t.Fatalf("handleAnalyze: %v", err)
	}
	if !strings.HasPrefix(base, "ANALYSIS for: Greet\n") {
		t.Fatalf("base output = %q, want ANALYSIS prefix", base)
	}

	js, err := s.handleAnalyze(context.Background(), map[string]any{"root": root, "change": "Greet", "profile": "machine-json"})
	if err != nil {
		t.Fatalf("handleAnalyze machine-json: %v", err)
	}
	var m struct {
		Profile string `json:"profile"`
		Format  string `json:"format"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatalf("machine-json output not valid JSON: %v\n%s", err, js)
	}
	if m.Profile != "machine-json" || m.Format != "json" {
		t.Errorf("decoded profile=%q format=%q; want machine-json/json", m.Profile, m.Format)
	}
	if !strings.HasPrefix(m.Content, "ANALYSIS for: Greet\n") {
		t.Errorf("wrapped content should carry the ANALYSIS prefix, got %q", m.Content)
	}
	if strings.Contains(m.Content, "[task: ") {
		t.Errorf("wrapped content must not carry the ephemeral task line (QA 6.6), got %q", m.Content)
	}

	if _, err := s.handleAnalyze(context.Background(), map[string]any{"root": root, "change": "Greet", "profile": "bogus"}); err == nil || !strings.Contains(err.Error(), "unknown profile") {
		t.Errorf("unknown profile: err = %v, want rejection with 'unknown profile'", err)
	}
}

// TestHandleAnalyzeProfileAndLensCombined proves the lens and profile args
// compose: with both set, the lensed analysis is wrapped in the machine-json
// envelope (the lens re-ranks facts inside the ANALYSIS content; the profile
// shapes the whole response).
func TestHandleAnalyzeProfileAndLensCombined(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()
	js, err := s.handleAnalyze(context.Background(), map[string]any{"root": root, "change": "Greet", "lens": "security", "profile": "machine-json"})
	if err != nil {
		t.Fatalf("combined lens+profile: %v", err)
	}
	var m struct {
		Profile string `json:"profile"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatalf("combined output not valid JSON: %v\n%s", err, js)
	}
	if m.Profile != "machine-json" {
		t.Errorf("profile = %q, want machine-json", m.Profile)
	}
	if !strings.Contains(m.Content, "ANALYSIS for: Greet") {
		t.Errorf("content should carry the analysis, got %q", m.Content)
	}
	if strings.Contains(m.Content, "[task: ") {
		t.Errorf("content must not carry the ephemeral task line (QA 6.6), got %q", m.Content)
	}
}

func TestHandleImpactNoDuplicateHeader(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()
	out, err := s.handleImpact(context.Background(), map[string]any{"root": root, "change": "Greet"})
	if err != nil {
		t.Fatalf("handleImpact: %v", err)
	}
	if n := strings.Count(out, "IMPACT for:"); n != 1 {
		t.Errorf("handleImpact output contains %d \"IMPACT for:\" headers, want exactly 1:\n%s", n, out)
	}
	if strings.Contains(out, "[task: ") {
		t.Errorf("default (ephemeral) impact output must not carry the task line (QA 6.6):\n%s", out)
	}
}

// TestHandleMetaRoutesLLMProviders pins the P1 gate after the 2026-10-18
// surface slim removed kern_llm_providers: a request about the LLM provider
// chain carries no code intent and is refused (the old route reclassified to
// kern_llm_providers; the gate message teaches the surviving surface).
func TestHandleMetaRoutesLLMProviders(t *testing.T) {
	t.Parallel()
	s := newTestServer()
	_, err := s.CallTool(context.Background(), "kern_meta", map[string]any{
		"request": "list the LLM provider chain and whether a host sampler is connected",
	})
	if err == nil || !strings.Contains(err.Error(), "no code intent detected") {
		t.Errorf("kern_meta must refuse a no-code-intent request after the slim, got: %v", err)
	}
}

// TestHandleVerifyRejectsUnknownType: kern_verify with types=123 (a number
// coerced to the string "123") must be rejected up front with an error naming
// the type — never a vacuous "summary: PASS" run. The rejection happens
// before the exec firewall and before any check, so it needs no platform and
// returns instantly.
func TestHandleVerifyRejectsUnknownType(t *testing.T) {
	t.Parallel()
	s := newTestServer()
	_, err := s.handleVerify(context.Background(), "", map[string]any{"root": ".", "types": "123"})
	if err == nil {
		t.Fatal("handleVerify(types=123) must error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown verify type: 123") {
		t.Fatalf("error must name the offending type, got: %v", err)
	}
	if !strings.Contains(err.Error(), "known:") {
		t.Fatalf("error must list the known types, got: %v", err)
	}
	// A garbage token mixed with valid ones is rejected too, and the valid
	// token is never silently dropped.
	_, err = s.handleVerify(context.Background(), "", map[string]any{"root": ".", "types": "build,zzz"})
	if err == nil || !strings.Contains(err.Error(), "unknown verify type: zzz") {
		t.Fatalf("handleVerify(types=build,zzz): err = %v, want rejection of zzz", err)
	}
}

// TestHandleVerifyAcceptsBuildTest: kern_verify with a valid exec type list
// passes the type gate and runs the real build+test verification on a small
// compiling fixture (exec firewall + fail-closed sandbox gate opted in, same
// pattern as the existing exec/sandbox tests).
func TestHandleVerifyAcceptsBuildTest(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")        // build/test are governed exec checks
	t.Setenv("KERN_ALLOW_NET", "1")         // fail-closed gate: opt into unisolated runs on hosts without netns (darwin)
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // hermetic Go build cache
	root := mcpProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()
	out, err := s.handleVerify(context.Background(), "", map[string]any{"root": root, "types": "build,test"})
	if err != nil {
		t.Fatalf("handleVerify(build,test): %v", err)
	}
	if !strings.Contains(out, "verdict:") || !strings.Contains(out, "[task: ") {
		t.Errorf("output missing verdict/task line:\n%s", out)
	}
	if strings.Contains(out, "unknown verify type") {
		t.Errorf("valid types must not trip the type gate:\n%s", out)
	}
}

// TestHandleVerifyDeniedWithoutExecAllowlist pins the agent-surface gate
// (P3): the MCP kern_verify types path for command-executing types must fail
// closed WITHOUT KERN_ALLOW_EXEC / KERN_TOOLS — the CLI gate removal must
// not open an agent hole. build/test are governed exec checks; the denial
// must surface before any check runs (no real build on the fixture).
func TestHandleVerifyDeniedWithoutExecAllowlist(t *testing.T) {
	// The dev box exports KERN_ALLOW_EXEC globally; clear it plus the tool
	// allowlist so the denial is deterministic.
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_TOOLS", "")
	t.Setenv("KERN_EXEC_RISK", "")
	root := mcpProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()
	_, err := s.handleVerify(context.Background(), "", map[string]any{"root": root, "types": "build,test"})
	if err == nil {
		t.Fatal("kern_verify(build,test) without KERN_ALLOW_EXEC must be denied, got nil")
	}
	if !strings.Contains(err.Error(), "command execution blocked") {
		t.Fatalf("denial must name the exec firewall, got: %v", err)
	}
}

// TestHandleVerifyAcceptsArchitecture: kern_verify with the index-only
// architecture check runs in-process (no exec allowlist required) and
// returns the typed verdict.
func TestHandleVerifyAcceptsArchitecture(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := mcpProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()
	out, err := s.handleVerify(context.Background(), "", map[string]any{"root": root, "types": "architecture"})
	if err != nil {
		t.Fatalf("handleVerify(architecture): %v", err)
	}
	if !strings.Contains(out, "verdict:") {
		t.Errorf("output missing verdict:\n%s", out)
	}
	if strings.Contains(out, "unknown verify type") {
		t.Errorf("architecture must not trip the type gate:\n%s", out)
	}
}

// TestHandleVerifyLicenseWarnSurfacesFindings pins P0-4: kern_verify with
// types=license on a go.mod-only fixture returns a WARN verdict with err==nil
// (license findings never fail the check), and the rendered output must carry
// the per-check status AND the finding detail lines — never just the bare
// "verdict: WARN / summary:" two-liner that used to drop them. The fixture's
// main module has no LICENSE file, so it classifies "unknown" → one finding.
func TestHandleVerifyLicenseWarnSurfacesFindings(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := mcpProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()
	out, err := s.handleVerify(context.Background(), "", map[string]any{"root": root, "types": "license"})
	if err != nil {
		t.Fatalf("handleVerify(license): %v", err)
	}
	if !strings.Contains(out, "verdict: WARN") {
		t.Errorf("output missing WARN verdict:\n%s", out)
	}
	if !strings.Contains(out, "license: WARN") {
		t.Errorf("output missing per-check license status line:\n%s", out)
	}
	if !strings.Contains(out, "unknown license: demo") {
		t.Errorf("output missing license finding detail (P0-4 regression):\n%s", out)
	}
	if strings.Contains(out, "license: OK") {
		t.Errorf("output must not claim a clean license OK:\n%s", out)
	}
	// Verify always persists its authoritative task (t-<n>, queryable via
	// kern task <id>), so the task line legitimately stays.
	if !strings.Contains(out, "[task: t-") {
		t.Errorf("verify output must carry the persisted task line:\n%s", out)
	}
}

// TestHandleWhatIfGarbageWarns: kern_what_if with an unresolvable change
// (bare symbol absent from the index) must surface a visible not-found
// warning instead of a clean "Safe to proceed" bill; a real symbol from the
// fixture must not warn.
func TestHandleWhatIfGarbageWarns(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := mcpProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	out, err := s.handleWhatIf(context.Background(), map[string]any{"root": root, "change": "ZZZgarbage"})
	if err == nil {
		t.Fatalf("handleWhatIf(garbage) must error with a not-found message, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "no symbol named") {
		t.Errorf("garbage change must surface a not-found error, got: %v", err)
	}

	out2, err := s.handleWhatIf(context.Background(), map[string]any{"root": root, "change": "Greet"})
	if err != nil {
		t.Fatalf("handleWhatIf(Greet): %v", err)
	}
	if strings.Contains(out2, "not found") {
		t.Errorf("valid symbol must not carry the not-found warning, got:\n%s", out2)
	}
}

// TestHandleLoopAutonomousFailsFastWithoutProvider: kern_loop mode=autonomous
// with no reachable LLM provider must fail fast with a clear provider error
// instead of silently running the ~180s provider-chain fallthrough. The
// provider is pinned to Ollama at an unreachable address so the test is
// deterministic on any host.

// TestHandleAnalyzeTaskPersistenceOptIn locks the F9 opt-in contract on the
// MCP surface: kern_analyze is read-only, so a DEFAULT call must create only
// an ephemeral in-memory task (a-<n> ID, no store write) and must NOT leak
// its bookkeeping annotation into the payload (QA 6.6 — the ephemeral ID
// dies with the process). Passing persist_task=true opts into the
// authoritative record (t-<n>, queryable via kern task <id>), which a fresh
// TaskService must resolve — and only then does the output carry the task
// line, because the ID is a real handle.
func TestHandleAnalyzeTaskPersistenceOptIn(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	taskID := func(out string) string {
		t.Helper()
		const marker = "[task: "
		start := strings.Index(out, marker)
		if start < 0 {
			t.Fatalf("output has no %q line:\n%s", marker, out)
		}
		rest := out[start+len(marker):]
		end := strings.Index(rest, " — ")
		if end < 0 {
			t.Fatalf("cannot parse task line from output:\n%s", out)
		}
		return rest[:end]
	}

	// Default kern_analyze call: the task record must stay ephemeral AND the
	// payload must not carry the task line (QA 6.6).
	out, err := s.handleAnalyze(context.Background(), map[string]any{"root": root, "change": "Greet"})
	if err != nil {
		t.Fatalf("handleAnalyze (default): %v", err)
	}
	if strings.Contains(out, "[task: ") {
		t.Fatalf("default kern_analyze must not carry the ephemeral task line (QA 6.6):\n%s", out)
	}

	// persist_task=true: the caller opted into the authoritative record, so
	// the footer returns the queryable ID.
	out, err = s.handleAnalyze(context.Background(), map[string]any{"root": root, "change": "Greet", "persist_task": true})
	if err != nil {
		t.Fatalf("handleAnalyze (persist_task=true): %v", err)
	}
	perID := taskID(out)
	if !strings.HasPrefix(perID, "t-") {
		t.Fatalf("persist_task=true task id = %q, want store-assigned t-<n> (authoritative record)", perID)
	}
	ts := tasklife.NewTaskService(mustMCPPlatform(t, root), eventbus.New())
	if got, ok := ts.Get(perID); !ok {
		t.Fatalf("task %q not queryable from a fresh TaskService after persist_task=true", perID)
	} else if got.State == "" {
		t.Fatalf("task %q loaded from store has no state", perID)
	}
}

// mustMCPPlatform loads a Platform for root, failing the test on error. (The
// test helper mirrors the CLI's mustApp pattern; there is no package-level
// helper here yet.)
func mustMCPPlatform(t *testing.T, root string) *app.Platform {
	t.Helper()
	p, err := app.New(root)
	if err != nil {
		t.Fatalf("app.New(%s): %v", root, err)
	}
	return p
}

// TestVerifyWithoutCommandKeepsOldBehavior pins the kern_verify dispatch
// routing: command mode (verifyCommand) only engages when a command or an
// anchor argument is present; a plain verify (types=...) must keep the old
// architecture-verdict path. The command runner itself lives in
// internal/verifycmd; this test guards the mcp-side routing boundary.
func TestVerifyWithoutCommandKeepsOldBehavior(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A real source file keeps the plain verify path from rejecting the
	// fixture as an empty index (rejectEmptyIndex fires when a root has no
	// indexable source files); the assertion is about dispatch, not the
	// architecture verdict.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := mcpAssertOK(t, "kern_verify", map[string]any{"root": dir, "types": "architecture"})
	if strings.Contains(out, "anchor=") {
		t.Fatalf("plain verify must not take the command path:\n%s", out)
	}
}
