//go:build !notreesitter

package agentwire

import (
	"strconv"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// TestEmbeddedPluginParses is the syntax gate for the embedded opencode
// plugin: it parses assets/plugin/kern.ts with the tree-sitter TypeScript
// grammar and fails on ANY error node. The plugin ships as a runtime asset —
// the Go test suite never executes it, so a syntactically corrupt plugin
// passes every byte-parity and hash-registry gate while opencode silently
// fails to load it on every host (observed live 2026-10-19: commit aa4d411e
// shipped an orphaned `description:` line at the kern_explore entry; the
// kern guard went dead on all hosts for a day with zero test failures).
// Hash/parity tests prove the copies are identical to each other — only a
// real parse proves the bytes are loadable.
func TestEmbeddedPluginParses(t *testing.T) {
	src, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatal(err)
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())); err != nil {
		t.Fatalf("set language: %v", err)
	}
	tree := parser.Parse(src, nil)
	defer tree.Close()
	root := tree.RootNode()
	if root.HasError() {
		// Locate the first ERROR node for an actionable message.
		var at string
		var walk func(n *sitter.Node)
		walk = func(n *sitter.Node) {
			if at != "" || n == nil {
				return
			}
			if n.IsError() {
				p := n.StartPosition()
				at = "line " + strconv.Itoa(int(p.Row)+1) + ", column " + strconv.Itoa(int(p.Column)+1)
				return
			}
			for i := uint(0); i < n.ChildCount(); i++ {
				walk(n.Child(i))
				if at != "" {
					return
				}
			}
		}
		walk(root)
		t.Fatalf("embedded plugin kern.ts has a syntax error (first error node at %s) — opencode cannot load a syntactically invalid plugin, and every byte-parity gate stays green while the guard is dead", at)
	}
}
