package langparse

import "strings"

// ImplIdentity derives the implemented (self) type and a per-block
// discriminator from a Rust impl header.
//
// The graph keys declaration nodes by FullName (receiver + name), so two impl
// blocks for the same self type — `impl From<String> for Failure` and
// `impl From<u8> for Failure` — would otherwise share one identity and trip
// the fail-loud duplicate gate (internal/index/engine.go). The discriminator
// is the normalized header; callers store it in Symbol.Outer, which by
// contract never joins FullName, so graph keys and the parity contract are
// unchanged.
//
// Returns ("", "") when the header is not an impl block.
func ImplIdentity(header string) (selfType, discriminator string) {
	h := normalizeImplHeader(header)
	if h == "" {
		return "", ""
	}
	rest, ok := cutImplKeyword(h)
	if !ok {
		return "", ""
	}
	rest = strings.TrimSpace(stripLeadingGenerics(rest))
	if rest == "" {
		return "", ""
	}
	target := rest
	if i := topLevelFor(rest); i >= 0 {
		target = rest[i+len(" for "):]
	}
	self := baseSelfType(target)
	if self == "" {
		return "", ""
	}
	return self, h
}

// normalizeImplHeader keeps only the declaration header (everything before
// the opening brace) and collapses whitespace so multi-line headers produce
// one stable discriminator.
func normalizeImplHeader(header string) string {
	if i := strings.IndexByte(header, '{'); i >= 0 {
		header = header[:i]
	}
	return strings.Join(strings.Fields(header), " ")
}

// cutImplKeyword strips the leading `impl` (and an optional `unsafe`) keyword
// and returns the remaining header.
func cutImplKeyword(h string) (string, bool) {
	h = strings.TrimPrefix(h, "unsafe ")
	if !strings.HasPrefix(h, "impl") {
		return "", false
	}
	after := h[len("impl"):]
	if after != "" && after[0] != ' ' && after[0] != '<' {
		return "", false
	}
	return strings.TrimSpace(after), true
}

// stripLeadingGenerics drops a leading generic parameter list
// (`<T: Clone, U>`), including nested angle brackets.
func stripLeadingGenerics(s string) string {
	if !strings.HasPrefix(s, "<") {
		return s
	}
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			depth++
		case '>':
			depth--
			if depth == 0 {
				return s[i+1:]
			}
		}
	}
	return s
}

// topLevelFor returns the byte offset of the ` for ` separator outside any
// generic argument list, or -1.
func topLevelFor(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		case ' ':
			if depth == 0 && strings.HasPrefix(s[i:], " for ") {
				return i
			}
		}
	}
	return -1
}

// topLevelWhere returns the byte offset of a top-level ` where ` clause, or -1.
func topLevelWhere(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		case ' ':
			if depth == 0 && strings.HasPrefix(s[i:], " where ") {
				return i
			}
		}
	}
	return -1
}

// baseSelfType reduces a self-type expression to its bare type name:
// `&'a mut Foo<T>` -> "Foo", `crate::model::Bar` -> "Bar". It takes the last
// identifier outside a generic argument list and ignores `mut`/`dyn`/lifetime
// tokens.
func baseSelfType(target string) string {
	if i := topLevelWhere(target); i >= 0 {
		target = target[:i]
	}
	depth := 0
	last := ""
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		tok := cur.String()
		cur.Reset()
		if depth == 0 && tok != "mut" && tok != "dyn" {
			last = tok
		}
	}
	for i := 0; i < len(target); i++ {
		c := target[i]
		switch {
		case c == '<':
			flush()
			depth++
		case c == '>':
			flush()
			if depth > 0 {
				depth--
			}
		case c == ':' || c == '&' || c == '\'' || c == ' ' || c == ',' || c == '(' || c == ')' ||
			c == '[' || c == ']' || c == ';':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return last
}
