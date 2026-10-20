package index

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/calleefilter"
)

// TestIsNoiseCallee pins the shared record-time noise rule (internal/calleefilter):
// Go builtins and bare type conversions are dropped, two-segment qualified
// forms are preserved (cross-package edges depend on them), chains beyond
// two segments are dropped only when they are builder chains (the receiver
// is a call result), and receiver-field chains survive for the merge-time
// rewrite.
func TestIsNoiseCallee(t *testing.T) {
	for _, name := range []string{
		"len", "cap", "make", "new", "append", "copy", "delete",
		"panic", "print", "println", "recover",
		"string", "int", "int8", "int64", "uint", "uint8", "uintptr",
		"float32", "float64", "complex64", "complex128",
		"bool", "byte", "rune", "error",
		"min", "max", "clear", // Go 1.21 builtins (L5-5)
	} {
		if !calleefilter.IsNoiseCallee(name, false) {
			t.Errorf("calleefilter.IsNoiseCallee(%q) = false, want true (builtin/conversion)", name)
		}
	}
	for _, name := range []string{
		"helper", "Do", "Save", "run", "Parse", "Build",
	} {
		if calleefilter.IsNoiseCallee(name, false) {
			t.Errorf("calleefilter.IsNoiseCallee(%q) = true, want false (ordinary name)", name)
		}
	}
	// Two-segment qualified forms always survive.
	for _, name := range []string{"db.Do", "fmt.Sprintf", "http.HandleFunc", "a.Shared"} {
		if calleefilter.IsNoiseCallee(name, true) {
			t.Errorf("calleefilter.IsNoiseCallee(%q, true) = true, want false (2-segment qualified form)", name)
		}
	}
	// Builder chains (receiver is a call result) beyond two segments drop.
	for _, name := range []string{"logger.Info.Int.Dur.Msg", "foo.Bar.Baz", "a.b.c.D"} {
		if !calleefilter.IsNoiseCallee(name, true) {
			t.Errorf("calleefilter.IsNoiseCallee(%q, true) = false, want true (builder chain)", name)
		}
	}
	// Receiver-field chains beyond two segments survive the extractor
	// (chainHasCall=false) so the merge-time rewrite can complete them.
	for _, name := range []string{"App.taskSvc.Deploy", "a.svc.client.Send"} {
		if calleefilter.IsNoiseCallee(name, false) {
			t.Errorf("calleefilter.IsNoiseCallee(%q, false) = true, want false (receiver-field chain)", name)
		}
	}
}

// TestGoExtractorFiltersBuiltinsAndChains builds a Go file whose functions
// call builtins, conversions, a 2-segment stdlib call, a builder chain and a
// receiver-field chain, and pins the recorded graph: noise absent, real edges
// present, field chain resolved to the field type's method.
func TestGoExtractorFiltersBuiltinsAndChains(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"app.go": `package app

import "fmt"

type TaskService struct{}

func (t *TaskService) Deploy() {}

type App struct {
	taskSvc *TaskService
}

type Logger struct{}

func (l *Logger) Info() *Logger { return l }
func (l *Logger) Int(i int) *Logger { return l }
func (l *Logger) Msg(s string) {}

func helper() {}

func Use(a *App, l *Logger, items []string) {
	_ = len(items)
	items = append(items, "x")
	_ = string(42)
	_ = uint(1)
	helper()
	fmt.Println("hi")
	l.Info().Int(1).Msg("done")
	a.taskSvc.Deploy()
}
`,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	callees := map[string]bool{}
	for _, ce := range ix.Calls["Use"] {
		callees[ce.Target] = true
	}
	for _, banned := range []string{"len", "append", "string", "uint", "logger.Info.Int.Dur.Msg", "l.Info.Int.Msg"} {
		if callees[banned] {
			t.Errorf("Use callees contain filtered noise %q: %v", banned, ix.Calls["Use"])
		}
	}
	for _, want := range []string{"helper", "fmt.Println"} {
		if !callees[want] {
			t.Errorf("Use callees missing %q: %v", want, ix.Calls["Use"])
		}
	}
	// The builder chain's segments must not leak either (only 2-segment
	// forms like "Logger.Info" would be legal; the 4-segment chain must not
	// appear under any qualified form).
	for _, ce := range ix.Calls["Use"] {
		if strings.Count(ce.Target, ".") >= 2 {
			t.Errorf("Use recorded a %d-segment chain %q: %v", strings.Count(ce.Target, ".")+1, ce.Target, ix.Calls["Use"])
		}
	}
	// The receiver-field chain survived record time and was completed by the
	// merge-time rewrite into a canonical 2-segment edge.
	if got := ix.Callers["TaskService.Deploy"]; len(got) != 1 || got[0] != "Use" {
		t.Fatalf("Callers[TaskService.Deploy] = %v, want [Use] (field chain preserved and resolved)", got)
	}
}

// TestIsTestSymbolRequiresTestFile pins L5-5: IsTestSymbol classifies by
// FILE only — a production TestConnection/BenchmarkPrice in a non-test
// file is a real project symbol (never excluded from communities or the
// test-file rule), while any symbol in a _test.go file is test-only.
func TestIsTestSymbolRequiresTestFile(t *testing.T) {
	if calleefilter.IsTestSymbol("pkg/connection.go") {
		t.Error("IsTestSymbol(production file) = true, want false (Test-prefixed prod symbols are real)")
	}
	for _, f := range []string{"pkg/app_test.go", "bench_test.go"} {
		if !calleefilter.IsTestSymbol(f) {
			t.Errorf("IsTestSymbol(%q) = false, want true (test file)", f)
		}
	}
}

// TestGoExtractorKeepsCrossPackageQualified pins the cross-package edge
// contract: 2-segment package-qualified callees ("b.B2") must survive the
// filter — crossesBoundary BFS's over raw endpoints like these.
func TestGoExtractorKeepsCrossPackageQualified(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a/a.go": `package a

import "demo/b"

func A1() { b.B2() }
`,
		"b/b.go": `package b

func B2() {}
`,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	callees := ix.Calls["A1"]
	if len(callees) != 1 || callees[0].Target != "b.B2" {
		t.Fatalf("A1 callees = %v, want [b.B2] (2-segment qualified edge preserved)", callees)
	}
	if got := ix.Callers["B2"]; len(got) != 1 || got[0] != "A1" {
		t.Fatalf("Callers[B2] = %v, want [A1]", got)
	}
}

// TestIsNoiseCalleeForTestingReceivers pins the F3-6 test-file rule: inside a
// _test.go caller, testing.T/B method calls on the conventional short
// receivers are harness plumbing and drop; production callers and non-testing
// methods on the same receiver shape are kept.
func TestIsNoiseCalleeForTestingReceivers(t *testing.T) {
	for _, name := range []string{
		"t.Run", "t.Errorf", "t.Fatalf", "t.Parallel", "t.Cleanup",
		"T.Fatalf", "T.Run", "TB.Helper", "B.Reset",
		"t.TempDir", "b.Reset", "b.ReportAllocs", "tb.Helper", "f.Fuzz", "m.Run",
	} {
		if !calleefilter.IsNoiseCalleeFor(name, false, "pkg/thing_test.go") {
			t.Errorf("IsNoiseCalleeFor(%q, false, _test.go) = false, want true (testing harness)", name)
		}
	}
	for _, name := range []string{"t.Run", "t.Fatalf", "b.Reset", "tb.Helper"} {
		if calleefilter.IsNoiseCalleeFor(name, false, "pkg/thing.go") {
			t.Errorf("IsNoiseCalleeFor(%q, false, production) = true, want false (short receiver in production)", name)
		}
	}
	for _, name := range []string{"t.CustomMethod", "t.run", "x.Run"} {
		if calleefilter.IsNoiseCalleeFor(name, false, "pkg/thing_test.go") {
			t.Errorf("IsNoiseCalleeFor(%q, false, _test.go) = true, want false (not a testing method)", name)
		}
	}
}

// TestGoExtractorFiltersTestingHarnessCallees pins the record-time half of
// F3-6: a _test.go function calling testing.T methods plus a real project call
// records only the project edge.
func TestGoExtractorFiltersTestingHarnessCallees(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"subject_test.go": `package app

func TestSubject(t *testing.T) {
	t.Parallel()
	t.Run("sub", func(t *testing.T) { t.Errorf("boom") })
	helper()
}

func helper() {}
`,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	callees := map[string]bool{}
	for _, ce := range ix.Calls["TestSubject"] {
		callees[ce.Target] = true
	}
	for _, banned := range []string{"t.Parallel", "t.Run", "t.Errorf"} {
		if callees[banned] {
			t.Errorf("TestSubject callees contain testing-harness noise %q: %v", banned, ix.Calls["TestSubject"])
		}
	}
	if !callees["helper"] {
		t.Errorf("TestSubject callees missing the real project call: %v", ix.Calls["TestSubject"])
	}
}
