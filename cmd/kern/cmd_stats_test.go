package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// statsFixtureDir seeds a fresh stats ledger under a temp XDG_CACHE_HOME and
// returns its dir, so tests can exercise runStats without touching the real
// cache. Entry lines follow the stats.Entry JSONL schema (a tool_call with an
// agent, an optimization entry with an agent, and an unattributed one).
func statsFixtureDir(t *testing.T) string {
	t.Helper()
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	dir := filepath.Join(cacheRoot, "kern", "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().Format("2006-01-02") + ".jsonl"
	lines := []string{
		`{"time":"2026-09-25T10:00:00Z","operation":"tool_call","tool":"kern_search","agent":"planner","after_tokens":120}`,
		`{"operation":"optimize_prompt","tool":"kern_optimize","agent":"coder","model":"gpt-4o","before_tokens":1000,"after_tokens":400,"saved_tokens":600,"saved_percent":60,"cost_saved_usd":0.0015}`,
		`{"operation":"optimize_prompt","before_tokens":100,"after_tokens":50,"saved_tokens":50,"saved_percent":50,"cost_saved_usd":0.000125}`,
	}
	if err := os.WriteFile(filepath.Join(dir, day), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// statsRootedFixtureDir seeds a ledger with two rooted projects plus one
// rootless legacy entry; the rooted helpers can assert exact isolation.
func statsRootedFixtureDir(t *testing.T) string {
	t.Helper()
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	dir := filepath.Join(cacheRoot, "kern", "stats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	here, other := t.TempDir(), t.TempDir()
	day := time.Now().UTC().Format("2006-01-02") + ".jsonl"
	lines := []string{
		`{"operation":"tool_call","tool":"kern_search","root":` + quoteJSON(here) + `,"after_tokens":100}`,
		`{"operation":"tool_call","tool":"kern_buddy","root":` + quoteJSON(other) + `,"after_tokens":20}`,
		`{"operation":"tool_call","tool":"kern_rootless","after_tokens":5}`,
	}
	if err := os.WriteFile(filepath.Join(dir, day), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return here
}

func quoteJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestStatsDefaultOutputHasLedgerHints pins that the default stats summary
// advertises the per-tool, per-agent, and per-project diagnostics.
func TestStatsDefaultOutputHasLedgerHints(t *testing.T) {
	statsFixtureDir(t)
	out := captureStdout(t, func() {
		runStats("stats", []string{"--days", "1"})
	})
	for _, want := range []string{
		"kern stats (last 1 days)",
		"per-tool ledger: kern stats --by-tool · per-agent: kern stats --by-agent · per-project: kern stats --by-root",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stats default output missing %q:\n%s", want, out)
		}
	}
}

// TestStatsNetOutput pins the `kern stats --net` surface: the labeled net
// ledger with its honest components (gross saved tokens minus rules-injection
// minus slice re-reads). The rules cost is computed from the --root's rule
// files at runtime, so the fixture root carries a known AGENTS.md.
func TestStatsNetOutput(t *testing.T) {
	statsFixtureDir(t)
	root := t.TempDir()
	// 50 repeated lines: a known, non-zero rules-injection token count.
	rule := strings.Repeat("// rule line for the net ledger fixture\n", 50)
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		runStats("stats", []string{"--net", "--days", "1", "--root", root})
	})
	for _, want := range []string{
		"kern stats --net (last 1 days)",
		"gross saved tokens",
		"- rules injection",
		"- slice re-reads",
		"net saved tokens",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stats --net output missing %q:\n%s", want, out)
		}
	}
	// Fixture ledger: 600 + 50 = 650 gross saved tokens (see statsFixtureDir),
	// no slice re-reads, minus the AGENTS.md rules cost.
	if !strings.Contains(out, "gross saved tokens : 650") {
		t.Fatalf("stats --net must report the gross total, got:\n%s", out)
	}
	if !strings.Contains(out, "- slice re-reads   : 0 tokens") {
		t.Fatalf("stats --net must report slice re-reads (0 in this fixture), got:\n%s", out)
	}
}

// TestStatsByAgentRendersTable pins the --by-agent render path: the table
// lists every attributed agent plus the (unattributed) bucket.
func TestStatsByAgentRendersTable(t *testing.T) {
	statsFixtureDir(t)
	out := captureStdout(t, func() {
		runStats("stats", []string{"--by-agent", "--days", "1"})
	})
	for _, want := range []string{
		"kern stats --by-agent (last 1 days)",
		"coder",
		"planner",
		"(unattributed)",
		"tokens saved",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stats --by-agent output missing %q:\n%s", want, out)
		}
	}
}

// TestStatsByAgentJSON pins the JSON form of the per-agent ledger.
func TestStatsByAgentJSON(t *testing.T) {
	statsFixtureDir(t)
	out := captureStdout(t, func() {
		runStats("stats", []string{"--by-agent", "--days", "1", "--json"})
	})
	for _, want := range []string{`"agent": "coder"`, `"agent": "planner"`, `"(unattributed)"`, `"tokens_saved"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("stats --by-agent --json output missing %q:\n%s", want, out)
		}
	}
}

// TestStatsByRootRendersProjects pins the local per-project diagnostic surface.
func TestStatsByRootRendersProjects(t *testing.T) {
	here := statsRootedFixtureDir(t)
	out := captureStdout(t, func() {
		runStats("stats", []string{"--by-root", "--days", "1"})
	})
	for _, want := range []string{
		"kern stats --by-root (last 1 days)",
		"tool calls",
		"tokens saved",
		here,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stats --by-root output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "kern_rootless") {
		t.Fatalf("stats --by-root must not turn legacy rootless entries into a project row:\n%s", out)
	}
}

func TestStatsByToolRootScopesProject(t *testing.T) {
	root := statsRootedFixtureDir(t)
	out := captureStdout(t, func() {
		runStats("stats", []string{"--by-tool", "--root", root, "--days", "1"})
	})
	if !strings.Contains(out, "root: "+root) || !strings.Contains(out, "kern_search") {
		t.Fatalf("stats --by-tool --root output missing project scope/tool:\n%s", out)
	}
	if strings.Contains(out, "kern_buddy") || strings.Contains(out, "kern_rootless") {
		t.Fatalf("project-scoped tool ledger leaked another project:\n%s", out)
	}
}

// TestStatsCostModelLineAssumed pins the labeled fallback: with no model or
// rate configured (a fresh install), the cost line names the flat 1e-05 as
// an assumption instead of presenting it as a confident figure.
func TestStatsCostModelLineAssumed(t *testing.T) {
	t.Setenv("KERN_MODEL", "")
	t.Setenv("KERN_LLM_MODEL", "")
	t.Setenv("KERN_COST_PER_TOKEN", "")
	statsFixtureDir(t)
	out := captureStdout(t, func() {
		runStats("stats", []string{"--days", "1"})
	})
	if !strings.Contains(out, "cost model   : $0.000010/token (assumed — set llm.model or cost_per_token)") {
		t.Fatalf("expected the labeled assumed cost line, got:\n%s", out)
	}
}

// TestStatsCostModelLineTable pins the engaged per-model table: with a model
// configured, the cost line shows the table rate and the model tier.
func TestStatsCostModelLineTable(t *testing.T) {
	t.Setenv("KERN_MODEL", "")
	t.Setenv("KERN_LLM_MODEL", "gpt-4o")
	t.Setenv("KERN_COST_PER_TOKEN", "")
	statsFixtureDir(t)
	out := captureStdout(t, func() {
		runStats("stats", []string{"--days", "1"})
	})
	if !strings.Contains(out, "cost model   : $2.5/1M (gpt-4o)") {
		t.Fatalf("expected the engaged table cost line, got:\n%s", out)
	}
}

// TestStatsCostModelLineOverride pins the override label: an explicit
// operator rate is shown as an override, not a table engagement.
func TestStatsCostModelLineOverride(t *testing.T) {
	t.Setenv("KERN_MODEL", "")
	t.Setenv("KERN_LLM_MODEL", "gpt-4o")
	t.Setenv("KERN_COST_PER_TOKEN", "0.001")
	statsFixtureDir(t)
	out := captureStdout(t, func() {
		runStats("stats", []string{"--days", "1"})
	})
	if !strings.Contains(out, "cost model   : $0.001000/token (operator override)") {
		t.Fatalf("expected the operator override cost line, got:\n%s", out)
	}
}
