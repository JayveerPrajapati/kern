package asttypes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"strings"
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
func (symbol Symbol) DeclarationID() string {
	params := append([]string{}, symbol.Params...)
	data, _ := json.Marshal([]any{
		path.Clean(strings.ReplaceAll(symbol.File, "\\", "/")),
		symbol.Lang, symbol.Kind, symbol.Outer, symbol.Receiver,
		symbol.Name, symbol.Line, params,
	})
	digest := sha256.Sum256(data)
	return "decl:" + hex.EncodeToString(digest[:])
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
