package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// gateFixture writes a tiny standalone Go module with a small call graph so
// the gate test exercises the real indexer, graph and engine WITHOUT scanning
// the whole kern repository (which previously hung >90s and spawned runaway
// processes).
func gateFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module gatefixture\n\ngo 1.20\n",
		"main.go": `package main

func base() string { return "b" }

func helper() string { return "h" + base() }

func other() string { return helper() }

func main() { println(other()) }
`,
		"main_test.go": `package main

import "testing"

func TestHelper(t *testing.T) {
	if helper() != "hb" {
		t.Fail()
	}
}
`,
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return dir
}

// TestMVP1GateAnalyzeChange is the MVP1 GATE test .
// It runs the killer workflow "Analyze this proposed change" end-to-end
// against a small in-repo fixture module and asserts all 9 output fields are
// populated. This test is the gate that must pass before MVP2 work begins.
// It is scoped to the tiny fixture (not the whole kern repo) so it completes
// in seconds and never spawns runaway index/build processes.
func TestMVP1GateAnalyzeChange(t *testing.T) {
	root := gateFixture(t)

	// Step 1: Build the index for the fixture module
	t.Log("[1/6] Building index...")
	start := time.Now()
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build(%q) returned error: %v", root, err)
	}
	t.Logf("      Index built: %d symbols, %d call edges (%.1fs)",
		len(ix.Symbols), len(ix.Calls), time.Since(start).Seconds())

	if len(ix.Symbols) == 0 {
		t.Fatal("index build produced 0 symbols — cannot run gate test")
	}

	// Step 2: Build the canonical knowledge graph.
	t.Log("[2/6] Building canonical knowledge graph...")
	start = time.Now()
	g := intelligence.FromIndex(ix)
	t.Logf("      Graph built: %d nodes, %d edges, hash=%s... (%.1fs)",
		len(g.Nodes), len(g.Edges), graphHashPrefix(g.Version.GraphHash, 8), time.Since(start).Seconds())

	if len(g.Nodes) == 0 {
		t.Fatal("graph build produced 0 nodes")
	}

	// Step 3: Set up engineering memory store with a seeded entry about the
	// fixture's helper scope.
	t.Log("[3/6] Setting up engineering memory store...")
	start = time.Now()
	store := memory.NewMemoryStore(root)
	store.Add(domain.Memory{
		Type:    domain.MemoryDecision,
		Content: "helper must compose base() to stay consistent with callers",
		Scope:   "helper",
		Tags:    []string{"gatefixture", "helper"},
	})
	t.Logf("      Memory store ready, seeded 1 entry (%.1fs)", time.Since(start).Seconds())

	// Step 4: Set up governance firewall with a context-engine agent.
	t.Log("[4/6] Setting up governance firewall...")
	start = time.Now()
	fw := governance.NewFirewall()
	agent := governance.NewAgent("context-engine", "Context Engine", "analyzer", []governance.Permission{
		{Resource: "source", Action: "read"},
		{Resource: "source", Action: "write"},
	})
	fw.WithAgents(agent)
	t.Logf("      Firewall ready with %d default policies (%.1fs)",
		len(governance.DefaultPolicies()), time.Since(start).Seconds())

	// Step 5: Create context engine.
	t.Log("[5/6] Creating context engine...")
	start = time.Now()
	engine := NewEngine(root, &g, store, fw)
	t.Logf("      Engine ready (%.1fs)", time.Since(start).Seconds())

	// Step 6: THE KILLER WORKFLOW — Analyze a proposed change to "helper".
	target := "helper"
	t.Logf("[6/6] === ANALYZING PROPOSED CHANGE TO: %q ===", target)

	start = time.Now()
	pkt, err := engine.AnalyzeChange(target)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("AnalyzeChange(%q) returned error: %v", target, err)
	}

	// Render text output.
	text := RenderText(pkt)
	t.Logf("--- TEXT OUTPUT ---\n%s", text)

	if strings.TrimSpace(text) == "" {
		t.Error("RenderText produced empty output")
	}

	// Metrics.
	m := Measure(pkt, duration)
	t.Logf("--- METRICS ---")
	t.Logf("Token count:         %d", pkt.TokenCount)
	t.Logf("Token reduction:     %.1f%%", m.TokenReduction)
	t.Logf("Retrieval relevance: %.1f%%", m.RetrievalRelevance)
	t.Logf("Latency:             %v", m.Latency)
	t.Logf("Cost (est):          $%.4f", m.Cost)

	// JSON output.
	jsonStr, err := RenderJSON(pkt)
	if err != nil {
		t.Errorf("RenderJSON returned error: %v", err)
	} else if len(jsonStr) == 0 {
		t.Error("RenderJSON produced empty output")
	} else {
		t.Logf("--- JSON OUTPUT (%d chars) ---", len(jsonStr))
	}

	// === MVP1 GATE ACCEPTANCE CHECK ===
	t.Log("=== MVP1 GATE ACCEPTANCE CHECK ===")

	checks := []struct {
		name   string
		pass   bool
		detail string
	}{
		{"Relevant code (Symbols)", len(pkt.Symbols) > 0, plural(len(pkt.Symbols), "symbol")},
		{"Files", len(pkt.Files) > 0, plural(len(pkt.Files), "file")},
		{"Dependencies (Edges)", len(pkt.Dependencies) > 0, plural(len(pkt.Dependencies), "edge")},
		{"Historical memory", len(pkt.Memory) > 0, plural(len(pkt.Memory), "memory")},
		{"Risks", len(pkt.Risks) > 0, plural(len(pkt.Risks), "risk")},
		{"Evidence (Facts)", len(pkt.Facts) > 0, plural(len(pkt.Facts), "claim")},
		{"Required validation", len(pkt.RequiredValidation) > 0, plural(len(pkt.RequiredValidation), "step")},
		{"Token count measured", pkt.TokenCount > 0, plural(pkt.TokenCount, "token")},
		{"Text output rendered", len(text) > 0, plural(len(text), "char")},
		{"JSON output rendered", len(jsonStr) > 0, plural(len(jsonStr), "char")},
	}

	allPass := true
	for _, c := range checks {
		status := "PASS"
		if !c.pass {
			status = "FAIL"
			allPass = false
		}
		t.Logf("  [%s] %-30s %s", status, c.name, c.detail)
		if !c.pass {
			t.Errorf("GATE CHECK FAILED: %s — %s", c.name, c.detail)
		}
	}

	if allPass {
		t.Log("=== MVP1 GATE: ALL CHECKS PASSED ===")
	} else {
		t.Error("=== MVP1 GATE: SOME CHECKS FAILED ===")
	}
}

func graphHashPrefix(hash string, n int) string {
	if len(hash) < n {
		return hash
	}
	return hash[:n]
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	// simple plural
	if strings.HasSuffix(unit, "y") {
		return itoa(n) + " " + unit[:len(unit)-1] + "ies"
	}
	return itoa(n) + " " + unit + "s"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
