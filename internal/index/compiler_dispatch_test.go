package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGoCompilerDispatchUsesMethodSets(t *testing.T) {
	source := `package demo
type Reader interface { Read() string }
type Writer interface { Read() string; Write() }
type Good struct{}
func (Good) Read() string { return "good" }
type Other struct{}
func (Other) Read() string { return "other" }
func (Other) Write() {}
type Wrong struct{}
func (Wrong) Read() int { return 1 }
func Use(value Reader) { value.Read() }
func UseWriter(value Writer) { value.Read() }
func Work() string { return "work" }
func WrongWork() int { return 1 }
func Dynamic(fn func() string) { fn() }
func Assigned() { fn := Work; fn() }
func Bound() { fn := Good{}.Read; fn() }
func Seed() { Use(Good{}); Use(Other{}); UseWriter(Other{}); Dynamic(Work) }
`
	root := writeTree(t, map[string]string{"go.mod": "module demo\n\ngo 1.22\n", "main.go": source})
	ix, err := BuildWithOptions(root, WithGoTypes())
	if err != nil {
		t.Fatal(err)
	}
	assertTargets := func(ix *Index, owner string, want ...string) {
		t.Helper()
		seen := map[string]bool{}
		for _, edge := range ix.Calls[owner] {
			if edge.Synth != "dispatch:go-cha" {
				continue
			}
			if edge.Binding != "go/ssa-cha" || edge.Confidence != ConfidenceLow || edge.FromID == "" || edge.ToID == "" {
				t.Fatalf("dispatch candidate presented as static certainty: %+v", edge)
			}
			seen[edge.Target] = true
		}
		if len(seen) != len(want) {
			t.Fatalf("%s candidates = %v, want %v", owner, seen, want)
		}
		for _, name := range want {
			if !seen[name] {
				t.Fatalf("%s missing candidate %s: %v", owner, name, seen)
			}
		}
	}
	assertTargets(ix, "Use", "Good.Read", "Other.Read")
	assertTargets(ix, "UseWriter", "Other.Read")
	assertTargets(ix, "Dynamic", "Work", "Good.Read")
	assertTargets(ix, "Assigned", "Work")
	assertTargets(ix, "Bound", "Good.Read")
	data, err := json.Marshal(ix)
	if err != nil {
		t.Fatal(err)
	}
	var restored Index
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	assertTargets(&restored, "Use", "Good.Read", "Other.Read")
	for _, edge := range restored.CallBindings().Edges {
		if edge.Binding == "go/ssa-cha" && (edge.Confidence != "low" || edge.Synth != "dispatch:go-cha" || edge.Resolution != "resolved") {
			t.Fatalf("export lost dispatch provenance: %+v", edge)
		}
	}
	updatedSource := `package demo
type Reader interface { Read() string }
type Other struct{}
func (Other) Read() string { return "other" }
func Use(value Reader) { value.Read() }
func Seed() { Use(Other{}) }
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(updatedSource), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, err := Update(root, &restored)
	if err != nil {
		t.Fatal(err)
	}
	assertTargets(updated, "Use", "Other.Read")
}
