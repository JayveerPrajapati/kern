package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestGraphCtxPreservesSourceScopedNeighbors(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Name: "helper", File: "a/main.go", Lang: "go", Kind: "func", Line: 1},
			{Name: "helper", File: "b/main.go", Lang: "go", Kind: "func", Line: 1},
			{Name: "run", File: "a/main.go", Lang: "go", Kind: "func", Line: 4},
			{Name: "run", File: "b/main.go", Lang: "go", Kind: "func", Line: 4},
		},
		Calls: map[string][]index.CallEdge{
			"run": {
				{Target: "helper", SourceFile: "a/main.go", Confidence: index.ConfidenceHigh},
				{Target: "helper", SourceFile: "b/main.go", Confidence: index.ConfidenceMedium},
			},
		},
	}
	text, err := GraphCtx(ix, "a.helper", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "run [EXTRACTED] — a/main.go:4") || strings.Contains(text, "b/main.go") || strings.Contains(text, "decl:") {
		t.Fatalf("graph context lost labels or mixed source ownership: %s", text)
	}
}

func TestGraphCtxFlagsAmbiguousBinding(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Name: "run", Receiver: "App", File: "App.java", Lang: "java", Kind: "method", Line: 1},
			{Name: "run", Receiver: "App", File: "App.java", Lang: "java", Kind: "method", Line: 4},
			{Name: "helper", Receiver: "App", File: "App.java", Lang: "java", Kind: "method", Line: 10},
		},
		Calls: map[string][]index.CallEdge{
			"App.run": {{Target: "helper", SourceFile: "App.java", Confidence: index.ConfidenceHigh}},
		},
	}
	text, err := GraphCtx(ix, "App.run", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "helper [EXTRACTED] — App.java:10 (binding: ambiguous)") {
		t.Fatalf("ambiguous overload binding hidden: %s", text)
	}
}
