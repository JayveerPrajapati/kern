package intel

import (
	"strings"
	"testing"
)

func TestExploreQualifiedDefinitionKeepsIdentity(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module example.com/mod\n\ngo 1.22\n",
		"a/a.go": "package a\n\nimport \"strings\"\n\nfunc shared() { strings.TrimSpace(\"a\") }\nfunc OnlyA() { shared() }\n",
		"b/b.go": "package b\n\nimport \"strings\"\n\nfunc shared() { strings.Contains(\"b\", \"b\") }\nfunc OnlyB() { shared() }\n",
	})
	ix := buildIndex(t, dir)
	for _, query := range []string{"a.shared", "b.shared"} {
		t.Run(query, func(t *testing.T) {
			rep, err := Explore(ix, query, 2, 30)
			if err != nil {
				t.Fatal(err)
			}
			prefix := query[:1]
			wantFile := prefix + "/" + prefix + ".go"
			if rep.Definition.File != wantFile {
				t.Fatalf("definition = %s, want %s", rep.Definition.File, wantFile)
			}
			if !strings.Contains(rep.Evidence, "evidence: "+wantFile+":") {
				t.Errorf("evidence = %s, want selected definition", rep.Evidence)
			}
			if len(rep.BlastFiles) != 1 || rep.BlastFiles[0] != wantFile {
				t.Errorf("blast files = %v, want [%s]", rep.BlastFiles, wantFile)
			}
			want, banned := "strings.TrimSpace", "strings.Contains"
			if prefix == "b" {
				want, banned = banned, want
			}
			if !contains(rep.Callees, want) || contains(rep.Callees, banned) {
				t.Errorf("callees = %v, want %s without %s", rep.Callees, want, banned)
			}
			if strings.Contains(rep.Source, "calls: "+banned) || strings.Contains(rep.Source, ", "+banned) {
				t.Errorf("source footer contains another definition's call: %s", rep.Source)
			}
		})
	}
}
