package meta_test

import (
	"reflect"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
	"github.com/JayveerPrajapati/kern/internal/mcp/meta"
)

func TestSemanticMetaRouteHits(t *testing.T) {
	tool, args, score, ok := meta.SemanticMetaRoute("show me the implementation plan")
	if !ok {
		t.Fatal("expected a semantic route, got none")
	}
	if tool != "kern_plan" {
		t.Fatalf("tool = %q, want kern_plan", tool)
	}
	if score < 0.16 {
		t.Errorf("score = %.3f, want >= 0.16", score)
	}
	if _, has := args["change"]; !has {
		t.Errorf("args = %+v, want a query-ish param (change)", args)
	}
}

func TestSemanticMetaRouteBuddyRescue(t *testing.T) {
	tool, _, _, ok := meta.SemanticMetaRoute("brief me on this project conventions")
	if !ok || tool != "kern_buddy" {
		t.Fatalf("route = %q ok=%v, want kern_buddy", tool, ok)
	}
}

func TestSemanticMetaRouteExplanationRestricted(t *testing.T) {
	for _, phrase := range []string{
		"how does the llm provider work",
		"how does the index work",
		"what is the pack tool",
	} {
		if _, _, _, ok := meta.SemanticMetaRoute(phrase); ok {
			t.Errorf("SemanticMetaRoute(%q) ok = true, want false (explanation questions stay on the search fallback)", phrase)
		}
	}
}

func TestSemanticMetaRouteGibberish(t *testing.T) {
	tool, args := meta.ClassifyMetaRequest("qwerty zxcv asdf")
	if tool != "kern_search" {
		t.Fatalf("ClassifyMetaRequest = %q, want kern_search", tool)
	}
	if args["query"] != "qwerty zxcv asdf" {
		t.Errorf("args = %+v, want raw request as query", args)
	}
}

func TestClassifyMetaRequestEscalationMarker(t *testing.T) {
	tool, args := meta.ClassifyMetaRequest("brief me on this project conventions")
	if tool != "kern_buddy" {
		t.Fatalf("tool = %q, want kern_buddy (escalated)", tool)
	}
	if v, _ := args[meta.ViaSemanticArg].(bool); !v {
		t.Errorf("via_semantic marker missing from args: %+v", args)
	}
}

func TestClassifyMetaRequestDeterministicRoutesUntouched(t *testing.T) {
	cases := []string{
		"how does NewServer work",
		"plan adding a greet function",
		"verify this",
		"what breaks if I change dispatch",
		"how does CLI command dispatch work in this repo?",
	}
	for _, c := range cases {
		_, args := meta.ClassifyMetaRequest(c)
		if v, _ := args[meta.ViaSemanticArg].(bool); v {
			t.Errorf("ClassifyMetaRequest(%q) carries via_semantic; keyword routes must not", c)
		}
	}
}

// TestMetaToolTokensCached guards the semantic-router token cache: the
// second lookup returns the same slice (no retokenization), with content
// identical to a fresh MetaTokens computation.
func TestMetaToolTokensCached(t *testing.T) {
	if len(catalog.All) == 0 {
		t.Fatal("catalog empty")
	}
	tool := catalog.All[0]
	a := meta.MetaToolTokens(tool)
	b := meta.MetaToolTokens(tool)
	if len(a) == 0 {
		t.Fatalf("empty tokens for tool %q", tool.Name)
	}
	if reflect.ValueOf(a).Pointer() != reflect.ValueOf(b).Pointer() {
		t.Fatal("MetaToolTokens retokenized on second call - cache not effective")
	}
	if want := meta.MetaTokens(tool.Name + " " + tool.Description); !reflect.DeepEqual(a, want) {
		t.Fatalf("cached tokens diverge from fresh computation")
	}
}
