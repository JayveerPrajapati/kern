package fw

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
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

// TestReadmeFrameworkCountDerived pins the README's two framework-count
// claims to the live catalog. The banner ("N Frameworks Recognized") and the
// details summary ("Recognized Frameworks (N)") are the only public counts; a
// catalog entry added or removed must fail here instead of drifting the doc.
func TestReadmeFrameworkCountDerived(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	readme := string(b)
	want := strconv.Itoa(len(Catalog()))

	banner := regexp.MustCompile(`(\d+) Frameworks Recognized`).FindStringSubmatch(readme)
	if len(banner) != 2 {
		t.Fatal(`README banner lacks an "N Frameworks Recognized" claim to guard`)
	}
	if banner[1] != want {
		t.Errorf("README banner claims %s frameworks, catalog has %s", banner[1], want)
	}

	summary := regexp.MustCompile(`Recognized Frameworks \((\d+)\)`).FindStringSubmatch(readme)
	if len(summary) != 2 {
		t.Fatal(`README details summary lacks a "Recognized Frameworks (N)" claim to guard`)
	}
	if summary[1] != want {
		t.Errorf("README details summary claims %s frameworks, catalog has %s", summary[1], want)
	}
}
