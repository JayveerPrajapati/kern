package intel

import (
	"os"
	"path/filepath"
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

// TestDeleteCheckSeesConstructorInferredCallers pins the fix for the
// receiver-qualified edge blind spot: a method called through a
// constructor-inferred receiver variable ("New.M" edges) must NEVER be
// reported safe-to-delete.
func TestDeleteCheckSeesConstructorInferredCallers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "s.go"), []byte(`package s

type S struct{}

func New() *S { return &S{} }

func (s *S) M() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "use.go"), []byte(`package s

func Use() {
	x := New()
	x.M()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := DeleteCheck(ix, "S.M")
	if r.Safe {
		t.Fatalf("DeleteCheck(S.M) = safe with reason %q, want unsafe (Use calls it via constructor-inferred receiver)", r.Reason)
	}
	if len(r.Callers) != 1 || r.Callers[0] != "Use" {
		t.Fatalf("DeleteCheck callers = %v, want [Use]", r.Callers)
	}
}

// TestDeleteCheckDuplicateCallerNamePrefersProduction pins the second half of
// the false-safe bug: a caller name defined BOTH in production and in a test
// file must classify as a production caller (the test def shadowing the live
// one used to make DeleteCheck report "only referenced from tests" → safe).
func TestDeleteCheckDuplicateCallerNamePrefersProduction(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "helper.go"), []byte(`package lib

type S struct{}

func New() *S { return &S{} }

func (s *S) M() {}

func callM() {
	x := New()
	x.M()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Test-file duplicate of "callM"? No — duplicate of "worker"? worker
	// must NOT be the caller of M. The caller of M is callM (production).
	// Give callM a test-file duplicate so the old buildFileMap picked the
	// test def.
	if err := os.WriteFile(filepath.Join(dir, "lib", "callm_test.go"), []byte(`package lib

func TestCallM(t *testing.T) {}

func callM() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := DeleteCheck(ix, "S.M")
	if r.Safe {
		t.Fatalf("DeleteCheck(S.M) = safe (%q), want unsafe: callM (production) calls it via constructor-inferred receiver", r.Reason)
	}
	found := false
	for _, c := range r.Callers {
		if c == "callM" {
			found = true
		}
	}
	if !found {
		t.Fatalf("DeleteCheck callers = %v, want callM classified as a production caller", r.Callers)
	}
}
