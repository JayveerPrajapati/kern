//go:build !notreesitter

package langparse

import (
	"fmt"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

var methodLike = map[string]bool{
	"method_declaration":        true,
	"method_definition":         true,
	"method_signature":          true,
	"abstract_method_signature": true,
	"constructor_declaration":   true,
	"function_signature_item":   true,
}

func ExtractName(node *sitter.Node, src []byte, kind string) string {

	nameKinds := map[string]bool{
		"identifier": true, "type_identifier": true, "property_identifier": true,
		"name": true, "field_identifier": true, "namespace_name": true,
		"class_name": true, "method_name": true, "word": true, "variable_name": true,
		"constant": true, "id_name": true, "keyframes_name": true,
		"property_name": true,
	}
	// Qualified identifiers (C++ "Shape::Shape") keep their full text so the
	// caller can split receiver from method name.
	if node.Kind() == "qualified_identifier" {
		return strings.ReplaceAll(string(src[node.StartByte():node.EndByte()]), " ", "")
	}
	// Leaf name nodes (e.g. CSS property_name) are their own name.
	if nameKinds[node.Kind()] {
		return string(src[node.StartByte():node.EndByte()])
	}
	// Field-based names (Dart signatures carry the name in a "name" field;
	// other grammars fall through to child scanning).
	if f := node.ChildByFieldName("name"); f != nil && nameKinds[f.Kind()] {
		return string(src[f.StartByte():f.EndByte()])
	}
	// Method-like declarations carry the return type before the name; the name
	// is the last name-bearing child ("public String list()" -> "list").
	if methodLike[node.Kind()] {
		last := ""
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child == nil {
				continue
			}
			ck := child.Kind()
			if nameKinds[ck] {
				last = string(src[child.StartByte():child.EndByte()])
			}
		}
		if last != "" {
			return last
		}
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		ck := child.Kind()
		if nameKinds[ck] {
			return string(src[child.StartByte():child.EndByte()])
		}
		if ck == "function_declarator" || ck == "declarator" || ck == "variable_declarator" ||
			ck == "name" || ck == "method_declarator" || ck == "impl_item" || ck == "class_definition" ||
			ck == "qualified_identifier" || ck == "function_signature" {
			if n := ExtractName(child, src, ck); n != "" {
				return n
			}
		}
	}
	return ""
}

func FindReceiver(node, root *sitter.Node, src []byte) string {
	// funcName carries the OUTERMOST enclosing function for a nested def:
	// a same-named nested def (e.g. `async def gen()` inside `_factory`
	// closures inside different test methods) must key on the method it
	// belongs to, not the nearest closure — the closures share a name, so
	// stopping at the first function ancestor collapsed distinct scopes
	// into one identity (duplicate-symbol conflict).
	funcName := ""
	for p, depth := node.Parent(), 1; p != nil && p.Id() != root.Id(); p, depth = p.Parent(), depth+1 {
		switch p.Kind() {
		case "impl_item", "struct_item", "trait_item", "class_definition",
			"class_declaration", "interface_declaration", "enum_declaration",
			"record_declaration", "struct_declaration", "trait_declaration",
			"namespace_declaration", "module", "class":
			// A nested def under a class resolves to its enclosing
			// method when one was seen; otherwise to the class itself.
			if funcName != "" {
				return funcName
			}
			if name := ExtractName(p, src, p.Kind()); name != "" {
				return name
			}
		case "class_body", "declaration_list", "body", "block", "body_statement":
			// An ANONYMOUS class body (Java `new Foo<>() { ... }`): its
			// methods get a per-occurrence receiver — the implemented type
			// + creation line (extractName cannot dig through `<>`).
			if p.Kind() == "class_body" && p.Parent() != nil && p.Parent().Kind() == "object_creation_expression" {
				if ty := p.Parent().ChildByFieldName("type"); ty != nil {
					base := string(src[ty.StartByte():ty.EndByte()])
					if j := strings.IndexByte(base, '<'); j > 0 {
						base = base[:j]
					}
					if base != "" {
						return fmt.Sprintf("%s@%d", base, int(p.Parent().StartPosition().Row)+1)
					}
				}
			}
			// intermediate container — keep walking up
		default:
			// A function-kind ancestor at depth > 1 — a body node in
			// between — is a NESTED def (Python helpers inside test
			// methods, JS closures): it belongs to the enclosing
			// FUNCTION, keeping same-named helpers in different methods
			// distinct (WD-3). Depth > 1 excludes the symbol's own
			// wrapper (Dart method_definition wraps method_signature).
			// Keep walking up: the outermost function (the method) wins,
			// so nested closures under different methods stay distinct.
			if depth > 1 && FuncKinds[p.Kind()] {
				if name := ExtractName(p, src, p.Kind()); name != "" {
					funcName = name
				}
			}
		}
	}
	return funcName
}

// findEnclosingFunction finds the function containing a call and returns its
// receiver-qualified name (e.g. "User.login") so call edges land on the same
// key the definition uses.
// funcKinds are node kinds that carry a function/method name.
var FuncKinds = map[string]bool{
	"function_item": true, "function_signature_item": true, "method_declaration": true,
	"function_definition": true, "constructor_declaration": true, "method_definition": true,
	"function_signature": true, "function_declaration": true, "generator_function_declaration": true,
	"method_signature": true, "getter_signature": true, "setter_signature": true,
	"constructor_signature": true, "factory_constructor_signature": true, "operator_signature": true,
	"local_function_declaration": true,
	"method":                     true, "singleton_method": true,
}

func RootOf(node *sitter.Node) *sitter.Node {
	for p := node.Parent(); p != nil; p = p.Parent() {
		node = p
	}
	return node
}

// extractCallee extracts the callee name from a call expression. Member-style
// nodes keep their full dotted chain ("System.out.println" stays as-is) so the
// edge matches the regex extractor's keys; scoped identifiers resolve to their
// last segment ("Point::new" -> "new").
func ExtractCallee(node *sitter.Node, src []byte) string {
	// A name-bearing node returns its own text.
	switch node.Kind() {
	case "identifier", "type_identifier", "field_identifier", "property_identifier", "name", "method_name", "word":
		return string(src[node.StartByte():node.EndByte()])
	}
	// Member-style nodes resolve to their dotted chain (scoped identifiers keep
	// only the last segment so "Point::new" lands on "new" like the regex).
	switch node.Kind() {
	case "member_expression", "attribute", "method_invocation", "field_expression", "field_access", "call", "function_expression":
		parts := []string{}
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child == nil {
				continue
			}
			ck := child.Kind()
			// Only name-bearing and nested-member segments count; argument
			// lists, index expressions and punctuation are skipped so
			// "System.out.println(...)" resolves to "System.out.println".
			if ck == "identifier" || ck == "type_identifier" || ck == "field_identifier" ||
				ck == "property_identifier" || ck == "name" || ck == "method_name" || ck == "word" ||
				ck == "constant" {
				parts = append(parts, string(src[child.StartByte():child.EndByte()]))
			} else if ck == "member_expression" || ck == "attribute" || ck == "method_invocation" || ck == "field_expression" || ck == "field_access" || ck == "scoped_identifier" || ck == "call" || ck == "function_expression" {
				if seg := ExtractCallee(child, src); seg != "" {
					parts = append(parts, seg)
				}
			}
		}
		return strings.Join(parts, ".")
	case "scoped_identifier":
		name := ""
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child == nil {
				continue
			}
			if seg := ExtractCallee(child, src); seg != "" {
				name = seg
			}
		}
		return name
	case "selector": // Dart: callee is the receiver chain before the args
		if p := node.Parent(); p != nil && p.Kind() != "arguments" && p.Kind() != "argument_part" {
			txt := strings.TrimSpace(string(src[p.StartByte():node.StartByte()]))
			if i := strings.LastIndex(txt, "="); i >= 0 {
				txt = txt[i+1:]
			}
			return strings.TrimSpace(txt)
		}
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		ck := child.Kind()
		if ck == "command_name" {
			if name := ExtractCallee(child, src); name != "" {
				return name
			}
			continue
		}
		switch ck {
		case "identifier", "type_identifier", "field_identifier", "property_identifier", "name", "method_name", "word", "constant":
			return string(src[child.StartByte():child.EndByte()])
		case "member_expression", "attribute", "method_invocation", "field_expression", "field_access", "scoped_identifier":
			if name := ExtractCallee(child, src); name != "" {
				return name
			}
		}
	}
	return ""
}

// TreeSitterAvailable reports whether tree-sitter is available for the given language.
