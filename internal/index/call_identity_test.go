package index

import (
	"encoding/json"
	"github.com/JayveerPrajapati/kern/internal/langparse"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCallsForSourceIdentity(t *testing.T) {
	fixtures := []struct {
		name, suffix, source string
	}{
		{"go", "go", "package fixture\nfunc Shared() { HELPER() }\nfunc HELPER() {}\n"},
		{"go-method", "go", "package fixture\ntype Service struct{}\nfunc (service Service) Shared() { HELPER() }\nfunc HELPER() {}\n"},
		{"python", "py", "def Shared():\n    HELPER()\ndef HELPER():\n    pass\n"},
		{"typescript", "ts", "function Shared() { HELPER(); }\nfunction HELPER() {}\n"},
		{"java", "java", "class Service {\n    void Shared() {\n        HELPER();\n    }\n    void HELPER() {}\n}\n"},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			files := map[string]string{}
			for _, prefix := range []string{"a", "b"} {
				files[prefix+"/source."+fixture.suffix] = strings.ReplaceAll(fixture.source, "HELPER", prefix+"Helper")
			}
			ix, err := Build(writeTree(t, files))
			if err != nil {
				t.Fatal(err)
			}
			assertSourceCalls(t, ix, fixture.suffix)
			data, err := json.Marshal(ix)
			if err != nil {
				t.Fatal(err)
			}
			var loaded Index
			if err := json.Unmarshal(data, &loaded); err != nil {
				t.Fatal(err)
			}
			assertSourceCalls(t, &loaded, fixture.suffix)
		})
	}
}

func assertSourceCalls(t *testing.T, ix *Index, suffix string) {
	t.Helper()
	for _, prefix := range []string{"a", "b"} {
		file := prefix + "/source." + suffix
		definition, ok := symbolInFile(ix, "Shared", file)
		if !ok {
			t.Fatalf("Shared missing in %s", file)
		}
		got := ix.CallsFor(definition)
		want := prefix + "Helper"
		found := false
		for _, target := range got {
			if target == want || strings.HasSuffix(target, "."+want) {
				found = true
			}
			if strings.HasSuffix(target, "Helper") && target != want && !strings.HasSuffix(target, "."+want) {
				t.Errorf("CallsFor(%s) leaked another source's helper: %s", file, target)
			}
		}
		if !found {
			t.Errorf("CallsFor(%s) = %v, missing %s", file, got, want)
		}
		for _, edge := range ix.Calls[definition.FullName()] {
			if edge.SourceFile == "" {
				t.Errorf("edge %v missing source file", edge)
			}
		}
	}
}

func TestUpdateSourceIdentityAfterEditAndDelete(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := writeTree(t, map[string]string{
		"a/source.go": "package a\nfunc Shared() { aHelper() }\nfunc aHelper() {}\nfunc replacement() {}\n",
		"b/source.go": "package b\nfunc Shared() { bHelper() }\nfunc bHelper() {}\n",
	})
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := prior.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	assertSourceCalls(t, loaded, "go")
	writeFileAt(t, root, "a/source.go", "package a\nfunc Shared() { replacement() }\nfunc aHelper() {}\nfunc replacement() {}\n")
	updated, err := Update(root, loaded)
	if err != nil {
		t.Fatal(err)
	}
	assertUpdateMatchesBuild(t, root, updated)
	definition, ok := symbolInFile(updated, "Shared", "a/source.go")
	if !ok || !reflect.DeepEqual(updated.CallsFor(definition), []string{"replacement"}) {
		t.Fatalf("edited Shared callees = %v, want replacement", updated.CallsFor(definition))
	}
	if err := os.Remove(filepath.Join(root, "a/source.go")); err != nil {
		t.Fatal(err)
	}
	deleted, err := Update(root, updated)
	if err != nil {
		t.Fatal(err)
	}
	assertUpdateMatchesBuild(t, root, deleted)
	for _, edge := range deleted.Calls["Shared"] {
		if edge.SourceFile != "b/source.go" {
			t.Errorf("deleted source edge survived: %+v", edge)
		}
	}
}

func assertUpdateMatchesBuild(t *testing.T, root string, updated *Index) {
	t.Helper()
	fresh, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated.Calls, fresh.Calls) {
		t.Errorf("incremental calls = %v, fresh calls = %v", updated.Calls, fresh.Calls)
	}
}

func TestDedupeCallEdgesKeepsSourceIdentity(t *testing.T) {
	got := langparse.DedupeCallEdges([]CallEdge{
		{Target: "helper", SourceFile: "b.go", Confidence: ConfidenceLow},
		{Target: "helper", SourceFile: "a.go", Confidence: ConfidenceLow},
		{Target: "helper", SourceFile: "a.go", Confidence: ConfidenceHigh},
	})
	want := []CallEdge{
		{Target: "helper", SourceFile: "a.go", Confidence: ConfidenceHigh},
		{Target: "helper", SourceFile: "b.go", Confidence: ConfidenceLow},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deduped edges = %v, want %v", got, want)
	}
}
