package index

import "testing"

func TestContextCallerLabelsAmbiguousNameStaysBare(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a/a.go": "package a\n\nfunc Generate() {}\n\nfunc Only() {}\n",
		"b/b.go": "package b\n\nfunc Generate() {}\n",
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	labels := contextCallerLabels(ix, []string{"Generate", "Only"})
	if labels[0] != "Generate" {
		t.Errorf("ambiguous caller must stay bare, got %q", labels[0])
	}
	if labels[1] == "Only" {
		t.Errorf("unique caller must still carry its location, got %q", labels[1])
	}
}
