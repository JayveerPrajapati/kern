package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// safeChangeRoot builds a Platform over a temp copy of the fixture.
func safeChangeRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	entries, err := os.ReadDir("testdata/safechange")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join("testdata/safechange", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestAnalyzeFallsBackToResolvableSymbol verifies the robustness
// fix: a natural-language request that names several identifiers — such as the
// flagship "Add tenant-aware caching to UserService" — must analyze successfully
// even though the FIRST extracted candidate ("tenant") is not a graph symbol.
// Without the fallback, Platform.Analyze failed with
// `context: symbol "tenant" not found in graph`.
func TestAnalyzeFallsBackToResolvableSymbol(t *testing.T) {
	root := safeChangeRoot(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const flagship = "Add tenant-aware caching to UserService"
	pkt, text, err := p.Analyze(flagship)
	if err != nil {
		t.Fatalf("Analyze(%q): %v", flagship, err)
	}
	if len(pkt.Symbols) == 0 {
		t.Error("analyze returned no symbols")
	}
	found := false
	for _, s := range pkt.Symbols {
		if s.Name == "UserService" {
			found = true
		}
	}
	if !found {
		t.Errorf("analyze did not resolve to UserService; symbols = %v", symbolNames(pkt))
	}
	if text == "" {
		t.Error("analyze returned empty rendered text")
	}
}

// TestRiskFallsBackToResolvableSymbol verifies the same fallback on the Risk
// path (which previously passed the raw multi-word change straight into the
// context engine and failed identically).
func TestRiskFallsBackToResolvableSymbol(t *testing.T) {
	root := safeChangeRoot(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pkt, text, err := p.Risk("Add tenant-aware caching to UserService")
	if err != nil {
		t.Fatalf("Risk(%q): %v", "Add tenant-aware caching to UserService", err)
	}
	if len(pkt.Symbols) == 0 {
		t.Error("risk returned no symbols")
	}
	if text == "" {
		t.Error("risk returned empty rendered text")
	}
}

// symbolNames extracts symbol names from a context packet.
func symbolNames(pkt domain.ContextPacket) []string {
	var out []string
	for _, s := range pkt.Symbols {
		out = append(out, s.Name)
	}
	return out
}
