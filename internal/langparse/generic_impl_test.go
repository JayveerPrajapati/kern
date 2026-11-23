package langparse

import "testing"

// TestGenericImplMethodsCarryReceiver is the regression for `impl<'a> X<'a> {`:
// the impl rule required whitespace after `impl`, so a generic impl was not a
// declaration at all and every method inside it extracted as a bare func —
// `SymbolIndex.resolve` could not be reached by Type.Method or a qualified name.
func TestGenericImplMethodsCarryReceiver(t *testing.T) {
	src := []byte(`pub struct Generic<'a> {
    value: &'a [u8],
}

impl<'a> Generic<'a> {
    pub fn new(value: &'a [u8]) -> Self {
        Self { value }
    }

    pub fn first(&self) -> u8 {
        self.value.len() as u8
    }
}
`)
	syms, _, _, _, err := ExtractForeignRegex("probe.rs", src, "rust")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"new": "Generic", "first": "Generic"}
	for _, s := range syms {
		if w, ok := want[s.Name]; ok {
			if s.Kind != "method" || s.Receiver != w {
				t.Errorf("%s: kind=%q receiver=%q; want method/%s", s.Name, s.Kind, s.Receiver, w)
			}
		}
	}
	foundImpl := false
	for _, s := range syms {
		if s.Kind == "impl" && s.Name == "Generic" {
			foundImpl = true
		}
	}
	if !foundImpl {
		t.Error("generic impl block was not extracted as an impl declaration")
	}
}

// TestGenericImplVariants pins the other generic shapes the rule must accept.
func TestGenericImplVariants(t *testing.T) {
	cases := []struct{ line, want string }{
		{"impl<'a> Generic<'a> {", "Generic"},
		{"impl Generic {", "Generic"},
		{"impl<T> Wrapper<T> {", "Wrapper"},
		{"impl<T: Clone> Wrapper<T> {", "Wrapper"},
		{"impl<'a, T> Pair<'a, T> {", "Pair"},
		// The impl symbol is named after the SELF type (Map), not the trait.
		{"impl<K, V> From<Vec<(K, V)>> for Map<K, V> {", "Map"},
	}
	for _, tc := range cases {
		syms, _, _, _, err := ExtractForeignRegex("probe.rs", []byte(tc.line+"\n}\n"), "rust")
		if err != nil {
			t.Fatalf("%q: %v", tc.line, err)
		}
		found := false
		for _, s := range syms {
			if s.Kind == "impl" && s.Name == tc.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: no impl named %q in %v", tc.line, tc.want, syms)
		}
	}
}
