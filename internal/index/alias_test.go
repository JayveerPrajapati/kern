package index

import "testing"

// TestExtractGoTypeAliasRecordsRHS pins the alias field the type-oriented
// surfaces (impact/refs/explore) rely on: `type A = B` must carry "B"
// ("pkg.B" when qualified) while definitions (`type H int`) carry nothing.
func TestExtractGoTypeAliasRecordsRHS(t *testing.T) {
	src := []byte(`package p

type A = B
type C = pkg.D
type E[T any] = F[T]
type G = struct{ X int }
type H int
`)
	syms, _, _, _, err := extract("a.go", src)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "B", "C": "pkg.D", "E": "F", "G": "", "H": ""}
	got := map[string]string{}
	for _, s := range syms {
		if _, ok := want[s.Name]; ok {
			got[s.Name] = s.Alias
		}
	}
	for name, w := range want {
		if g, ok := got[name]; !ok {
			t.Errorf("symbol %s missing from extraction", name)
		} else if g != w {
			t.Errorf("symbol %s alias = %q, want %q", name, g, w)
		}
	}
}
