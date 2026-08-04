package index

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		"main.go":  srcMain,
		"user.go":  srcOther,
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

func TestPersistRoundTrip(t *testing.T) {
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
