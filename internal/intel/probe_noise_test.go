package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// QA 5.6(a) (probe noise): kern_probe emitted noise anchors matching
// stopwords (body/to/on) as symbol-ish anchors and drew "connecting paths"
// ending at a GitHub-workflows YAML prop. The anchor filter drops non-code
// kinds (data-file props, docs headings) and all-lowercase stopword tokens.

// probeNoiseFixture builds an index with real code symbols PLUS YAML workflow
// props (the exact noise source from the finding): keys like "on", "to",
// "body" in .github/workflows/ci.yml.
func probeNoiseFixture() *index.Index {
	return &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "buildRelease", File: "release.go", Line: 10},
			{Kind: "func", Name: "verifyPipeline", File: "pipeline.go", Line: 20},
			{Kind: "func", Name: "UploadArtifact", File: "upload.go", Line: 30},
			{Kind: "prop", Name: "on", File: ".github/workflows/ci.yml", Line: 1},
			{Kind: "prop", Name: "to", File: ".github/workflows/ci.yml", Line: 2},
			{Kind: "prop", Name: "body", File: ".github/workflows/ci.yml", Line: 3},
			{Kind: "heading", Name: "Introduction", File: "README.md", Line: 1},
		},
		Calls:   map[string][]index.CallEdge{},
		Callers: map[string][]string{},
	}
}

// TestProbeAnchorsExcludeYAMLPropsAndHeadings: a task containing the
// stopword-shaped YAML props ("on", "to", "body") must anchor NONE of them —
// and no "connecting path" may end at a prop.
func TestProbeAnchorsExcludeYAMLPropsAndHeadings(t *testing.T) {
	ix := probeNoiseFixture()
	r := Probe(ix, "how does the request body flow on to the build release pipeline", 4000)
	for _, a := range r.Anchors {
		if strings.Contains(a.File, ".yml") || a.Kind == "prop" {
			t.Errorf("anchor %s (%s %s:%d) must not be a YAML prop", a.Name, a.Kind, a.File, a.Line)
		}
		if a.Kind == "heading" {
			t.Errorf("anchor %s must not be a docs heading", a.Name)
		}
	}
	for _, p := range r.Paths {
		for _, node := range p {
			if node == "on" || node == "to" || node == "body" {
				t.Errorf("connecting path %v contains a YAML prop", p)
			}
		}
	}
}

// TestProbeYAMLishTaskAllowsProps: a task that IS about the yml file may
// anchor prop keys — the filter only excludes them for non-YAML tasks.
func TestProbeYAMLishTaskAllowsProps(t *testing.T) {
	ix := probeNoiseFixture()
	r := Probe(ix, "check the ci.yml workflow on trigger", 4000)
	foundProp := false
	for _, a := range r.Anchors {
		if a.Kind == "prop" {
			foundProp = true
		}
	}
	if !foundProp {
		t.Errorf("YAML-ish task must be allowed prop anchors, got %+v", r.Anchors)
	}
}

// TestProbeExactResolvedCodeSymbolsStillAnchor pins the documented precedence:
// capitalized code symbols that collide with English verbs keep anchoring
// exactly (Add), and camelCase prose that resolves stays anchored — the
// filter must not over-prune real code.
func TestProbeExactResolvedCodeSymbolsStillAnchor(t *testing.T) {
	ix := probeNoiseFixture()
	r := Probe(ix, "UploadArtifact failed in the build release step", 4000)
	found := false
	for _, a := range r.Anchors {
		if a.Name == "UploadArtifact" {
			found = true
			if a.Fuzzy {
				t.Error("exact anchor UploadArtifact must not be labeled fuzzy")
			}
		}
	}
	if !found {
		t.Errorf("real code symbol UploadArtifact must anchor, got %+v", r.Anchors)
	}
}
