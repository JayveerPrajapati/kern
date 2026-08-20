package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestDeleteCheckValueReferenceUnsafe: a function referenced as a value
// (`f := Foo`) has no call edge, so without the non-call scan it would be
// reported safe to delete. It must be unsafe.
func TestDeleteCheckValueReferenceUnsafe(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": `package main

func Foo() {}

func bar() {
	f := Foo
	f()
}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := DeleteCheck(ix, "Foo")
	if r.Safe {
		t.Fatalf("Foo is referenced as a function value, must be unsafe, got %+v", r)
	}
	if len(r.NonCallRefs) == 0 {
		t.Fatalf("expected NonCallRefs to include the referencing file, got %+v", r)
	}
	if !strings.Contains(r.Reason, "outside a call") {
		t.Fatalf("reason should explain the non-call reference, got %q", r.Reason)
	}
}

// TestDeleteCheckMethodValueUnsafe: a method reached as a method value
// (`h := recv.M`) is not a call edge and must not be safe to delete.
func TestDeleteCheckMethodValueUnsafe(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"p/p.go": `package p

type T struct{}

func (t *T) Method() {}

func Use(t *T) func() {
	return t.Method
}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := DeleteCheck(ix, "T.Method")
	if r.Safe {
		t.Fatalf("method value reference must be unsafe, got %+v", r)
	}
	if len(r.NonCallRefs) == 0 {
		t.Fatalf("expected NonCallRefs for method value, got %+v", r)
	}
}

// TestDeleteCheckTrulyUnusedStillSafe guards against the non-call scan falsely
// treating a symbol's own definition as a reference.
func TestDeleteCheckTrulyUnusedStillSafe(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": `package main

func orphan() {}

func main() {
	used()
}

func used() {}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := DeleteCheck(ix, "orphan")
	if !r.Safe {
		t.Fatalf("orphan is unreferenced and private, must be safe, got %+v", r)
	}
	if len(r.NonCallRefs) != 0 {
		t.Fatalf("orphan must not have non-call references, got %+v", r.NonCallRefs)
	}
}

// TestDeadCodeSkipsValueReferenced verifies DeadCode does not list a function
// that is referenced as a value even though it has no callers.
func TestDeadCodeSkipsValueReferenced(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": `package main

func Foo() {}

func bar() {
	f := Foo
	_ = f
}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	dead := DeadCode(ix)
	for _, d := range dead {
		if d.Name == "Foo" {
			t.Fatalf("Foo is referenced as a value and must not be dead, got %+v", dead)
		}
	}
}

// TestDeadCodeReportsTrulyUnused confirms DeadCode still reports a private
// function with no callers and no references.
func TestDeadCodeReportsTrulyUnused(t *testing.T) {
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
	found := false
	for _, d := range DeadCode(ix) {
		if d.Name == "orphan" {
			found = true
		}
	}
	if !found {
		t.Fatal("orphan has no callers and no references and must be reported dead")
	}
}