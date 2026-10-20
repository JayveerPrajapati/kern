package index

import (
	"encoding/json"
	"github.com/JayveerPrajapati/kern/internal/langparse"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func requireJavaCompiler(t *testing.T) {
	t.Helper()
	if !TreesitterEnabled() {
		t.Skip("Java compiler binding tests need the tree-sitter extraction path: without it (-tags notreesitter) the regex fallback does not extract same-line Java methods, so the javac binding has no symbols to attach edges to")
	}
	output, err := exec.Command("java", "-version").CombinedOutput()
	if err != nil {
		t.Skip("local JDK unavailable")
	}
	match := regexp.MustCompile(`version "(\d+)`).FindStringSubmatch(string(output))
	if len(match) != 2 {
		t.Skip("cannot determine local JDK version")
	}
	major, _ := strconv.Atoi(match[1])
	if major < 17 {
		t.Skip("Java compiler binding requires JDK 17+")
	}
}

func TestJavaCompilerBindingsResolveSameLineOverloads(t *testing.T) {
	requireJavaCompiler(t)
	source := `class Api { int choose(int value) { return value; } int choose(String value) { return 1; } void caller(int value) { choose(value); } void caller(String value) { choose(value); } }`
	root := writeTree(t, map[string]string{"Api.java": source})
	ix, err := BuildWithOptions(root, WithJavaCompiler(""))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Symbol{}
	for _, symbol := range ix.Symbols {
		byID[symbol.DeclarationID()] = symbol
	}
	calls := ix.Calls["Api.caller"]
	if len(calls) != 2 {
		t.Fatalf("overloaded caller edges = %+v, symbols = %+v", calls, ix.Symbols)
	}
	for _, call := range calls {
		caller, target := byID[call.FromID], byID[call.ToID]
		if call.Binding != "javac" || caller.Name != "caller" || target.Name != "choose" || len(caller.Params) != 1 || len(target.Params) != 1 || caller.Params[0] != target.Params[0] {
			t.Fatalf("wrong compiler overload/owner selection: %+v, caller=%+v target=%+v", call, caller, target)
		}
	}
	graph := ix.CallBindings()
	if len(graph.Edges) != 2 || graph.UnresolvedCalls != 0 {
		t.Fatalf("compiler facts remained ambiguous: %+v", graph)
	}
	for _, edge := range graph.Edges {
		if edge.Binding != "javac" {
			t.Fatalf("missing compiler provenance: %+v", edge)
		}
	}
	data, err := json.Marshal(ix)
	if err != nil {
		t.Fatal(err)
	}
	var restored Index
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !restored.JavaCompiler || restored.Calls["Api.caller"][0].ToID == "" {
		t.Fatal("JSON lost Java compiler metadata")
	}
	if err := os.WriteFile(filepath.Join(root, "Api.java"), []byte("\n"+source), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, err := Update(root, &restored)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.JavaCompiler || len(updated.Calls["Api.caller"]) != 2 || updated.Calls["Api.caller"][0].FromID == calls[0].FromID {
		t.Fatal("incremental Java compiler bindings retained stale identities")
	}
}

func TestJavaCompilerDispatchRequiresOverrides(t *testing.T) {
	requireJavaCompiler(t)
	ix, err := BuildWithOptions(writeTree(t, map[string]string{"Api.java": `
interface Job { int run(int value); }
class Good implements Job { public int run(int value) { return value; } int run(String value) { return 2; } }
class Derived extends Good { public int run(int value) { return 3; } }
class Unrelated { int run(int value) { return 4; } }
class Api { void dispatch(Job job) { job.run(1); } void exact() { new Good().run(1); } }
`}), WithJavaCompiler(""))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	byID := map[string]Symbol{}
	for _, symbol := range ix.Symbols {
		byID[symbol.DeclarationID()] = symbol
	}
	for _, edge := range ix.Calls["Api.dispatch"] {
		if edge.Synth == "" {
			continue
		}
		if edge.Synth != "dispatch:javac" || edge.Binding != "javac" || edge.Confidence != ConfidenceLow || edge.ToID == "" {
			t.Fatalf("dispatch provenance lost: %+v", edge)
		}
		seen[edge.Target] = true
		if len(byID[edge.ToID].Params) != 1 || byID[edge.ToID].Params[0] != "int value" {
			t.Fatalf("dispatch selected a different overload: %+v", edge)
		}
	}
	if len(seen) != 2 || !seen["Good.run"] || !seen["Derived.run"] {
		t.Fatalf("invalid runtime override candidates: %v", seen)
	}
	for _, edge := range ix.Calls["Api.exact"] {
		if edge.Synth != "" {
			t.Fatalf("fresh concrete receiver gained polymorphic targets: %+v", edge)
		}
	}
}

func TestJavaCompilerBindingsKeepExternalOverloadsDistinct(t *testing.T) {
	requireJavaCompiler(t)
	ix, err := BuildWithOptions(writeTree(t, map[string]string{"Api.java": "class Api { void call() { System.out.println(1); System.out.println(\"one\"); } }"}), WithJavaCompiler(""))
	if err != nil {
		t.Fatal(err)
	}
	calls := ix.Calls["Api.call"]
	if len(calls) != 2 || calls[0].Target == calls[1].Target || calls[0].Binding != "javac" || calls[1].Binding != "javac" || calls[0].ToID != "" || calls[1].ToID != "" {
		t.Fatalf("external overload references pooled or guessed: %+v", calls)
	}
	if !strings.Contains(langparse.JavaCompilerSource, "-proc:none") || !strings.Contains(langparse.JavaCompilerSource, "-implicit:none") {
		t.Fatal("javac must not execute annotation processors or generate implicit outputs")
	}
}

func TestJavaCompilerFailureDoesNotPublishBindings(t *testing.T) {
	requireJavaCompiler(t)
	root := writeTree(t, map[string]string{"Broken.java": "class Broken { void run() { missing(); } }"})
	ix, err := BuildWithOptions(root, WithJavaCompiler(""))
	if err == nil || ix != nil || !strings.Contains(err.Error(), "Java compiler bindings") {
		t.Fatalf("invalid source accepted: %v, %v", ix, err)
	}
}
