package index

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const srcMain = `package main

import "strings"

func main() {
	greet("world")
}

func greet(name string) string {
	return "hi " + strings.ToUpper(name)
}
`

const srcOther = `package main

type User struct {
	Name string
}

func (u User) Login() bool {
	return greet(u.Name) != ""
}

func helper() {
	_ = User{Name: "x"}
}
`

func TestBuildAndSearch(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": srcMain,
		"user.go": srcOther,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(ix.Symbols) < 5 {
		t.Fatalf("expected several symbols, got %d", len(ix.Symbols))
	}
	if len(ix.Calls) == 0 {
		t.Fatal("expected call edges")
	}

	greet := ix.symbolsFor("greet")
	if len(greet) == 0 {
		t.Fatal("expected symbol greet")
	}

	callers := ix.CallersOf("greet")
	if !contains(callers, "main") {
		t.Fatalf("expected main to call greet, got %v", callers)
	}

	method := ix.symbolsFor("User.Login")
	if len(method) != 1 || method[0].Kind != "method" {
		t.Fatalf("expected method User.Login, got %+v", method)
	}
}

func TestCallersSameNameFuncAndMethod(t *testing.T) {
	src := `package main

type Server struct{}

func (s Server) Start() {}

func Start() {
	s := Server{}
	s.Start()
}
`
	dir := writeTree(t, map[string]string{"main.go": src})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A bare function Start calling method Server.Start must keep the edge
	// under the method's full name even though simple == caller.
	if callers := ix.CallersOf("Server.Start"); !contains(callers, "Start") {
		t.Errorf("expected Start to call Server.Start, got %v", callers)
	}
	// And it must never forge a self-caller edge on the bare name.
	if callers := ix.CallersOf("Start"); contains(callers, "Start") {
		t.Errorf("Start must not be its own caller, got %v", callers)
	}
}

func TestGoInheritanceEdges(t *testing.T) {
	src := `package main

type Reader interface {
	Read() int
}

type Logger interface {
	Reader
	Log(msg string)
}

type Base struct{ ID int }

type Item struct {
	Base
	Logger
}
`
	dir := writeTree(t, map[string]string{"types.go": src})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// SupertypesOf: Logger embeds Reader; Item embeds Base + Logger.
	if sup := ix.SupertypesOf(Symbol{Name: "Logger", Kind: "interface"}); !contains(sup, "embeds:Reader") {
		t.Errorf("expected Logger to embed Reader, got %v", sup)
	}
	sup := ix.SupertypesOf(Symbol{Name: "Item", Kind: "struct"})
	if !contains(sup, "embeds:Base") || !contains(sup, "embeds:Logger") {
		t.Errorf("expected Item to embed Base and Logger, got %v", sup)
	}
	// SubtypesOf: Reader has Logger (and transitively Item via Logger embedding).
	if subs := ix.SubtypesOf(Symbol{Name: "Reader", Kind: "interface"}); !contains(subs, "Logger") {
		t.Errorf("expected Reader subtypes to include Logger, got %v", subs)
	}
	if subs := ix.SubtypesOf(Symbol{Name: "Base", Kind: "struct"}); !contains(subs, "Item") {
		t.Errorf("expected Base subtypes to include Item, got %v", subs)
	}
}

func TestSearchPatterns(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Search("func greet", 10)) != 1 {
		t.Fatal("expected func greet")
	}
	if len(ix.Search("greet", 10)) == 0 {
		t.Fatal("expected greet match")
	}
}

func TestSearchWildcard(t *testing.T) {
	dir := writeTree(t, map[string]string{"user.go": srcOther})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Search("User", 10)) == 0 {
		t.Fatal("expected User match")
	}
	if len(ix.Search("*ser*", 10)) == 0 {
		t.Fatal("expected wildcard match")
	}
}

func TestContextSlices(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := ix.Context("greet", 4)
	if !strings.Contains(ctx, "func greet") {
		t.Fatalf("expected definition in context, got:\n%s", ctx)
	}
	if !strings.Contains(ctx, "callers:") {
		t.Fatalf("expected callers in context, got:\n%s", ctx)
	}
}

func TestWholeGraphCapsAndEdges(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": srcMain,
		"user.go": srcOther,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	g := ix.WholeGraph(2)
	if len(g.Nodes) != 2 {
		t.Fatalf("expected 2 nodes with limit=2, got %d", len(g.Nodes))
	}
	if g.Root != "" {
		t.Errorf("whole graph must have empty root, got %q", g.Root)
	}
	for _, n := range g.Nodes {
		if n.Pkg == "" {
			t.Errorf("node %s missing pkg", n.ID)
		}
		if n.Role != "def" {
			t.Errorf("node %s role=%q, want def", n.ID, n.Role)
		}
	}
	if len(g.Edges) == 0 {
		t.Fatal("expected call edges between kept symbols")
	}
	for _, e := range g.Edges {
		if e.ConfidenceLabel != "EXTRACTED" {
			t.Errorf("same-pkg edge %s->%s label=%q, want EXTRACTED", e.From, e.To, e.ConfidenceLabel)
		}
	}
}

func TestWholeGraphHTMLWholeBranch(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": srcMain,
		"user.go": srcOther,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	g := ix.WholeGraph(0)
	html := g.GraphHTML()
	for _, want := range []string{"const whole = (g.root === '')", `filter symbols`, "whole repo (", "band-label", "wholeDraw"} {
		if !strings.Contains(html, want) {
			t.Errorf("whole-repo HTML missing %q", want)
		}
	}
	// Neighborhood HTML must still be the three-column mode.
	ng, ok := ix.Neighborhood("greet")
	if !ok {
		t.Fatal("no neighborhood for greet")
	}
	nh := ng.GraphHTML()
	if !strings.Contains(nh, "const whole = (g.root === '')") {
		t.Error("neighborhood HTML must embed the whole-repo flag")
	}
	if !strings.Contains(nh, "kern graph: greet") {
		t.Errorf("neighborhood title missing root: %q", nh[:200])
	}
}

func TestNeighborhoodConfidenceLabels(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": srcMain,
		"user.go": srcOther,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := ix.Neighborhood("greet")
	if !ok || len(g.Edges) == 0 {
		t.Fatalf("expected edges for greet, got ok=%v edges=%d", ok, len(g.Edges))
	}
	for _, e := range g.Edges {
		if e.Confidence != "" && e.ConfidenceLabel == "" {
			t.Errorf("edge %s->%s has Confidence=%q but empty ConfidenceLabel", e.From, e.To, e.Confidence)
		}
		// Every edge should carry the standard label.
		switch e.ConfidenceLabel {
		case "EXTRACTED", "INFERRED", "AMBIGUOUS":
		default:
			t.Errorf("edge %s->%s has unexpected ConfidenceLabel=%q", e.From, e.To, e.ConfidenceLabel)
		}
	}
}

func TestPersistRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Symbols) != len(ix.Symbols) {
		t.Fatalf("symbol count mismatch: %d vs %d", len(loaded.Symbols), len(ix.Symbols))
	}
}

func TestWatchDetectsChanges(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	content, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	prev := map[string]string{"main.go": cacheHash(content)}

	// Deletion detected.
	changes := diff(prev, map[string]string{})
	if len(changes) != 1 || changes[0].Kind != ChangeRemoved {
		t.Fatalf("expected 1 removal, got %+v", changes)
	}

	// Modification detected.
	newContent := []byte(strings.ReplaceAll(string(content), "greet(\"world\")", "greet(\"again\")"))
	cur := map[string]string{"main.go": cacheHash(newContent)}
	changes = diff(prev, cur)
	if len(changes) != 1 || changes[0].Kind != ChangeModified {
		t.Fatalf("expected 1 modification, got %+v", changes)
	}

	// No change detected.
	changes = diff(prev, map[string]string{"main.go": cacheHash(content)})
	if len(changes) != 0 {
		t.Fatalf("expected no changes, got %+v", changes)
	}
}

func TestStaleGate(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ix.Stale() {
		t.Fatal("fresh index reported stale")
	}
	if ix.MaxMtime == 0 {
		t.Fatal("expected MaxMtime captured at build")
	}

	// The stat gate must match the content manifest count exactly.
	if maxMtime, count, err := indexableMaxMtime(dir); err != nil || count != len(ix.FileHashes) || maxMtime != ix.MaxMtime {
		t.Fatalf("gate mismatch: count %d vs %d, mtime %d vs %d (err %v)", count, len(ix.FileHashes), maxMtime, ix.MaxMtime, err)
	}

	// Corrupt the manifest hash: the gate must short-circuit before the hash
	// comparison and still report fresh, proving it is what decides.
	ix.FileHashes["main.go"] = "bogus"
	if ix.Stale() {
		t.Fatal("gate should have short-circuited before hash check")
	}
}

func TestStaleEditDetected(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Real-world edits advance mtime; Chtimes simulates that deterministically
	// even on coarse-granularity filesystems where two back-to-back writes
	// share a clock tick.
	p := filepath.Join(dir, "main.go")
	future := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(srcMain, "hi ", "hey ")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	if !ix.Stale() {
		t.Fatal("edited file not reported stale")
	}
}

func TestStaleAddDetected(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "user.go")
	if err := os.WriteFile(p, []byte(srcOther), 0o644); err != nil {
		t.Fatal(err)
	}
	if !ix.Stale() {
		t.Fatal("added file not reported stale")
	}
}

func TestStaleDeleteDetected(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "main.go")); err != nil {
		t.Fatal(err)
	}
	if !ix.Stale() {
		t.Fatal("removed file not reported stale")
	}
}

func TestStaleTouchIsFresh(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Touching a file updates mtime but not content: the gate trips and the
	// hash fallback must confirm the index is still fresh.
	p := filepath.Join(dir, "main.go")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	if ix.Stale() {
		t.Fatal("touch with unchanged content reported stale")
	}
}

func TestStaleLegacyIndex(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// An index without a MaxMtime gate (MaxMtime == 0) must fall back to the
	// exact hash comparison: fresh stays fresh, edits are still detected.
	ix.MaxMtime = 0
	if ix.Stale() {
		t.Fatal("legacy index (MaxMtime 0) with unchanged tree reported stale")
	}
	p := filepath.Join(dir, "main.go")
	future := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(srcMain, "hi ", "hey ")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	if !ix.Stale() {
		t.Fatal("legacy index (MaxMtime 0) did not detect edit")
	}
}

func cacheHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// W2-15: an external call to fmt.Println must never register a caller under
// an unrelated local symbol named Println.
func TestForeignCalleeNeverAliasesLocalSymbol(t *testing.T) {
	src := `package main

import "fmt"

func Println(s string) {}

func main() {
	fmt.Println("x")
}
`
	dir := writeTree(t, map[string]string{"main.go": src})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The foreign edge is recorded under its own key.
	if callers := ix.Callers["fmt.Println"]; !contains(callers, "main") {
		t.Errorf("expected main to call fmt.Println, got %v", callers)
	}
	// The local Println must have no callers.
	sym, ok := ix.FindSymbol("Println")
	if !ok {
		t.Fatal("expected local Println symbol")
	}
	if callers := ix.CallersFor(sym); len(callers) != 0 {
		t.Errorf("local Println must have no callers, got %v", callers)
	}
	if callers := ix.CallersOfName("Println"); len(callers) != 0 {
		t.Errorf("CallersOfName(Println) must be empty for a local symbol, got %v", callers)
	}
}

// W2-16: a call to Alpha.Save must never show up as a caller of Beta.Save.
func TestSameNameMethodsDoNotMergeCallers(t *testing.T) {
	src := `package main

type Alpha struct{}

func (a Alpha) Save() {}

type Beta struct{}

func (b Beta) Save() {}

func main() {
	a := Alpha{}
	a.Save()
}
`
	dir := writeTree(t, map[string]string{"main.go": src})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	alpha, ok := ix.FindSymbol("Alpha.Save")
	if !ok {
		t.Fatal("expected Alpha.Save")
	}
	if callers := ix.CallersFor(alpha); !contains(callers, "main") {
		t.Errorf("expected main to call Alpha.Save, got %v", callers)
	}
	beta, ok := ix.FindSymbol("Beta.Save")
	if !ok {
		t.Fatal("expected Beta.Save")
	}
	if callers := ix.CallersFor(beta); len(callers) != 0 {
		t.Errorf("Beta.Save must have no callers, got %v", callers)
	}
}

// W2-18: hub-style exact lookup and why-style lookup must agree.
func TestCallerLookupsAgree(t *testing.T) {
	src := `package main

type Server struct{}

func (s Server) Run() {}

func Start() {
	s := Server{}
	s.Run()
}
`
	dir := writeTree(t, map[string]string{"main.go": src})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	run, ok := ix.FindSymbol("Server.Run")
	if !ok {
		t.Fatal("expected Server.Run")
	}
	exact := ix.Callers["Server.Run"]
	attributed := ix.CallersFor(run)
	if len(exact) != len(attributed) {
		t.Fatalf("exact lookup %v must equal attributed lookup %v", exact, attributed)
	}
}

func TestExtensionlessShebangScript(t *testing.T) {
	// ix-13: extensionless scripts with shebangs must be indexed.
	dir := writeTree(t, map[string]string{
		"scripts/run": `#!/usr/bin/env python3
def main():
    print("hello")
main()
`,
		"scripts/deploy": `#!/bin/bash
deploy() {
  echo deploy
}
deploy
`,
		"Makefile": "build:\n\tgo build\n",
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(ix.Languages(), "python") {
		t.Fatalf("expected python in languages, got %v", ix.Languages())
	}
	if !contains(ix.Languages(), "shell") {
		t.Fatalf("expected shell in languages, got %v", ix.Languages())
	}
	if len(ix.symbolsFor("build")) != 0 {
		t.Fatal("Makefile without shebang must not be indexed as a source file")
	}
	if s := ix.symbolsFor("main"); len(s) == 0 {
		t.Fatal("expected func main from extensionless python script")
	}
}
