package intel

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// purityCheck writes files under a temp root, builds a real index over them,
// and runs CheckPurity over the (sorted) file list. Sorting keeps the input
// order deterministic so two calls over the same content compare equal.
func purityCheck(t *testing.T, files map[string]string) []Violation {
	t.Helper()
	dir := writeTree(t, files)
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(files))
	for rel := range files {
		names = append(names, rel)
	}
	sort.Strings(names)
	return CheckPurity(ix, names)
}

// TestCheckPurityPassesPureFunction: an @pure function that only does local
// computation (:=, arithmetic) mutates nothing outside its frame.
func TestCheckPurityPassesPureFunction(t *testing.T) {
	files := map[string]string{
		"calc.go": `package calc

// Add sums two numbers. @pure
func Add(a, b int) int {
	x := a + b
	y := x * 2
	return y
}
`,
	}
	got := purityCheck(t, files)
	if len(got) != 0 {
		t.Fatalf("expected no violations for pure local computation, got %+v", got)
	}
}

// TestCheckPurityFlagsPackageVarWrite: an @pure function that writes a
// package-level var (incdec and assignment, declared in the same file) must
// produce one violation per offending statement.
func TestCheckPurityFlagsPackageVarWrite(t *testing.T) {
	files := map[string]string{
		"main.go": `package main

var counter int

// Inc bumps the counter. @pure
func Inc() {
	counter++
}

// Set overwrites the counter. @pure
func Set(n int) {
	counter = n
}
`,
	}
	got := purityCheck(t, files)
	if len(got) != 2 {
		t.Fatalf("expected 2 violations, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].RuleTo, "counter") || got[0].Symbol != "Inc" {
		t.Errorf("violation[0] = %+v; want var counter write in Inc", got[0])
	}
	if !strings.Contains(got[1].RuleTo, "counter") || got[1].Symbol != "Set" {
		t.Errorf("violation[1] = %+v; want var counter write in Set", got[1])
	}
	for _, v := range got {
		if v.Line == 0 {
			t.Errorf("expected a source line on %+v", v)
		}
		if v.RuleFrom != "@pure" {
			t.Errorf("RuleFrom = %q, want @pure", v.RuleFrom)
		}
	}
}

// TestCheckPurityFlagsSiblingFileVarWrite: the package-var set must merge
// cross-file declarations from the index — `var counter int` declared in a
// sibling file of the same directory still counts.
func TestCheckPurityFlagsSiblingFileVarWrite(t *testing.T) {
	files := map[string]string{
		"state.go": `package main

var counter int
`,
		"ops.go": `package main

// Inc bumps the shared counter. @pure
func Inc() {
	counter++
}
`,
	}
	got := purityCheck(t, files)
	if len(got) != 1 {
		t.Fatalf("expected 1 violation for cross-file package var write, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].RuleTo, "counter") {
		t.Errorf("RuleTo = %q, want var counter", got[0].RuleTo)
	}
}

// TestCheckPurityFlagsReceiverFieldWrite: an @pure method that assigns to a
// receiver field mutates outside its stack frame.
func TestCheckPurityFlagsReceiverFieldWrite(t *testing.T) {
	files := map[string]string{
		"thing.go": `package thing

type Thing struct {
	Count int
}

// Mutate sets the count. @pure
func (t *Thing) Mutate() {
	t.Count = 1
}
`,
	}
	got := purityCheck(t, files)
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(got), got)
	}
	if got[0].RuleTo != "receiver:Count" {
		t.Errorf("RuleTo = %q, want receiver:Count", got[0].RuleTo)
	}
	if got[0].Symbol != "Thing.Mutate" {
		t.Errorf("Symbol = %q, want Thing.Mutate", got[0].Symbol)
	}
}

// TestCheckPurityFlagsPointerParamWrite: an @pure function that writes through
// a pointer parameter mutates caller state.
func TestCheckPurityFlagsPointerParamWrite(t *testing.T) {
	files := map[string]string{
		"ptr.go": `package ptr

// Set writes through a pointer. @pure
func P(p *int) {
	*p = 5
}
`,
	}
	got := purityCheck(t, files)
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(got), got)
	}
	if got[0].RuleTo != "param:p" {
		t.Errorf("RuleTo = %q, want param:p", got[0].RuleTo)
	}
	if got[0].Symbol != "P" {
		t.Errorf("Symbol = %q, want P", got[0].Symbol)
	}
}

// TestCheckPurityFlagsChannelSend: sending on a channel is an observable
// mutation even though it is not an assignment.
func TestCheckPurityFlagsChannelSend(t *testing.T) {
	files := map[string]string{
		"send.go": `package send

// Push emits a value. @pure
func S(ch chan int) {
	ch <- 1
}
`,
	}
	got := purityCheck(t, files)
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(got), got)
	}
	if got[0].RuleTo != "chan:ch" {
		t.Errorf("RuleTo = %q, want chan:ch", got[0].RuleTo)
	}
}

// TestCheckPurityIgnoresUnannotated: the identical mutations without an @pure
// doc comment are not the validator's concern.
func TestCheckPurityIgnoresUnannotated(t *testing.T) {
	files := map[string]string{
		"main.go": `package main

var counter int

type Thing struct {
	Count int
}

func Inc() {
	counter++
}

func (t *Thing) Mutate() {
	t.Count = 1
}

func P(p *int) {
	*p = 5
}

func S(ch chan int) {
	ch <- 1
}
`,
	}
	got := purityCheck(t, files)
	if len(got) != 0 {
		t.Fatalf("expected no violations without @pure, got %+v", got)
	}
}

// TestCheckPurityLocalMutationsAllowed: writes to locals — := variables, var
// declarations, and local slice/array element writes — stay on the stack
// frame and are allowed inside @pure functions.
func TestCheckPurityLocalMutationsAllowed(t *testing.T) {
	files := map[string]string{
		"local.go": `package local

// Local touches only its own frame. @pure
func Local() {
	s := make([]int, 3)
	s[0] = 1
	x := 1
	x++
	var y int
	y = 2
	_ = y
}
`,
	}
	got := purityCheck(t, files)
	if len(got) != 0 {
		t.Fatalf("expected no violations for local mutations, got %+v", got)
	}
}

// TestCheckPuritySkipsNonGoAndUnparseable: non-Go files and files that fail to
// parse are skipped without panic; valid .go files in the list still checked.
func TestCheckPuritySkipsNonGoAndUnparseable(t *testing.T) {
	files := map[string]string{
		"x.py":      "def f():\n    pass\n",
		"broken.go": "package broken\n\nfunc (\n",
		"good.go": `package good

var v int

// Set mutates the package var. @pure
func Set() {
	v = 1
}
`,
	}
	got := purityCheck(t, files)
	if len(got) != 1 {
		t.Fatalf("expected exactly the good.go violation, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].RuleTo, "v") || got[0].Symbol != "Set" {
		t.Errorf("unexpected violation: %+v", got[0])
	}
}

// TestCheckPurityDeterministic: the same inputs produce byte-identical
// violation lists across runs (files in input order, functions in source
// order, violations in AST order).
func TestCheckPurityDeterministic(t *testing.T) {
	files := map[string]string{
		"main.go": `package main

var counter int

type Thing struct {
	Count int
}

// Inc bumps the counter. @pure
func Inc() {
	counter++
}

// Mutate sets the count. @pure
func (t *Thing) Mutate() {
	t.Count = 1
}

// Push emits a value. @pure
func S(ch chan int) {
	ch <- 1
}
`,
	}
	first := purityCheck(t, files)
	second := purityCheck(t, files)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("CheckPurity is not deterministic:\nfirst  %+v\nsecond %+v", first, second)
	}
	if len(first) != 3 {
		t.Fatalf("expected 3 violations, got %d: %+v", len(first), first)
	}
}

// TestBoundariesParsesPureFlag: "pure": true in .kern/boundaries.json opts
// into @pure assertions; absent, the flag defaults to false and nothing
// changes.
func TestBoundariesParsesPureFlag(t *testing.T) {
	dir := writeTree(t, map[string]string{
		".kern/boundaries.json": `{"pure": true}`,
	})
	b, err := LoadBoundaries(dir)
	if err != nil {
		t.Fatalf("boundaries file with pure flag must load: %v", err)
	}
	if b == nil || !b.Pure {
		t.Fatalf("expected Pure=true, got %+v", b)
	}
	// Default: no "pure" field -> false.
	dir2 := writeTree(t, map[string]string{
		".kern/boundaries.json": `{"rules": []}`,
	})
	b2, err := LoadBoundaries(dir2)
	if err != nil {
		t.Fatalf("default boundaries file must load: %v", err)
	}
	if b2 == nil || b2.Pure {
		t.Fatalf("expected Pure=false by default, got %+v", b2)
	}
}

// TestCheckPurityNilIndex: CheckPurity must tolerate a nil index (files
// resolved relative to "."); the same-file package vars still work.
func TestCheckPurityNilIndex(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": `package main

var counter int

// Inc bumps the counter. @pure
func Inc() {
	counter++
}
`,
	})
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cerr := os.Chdir(wd); cerr != nil {
			t.Errorf("restore cwd: %v", cerr)
		}
	}()
	got := CheckPurity(nil, []string{"main.go"})
	if len(got) != 1 || !strings.Contains(got[0].RuleTo, "counter") {
		t.Fatalf("expected 1 violation via nil index, got %+v", got)
	}
}
