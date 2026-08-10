//go:build treesitter

package index

import (
	"fmt"
	"path/filepath"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/tree-sitter/tree-sitter-bash/bindings/go"
	"github.com/tree-sitter/tree-sitter-c/bindings/go"
	"github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	"github.com/tree-sitter/tree-sitter-css/bindings/go"
	"github.com/tree-sitter/tree-sitter-go/bindings/go"
	"github.com/tree-sitter/tree-sitter-java/bindings/go"
	"github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	"github.com/tree-sitter/tree-sitter-php/bindings/go"
	"github.com/tree-sitter/tree-sitter-python/bindings/go"
	"github.com/tree-sitter/tree-sitter-ruby/bindings/go"
	"github.com/tree-sitter/tree-sitter-rust/bindings/go"
	"github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// tsLanguageMap maps kern language IDs to tree-sitter Language pointers.
var tsLanguageMap = map[string]*sitter.Language{
	"go":         sitter.NewLanguage(tree_sitter_go.Language()),
	"python":     sitter.NewLanguage(tree_sitter_python.Language()),
	"javascript": sitter.NewLanguage(tree_sitter_javascript.Language()),
	"typescript": sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()),
	"tsx":        sitter.NewLanguage(tree_sitter_typescript.LanguageTSX()),
	"c":          sitter.NewLanguage(tree_sitter_c.Language()),
	"cpp":        sitter.NewLanguage(tree_sitter_cpp.Language()),
	"java":       sitter.NewLanguage(tree_sitter_java.Language()),
	"rust":       sitter.NewLanguage(tree_sitter_rust.Language()),
	"ruby":       sitter.NewLanguage(tree_sitter_ruby.Language()),
	"php":        sitter.NewLanguage(tree_sitter_php.LanguagePHP()),
	"bash":       sitter.NewLanguage(tree_sitter_bash.Language()),
	"css":        sitter.NewLanguage(tree_sitter_css.Language()),
}

// tsNodeTypes maps tree-sitter node types to kern symbol kinds. Keys are
// grammar node types shared across languages; several languages emit the same
// node type names, so each appears once.
var tsNodeTypes = map[string]string{
	// Go (unused in practice — Go uses the AST extractor)
	"function_declaration": "func",
	"method_declaration":   "method",
	"type_declaration":     "type",
	"var_declaration":      "var",
	"const_declaration":    "const",
	"struct_type":          "struct",
	"interface_type":       "interface",
	// JavaScript/TypeScript
	"function_expression":            "func",
	"generator_function_declaration": "func",
	"arrow_function":                 "func",
	"method_definition":              "method",
	"class_declaration":              "class",
	"class":                          "class",
	"abstract_class_declaration":     "class",
	"interface_declaration":          "interface",
	"type_alias_declaration":         "type",
	"enum_declaration":               "enum",
	"variable_declaration":           "var",
	"lexical_declaration":            "const",
	"function_signature":             "func",
	"method_signature":               "method",
	"property_signature":             "prop",
	"public_field_definition":        "prop",
	"abstract_method_signature":      "method",
	// Python
	"function_definition":       "func",
	"class_definition":          "class",
	"async_function_definition": "func",
	"type_alias_statement":      "type",
	// Rust
	"function_item":           "func",
	"function_signature_item": "func",
	"struct_item":             "struct",
	"enum_item":               "enum",
	"trait_item":              "trait",
	"impl_item":               "impl",
	"const_item":              "const",
	"static_item":             "var",
	"type_item":               "type",
	"union_item":              "union",
	"mod_item":                "module",
	// Java / C# / PHP (shared declaration names)
	"record_declaration":          "record",
	"constructor_declaration":     "method",
	"field_declaration":           "var",
	"annotation_type_declaration": "type",
	"struct_declaration":          "struct",
	"namespace_declaration":       "namespace",
	"trait_declaration":           "trait",
	"property_declaration":        "prop",
	// C/C++
	"class_specifier":      "class",
	"struct_specifier":     "struct",
	"enum_specifier":       "enum",
	"union_specifier":      "union",
	"namespace_definition": "namespace",
	"type_definition":      "type",
	// Ruby
	"method":           "method",
	"singleton_method": "method",
	"module":           "module",
	// Bash
	"variable_assignment": "var",
	// CSS
	"class_selector":      "class",
	"id_selector":         "const",
	"keyframes_statement": "func",
	"property_name":       "prop",
}

// tsLanguageFor maps kern language ids (from detectLang) to tree-sitter
// grammar ids. detectLang uses "shell" for shell files and "typescript" for
// both .ts and .tsx; the grammars are keyed "bash" and "tsx" respectively.
func tsLanguageFor(lang, rel string) (string, bool) {
	switch lang {
	case "shell":
		return "bash", true
	case "typescript":
		if strings.HasSuffix(rel, ".tsx") || strings.Contains(rel, ".tsx.") {
			return "tsx", true
		}
		return "typescript", true
	}
	_, ok := tsLanguageMap[lang]
	return lang, ok
}

// tsExtractor uses tree-sitter to extract symbols, calls and inheritance.
func tsExtract(rel string, src []byte, lang string) ([]Symbol, map[string][]string, map[string][]string, *Pkg, error) {
	tsLangID, ok := tsLanguageFor(lang, rel)
	if !ok {
		return nil, nil, nil, nil, fmt.Errorf("no tree-sitter grammar for %s", lang)
	}
	tsLang := tsLanguageMap[tsLangID]

	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(tsLang); err != nil {
		return nil, nil, nil, nil, err
	}

	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, nil, nil, nil, fmt.Errorf("parse failed")
	}
	defer tree.Close()

	root := tree.RootNode()
	if root.IsError() || root.HasError() {
		return nil, nil, nil, nil, fmt.Errorf("parse has errors")
	}

	calls := map[string][]string{}

	// First pass: collect all definitions
	defs := collectDefinitions(root, src, rel, lang)

	// Second pass: collect call edges
	collectCalls(root, src, defs, calls)

	// Third pass: collect inheritance (extends / implements) edges
	inherits := collectInheritance(root, src, defs, lang)

	// Build package info
	pkg := &Pkg{
		Name:  filepath.Base(filepath.Dir(rel)),
		Path:  filepath.Dir(rel),
		Files: []string{rel},
		Lang:  lang,
	}

	return defs, calls, inherits, pkg, nil
}

// collectDefinitions walks the AST and extracts symbol definitions.
func collectDefinitions(node *sitter.Node, src []byte, file, lang string) []Symbol {
	var syms []Symbol
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		kind := n.Kind()
		if symKind, ok := tsNodeTypes[kind]; ok {
			name := extractName(n, src, kind)
			// Skip the file root node itself (e.g. Python's "module"): it is a
			// container, not a symbol in its own right.
			if name != "" && n != node {
				// CSS custom properties are the only props worth indexing; plain
				// property names (color, opacity) are noise.
				if kind == "property_name" && !strings.HasPrefix(name, "--") {
					return
				}
				start := n.StartPosition()
				end := n.EndPosition()
				sym := Symbol{
					Kind: symKind,
					Name: name,
					File: file,
					Line: int(start.Row) + 1,
					End:  int(end.Row) + 1,
					Lang: lang,
				}
				// JS/TS arrow functions assigned via const/let/var read as
				// function definitions, matching the regex path.
				if symKind == "const" || symKind == "var" {
					if hasDescendantKind(n, "arrow_function") {
						sym.Kind = "func"
					}
				}
				// C++ out-of-class definitions carry a qualified name
				// ("Shape::Shape") — split receiver from method name.
				if i := strings.Index(name, "::"); i >= 0 {
					sym.Receiver = name[:i]
					sym.Name = name[i+2:]
					sym.Kind = "method"
				}
				// Try to find receiver for methods, and promote functions that
				// live inside a class/impl container to methods.
				if symKind == "method" || symKind == "func" {
					if recv := findReceiver(n, node, src); recv != "" {
						sym.Kind = "method"
						sym.Receiver = recv
					} else if symKind == "method" {
						sym.Kind = "func"
					}
				}
				syms = append(syms, sym)
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			if child := n.Child(i); child != nil {
				walk(child)
			}
		}
	}
	walk(node)
	return syms
}

// collectCalls walks the AST and extracts call edges.
func collectCalls(node *sitter.Node, src []byte, defs []Symbol, calls map[string][]string) {
	// Build a set of locally-defined symbol names so calls to them are always
	// kept even when the regex keyword heuristic would not match.
	nameSet := make(map[string]bool)
	for _, d := range defs {
		nameSet[d.FullName()] = true
		nameSet[d.Name] = true
	}

	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		kind := n.Kind()
		// Look for call expressions, bash commands whose first word names a
		// local function, and object/type creations (Java `new App(1)`).
		isCall := strings.HasSuffix(kind, "call") || strings.HasSuffix(kind, "invocation") ||
			kind == "call_expression" || kind == "command" || kind == "object_creation_expression" ||
			kind == "new_expression"
		if isCall {
			// Try to extract the callee name
			if callee := extractCallee(n, src); callee != "" {
				// Find the enclosing function
				if caller := findEnclosingFunction(n, src); caller != "" {
					if _, ok := nameSet[callee]; ok || !isKeywordCall(callee) {
						if callee != caller {
							calls[caller] = append(calls[caller], callee)
						}
					}
				}
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			if child := n.Child(i); child != nil {
				walk(child)
			}
		}
	}
	walk(node)
}

// collectInheritance walks the AST and extracts extends / implements edges.
// Results are keyed by the subtype's symbol name; values carry the edge kind
// prefix ("extends:Base", "implements:IFace") so queries can distinguish
// inheritance from interface implementation.
func collectInheritance(node *sitter.Node, src []byte, defs []Symbol, lang string) map[string][]string {
	inherits := map[string][]string{}

	// collectNamesDeep returns every name-bearing descendant's text under n,
	// recursing through wrapper nodes (type_list, class_heritage, ...).
	var collectNamesDeep func(n *sitter.Node) []string
	collectNamesDeep = func(n *sitter.Node) []string {
		var out []string
		switch n.Kind() {
		case "identifier", "type_identifier", "constant", "name", "namespace_name",
			"class_name", "scoped_identifier", "qualified_identifier":
			return []string{string(src[n.StartByte():n.EndByte()])}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			if c := n.Child(i); c != nil {
				out = append(out, collectNamesDeep(c)...)
			}
		}
		return out
	}

	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		kind := n.Kind()
		subtype := ""
		var bases []string

		switch kind {
		case "class_definition": // Python: class Cat(Animal, Pet):
			subtype = extractName(n, src, kind)
			for i := uint(0); i < n.ChildCount(); i++ {
				c := n.Child(i)
				if c != nil && c.Kind() == "argument_list" {
					bases = append(bases, tag("extends", collectNamesDeep(c))...)
				}
			}
		case "class_declaration", "interface_declaration": // JS/TS/PHP/Java
			subtype = extractName(n, src, kind)
			extendsKinds := map[string]bool{
				"extends_clause": true, "superclass": true, "extends_interfaces": true,
				"extends_type_clause": true, "base_clause": true, "base_class_clause": true,
				"class_heritage": true,
			}
			implementsKinds := map[string]bool{
				"implements_clause": true, "super_interfaces": true, "class_interface_clause": true,
			}
			for i := uint(0); i < n.ChildCount(); i++ {
				c := n.Child(i)
				if c == nil {
					continue
				}
				ck := c.Kind()
				switch {
				case extendsKinds[ck]:
					// class_heritage nests extends/implements clauses; unwrap it
					// so each clause is classified by its own kind.
					for j := uint(0); j < c.ChildCount(); j++ {
						cc := c.Child(j)
						if cc == nil {
							continue
						}
						cck := cc.Kind()
						if extendsKinds[cck] {
							bases = append(bases, tag("extends", collectNamesDeep(cc))...)
						} else if implementsKinds[cck] {
							bases = append(bases, tag("implements", collectNamesDeep(cc))...)
						}
					}
					if ck != "class_heritage" {
						bases = append(bases, tag("extends", collectNamesDeep(c))...)
					}
				case implementsKinds[ck]:
					bases = append(bases, tag("implements", collectNamesDeep(c))...)
				}
			}
		case "class": // Ruby: class Cat < Animal
			subtype = extractName(n, src, kind)
			for i := uint(0); i < n.ChildCount(); i++ {
				c := n.Child(i)
				if c != nil && c.Kind() == "superclass" {
					bases = append(bases, tag("extends", collectNamesDeep(c))...)
				}
			}
		case "class_specifier": // C++: class Cat : public Animal, virtual Pet
			subtype = extractName(n, src, kind)
			for i := uint(0); i < n.ChildCount(); i++ {
				c := n.Child(i)
				if c != nil && c.Kind() == "base_class_clause" {
					bases = append(bases, tag("extends", collectNamesDeep(c))...)
				}
			}
		case "impl_item": // Rust: impl Animal for Cat
			names := collectNamesDeep(n)
			if len(names) >= 2 {
				subtype = names[len(names)-1]
				bases = append(bases, tag("implements", names[:len(names)-1])...)
			}
		case "trait_item": // Rust: trait Meow: Speak
			subtype = extractName(n, src, kind)
			for i := uint(0); i < n.ChildCount(); i++ {
				c := n.Child(i)
				if c != nil && c.Kind() == "trait_bounds" {
					bases = append(bases, tag("extends", collectNamesDeep(c))...)
				}
			}
		}

		if subtype != "" && len(bases) > 0 {
			for _, b := range bases {
				if b != "" && b != subtype {
					inherits[subtype] = append(inherits[subtype], b)
				}
			}
		}

		for i := uint(0); i < n.ChildCount(); i++ {
			if child := n.Child(i); child != nil {
				walk(child)
			}
		}
	}
	walk(node)
	return inherits
}

// tag prefixes every base name with the given edge kind.
func tag(kind string, names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, kind+":"+n)
	}
	return out
}

// isKeywordCall reports whether a bare callee name looks like a language
// keyword or control-flow word rather than a user symbol, so it is not
// recorded as a call edge.
func isKeywordCall(name string) bool {
	kw := map[string]bool{
		"if": true, "else": true, "elif": true, "for": true, "while": true, "switch": true,
		"case": true, "return": true, "break": true, "continue": true, "yield": true,
		"new": true, "delete": true, "typeof": true, "instanceof": true, "in": true,
		"of": true, "await": true, "async": true, "try": true, "catch": true, "finally": true,
		"throw": true, "raise": true, "pass": true, "import": true, "export": true, "from": true,
		"print": true, "exit": true, "assert": true, "super": true, "this": true,
		"then": true, "do": true, "def": true, "fn": true, "func": true, "function": true,
		"match": true, "with": true, "as": true, "struct": true, "enum": true, "trait": true,
		"impl": true, "let": true, "mut": true, "const": true, "var": true, "static": true,
		"class": true, "interface": true, "echo": true, "printf": true, "puts": true,
	}
	return kw[name]
}

// extractName gets the identifier name from a definition node. It looks for
// the first direct name-bearing child; if none exists it descends shallowly so
// declaration shapes that wrap their name (C's function_declarator, Java's
// variable_declarator, PHP's name child) still resolve.
// methodLike node kinds put the method name after their return type, so the
// name is the last name-bearing child rather than the first.
var methodLike = map[string]bool{
	"method_declaration":        true,
	"method_definition":         true,
	"method_signature":          true,
	"abstract_method_signature": true,
	"constructor_declaration":   true,
	"function_signature_item":   true,
}

func extractName(node *sitter.Node, src []byte, kind string) string {
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
			ck == "qualified_identifier" {
			if n := extractName(child, src, ck); n != "" {
				return n
			}
		}
	}
	return ""
}

// hasDescendantKind reports whether any node at or below n has the given kind.
func hasDescendantKind(n *sitter.Node, want string) bool {
	if n.Kind() == want {
		return true
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		if child := n.Child(i); child != nil && hasDescendantKind(child, want) {
			return true
		}
	}
	return false
}

// findReceiver finds the receiver/container type for a method. Rust methods
// live under impl_item; class methods (Java/C#/TS) under a class body whose
// ancestor carries the type name; Python methods under class_definition; PHP
// under class_declaration.
func findReceiver(node, root *sitter.Node, src []byte) string {
	for p := node.Parent(); p != nil && p.Id() != root.Id(); p = p.Parent() {
		switch p.Kind() {
		case "impl_item", "struct_item", "trait_item", "class_definition",
			"class_declaration", "interface_declaration", "enum_declaration",
			"record_declaration", "struct_declaration", "trait_declaration",
			"namespace_declaration", "module", "class":
			if name := extractName(p, src, p.Kind()); name != "" {
				return name
			}
		case "class_body", "declaration_list", "body", "block", "body_statement":
			// intermediate container — keep walking up
		}
	}
	return ""
}

// findEnclosingFunction finds the function containing a call and returns its
// receiver-qualified name (e.g. "User.login") so call edges land on the same
// key the definition uses.
func findEnclosingFunction(node *sitter.Node, src []byte) string {
	for p := node.Parent(); p != nil; p = p.Parent() {
		k := p.Kind()
		if strings.HasSuffix(k, "function") || strings.HasSuffix(k, "method") || k == "function_item" ||
			k == "function_signature_item" || k == "method_declaration" || k == "function_definition" ||
			k == "constructor_declaration" || k == "method_definition" || k == "function_signature" ||
			k == "function_declaration" || k == "generator_function_declaration" {
			if name := extractName(p, src, k); name != "" {
				// C++ qualified definitions ("Shape::area") map to "Shape.area".
				if i := strings.Index(name, "::"); i >= 0 {
					return name[:i] + "." + name[i+2:]
				}
				if recv := findReceiver(p, rootOf(p), src); recv != "" {
					return recv + "." + name
				}
				return name
			}
		}
	}
	return ""
}

// rootOf climbs to the top of the tree from any node.
func rootOf(node *sitter.Node) *sitter.Node {
	for p := node.Parent(); p != nil; p = p.Parent() {
		node = p
	}
	return node
}

// extractCallee extracts the callee name from a call expression. Member-style
// nodes keep their full dotted chain ("System.out.println" stays as-is) so the
// edge matches the regex extractor's keys; scoped identifiers resolve to their
// last segment ("Point::new" -> "new").
func extractCallee(node *sitter.Node, src []byte) string {
	// A name-bearing node returns its own text.
	switch node.Kind() {
	case "identifier", "type_identifier", "field_identifier", "property_identifier", "name", "method_name", "word":
		return string(src[node.StartByte():node.EndByte()])
	}
	// Member-style nodes resolve to their dotted chain (scoped identifiers keep
	// only the last segment so "Point::new" lands on "new" like the regex).
	switch node.Kind() {
	case "member_expression", "attribute", "method_invocation", "field_expression", "field_access", "call":
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
			} else if ck == "member_expression" || ck == "attribute" || ck == "method_invocation" || ck == "field_expression" || ck == "field_access" || ck == "scoped_identifier" || ck == "call" {
				if seg := extractCallee(child, src); seg != "" {
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
			if seg := extractCallee(child, src); seg != "" {
				name = seg
			}
		}
		return name
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		ck := child.Kind()
		if ck == "command_name" {
			if name := extractCallee(child, src); name != "" {
				return name
			}
			continue
		}
		switch ck {
		case "identifier", "type_identifier", "field_identifier", "property_identifier", "name", "method_name", "word", "constant":
			return string(src[child.StartByte():child.EndByte()])
		case "member_expression", "attribute", "method_invocation", "field_expression", "field_access", "scoped_identifier":
			if name := extractCallee(child, src); name != "" {
				return name
			}
		}
	}
	return ""
}

// TreeSitterAvailable reports whether tree-sitter is available for the given language.
func TreeSitterAvailable(lang string) bool {
	if _, ok := tsLanguageFor(lang, ""); ok {
		return true
	}
	return false
}
