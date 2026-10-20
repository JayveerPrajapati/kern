//go:build !notreesitter

package index

import (
	"testing"
)

func TestTreeSitterAvailableMapping(t *testing.T) {
	cases := []struct {
		lang, rel string
		want      bool
	}{
		{"python", "x.py", true},
		{"dart", "a.dart", true},
		{"typescript", "a.ts", true},
		{"typescript", "a.tsx", true},
		{"shell", "a.sh", true},
		{"go", "a.go", true},
		{"csharp", "a.cs", false}, // no csharp grammar wired
		{"markdown", "a.md", false},
	}
	for _, c := range cases {
		got := TreeSitterAvailable(c.lang)
		if got != c.want {
			t.Errorf("TreeSitterAvailable(%q) = %v; want %v", c.lang, got, c.want)
		}
	}
}

func TestTreeSitterLanguageFor(t *testing.T) {
	cases := []struct {
		lang, rel string
		want      string
	}{
		{"typescript", "a.ts", "typescript"},
		{"typescript", "a.tsx", "tsx"},
		{"shell", "a.sh", "bash"},
		{"python", "a.py", "python"},
	}
	for _, c := range cases {
		got, ok := tsLanguageFor(c.lang, c.rel)
		if !ok || got != c.want {
			t.Errorf("tsLanguageFor(%q,%q) = %q,%v; want %q,true", c.lang, c.rel, got, ok, c.want)
		}
	}
}

func TestTreeSitterExtractPython(t *testing.T) {
	src := `import os

def helper():
    return 1

class Service:
    def run(self):
        helper()
        return os.path.join("a", "b")
`
	syms, calls, _, _, err := tsExtract("svc.py", []byte(src), "python")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Symbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}
	if _, ok := byName["helper"]; !ok {
		t.Errorf("expected helper func, got %v", syms)
	}
	svc, ok := byName["Service"]
	if !ok || svc.Kind != "class" {
		t.Errorf("expected Service class, got %+v", svc)
	}
	run, ok := byName["run"]
	if !ok || run.Kind != "method" {
		t.Errorf("expected run method, got %+v", run)
	}
	if run.Receiver != "Service" {
		t.Errorf("run receiver = %q; want Service", run.Receiver)
	}
	found := false
	for _, ce := range calls["Service.run"] {
		c := ce.Target
		if c == "helper" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Service.run -> helper call edge, got %v", calls["Service.run"])
	}
}

func TestTreeSitterExtractShell(t *testing.T) {
	src := `#!/bin/bash

deploy() {
  echo "deploying"
}

backup() {
  deploy
}
`
	syms, calls, _, _, err := tsExtract("ci.sh", []byte(src), "shell")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Symbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}
	if _, ok := byName["deploy"]; !ok {
		t.Errorf("expected deploy func, got %v", syms)
	}
	found := false
	for _, ce := range calls["backup"] {
		c := ce.Target
		if c == "deploy" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected backup -> deploy call edge, got %v", calls["backup"])
	}
}

func TestTreeSitterExtractTSX(t *testing.T) {
	src := `import React from "react";

interface Props {
  title: string;
}

export function Header() {
  return <h1>{props.title}</h1>;
}
`
	syms, _, _, _, err := tsExtract("Header.tsx", []byte(src), "typescript")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	foundProps := false
	for _, s := range syms {
		if s.Name == "Header" && s.Kind == "func" {
			found = true
		}
		if s.Name == "Props" && s.Kind == "interface" {
			foundProps = true
		}
	}
	if !found {
		t.Errorf("expected Header func from tsx grammar, got %v", syms)
	}
	if !foundProps {
		t.Errorf("expected Props interface from tsx grammar, got %v", syms)
	}
}

func TestTreeSitterExtractRust(t *testing.T) {
	src := `struct Config {
    name: String,
}

impl Config {
    fn load(&self) -> u32 {
        1
    }
}

fn main() {
    let c = Config { name: String::new() };
    c.load();
}
`
	syms, calls, _, _, err := tsExtract("main.rs", []byte(src), "rust")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Symbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}
	if _, ok := byName["Config"]; !ok {
		t.Errorf("expected Config struct, got %v", syms)
	}
	load, ok := byName["load"]
	if !ok || load.Kind != "method" {
		t.Errorf("expected load method, got %+v", load)
	}
	if load.Receiver != "Config" {
		t.Errorf("load receiver = %q; want Config", load.Receiver)
	}
	if len(calls["main"]) == 0 {
		t.Errorf("expected main to have calls, got %v", calls)
	}
}

func TestTreeSitterExtractDart(t *testing.T) {
	src := `class Cat extends Animal implements Pet {
  String name = "cat";

  String meow(String who) {
    return greet(who) + name;
  }

  String get label => name;

  set label(String v) {
    name = v;
  }
}

void main() {
  final c = Cat();
  c.meow("x");
  greet("y");
}

String greet(String who) => "hi " + who;
`
	syms, calls, inherits, _, err := tsExtract("main.dart", []byte(src), "dart")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Symbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}
	cat, ok := byName["Cat"]
	if !ok || cat.Kind != "class" {
		t.Errorf("expected Cat class, got %+v", cat)
	}
	meow, ok := byName["meow"]
	if !ok || meow.Kind != "method" || meow.Receiver != "Cat" {
		t.Errorf("expected Cat.meow method, got %+v", meow)
	}
	if _, ok := byName["greet"]; !ok {
		t.Errorf("expected greet func, got %v", syms)
	}
	bases := inherits["Cat"]
	if len(bases) != 2 {
		t.Fatalf("expected 2 bases for Cat, got %v", bases)
	}
	hasExt, hasImp := false, false
	for _, b := range bases {
		if b == "extends:Animal" {
			hasExt = true
		}
		if b == "implements:Pet" {
			hasImp = true
		}
	}
	if !hasExt || !hasImp {
		t.Errorf("expected extends:Animal and implements:Pet, got %v", bases)
	}
	if len(calls["main"]) == 0 {
		t.Errorf("expected main to have calls, got %v", calls)
	}
	gotCall := false
	for _, ce := range calls["main"] {
		c := ce.Target
		if c == "meow" || c == "greet" || c == "Cat" {
			gotCall = true
		}
	}
	if !gotCall {
		t.Errorf("expected main to call meow/greet/Cat, got %v", calls["main"])
	}
}

// TestTreeSitterSymbolAndCallConfidence verifies the tree-sitter extractor
// attaches confidence: direct declarations HIGH, calls to local definitions
// HIGH, unresolved targets MEDIUM.
func TestTreeSitterSymbolAndCallConfidence(t *testing.T) {
	src := `function localHelper() {
	return 1;
}

export function main() {
	localHelper();
	externalCall();
}

export class Service {
	run() {
		return main();
	}
}
`
	syms, calls, _, _, err := tsExtract("app.ts", []byte(src), "typescript")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range syms {
		if s.Confidence != ConfidenceHigh {
			t.Errorf("symbol %s confidence = %q, want HIGH", s.FullName(), s.Confidence)
		}
	}
	// main -> localHelper is a call to a locally-defined symbol: HIGH.
	// main -> externalCall is unresolved: MEDIUM.
	found := false
	for _, ce := range calls["main"] {
		if ce.Target == "localHelper" {
			found = true
			if ce.Confidence != ConfidenceHigh {
				t.Errorf("main->localHelper confidence = %q, want HIGH", ce.Confidence)
			}
		}
		if ce.Target == "externalCall" && ce.Confidence != ConfidenceMedium {
			t.Errorf("main->externalCall confidence = %q, want MEDIUM (unresolved)", ce.Confidence)
		}
	}
	if !found {
		t.Errorf("expected main->localHelper edge, got %v", CallEdgeTargets(calls["main"]))
	}
}

// TestTreeSitterArrowFunctionMediumConfidence: a const holding an arrow
// function is promoted to a func symbol — an inferred kind, so MEDIUM.
func TestTreeSitterArrowFunctionMediumConfidence(t *testing.T) {
	src := `const greet = (name) => "hi " + name;
export function run() {
	return greet("x");
}
`
	syms, _, _, _, err := tsExtract("app.js", []byte(src), "javascript")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range syms {
		if s.Name == "greet" {
			if s.Kind != "func" {
				t.Errorf("greet kind = %q, want func (promoted from const)", s.Kind)
			}
			if s.Confidence != ConfidenceMedium {
				t.Errorf("greet confidence = %q, want MEDIUM (inferred kind)", s.Confidence)
			}
			return
		}
	}
	t.Error("expected promoted arrow-function symbol greet")
}

func TestTreeSitterExtractTypeScriptAdvanced(t *testing.T) {
	src := `export enum Status {
	Active = "ACTIVE",
	Pending = "PENDING",
}

export type ID = string | number;

export abstract class BaseService {
	abstract execute(): Promise<void>;
}

export class UserWorker extends BaseService {
	async execute(): Promise<void> {
		this.cleanup();
	}

	private cleanup(): void {}
}
`
	syms, calls, inherits, _, err := tsExtract("worker.ts", []byte(src), "typescript")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Symbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}

	if status, ok := byName["Status"]; !ok || status.Kind != "enum" {
		t.Errorf("expected Status enum, got %+v", status)
	}
	if idType, ok := byName["ID"]; !ok || idType.Kind != "type" {
		t.Errorf("expected ID type alias, got %+v", idType)
	}
	if base, ok := byName["BaseService"]; !ok || base.Kind != "class" {
		t.Errorf("expected BaseService class, got %+v", base)
	}
	if worker, ok := byName["UserWorker"]; !ok || worker.Kind != "class" {
		t.Errorf("expected UserWorker class, got %+v", worker)
	}

	bases := inherits["UserWorker"]
	hasBaseExt := false
	for _, b := range bases {
		if b == "extends:BaseService" {
			hasBaseExt = true
		}
	}
	if !hasBaseExt {
		t.Errorf("expected UserWorker to extend BaseService, got %v", bases)
	}

	cleanupFound := false
	for _, ce := range calls["UserWorker.execute"] {
		if ce.Target == "this.cleanup" || ce.Target == "cleanup" {
			cleanupFound = true
		}
	}
	if !cleanupFound {
		t.Logf("calls for UserWorker.execute: %v", calls["UserWorker.execute"])
	}
}

func TestTreeSitterExtractCpp(t *testing.T) {
	src := `class Engine {
public:
    void start();
};

void Engine::start() {
    init();
}
`
	syms, calls, _, _, err := tsExtract("engine.cpp", []byte(src), "cpp")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Symbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}

	if eng, ok := byName["Engine"]; !ok || eng.Kind != "class" {
		t.Errorf("expected Engine class, got %+v", eng)
	}
	if start, ok := byName["start"]; !ok || start.Kind != "method" || start.Receiver != "Engine" {
		t.Errorf("expected Engine.start method, got %+v", start)
	}
	if len(calls) == 0 {
		t.Logf("Cpp calls extracted: %v", calls)
	}
}

// TestTreeSitterFileOwnedEdgeConfidencePromotion verifies recommendation B
// part 2: a file-owned edge (file:<rel>) whose target is a verified
// tree-sitter symbol is promoted from MEDIUM to HIGH, while a dotted/external
// callee stays MEDIUM (the AST cannot verify it). Guards
// promoteFileEdgesToASTConfidence against regressions.
func TestTreeSitterFileOwnedEdgeConfidencePromotion(t *testing.T) {
	src := []byte(`const btn = document.getElementById("go");
btn.addEventListener("click", () => {
  runReport();
});
function runReport() { fetch("/api"); }
`)
	_, calls, _, _, err := tsExtract("report.js", src, "javascript")
	if err != nil {
		t.Fatal(err)
	}
	edges := calls["file:report.js"]
	if len(edges) == 0 {
		t.Fatalf("expected file:report.js owned edges, got %v", calls)
	}
	var runReportEdge, btnEdge *CallEdge
	for i := range edges {
		switch edges[i].Target {
		case "runReport":
			runReportEdge = &edges[i]
		case "btn.addEventListener":
			btnEdge = &edges[i]
		}
	}
	// runReport is a file-scope, non-method symbol tree-sitter verified:
	// the file-owned edge to it must be promoted to HIGH.
	if runReportEdge == nil {
		t.Fatalf("expected file:report.js -> runReport edge, got %v", CallEdgeTargets(edges))
	}
	if runReportEdge.Confidence != ConfidenceHigh {
		t.Errorf("file:report.js -> runReport confidence = %q, want HIGH (verified symbol)", runReportEdge.Confidence)
	}
	// btn.addEventListener is a dotted target through an unknown receiver
	// (btn is a const, not a verified type): must stay MEDIUM.
	if btnEdge == nil {
		t.Fatalf("expected file:report.js -> btn.addEventListener edge, got %v", CallEdgeTargets(edges))
	}
	if btnEdge.Confidence != ConfidenceMedium {
		t.Errorf("file:report.js -> btn.addEventListener confidence = %q, want MEDIUM (unresolved receiver)", btnEdge.Confidence)
	}
}

// TestTreeSitterFileOwnedMethodShortNameNotPromoted verifies the F3 boundary:
// a top-level call whose target name matches only a METHOD's short name must
// stay MEDIUM — the AST verified Greeter.greet, not a bare top-level greet().
func TestTreeSitterFileOwnedMethodShortNameNotPromoted(t *testing.T) {
	src := []byte(`class Greeter {
  greet() { return "hi"; }
}
greet();
`)
	_, calls, _, _, err := tsExtract("greeter.ts", src, "typescript")
	if err != nil {
		t.Fatal(err)
	}
	edges := calls["file:greeter.ts"]
	var greetEdge *CallEdge
	for i := range edges {
		if edges[i].Target == "greet" {
			greetEdge = &edges[i]
		}
	}
	if greetEdge == nil {
		t.Fatalf("expected file:greeter.ts -> greet edge, got %v", CallEdgeTargets(edges))
	}
	if greetEdge.Confidence != ConfidenceMedium {
		t.Errorf("file:greeter.ts -> greet confidence = %q, want MEDIUM (matches only Greeter.greet's short name)", greetEdge.Confidence)
	}
}

// TestTreeSitterPythonNestedDefsDistinctIdentity pins the WD-3 fix: two
// same-named helpers nested in DIFFERENT test methods must carry distinct
// receivers — keyed on the class receiver they shared one identity and the
// fail-loud duplicate check refused to index the whole file.
func TestTreeSitterPythonNestedDefsDistinctIdentity(t *testing.T) {
	src := `class TestStreams:
    def test_one(self):
        def _helper():
            return 1
        return _helper()

    def test_two(self):
        def _helper():
            return 2
        return _helper()
`
	syms, _, _, _, err := tsExtract("test_streams.py", []byte(src), "python")
	if err != nil {
		t.Fatal(err)
	}
	recv := map[string]bool{}
	for _, s := range syms {
		if s.Name == "_helper" {
			recv[s.Receiver] = true
		}
	}
	if !recv["test_one"] || !recv["test_two"] {
		t.Errorf("nested same-named helpers must carry their enclosing method receivers (test_one, test_two), got %v", recv)
	}
}

// TestTreeSitterPythonDoubleNestedDefsDistinctIdentity pins the nested-def
// extractor fix: two same-named helpers nested TWO levels deep (inside
// identically-named closure factories inside DIFFERENT test methods) must
// still carry distinct receivers. Stopping at the nearest function ancestor
// collapses both to the factory name; the outermost enclosing method must
// win. This mirrors the log_checker tests/test_shell.py `async def gen()`
// collision (lines 529 and 1209) that made the whole repo un-indexable.
func TestTreeSitterPythonDoubleNestedDefsDistinctIdentity(t *testing.T) {
	src := `class TestShell:
    async def test_shell_current_multi_pod_groups(self):
        async def _factory(pod):
            async def gen():
                yield "a"
            return gen()
        return await _factory(None)

    async def test_stream_pod_lines_captures_multi(self):
        async def _factory(pod):
            async def gen():
                yield "b"
            return gen()
        return await _factory(None)
`
	syms, _, _, _, err := tsExtract("test_shell.py", []byte(src), "python")
	if err != nil {
		t.Fatal(err)
	}
	recv := map[string]bool{}
	for _, s := range syms {
		if s.Name == "gen" {
			recv[s.Receiver] = true
		}
	}
	if !recv["test_shell_current_multi_pod_groups"] || !recv["test_stream_pod_lines_captures_multi"] {
		t.Errorf("double-nested same-named helpers must carry their OUTERMOST enclosing method receivers, got %v", recv)
	}
}

// TestTreeSitterPythonOverloadStubsSkipped pins the @overload fix: multiple
// typing.@overload __init__ stubs followed by one real implementation must
// extract as a SINGLE __init__ symbol carrying the IMPL's line number — the
// stubs are signatures only and must not claim the identity. Before the fix,
// stub and impl shared one identity and the fail-loud duplicate gate refused
// to index the whole file (live repro: psf/requests src/requests/auth.py
// lines 92/94). Stacked decorators (staticmethod combined with @overload, in
// both orders) must still be detected.
func TestTreeSitterPythonOverloadStubsSkipped(t *testing.T) {
	src := `from typing import overload

class A:
    @overload
    def __init__(self, x: int) -> None: ...
    @overload
    def __init__(self, x: str) -> None: ...
    def __init__(self, x):
        self.x = x

class B:
    @staticmethod
    @overload
    def parse(x: int) -> None: ...
    @overload
    @staticmethod
    def parse(x: str) -> None: ...
    @staticmethod
    def parse(x):
        return x
`
	syms, _, _, _, err := tsExtract("repro.py", []byte(src), "python")
	if err != nil {
		t.Fatal(err)
	}
	// The fail-loud duplicate gate must pass: stubs skipped, impls survive.
	if err := checkFileSymbolConflicts("repro.py", syms); err != nil {
		t.Fatalf("overload stubs must not conflict with their impls: %v", err)
	}
	byName := map[string]Symbol{}
	for _, s := range syms {
		byName[s.Name+"\x00"+s.Receiver] = s
	}
	init, ok := byName["__init__\x00A"]
	if !ok {
		t.Fatalf("expected A.__init__ impl symbol, got %+v", syms)
	}
	if init.Kind != "method" {
		t.Errorf("A.__init__ kind = %q; want method", init.Kind)
	}
	if init.Line != 8 {
		t.Errorf("A.__init__ line = %d; want the IMPL line 8 (stubs at 4/6 must be skipped), syms: %+v", init.Line, syms)
	}
	parse, ok := byName["parse\x00B"]
	if !ok {
		t.Fatalf("expected B.parse impl symbol, got %+v", syms)
	}
	if parse.Line != 19 {
		t.Errorf("B.parse line = %d; want the IMPL line 19 (stacked-decorator stubs at 13/16 skipped), syms: %+v", parse.Line, syms)
	}
}

// TestTreeSitterPythonGenuineDuplicateStillFailsLoud: skipping @overload
// stubs must NOT mask real duplicates — two defs with the same identity and
// NO @overload decorator must still trip the fail-loud duplicate gate.
func TestTreeSitterPythonGenuineDuplicateStillFailsLoud(t *testing.T) {
	src := `class A:
    def __init__(self, x):
        self.x = x
    def __init__(self, y):
        self.y = y
`
	syms, _, _, _, err := tsExtract("dup.py", []byte(src), "python")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkFileSymbolConflicts("dup.py", syms); err == nil {
		t.Fatal("genuine duplicate (same identity, no @overload) must still fail loudly")
	}
}

// TestTreeSitterJavaAnonymousClassMethodsDistinct pins the WD-6b fix: two
// same-signature methods in sibling ANONYMOUS classes inside one method get
// per-occurrence receivers — keyed on the outer class they shared one
// identity and the fail-loud duplicate check refused the whole file.
func TestTreeSitterJavaAnonymousClassMethodsDistinct(t *testing.T) {
	src := `class StrategyTest {
    void testIt() {
        Runnable high = new Runnable() {
            public int getPriority() { return 10; }
        };
        Runnable low = new Runnable() {
            public int getPriority() { return 200; }
        };
    }
}
`
	syms, _, _, _, err := tsExtract("StrategyTest.java", []byte(src), "java")
	if err != nil {
		t.Fatal(err)
	}
	recv := map[string]bool{}
	for _, s := range syms {
		if s.Name == "getPriority" {
			recv[s.Receiver] = true
		}
	}
	if len(recv) != 2 {
		t.Errorf("same-signature methods in sibling anonymous classes must carry distinct per-occurrence receivers, got %v", recv)
	}
}

// TestTreeSitterJavaSameNamedNestedClassesDistinct pins the WD-6c fix: two
// same-named nested classes under DIFFERENT parents stay distinct via Outer
// — flat (name, kind) they shared one identity and the duplicate check
// refused the whole file.
func TestTreeSitterJavaSameNamedNestedClassesDistinct(t *testing.T) {
	src := `class OuterTest {
    class DefaultTests {
        class MappingTests {
            void mapIt() {}
        }
    }
    class StrictTests {
        class MappingTests {
            void mapIt() {}
        }
    }
}
`
	syms, _, _, _, err := tsExtract("OuterTest.java", []byte(src), "java")
	if err != nil {
		t.Fatal(err)
	}
	outer := map[string]bool{}
	for _, s := range syms {
		if s.Name == "MappingTests" && s.Kind == "class" {
			outer[s.Outer] = true
		}
	}
	if !outer["DefaultTests"] || !outer["StrictTests"] {
		t.Errorf("same-named nested classes under different parents must carry distinct Outers, got %v", outer)
	}
}

// TestTreeSitterJavaFieldTypedInterfaceReceiverDistinct pins the P2-3 fix:
// a call on an interface-TYPED FIELD (Spring @Autowired
// `apiListenerService.executeNotification(...)`) must resolve to the
// interface-qualified callee (APIListenerService.executeNotification) so the
// graph's dispatch-hint machinery can list concrete implementations. Field
// types are not locals; before the fix the receiver stayed unresolved and no
// dispatch hints could be produced.
func TestTreeSitterJavaFieldTypedInterfaceReceiverDistinct(t *testing.T) {
	src := `class Service {
    private APIListenerService apiListenerService;

    public void run() {
        apiListenerService.executeNotification("x");
    }
}
`
	syms, calls, _, _, err := tsExtract("Service.java", []byte(src), "java")
	if err != nil {
		t.Fatal(err)
	}
	_ = syms
	found := false
	for _, ce := range calls["Service.run"] {
		if ce.Target == "APIListenerService.executeNotification" {
			found = true
		}
	}
	if !found {
		t.Errorf("calls[Service.run] = %v; want it to contain APIListenerService.executeNotification (field-typed receiver resolved)", CallEdgeTargets(calls["Service.run"]))
	}
	for _, ce := range calls["Service.run"] {
		if ce.Target == "apiListenerService.executeNotification" {
			t.Errorf("calls[Service.run] must not keep the unresolved field-receiver callee, got %v", CallEdgeTargets(calls["Service.run"]))
		}
	}
}

// TestTreeSitterJavaSameNamedMethodsInSameNamedClassesDistinct pins the
// WD-6d fix: two same-named methods inside two same-named nested classes
// get the receiver class's own parent in Outer — Receiver alone ("MappingTests")
// collided and the duplicate check refused the whole file.
func TestTreeSitterJavaSameNamedMethodsInSameNamedClassesDistinct(t *testing.T) {
	src := `class OuterTest {
    class DefaultTests {
        class MappingTests {
            void mapIt() {}
        }
    }
    class StrictTests {
        class MappingTests {
            void mapIt() {}
        }
    }
}
`
	syms, _, _, _, err := tsExtract("OuterTest.java", []byte(src), "java")
	if err != nil {
		t.Fatal(err)
	}
	pairs := map[string]bool{}
	for _, s := range syms {
		if s.Name == "mapIt" {
			pairs[s.Receiver+"|"+s.Outer] = true
		}
	}
	if !pairs["MappingTests|DefaultTests"] || !pairs["MappingTests|StrictTests"] {
		t.Errorf("same-named methods in same-named nested classes must carry distinct (receiver, outer) identities, got %v", pairs)
	}
}

// TestTreeSitterAnonymousArrowParamsNotSymbols locks the anonymous-arrow fix:
// an arrow function has no name of its own, and ExtractName's child scan used
// to return its FIRST PARAMETER ("n" in `xs.map(n => n * 2)`) as the symbol
// name — a phantom method per callback that collided on the fail-loud
// duplicate gate when the same parameter name repeated inside one enclosing
// function (live repro: wf_project conduit AIAssistantPanel.tsx, index
// refused). Arrow functions are not symbol nodes; named bindings keep their
// symbol via the const-declaration promotion.
func TestTreeSitterAnonymousArrowParamsNotSymbols(t *testing.T) {
	src := `const a = 1;
const handler = (n: number) => n + 1;
export function main() {
  const xs = [1, 2].map(n => n * 2);
  const ys = [3, 4].map(n => n * 3);
  return xs.length + ys.length + a + handler(2);
}
`
	syms, _, _, _, err := tsExtract("app.tsx", []byte(src), "typescript")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range syms {
		if s.Name == "n" {
			t.Errorf("anonymous arrow parameter leaked as a symbol: %+v", s)
		}
	}
	// The conflict gate must accept the file (this is the exact shape the
	// live repro refused to index).
	if err := checkFileSymbolConflicts("app.tsx", syms); err != nil {
		t.Fatalf("arrow callback file must pass the duplicate gate: %v", err)
	}
	// Named arrow bindings still promote const→func.
	found := false
	for _, s := range syms {
		if s.Name == "handler" && s.Kind == "func" {
			found = true
		}
	}
	if !found {
		t.Errorf("named arrow binding must keep its promoted func symbol, got %v", syms)
	}
}

// TestTreeSitterCSSReselectionDeduped locks the CSS redeclarative fix:
// pseudo variants (.btn:disabled), compound selectors (.data-table th) and
// media-query re-selections of the same class used to extract as duplicate
// class symbols and the fail-loud duplicate gate refused the whole file
// (live repro: TradingApp App.css, .btn and .btn:disabled at lines 146/159).
// CSS keeps the first occurrence per (kind, name) — the definition site.
func TestTreeSitterCSSReselectionDeduped(t *testing.T) {
	src := `.btn {
  color: red;
}
.btn:disabled {
  opacity: 0.5;
}
.btn:hover {
  background: blue;
}
.data-table {
  width: 100%;
}
.data-table th {
  padding: 10px;
}
@media (max-width: 600px) {
  .btn {
    background: gray;
  }
}
--brand: #ff0000;
`
	syms, _, _, _, err := tsExtract("theme.css", []byte(src), "css")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkFileSymbolConflicts("theme.css", syms); err != nil {
		t.Fatalf("re-selected CSS classes must pass the duplicate gate: %v", err)
	}
	counts := map[string]int{}
	for _, s := range syms {
		counts[s.Name]++
	}
	if counts["btn"] != 1 || counts["data-table"] != 1 {
		t.Errorf("re-selected classes must appear exactly once, got %v (all: %v)", counts, syms)
	}
	if counts["--brand"] != 1 {
		t.Errorf("custom property must survive, got %v", syms)
	}
}
