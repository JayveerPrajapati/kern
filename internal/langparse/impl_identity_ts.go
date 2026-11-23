//go:build !notreesitter

package langparse

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// ImplNodeIdentity returns the implemented type and header discriminator for a
// tree-sitter impl_item node. The discriminator lives in Symbol.Outer, which
// never joins FullName, so several impls for one type in one file stay
// distinct without changing graph keys.
func ImplNodeIdentity(n *sitter.Node, src []byte) (selfType, discriminator string) {
	return ImplIdentity(string(src[n.StartByte():n.EndByte()]))
}

// EnclosingImplDisc returns the normalized header discriminator of the nearest
// enclosing Rust impl_item block, or "" when n is not inside one. Methods in
// distinct impl blocks for the same type (three `impl From<X> for Failure`
// blocks each holding `fn from`) inherit it so their identities stay distinct.
func EnclosingImplDisc(n, root *sitter.Node, src []byte) string {
	for p := n.Parent(); p != nil && p.Id() != root.Id(); p = p.Parent() {
		if p.Kind() == "impl_item" {
			if _, disc := ImplIdentity(string(src[p.StartByte():p.EndByte()])); disc != "" {
				return disc
			}
			return ""
		}
	}
	return ""
}
