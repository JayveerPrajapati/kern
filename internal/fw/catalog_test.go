package fw

import (
	"reflect"
	"sort"
	"testing"
)

func TestLangs(t *testing.T) {
	langs := Langs()
	if len(langs) < 2 {
		t.Fatalf("expected multiple languages, got %v", langs)
	}
	// Sorted and unique.
	for i := 1; i < len(langs); i++ {
		if langs[i] < langs[i-1] {
			t.Errorf("Langs() not sorted at index %d: %v", i, langs)
		}
		if langs[i] == langs[i-1] {
			t.Errorf("duplicate language %q in Langs()", langs[i])
		}
	}
	// Every catalog entry's language must be present.
	seen := map[string]bool{}
	for _, l := range langs {
		seen[l] = true
	}
	for _, f := range Catalog() {
		if !seen[f.Lang] {
			t.Errorf("catalog language %q missing from Langs()", f.Lang)
		}
	}
	// Deterministic and equal to a sorted copy.
	if !reflect.DeepEqual(langs, Langs()) {
		t.Error("Langs() not deterministic")
	}
	sorted := append([]string(nil), langs...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(langs, sorted) {
		t.Error("Langs() should be sorted")
	}
}
