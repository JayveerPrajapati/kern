//go:build treesitter

package index

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Java local-type resolution for the tree-sitter path.
//
// The regex path promotes Java to "resolved" precision by tracking per-method
// local types (collectJavaLocalTypes, java_resolve.go) and rewriting
// receiver-var calls (resolveJavaCallee): a call x.bar() on a variable x
// declared as `Foo x` becomes Foo.bar, so the edge binds cross-file against
// the Foo symbol. The tree-sitter path records member invocations as bare
// object.method strings (extractCallee) without that rewrite. This pass closes
// the gap with the exact same semantics:
//
//   - Pass 1 (per method/constructor): map each local's simple name to its
//     declared type, from formal parameters (plain/generic/final/array/dotted,
//     varargs via spread_parameter) and local variable declarations with an
//     explicit type. Only explicit types are tracked: `var`-inferred
//     declarations and primitive-typed parameters are skipped, so nothing is
//     fabricated for unknown receivers.
//   - Pass 2: for each method_invocation whose object is a simple identifier
//     known in the enclosing method's local map, rewrite callee obj.method to
//     Type.method in the calls map. Chained calls (getHelper().doThing()) keep
//     their bare method name, matching the regex path which records each call
//     site separately.

// tsJavaTypeKinds are tree-sitter-java node kinds that can appear as a
// declared type.
var tsJavaTypeKinds = map[string]bool{
	"type_identifier":        true,
	"generic_type":           true,
	"array_type":             true,
	"scoped_type_identifier": true,
	"integral_type":          true,
	"floating_point_type":    true,
	"boolean_type":           true,
	"void_type":              true,
	"identifier":             true, // `var` inference keyword
}

// resolveJavaCalls rewrites Java member-invocation callees through per-method
// local-type maps, mirroring the regex path's collectJavaLocalTypes +
// resolveJavaCallee pair. It must be called after collectCalls has filled
// calls (keyed by receiver-qualified function name) and only for Java trees.
func resolveJavaCalls(root *sitter.Node, src []byte, calls map[string][]string) {
	var walk func(n *sitter.Node, fn string, lt map[string]string)
	walk = func(n *sitter.Node, fn string, lt map[string]string) {
		k := n.Kind()
		// Entering a method/constructor resets the local-type context for its
		// whole subtree (a nested method replaces it again on the way down).
		if k == "method_declaration" || k == "constructor_declaration" {
			fn = tsJavaFnName(n, src)
			lt = tsJavaCollectLocals(n, src)
		}
		if k == "method_invocation" && fn != "" {
			tsJavaResolveInvocation(n, src, fn, lt, calls)
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			if c := n.Child(i); c != nil {
				walk(c, fn, lt)
			}
		}
	}
	walk(root, "", nil)
}

// tsJavaFnName returns the receiver-qualified name of a Java method or
// constructor node — the same naming findEnclosingFunction produces for call
// sites inside it. (findEnclosingFunction cannot be called on the method node
// itself: it starts climbing at the node's parent.)
func tsJavaFnName(node *sitter.Node, src []byte) string {
	name := extractName(node, src, node.Kind())
	if name == "" {
		return ""
	}
	if i := strings.Index(name, "::"); i >= 0 {
		return name[:i] + "." + name[i+2:]
	}
	if recv := findReceiver(node, rootOf(node), src); recv != "" {
		return recv + "." + name
	}
	return name
}

// tsJavaCollectLocals walks a method's subtree and maps every explicitly typed
// local (formal parameters, varargs parameters, local variable declarations)
// to its simple type name.
func tsJavaCollectLocals(method *sitter.Node, src []byte) map[string]string {
	lt := map[string]string{}
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Kind() {
		case "formal_parameter", "spread_parameter", "local_variable_declaration":
			tsJavaAddLocal(lt, n, src)
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			if c := n.Child(i); c != nil {
				walk(c)
			}
		}
	}
	walk(method)
	return lt
}

// tsJavaAddLocal records name -> simpleType from one parameter or local
// declaration node, unless the type or name is a keyword / inferred-type token
// (var) — mirroring javaAddLocalType's skip rules.
func tsJavaAddLocal(lt map[string]string, decl *sitter.Node, src []byte) {
	// The name: formal_parameter carries it as its own "name" field (its
	// variable_declarator_id rule is hidden); local_variable_declaration and
	// spread_parameter wrap it in a variable_declarator child.
	nameNode := decl.ChildByFieldName("name")
	if nameNode == nil {
		for i := uint(0); i < decl.ChildCount(); i++ {
			c := decl.Child(i)
			if c != nil && c.Kind() == "variable_declarator" {
				nameNode = c.ChildByFieldName("name")
				break
			}
		}
	}
	if nameNode == nil {
		return
	}
	typeNode := decl.ChildByFieldName("type")
	if typeNode == nil {
		// spread_parameter loses the "type" field; find the type child by kind.
		for i := uint(0); i < decl.ChildCount(); i++ {
			c := decl.Child(i)
			if c != nil && tsJavaTypeKinds[c.Kind()] {
				typeNode = c
				break
			}
		}
	}
	if typeNode == nil {
		return
	}
	name := string(src[nameNode.StartByte():nameNode.EndByte()])
	typ := javaSimpleTypeName(string(src[typeNode.StartByte():typeNode.EndByte()]))
	if typ == "" || name == "" {
		return
	}
	// Skip primitives/keywords (int, long, ...) and inferred-type tokens (var)
	// exactly like javaAddLocalType's spec.kw + javaInferredTypes checks.
	if javaKw[typ] || javaInferredTypes[typ] || javaInferredTypes[name] {
		return
	}
	lt[name] = typ
}

// tsJavaResolveInvocation rewrites one method_invocation's recorded callee in
// the calls map using the enclosing method's local types.
func tsJavaResolveInvocation(n *sitter.Node, src []byte, fn string, lt map[string]string, calls map[string][]string) {
	obj := n.ChildByFieldName("object")
	if obj == nil {
		return
	}
	callee := extractCallee(n, src)
	if callee == "" {
		return
	}
	nameNode := n.ChildByFieldName("name")
	switch obj.Kind() {
	case "identifier":
		// Simple receiver variable: v.method() -> Type.method() when v's type is
		// known locally. Unknown receivers stay unresolved (mirrors
		// resolveJavaCallee).
		if nameNode == nil {
			return
		}
		receiver := string(src[obj.StartByte():obj.EndByte()])
		if t, ok := lt[receiver]; ok && t != "" {
			tsJavaReplaceCallee(calls, fn, callee, t+"."+string(src[nameNode.StartByte():nameNode.EndByte()]))
		}
	case "method_invocation":
		// Chained call a().b(): record the bare method name, matching the regex
		// path which records each call site separately (a, then b).
		if nameNode == nil {
			return
		}
		tsJavaReplaceCallee(calls, fn, callee, string(src[nameNode.StartByte():nameNode.EndByte()]))
	}
	// Other object shapes (field_access chains like System.out.println, static
	// type_identifier receivers, this/super) are left exactly as extractCallee
	// recorded them.
}

// tsJavaReplaceCallee replaces every occurrence of oldCallee with newCallee in
// calls[fn]; if newCallee is already present the old entries are dropped
// instead (dedupe). It is a no-op when oldCallee is not recorded.
func tsJavaReplaceCallee(calls map[string][]string, fn, oldCallee, newCallee string) {
	if oldCallee == newCallee {
		return
	}
	list := calls[fn]
	if list == nil {
		return
	}
	hasNew := false
	for _, c := range list {
		if c == newCallee {
			hasNew = true
			break
		}
	}
	out := list[:0]
	for _, c := range list {
		if c == oldCallee {
			if !hasNew {
				out = append(out, newCallee)
			}
			continue
		}
		out = append(out, c)
	}
	calls[fn] = out
}
