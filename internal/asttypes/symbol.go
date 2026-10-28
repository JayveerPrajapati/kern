package asttypes

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Symbol is one top-level declaration.
type Symbol struct {
	Kind     string `json:"kind"` // func, method, struct, interface, type, const, var
	Name     string `json:"name"`
	Receiver string `json:"receiver,omitempty"`
	// Outer is the immediate enclosing class of a nested TYPE (WD-6c) or the
	// receiver class's own parent for a method (WD-6d) — the conflict-check
	// discriminator. Never joins FullName, so graph keys and the parity
	// contract are unchanged.
	Outer   string   `json:"outer,omitempty"`
	File    string   `json:"file"`
	Line    int      `json:"line"`
	End     int      `json:"end,omitempty"` // inclusive last line (0 if unknown)
	Params  []string `json:"params,omitempty"`
	Returns []string `json:"returns,omitempty"` // declared return type names
	Lang    string   `json:"lang,omitempty"`
	// Confidence is the parser's reliability in this symbol: HIGH for direct
	// declarations (functions, types, vars), MEDIUM for inferred kinds, LOW
	// for heuristic detections such as regex-derived entry points. Absent
	// in indexes written by older kern (String() then reports LOW).
	Confidence Confidence `json:"confidence,omitempty"`
	// Framework-aware entry-point metadata: a symbol with Entry set is a
	// framework entry point (HTTP handler, route, controller endpoint, task).
	Entry     bool   `json:"entry,omitempty"`
	Framework string `json:"framework,omitempty"` // fw framework id, e.g. "spring-mvc"
	Route     string `json:"route,omitempty"`     // route/path the entry serves, e.g. "/users"
}

// Lines returns the 1-based size of the declaration in source lines, falling
// back to a single line when the end is unknown.
func (s Symbol) Lines() int {
	if s.End > 0 && s.End >= s.Line {
		return s.End - s.Line + 1
	}
	return 1
}

// FullName returns the qualified name ("Type.Method" for methods).
func (s Symbol) FullName() string {
	if s.Receiver != "" {
		return s.Receiver + "." + s.Name
	}
	return s.Name
}

// DeclarationID returns the stable identity of a declaration: a digest over
// its file, language, kind, receiver, name, line and parameter list. It
// survives symbol edits (renames, body changes) so cross-file binding facts
// (compiler IDs, endpoint claims) stay attached to the same declaration.
//
// The digest input is byte-identical to the historical
// json.Marshal([]any{...}) encoding this function used to build via
// reflection — including HTML escaping (<, >, &), control-character escapes,
// and the always-array params element (the old code appended onto a
// non-nil empty slice, so a nil Params marshaled as [] too). The
// append-based encoder exists because this runs per symbol AND per call
// edge: buildCallerAdjacency and the export catalog call it tens of
// thousands of times per index, and the reflection marshal dominated that
// build in profiles. TestDeclarationIDMatchesJSONOracle pins the
// equivalence, so the persisted identity can never drift.
func (symbol Symbol) DeclarationID() string {
	var b []byte
	b = append(b, '[')
	b = appendJSONString(b, path.Clean(strings.ReplaceAll(symbol.File, "\\", "/")))
	b = append(b, ',')
	b = appendJSONString(b, symbol.Lang)
	b = append(b, ',')
	b = appendJSONString(b, symbol.Kind)
	b = append(b, ',')
	b = appendJSONString(b, symbol.Outer)
	b = append(b, ',')
	b = appendJSONString(b, symbol.Receiver)
	b = append(b, ',')
	b = appendJSONString(b, symbol.Name)
	b = append(b, ',')
	b = strconv.AppendInt(b, int64(symbol.Line), 10)
	b = append(b, ',', '[')
	for i, param := range symbol.Params {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendJSONString(b, param)
	}
	b = append(b, ']', ']')
	digest := sha256.Sum256(b)
	return "decl:" + hex.EncodeToString(digest[:])
}

// jsonHexDigits is the lowercase hex alphabet used by encoding/json's
// \u00XX escapes.
const jsonHexDigits = "0123456789abcdef"

// appendJSONString appends s to dst as a JSON string literal, byte-identical
// to what encoding/json (with its default HTML escaping) produces: printable
// ASCII passes through except the four escaped runes (" \ < > &), \n \r \t
// use two-char escapes, other control bytes become \u00XX, U+2028/U+2029 are
// escaped, and invalid UTF-8 becomes \ufffd.
func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(s); {
		b := s[i]
		if b < utf8.RuneSelf {
			switch {
			case b >= 0x20 && b != '"' && b != '&' && b != '<' && b != '>' && b != '\\':
				dst = append(dst, b)
			case b == '"' || b == '\\':
				dst = append(dst, '\\', b)
			case b == '\n':
				dst = append(dst, '\\', 'n')
			case b == '\r':
				dst = append(dst, '\\', 'r')
			case b == '\t':
				dst = append(dst, '\\', 't')
			default:
				dst = append(dst, '\\', 'u', '0', '0', jsonHexDigits[b>>4], jsonHexDigits[b&0xF])
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			// encoding/json replaces each invalid byte with the UTF-8
			// encoding of U+FFFD (not the \ufffd escape) — match it.
			dst = append(dst, '\xef', '\xbf', '\xbd')
			i++
		case r == '\u2028':
			dst = append(dst, '\\', 'u', '2', '0', '2', '8')
			i += size
		case r == '\u2029':
			dst = append(dst, '\\', 'u', '2', '0', '2', '9')
			i += size
		default:
			dst = append(dst, s[i:i+size]...)
			i += size
		}
	}
	return append(dst, '"')
}

// Pkg is one package/module discovered in the project.
type Pkg struct {
	Name    string       `json:"name"`
	Path    string       `json:"path"`
	Imports []ImportEdge `json:"imports"`
	Files   []string     `json:"files"`
	Lang    string       `json:"lang,omitempty"`
	// Exports is the per-file export record carried from extraction to
	// Index.ExportsByFile; it is not merged or persisted with the package.
	Exports []ExportEdge `json:"-"`
	// StructFields maps "StructName.fieldName" -> the bare type name of the
	// field's declared type (last identifier segment, pointer/array and
	// package qualifiers stripped), merged per package across files. The
	// merge-time callee rewrite uses it to complete receiver-field call
	// chains ("App.taskSvc.Deploy" -> "TaskService.Deploy") when the struct
	// is declared in a different file than the call. Absent in indexes
	// written by older kern: the rewrite then no-ops and the chain stays
	// alias-only, exactly as before.
	StructFields map[string]string `json:"struct_fields,omitempty"`
	// Constructors maps a package-level function name to its FIRST declared
	// return type name (bare, pointer stripped) — e.g. "NewHandlers" ->
	// "Handlers". Collected per file, merged per package across files. The
	// merge-time callee rewrite (rewriteConstructorCallees) uses it to
	// complete constructor-assigned receiver chains ("api.NewHandlers.Routes"
	// -> "Handlers.Routes") when the constructor is declared in a different
	// package than the call. Absent in indexes written by older kern: the
	// rewrite then no-ops and the chain stays unresolved, exactly as before.
	Constructors map[string]string `json:"constructors,omitempty"`
}
