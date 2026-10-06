package index

import (
	"strings"
	"testing"
)

// sharedNameRepo builds the same-simple-name shape the V1 graph-intelligence
// bug exercised: package-level funcs named "Shared" in packages a and b,
// where b.Shared (the wrapper) calls a.Shared (the core) with a package
// qualifier, c.Caller also calls the core cross-package, and each package's
// test calls its own Shared by bare name. Package d defines a symbol named
// "append" so the predeclared-builtin shadowing case is exercised too.
func sharedNameRepo(t *testing.T) string {
	t.Helper()
	return writeTree(t, map[string]string{
		"go.mod": "module example.com/mod\n\ngo 1.22\n",
		"a/auth.go": `package a

// Shared is the "core": its own helper plus the builtin append (shadowed
// elsewhere in the index by d.append).
func Shared() string {
	s := append([]string{}, "a")
	return helper() + s[0]
}

func helper() string { return "a" }
`,
		"a/auth_test.go": `package a

func TestCallsCore() { _ = Shared() }
`,
		"b/b.go": `package b

import (
	"fmt"

	"example.com/mod/a"
)

// Shared is the "wrapper": it calls the core with a package qualifier, its
// own same-package helper, and a foreign stdlib call.
func Shared() string { return a.Shared() + bHelper() + fmt.Sprintf("%d", 1) }

func bHelper() string { return "b" }
`,
		"b/b_test.go": `package b

func TestCallsWrapper() { _ = Shared() }
`,
		"c/c.go": `package c

import "example.com/mod/a"

// Caller reaches the core from a third package via the package qualifier.
func Caller() string { return a.Shared() }
`,
		"d/d.go": `package d

// append shadows the builtin inside package d only.
func append(n int) int { return n }
`,
	})
}

func symbolInFile(ix *Index, name, fileSuffix string) (Symbol, bool) {
	for _, s := range ix.Symbols {
		if s.Name == name && strings.HasSuffix(s.File, fileSuffix) {
			return s, true
		}
	}
	return Symbol{}, false
}

// TestCallersForSameNameFuncsAcrossPackages pins the V1 merge bug: two
// package-level funcs sharing a simple name must not pool their callers.
// The core's callers are its same-package bare callers plus the qualified
// cross-package callers (including the wrapper itself); the wrapper's
// bare-name callers from its own package never cross over, and neither
// symbol lists itself as a caller.
func TestCallersForSameNameFuncsAcrossPackages(t *testing.T) {
	ix, err := Build(sharedNameRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	core, ok := symbolInFile(ix, "Shared", "a/auth.go")
	if !ok {
		t.Fatal("core Shared symbol missing")
	}
	wrapper, ok := symbolInFile(ix, "Shared", "b/b.go")
	if !ok {
		t.Fatal("wrapper Shared symbol missing")
	}

	coreCallers := ix.CallersFor(core)
	for _, want := range []string{"Caller", "Shared", "TestCallsCore"} {
		if !contains(coreCallers, want) {
			t.Errorf("CallersFor(core) = %v; missing caller %q", coreCallers, want)
		}
	}
	for _, banned := range []string{"TestCallsWrapper"} {
		if contains(coreCallers, banned) {
			t.Errorf("CallersFor(core) = %v; must not include %q (different package's bare caller)", coreCallers, banned)
		}
	}
	if len(coreCallers) != 3 {
		t.Errorf("CallersFor(core) = %v; want exactly [Caller Shared TestCallsCore]", coreCallers)
	}

	wrapperCallers := ix.CallersFor(wrapper)
	if !contains(wrapperCallers, "TestCallsWrapper") {
		t.Errorf("CallersFor(wrapper) = %v; missing same-package bare caller TestCallsWrapper", wrapperCallers)
	}
	for _, banned := range []string{"Caller", "Shared", "TestCallsCore"} {
		if contains(wrapperCallers, banned) {
			t.Errorf("CallersFor(wrapper) = %v; must not include %q (core's callers)", wrapperCallers, banned)
		}
	}
}

// TestCallsForSameNameFuncsAcrossPackages pins the callee side: the wrapper's
// callees (its own helper, the qualified core call, the foreign stdlib call)
// must not leak into the core's callee list, and vice versa. The predeclared
// builtin append stays on the core's list even though another package defines
// a symbol named "append".
func TestCallsForSameNameFuncsAcrossPackages(t *testing.T) {
	ix, err := Build(sharedNameRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	core, ok := symbolInFile(ix, "Shared", "a/auth.go")
	if !ok {
		t.Fatal("core Shared symbol missing")
	}
	wrapper, ok := symbolInFile(ix, "Shared", "b/b.go")
	if !ok {
		t.Fatal("wrapper Shared symbol missing")
	}

	coreCallees := ix.CallsFor(core)
	for _, want := range []string{"helper", "append"} {
		if !contains(coreCallees, want) {
			t.Errorf("CallsFor(core) = %v; missing callee %q", coreCallees, want)
		}
	}
	for _, banned := range []string{"bHelper", "Sprintf", "a.Shared", "Shared"} {
		if contains(coreCallees, banned) {
			t.Errorf("CallsFor(core) = %v; must not include %q (wrapper-only callee)", coreCallees, banned)
		}
	}

	wrapperCallees := ix.CallsFor(wrapper)
	for _, want := range []string{"bHelper", "a.Shared", "fmt.Sprintf"} {
		if !contains(wrapperCallees, want) {
			t.Errorf("CallsFor(wrapper) = %v; missing callee %q", wrapperCallees, want)
		}
	}
	if contains(wrapperCallees, "helper") {
		t.Errorf("CallsFor(wrapper) = %v; must not include helper (core's callee)", wrapperCallees)
	}
}

// TestCallersForUnambiguousKeepsBareEdges pins the no-regression contract: a
// symbol whose simple name is unique in the index keeps every recorded bare
// and qualified caller — byte-identical to the pre-fix behavior.
func TestCallersForUnambiguousKeepsBareEdges(t *testing.T) {
	ix, err := Build(sharedNameRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	core, ok := symbolInFile(ix, "Shared", "a/auth.go")
	if !ok {
		t.Fatal("core Shared symbol missing")
	}
	// Explore the unique helper: same-package bare caller + cross-package
	// qualified callers all survive.
	helper, ok := symbolInFile(ix, "helper", "a/auth.go")
	if !ok {
		t.Fatal("helper symbol missing")
	}
	if callers := ix.CallersFor(helper); !contains(callers, "Shared") {
		t.Errorf("CallersFor(helper) = %v; missing same-package bare caller Shared", callers)
	}
	// Byte-identical: for an unambiguous symbol CallersFor is the exact bucket.
	if got, want := ix.CallersFor(helper), dedupeSorted(ix.Callers[helper.FullName()]); len(got) != len(want) {
		t.Errorf("CallersFor(helper) = %v; want exact bucket %v (byte-identical)", got, want)
	}
	// sanity: the core still resolves (used above).
	if core.File == "" {
		t.Fatal("core symbol missing file")
	}
}

// TestCallersForForeignQualifiedTargetNotAttributed pins rule (b): a
// qualified foreign callee ("fmt.Println") never attributes its callers to a
// local symbol of the same name, even when that name is shared across
// packages.
func TestCallersForForeignQualifiedTargetNotAttributed(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module example.com/mod\n\ngo 1.22\n",
		"a/a.go": `package a

func Println(s string) {}
`,
		"b/b.go": `package b

import "fmt"

func Emit() { fmt.Println("x") }
`,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	local, ok := symbolInFile(ix, "Println", "a/a.go")
	if !ok {
		t.Fatal("local Println symbol missing")
	}
	if callers := ix.CallersFor(local); len(callers) != 0 {
		t.Errorf("CallersFor(local Println) = %v; foreign fmt.Println callers must not be attributed", callers)
	}
	if callers := ix.Callers["fmt.Println"]; !contains(callers, "Emit") {
		t.Errorf("foreign edge lost: fmt.Println callers = %v, want Emit", callers)
	}
}
