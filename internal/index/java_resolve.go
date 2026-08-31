package index

import (
	"regexp"
	"strings"
)

// Java "resolved" precision: cross-file call binding via local-type tracking,
// mirroring Go's collectLocalTypes/resolveCallee pair in goast.go. Java writes
// types explicitly (Foo x = new Foo();), so the common case — a variable
// declared with a known type and a method called on it — can be resolved with
// pattern matching over the stripped ffile representation, with no
// type-inference engine. Calls on inferred (var), chained or unknown
// receivers are left unresolved, same as Go.

// reJavaLocalDecl matches a Java local variable declaration written with an
// explicit type: "Type var;", "final Type var = ...;", "List<String> items =
// ...;", "String[] arr = ...;", "com.example.Foo x = ...;". Group 1 is the
// declared type (dotted chain, generics and array brackets included), group 2
// the variable name. The trailing ";=" is optional so declarations with or
// without an initializer are both caught; call expressions never match because
// their identifier chain is followed by "(" rather than whitespace + name.
var reJavaLocalDecl = regexp.MustCompile(`^\s*(?:final\s+)?((?:[A-Za-z_$][\w$]*\.)*[A-Za-z_$][\w$]*(?:<[^>]*>)?(?:\s*\[\s*\])*)\s+([A-Za-z_$][\w$]*)\s*(?:=.*)?;`)

// reJavaForEachVar matches the loop variable of an enhanced for statement:
// "for (Type var : coll)". Group 1 is the element type, group 2 the variable.
var reJavaForEachVar = regexp.MustCompile(`^\s*for\s*\(\s*(?:final\s+)?((?:[A-Za-z_$][\w$]*\.)*[A-Za-z_$][\w$]*(?:<[^>]*>)?(?:\s*\[\s*\])*)\s+([A-Za-z_$][\w$]*)\s*:`)

// reJavaTryResource matches a resource declared in a try-with-resources
// header: "try (Type var = ...)". Group 1 is the type, group 2 the variable.
var reJavaTryResource = regexp.MustCompile(`^\s*try\s*\(\s*(?:final\s+)?((?:[A-Za-z_$][\w$]*\.)*[A-Za-z_$][\w$]*(?:<[^>]*>)?(?:\s*\[\s*\])*)\s+([A-Za-z_$][\w$]*)\s*=`)

// reJavaParam matches a single method parameter after normalization
// (annotations, final, varargs and array brackets stripped): "Type name",
// "List<String> items", "com.example.Foo x". Group 1 is the declared type,
// group 2 the parameter name.
var reJavaParam = regexp.MustCompile(`^((?:[A-Za-z_$][\w$]*\.)*[A-Za-z_$][\w$]*(?:<[^>]*>)?)\s+([A-Za-z_$][\w$]+)$`)

// javaInferredTypes are type-position tokens that do not name a type: "var"
// (Java 10+ local type inference) and contextual keywords that can precede an
// identifier without declaring it. spec.kw already covers return/throw/assert
// and the primitive types; this set closes the remaining gaps so the local
// type map never carries fake entries.
var javaInferredTypes = map[string]bool{
	"var": true, "yield": true, "sealed": true, "permits": true, "record": true,
}

// javaSimpleTypeName reduces a declared type to the simple name used by index
// symbols: "com.example.Foo" -> "Foo", "List<String>" -> "List",
// "String[]" -> "String". Simple names match the simple class names symbols
// are registered under, exactly as Go's receiverName does.
func javaSimpleTypeName(t string) string {
	t = strings.TrimSpace(t)
	if i := strings.IndexByte(t, '<'); i >= 0 {
		t = t[:i]
	}
	if i := strings.IndexByte(t, '['); i >= 0 {
		t = t[:i]
	}
	t = strings.TrimSpace(t)
	if i := strings.LastIndexByte(t, '.'); i >= 0 {
		t = t[i+1:]
	}
	return t
}

// collectJavaLocalTypes walks a Java method's signature and body lines in the
// stripped ffile representation and builds a map of local variable name to
// simple declared type. It covers method parameters, explicit local
// declarations (with or without initializer), enhanced-for loop variables and
// try-with-resources headers. Only explicit types are tracked — "var"
// inference, return values and chained calls stay unresolved, matching the
// "resolved means the common case" contract.
func collectJavaLocalTypes(f *ffile, sigLine, bodyEnd int, spec *langSpec) map[string]string {
	lt := map[string]string{}
	end := bodyEnd
	if end > len(f.lines) {
		end = len(f.lines)
	}
	if sigLine < end {
		// Method parameters are declared on the signature line itself.
		for name, typ := range javaParamsFromLine(f.clean[sigLine]) {
			if name != "" && typ != "" {
				javaAddLocalType(lt, typ, name, spec)
			}
		}
	}
	for i := sigLine; i < end; i++ {
		trimmed := strings.TrimSpace(f.clean[i])
		if trimmed == "" {
			continue
		}
		if m := reJavaLocalDecl.FindStringSubmatch(trimmed); m != nil {
			javaAddLocalType(lt, m[1], m[2], spec)
			continue
		}
		if m := reJavaForEachVar.FindStringSubmatch(trimmed); m != nil {
			javaAddLocalType(lt, m[1], m[2], spec)
			continue
		}
		if m := reJavaTryResource.FindStringSubmatch(trimmed); m != nil {
			javaAddLocalType(lt, m[1], m[2], spec)
		}
	}
	return lt
}

// javaAddLocalType records name -> simpleType in lt unless the type or name is
// a keyword or an inferred type token.
func javaAddLocalType(lt map[string]string, typ, name string, spec *langSpec) {
	if typ == "" || name == "" {
		return
	}
	base := javaSimpleTypeName(typ)
	if base == "" || spec.kw[base] || javaInferredTypes[base] {
		return
	}
	if spec.kw[name] || javaInferredTypes[name] {
		return
	}
	lt[name] = base
}

// javaParamsFromLine extracts name -> simple type for every parameter of a
// method signature line ("public void foo(Type1 a, Type2 b) {"). It skips a
// leading annotation so its argument parens are not mistaken for the parameter
// list, and splits top-level commas (generics like List<Map<K,V>> contain
// commas of their own).
func javaParamsFromLine(line string) map[string]string {
	out := map[string]string{}
	s := javaStripAnnotations(strings.TrimSpace(line))
	open := strings.IndexByte(s, '(')
	if open < 0 {
		return out
	}
	depth := 0
	end := -1
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return out
	}
	inner := s[open+1 : end]
	var segs []string
	depth = 0
	start := 0
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '<', '[', '{':
			depth++
		case '>', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				segs = append(segs, inner[start:i])
				start = i + 1
			}
		}
	}
	segs = append(segs, inner[start:])
	for _, seg := range segs {
		if name, typ := javaParamSeg(seg); name != "" {
			out[name] = typ
		}
	}
	return out
}

// javaParamSeg normalizes one parameter segment into (name, simpleType):
// annotations and "final" are stripped in any order, "Type..." varargs and
// "Type[]" array brackets are removed, and the remainder must be "Type name".
func javaParamSeg(seg string) (name, typ string) {
	s := strings.TrimSpace(seg)
	if s == "" {
		return "", ""
	}
	for {
		t := strings.TrimSpace(s)
		if t == "final" {
			s = ""
			break
		}
		if strings.HasPrefix(t, "final ") || strings.HasPrefix(t, "final\t") {
			s = t[len("final"):]
			continue
		}
		if strings.HasPrefix(t, "@") {
			s = javaStripAnnotation(t)
			continue
		}
		break
	}
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "...")
	s = strings.ReplaceAll(s, "[]", " ")
	s = strings.TrimSpace(s)
	m := reJavaParam.FindStringSubmatch(s)
	if m == nil {
		return "", ""
	}
	return m[2], javaSimpleTypeName(m[1])
}

// javaStripAnnotations removes every leading annotation ("@Foo", "@Foo(args)",
// "@pkg.Foo(args)") from s and returns the remainder.
func javaStripAnnotations(s string) string {
	t := strings.TrimSpace(s)
	for strings.HasPrefix(t, "@") {
		t = javaStripAnnotation(t)
		t = strings.TrimSpace(t)
	}
	return t
}

// javaStripAnnotation consumes one leading Java annotation from s and returns
// the remainder.
func javaStripAnnotation(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "@") {
		return t
	}
	i := 1
	for i < len(t) && (t[i] == '.' || t[i] == '$' || isIdentChar(t[i])) {
		i++
	}
	if i < len(t) && t[i] == '(' {
		depth := 1
		i++
		for i < len(t) && depth > 0 {
			switch t[i] {
			case '(':
				depth++
			case ')':
				depth--
			}
			i++
		}
	}
	return strings.TrimSpace(t[i:])
}

// resolveJavaCallee rewrites a receiver-var method call to its type-qualified
// form when the receiver variable's type is known locally: v.method() ->
// Type.method(). It mirrors Go's resolveCallee (goast.go): the segment before
// the last dot is looked up in the local types map. Bare calls, static chains
// and calls on unknown variables are left untouched.
func resolveJavaCallee(name string, lt map[string]string) string {
	i := strings.LastIndexByte(name, '.')
	if i <= 0 {
		return name
	}
	prefix, sel := name[:i], name[i+1:]
	if t, ok := lt[prefix]; ok && t != "" {
		return t + "." + sel
	}
	return name
}
