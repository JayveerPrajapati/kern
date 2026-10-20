package topology

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// The tests in this file moved from internal/intel/intel_test.go and
// internal/intel/intel_extra_test.go with the hubs/communities/architecture
// family; they use the writeTree fixture copy in fixtures_test.go.

func TestHubsRankByCallers(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       "package lib\n\nfunc Public() string {\n\treturn inner()\n}\n\nfunc inner() string {\n\treturn \"x\"\n}\n\nfunc Deep() {\n\tPublic()\n}\n\nfunc UntestedHot() {}\n",
		"client/client.go": "package client\n\nimport \"lib\"\n\nfunc Caller() {\n\tlib.Public()\n\tlib.UntestedHot()\n}\n\nfunc LocalOnly() {}\n",
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	hubs := Hubs(ix, 5)
	if len(hubs) == 0 {
		t.Fatal("expected at least one hub")
	}
	if hubs[0].Symbol != "Public" && hubs[0].Score < 3 {
		t.Errorf("expected Public to rank first, got %v", hubs[0])
	}
}

func TestBridgesDetectCrossPackage(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       "package lib\n\nfunc Public() string {\n\treturn inner()\n}\n\nfunc inner() string {\n\treturn \"x\"\n}\n\nfunc Deep() {\n\tPublic()\n}\n\nfunc UntestedHot() {}\n",
		"client/client.go": "package client\n\nimport \"lib\"\n\nfunc Caller() {\n\tlib.Public()\n\tlib.UntestedHot()\n}\n\nfunc LocalOnly() {}\n",
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	bridges := Bridges(ix, 10)
	found := false
	for _, b := range bridges {
		if b.Symbol == "Public" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Public to be a cross-package bridge, got %+v", bridges)
	}
}

func TestCommunitiesCluster(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       "package lib\n\nfunc Public() string {\n\treturn inner()\n}\n\nfunc inner() string {\n\treturn \"x\"\n}\n\nfunc Deep() {\n\tPublic()\n}\n\nfunc UntestedHot() {}\n",
		"client/client.go": "package client\n\nimport \"lib\"\n\nfunc Caller() {\n\tlib.Public()\n\tlib.UntestedHot()\n}\n\nfunc LocalOnly() {}\n",
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	comms := Communities(ix)
	if len(comms) == 0 {
		t.Fatal("expected at least one community")
	}
	big := comms[0]
	if big.Size < 3 {
		t.Errorf("expected the main cluster to span the call graph, got %+v", big)
	}
}

func TestCommunitiesGatedLargeRepo(t *testing.T) {
	ix := &index.Index{}
	for i := 0; i < index.MaxCommunitySymbols+1; i++ {
		ix.Symbols = append(ix.Symbols, index.Symbol{
			Kind: "func", Name: "f", File: "x.go", Line: i + 1,
		})
	}
	if comms := Communities(ix); len(comms) != 0 {
		t.Fatalf("expected no communities above the gate, got %d", len(comms))
	}
}

func TestAnalyzeArchitectureGatedLargeRepo(t *testing.T) {
	ix := &index.Index{Calls: map[string][]index.CallEdge{"main": {index.CallEdge{Target: "helper", Confidence: index.ConfidenceHigh}}}}
	for i := 0; i < index.MaxCommunitySymbols+1; i++ {
		ix.Symbols = append(ix.Symbols, index.Symbol{
			Kind: "func", Name: "f", File: "x.go", Line: i + 1,
		})
	}
	a := AnalyzeArchitecture(ix)
	if len(a.Communities) != 0 || len(a.Coupling) != 0 {
		t.Fatalf("expected empty architecture above the gate, got %d communities %d coupling", len(a.Communities), len(a.Coupling))
	}
	if !a.Gated {
		t.Errorf("expected Gated=true above the gate")
	}
	if a.SymbolCount != len(ix.Symbols) {
		t.Errorf("SymbolCount: got %d, want %d", a.SymbolCount, len(ix.Symbols))
	}
	if a.EdgeCount != 1 {
		t.Errorf("EdgeCount: got %d, want 1", a.EdgeCount)
	}
	// The render must surface a skip note, NOT the misleading
	// "no project-local call structure detected" message.
	out := RenderArch(a)
	if !strings.Contains(out, "skipped") {
		t.Errorf("expected skip note in render, got %q", out)
	}
	if strings.Contains(out, "no project-local call structure") {
		t.Errorf("gated render must not claim 'no call structure', got %q", out)
	}
}

func TestArchitectureOverview(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       "package lib\n\nfunc Public() string {\n\treturn inner()\n}\n\nfunc inner() string {\n\treturn \"x\"\n}\n\nfunc Deep() {\n\tPublic()\n}\n\nfunc UntestedHot() {}\n",
		"client/client.go": "package client\n\nimport \"lib\"\n\nfunc Caller() {\n\tlib.Public()\n\tlib.UntestedHot()\n}\n\nfunc LocalOnly() {}\n",
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := AnalyzeArchitecture(ix)
	if len(a.Communities) == 0 {
		t.Fatal("expected at least one community in the architecture view")
	}
	big := a.Communities[0]
	if big.Size < 3 {
		t.Errorf("expected the main community to span the call graph, got %+v", big)
	}
}
