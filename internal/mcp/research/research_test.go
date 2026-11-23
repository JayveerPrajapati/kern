package research

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	mcpgov "github.com/JayveerPrajapati/kern/internal/mcp/gov"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
)

func researchFixture(t *testing.T) *index.Index {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"guardrule/guardrule.go": `package guardrule

func ClassifyCommand(command string) bool { return command != "" }
`,
		"verifycmd/verify.go": `package verifycmd

import "guardrule"

func VerifyCommand(command string) bool { return guardrule.ClassifyCommand(command) }
`,
	}
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func TestResearchFiltersGovernedAnchorsAndStampsProvenance(t *testing.T) {
	ix := researchFixture(t)
	stamped := false
	gvc := mcpgov.GovContext{
		NewGov: func() (*mcpgov.Governor, error) {
			return &mcpgov.Governor{Allowed: map[string]bool{"ClassifyCommand": true}}, nil
		},
		StampGov: func(_ *mcpgov.Governor, symbols []provenance.SymbolProvenance) {
			stamped = true
			if len(symbols) == 0 {
				t.Fatal("governed research stamped no symbols")
			}
		},
		StampRaw: func([]provenance.SymbolProvenance) {
			t.Fatal("raw provenance must not be stamped when governance is active")
		},
	}

	out, err := Research(context.Background(), ix, gvc, map[string]any{
		"task": "ClassifyCommand and VerifyCommand reuse path",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ClassifyCommand") {
		t.Fatalf("allowed anchor filtered:\n%s", out)
	}
	if strings.Contains(out, "VerifyCommand func") || strings.Contains(out, "verifycmd/verify.go") {
		t.Fatalf("denied anchor or file leaked:\n%s", out)
	}
	if !stamped {
		t.Fatal("governed provenance was not stamped")
	}
}

func TestResearchRequiresTask(t *testing.T) {
	ix := researchFixture(t)
	gvc := mcpgov.GovContext{
		NewGov:   func() (*mcpgov.Governor, error) { return nil, nil },
		StampGov: func(*mcpgov.Governor, []provenance.SymbolProvenance) {},
		StampRaw: func([]provenance.SymbolProvenance) {},
	}
	if _, err := Research(context.Background(), ix, gvc, map[string]any{}); err == nil || err.Error() != "task is required" {
		t.Fatalf("error = %v, want task is required", err)
	}
}
