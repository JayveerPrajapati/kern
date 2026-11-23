//go:build !notreesitter

package index

import "testing"

// TestTreeSitterRustImplIdentity pins the AST-extractor side of the same bug:
// impl blocks keyed only by the self type collapsed three `impl From<X> for
// Failure` blocks into one identity.
func TestTreeSitterRustImplIdentity(t *testing.T) {
	syms, _, _, _, err := tsExtract("repro.rs", []byte(rustThreeFromImpls), "rust")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	assertRustImplIdentity(t, syms)
}
