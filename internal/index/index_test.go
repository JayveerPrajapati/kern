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
	if maxMtime, count := indexableMaxMtime(dir); count != len(ix.FileHashes) || maxMtime != ix.MaxMtime {
		t.Fatalf("gate mismatch: count %d vs %d, mtime %d vs %d", count, len(ix.FileHashes), maxMtime, ix.MaxMtime)
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
