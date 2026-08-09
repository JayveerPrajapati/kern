package intel

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestDeleteCheckUnusedPrivateSafe(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go": `package lib

func Public() string {
	return inner()
}

func inner() string {
	return "x"
}
`,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// inner is called from production (Public) -> unsafe.
	r2 := DeleteCheck(ix, "inner")
	if r2.Safe {
		t.Fatalf("inner is called by Public (production), must be unsafe, got %+v", r2)
	}
	if len(r2.Callers) != 1 || r2.Callers[0] != "Public" {
		t.Fatalf("expected Public as caller, got %+v", r2.Callers)
	}
}

func TestDeleteCheckTrulyUnusedSafe(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": `package main

func main() {
	used()
}

func used() {}

func orphan() {}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := DeleteCheck(ix, "orphan")
	if !r.Safe {
		t.Fatalf("orphan has no callers and is private, must be safe, got %+v", r)
	}
	if r.Exported {
		t.Fatal("orphan must not be flagged exported")
	}
}

func TestDeleteCheckProductionCallerUnsafe(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := DeleteCheck(ix, "Public")
	if r.Safe {
		t.Fatal("exported Public called by client.Caller must be unsafe")
	}
	if len(r.Callers) == 0 {
		t.Fatalf("expected production callers for Public, got %+v", r)
	}
	if !r.Exported {
		t.Fatal("Public should be flagged exported")
	}
}

func TestDeleteCheckTestOnlyCallerSafe(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go": `package lib

func testedOnly() {}
`,
		"lib/lib_test.go": `package lib

import "testing"

func TestTestedOnly(t *testing.T) {
	testedOnly()
}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := DeleteCheck(ix, "testedOnly")
	if !r.Safe {
		t.Fatalf("test-only caller should be safe, got %+v", r)
	}
	if len(r.TestCallers) != 1 || r.TestCallers[0] != "TestTestedOnly" {
		t.Fatalf("expected test caller split out, got %+v", r.TestCallers)
	}
	if len(r.Callers) != 0 {
		t.Fatalf("expected no production callers, got %+v", r.Callers)
	}
}

func TestDeleteCheckEntryPointUnsafe(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": `package main

func main() {
	start()
}

func start() {}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := DeleteCheck(ix, "main")
	if r.Safe {
		t.Fatal("main is an entry point, must be unsafe")
	}
	if !r.EntryPoint {
		t.Fatalf("main should be flagged entry point, got %+v", r)
	}
}

func TestDeleteCheckNotFound(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.go": "package main\n\nfunc main() {}\n"})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := DeleteCheck(ix, "Nope")
	if r.Defined {
		t.Fatal("Nope must not be defined")
	}
	if r.Safe {
		t.Fatal("undefined symbol must not be reported safe")
	}
}
