package tasklife

// Slice 5: a type's impact must aggregate its methods and constructors — the
// call graph attaches edges to those, never to the type node, so a bare type
// query must not report zero callers / a confident low-risk all-clear.

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

func TestImpactTypeAggregatesMethodsAndConstructors(t *testing.T) {
	root := t.TempDir()
	writeImpactFixture(t, root, "lib/store.go", `package lib

type Store struct{}

func (s *Store) Get() int { return 1 }

func (s *Store) Put(v int) {}

func NewStore() *Store { return &Store{} }

func Caller() int {
	s := NewStore()
	s.Put(1)
	return s.Get()
}
`)
	p := newTestPlatform(t, root)
	ts := NewTaskService(p, eventbus.New())

	_, rep, text, err := ts.Impact("lib.Store")
	if err != nil {
		t.Fatalf("Impact(lib.Store): %v", err)
	}
	if rep.TypeExpanded == "" {
		t.Fatal("type impact did not expand to methods/constructors")
	}
	if !strings.Contains(rep.TypeExpanded, "methods") || !strings.Contains(rep.TypeExpanded, "constructors") {
		t.Fatalf("type expansion = %q; want methods + constructors", rep.TypeExpanded)
	}
	if len(rep.WhoCalls) == 0 {
		t.Fatalf("type impact reports 0 callers despite callers of its methods (expansion = %q)", rep.TypeExpanded)
	}
	if !strings.Contains(text, "Type scope:") {
		t.Fatalf("impact text omits the type-scope disclosure:\n%s", text)
	}
}
