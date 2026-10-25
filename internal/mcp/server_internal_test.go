package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
)

// resolveRoot / withinRoot / validateRoot are the root-confinement primitives
// that guard index-using tools. This file tests them directly (package mcp =
// internal test package) so the unexported functions are exercised without
// going through tool dispatch.

func TestResolveRootEmptyFallsBackToCwd(t *testing.T) {
	t.Parallel()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := resolveRoot(""); got != cwd {
		t.Fatalf("resolveRoot(\"\") = %q, want cwd %q", got, cwd)
	}
}

func TestResolveRootCleansToAbsolute(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Relative input resolves against cwd.
	got := resolveRoot(filepath.Join("a", "b"))
	want, err := filepath.Abs(filepath.Join("a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(want) {
		t.Fatalf("resolveRoot(relative) = %q, want %q", got, filepath.Clean(want))
	}
	// Trailing slash is cleaned away.
	got = resolveRoot(dir + string(filepath.Separator))
	if strings.HasSuffix(got, string(filepath.Separator)) {
		t.Fatalf("resolveRoot must clean the trailing slash, got %q", got)
	}
	// `..` components are resolved lexically.
	nested := filepath.Join(dir, "sub", "..", "x")
	got = resolveRoot(nested)
	want = filepath.Join(dir, "x")
	if got != want {
		t.Fatalf("resolveRoot(%q) = %q, want %q", nested, got, want)
	}
}

func TestResolveRootDoesNotResolveSymlinks(t *testing.T) {
	t.Parallel()
	// SENTINEL: resolveRoot only does Abs+Clean — it deliberately has no
	// symlink awareness (EvalSymlinks lives in withinRoot, which resolves
	// both sides before the confinement check). A symlinked root therefore
	// stays the link path. If resolveRoot ever starts resolving symlinks,
	// this assertion fails, flagging the change.
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	if got := resolveRoot(link); got != filepath.Clean(link) {
		t.Fatalf("resolveRoot(symlink) = %q, want the unresolved link path %q", got, filepath.Clean(link))
	}
}

func TestResolveRootMissingPathStillResolves(t *testing.T) {
	t.Parallel()
	// resolveRoot performs no existence check; a nonexistent root still
	// resolves to a cleaned absolute path (validateRoot allows nonexistent
	// roots because several tools create the directory before indexing).
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if got := resolveRoot(missing); got != missing {
		t.Fatalf("resolveRoot(missing) = %q, want %q", got, missing)
	}
}

func TestWithinRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Normal relative path inside the root passes.
	p, err := withinRoot(root, filepath.Join("sub", "file.go"))
	if err != nil {
		t.Fatalf("within-root path rejected: %v", err)
	}
	if p != filepath.Join(root, "sub", "file.go") {
		t.Fatalf("resolved = %q", p)
	}

	// Nonexistent descendants are allowed (lexical fallback when the target
	// does not exist yet).
	if _, err := withinRoot(root, filepath.Join("new", "dir", "x.go")); err != nil {
		t.Fatalf("nonexistent descendant rejected: %v", err)
	}

	// `..` escape rejected.
	if _, err := withinRoot(root, ".."); err == nil {
		t.Fatal("`..` escape must be rejected")
	}
	if _, err := withinRoot(root, filepath.Join("..", "outside")); err == nil {
		t.Fatal("`../outside` escape must be rejected")
	}

	// Absolute path inside the root passes.
	inside := filepath.Join(root, "inside.go")
	if _, err := withinRoot(root, inside); err != nil {
		t.Fatalf("absolute path inside root rejected: %v", err)
	}

	// Absolute path outside the root rejected.
	outside := t.TempDir()
	if _, err := withinRoot(root, outside); err == nil {
		t.Fatal("absolute path outside root must be rejected")
	}

	// Symlink inside the root pointing outside rejected (EvalSymlinks).
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err == nil {
		if _, err := withinRoot(root, link); err == nil {
			t.Fatal("symlink escaping the root must be rejected")
		}
	}

	// Symlink outside pointing back into the root stays allowed.
	back := filepath.Join(outside, "back")
	if err := os.Symlink(root, back); err == nil {
		if _, err := withinRoot(root, back); err != nil {
			t.Fatalf("symlink into the root must be allowed: %v", err)
		}
	}
}

func TestValidateRoot(t *testing.T) {
	t.Parallel()
	// Filesystem root is never a project.
	if err := validateRoot("/"); err == nil {
		t.Fatal("filesystem root / must be rejected")
	}
	// Valid directory passes.
	dir := t.TempDir()
	if err := validateRoot(dir); err != nil {
		t.Fatalf("valid dir rejected: %v", err)
	}
	// A file (not a directory) is rejected.
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateRoot(f); err == nil {
		t.Fatal("non-directory root must be rejected")
	}
	// Nonexistent root is allowed by design (tools create it before indexing).
	if err := validateRoot(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatalf("nonexistent root should be allowed: %v", err)
	}
}

func TestIsFilesystemRoot(t *testing.T) {
	t.Parallel()
	if !isFilesystemRoot("/") {
		t.Fatal("`/` must be a filesystem root")
	}
	if isFilesystemRoot(t.TempDir()) {
		t.Fatal("a temp dir must not be a filesystem root")
	}
}

// TestArgBool pins the permissive arg-parsing contract shared by every
// tool handler that reads boolean flags (9 callers): missing/nil keys are
// false, real bools pass through, strings accept true/1 (trimmed), numbers
// are truthy on non-zero, and any other type is false.
func TestArgBool(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args map[string]any
		key  string
		want bool
	}{
		{name: "missing key", args: map[string]any{}, key: "verbose", want: false},
		{name: "nil value", args: map[string]any{"verbose": nil}, key: "verbose", want: false},
		{name: "bool true", args: map[string]any{"verbose": true}, key: "verbose", want: true},
		{name: "bool false", args: map[string]any{"verbose": false}, key: "verbose", want: false},
		{name: "string true", args: map[string]any{"verbose": "true"}, key: "verbose", want: true},
		{name: "string one", args: map[string]any{"verbose": "1"}, key: "verbose", want: true},
		{name: "string trimmed", args: map[string]any{"verbose": "  true  "}, key: "verbose", want: true},
		{name: "string false", args: map[string]any{"verbose": "false"}, key: "verbose", want: false},
		{name: "string zero", args: map[string]any{"verbose": "0"}, key: "verbose", want: false},
		{name: "number non-zero", args: map[string]any{"verbose": 1.0}, key: "verbose", want: true},
		{name: "number zero", args: map[string]any{"verbose": 0.0}, key: "verbose", want: false},
		{name: "unexpected type", args: map[string]any{"verbose": []string{"x"}}, key: "verbose", want: false},
		{name: "unexpected int", args: map[string]any{"verbose": 1}, key: "verbose", want: false},
	}
	for _, tc := range cases {
		if got := argBool(tc.args, tc.key); got != tc.want {
			t.Errorf("%s: argBool(%v, %q) = %v, want %v", tc.name, tc.args, tc.key, got, tc.want)
		}
	}
}

// TestValidateStringArgs pins the D6 string-argument coercion contract
// enforced at dispatch: scalar string-typed args accept string / JSON number
// / bool, while null, object and array values are rejected with an error
// naming the argument, the tool and the expected type. Undeclared keys and
// non-string-typed props pass through untouched; unknown tools validate
// nothing.
func TestValidateStringArgs(t *testing.T) {
	t.Parallel()
	// kern_search declares query/root as strings, limit as integer and
	// semantic as boolean (the non-string branches are pinned separately in
	// TestValidateArgTypesNonStringScalars).
	rejectCases := []struct {
		name string
		args map[string]any
		want string // substring expected in the error
	}{
		{name: "null", args: map[string]any{"query": nil}, want: `argument "query" for tool kern_search: expected a string, got null`},
		{name: "object", args: map[string]any{"query": map[string]any{}}, want: `argument "query" for tool kern_search: expected a string, got an object`},
		{name: "array any", args: map[string]any{"query": []any{"x"}}, want: `argument "query" for tool kern_search: expected a string, got an array`},
		{name: "array string", args: map[string]any{"query": []string{"x"}}, want: `argument "query" for tool kern_search: expected a string, got an array`},
		{name: "nested object", args: map[string]any{"query": map[string]any{"a": "b"}}, want: `argument "query" for tool kern_search: expected a string, got an object`},
		{name: "numeric root rejected", args: map[string]any{"query": "Greet", "root": 12345.0}, want: `argument "root" for tool kern_search: expected a path string, got a number`},
		{name: "int root rejected", args: map[string]any{"query": "Greet", "root": 12345}, want: `argument "root" for tool kern_search: expected a path string, got a number`},
	}
	for _, tc := range rejectCases {
		if err := validateArgTypes("kern_search", tc.args); err == nil {
			t.Errorf("%s: validateArgTypes(kern_search, %v) = nil, want error containing %q", tc.name, tc.args, tc.want)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not contain %q", tc.name, err, tc.want)
		}
	}

	acceptCases := []struct {
		name string
		args map[string]any
	}{
		{name: "plain string", args: map[string]any{"query": "Greet"}},
		{name: "JSON number coerced to string", args: map[string]any{"query": 12345.0}},
		{name: "Go-API int coerced to string", args: map[string]any{"query": 12345}},
		{name: "bool for strProp flag", args: map[string]any{"query": "Greet", "semantic": true}},
		{name: "string bool flag", args: map[string]any{"query": "Greet", "semantic": "true"}},
		{name: "absent optional", args: map[string]any{"query": "Greet"}},
		{name: "undeclared key untouched", args: map[string]any{"query": "Greet", "max_output": 100}},
	}
	for _, tc := range acceptCases {
		if err := validateArgTypes("kern_search", tc.args); err != nil {
			t.Errorf("%s: validateArgTypes(kern_search, %v) = %v, want nil", tc.name, tc.args, err)
		}
	}

	// Arguments not declared in the schema pass through untouched (their
	// handlers fail loud via atoiArg instead); object/array-typed schema
	// props also pass through, so the vehicle here is an undeclared arg.
	if err := validateArgTypes("kern_search", map[string]any{"zzz_undeclared": map[string]any{}}); err != nil {
		t.Errorf("undeclared prop with object value should pass through, got %v", err)
	}
	if err := validateArgTypes("kern_search", map[string]any{"zzz_undeclared2": []any{"x"}}); err != nil {
		t.Errorf("undeclared prop with array value should pass through, got %v", err)
	}

	// Unknown tool names validate nothing.
	if err := validateArgTypes("kern_no_such_tool", map[string]any{"query": map[string]any{}}); err != nil {
		t.Errorf("unknown tool should validate nothing, got %v", err)
	}
}

// TestValidateArgTypesNonStringScalars pins the D6 fail-loud contract for
// schema-declared integer/number/boolean properties (validateNonStringScalar
// via validateArgTypes): lenient forms (numbers, numeric strings, boolean
// strings, 0/1) pass; null, objects, arrays, non-numeric strings, floats
// where an integer is declared and non-0/1 numbers where a boolean is
// declared are rejected with errors naming the argument, the tool and the
// expected type — no silent defaults, no silent falses.
func TestValidateArgTypesNonStringScalars(t *testing.T) {
	t.Parallel()
	reject := []struct {
		tool string
		name string
		args map[string]any
		want string
	}{
		{tool: "kern_search", name: "integer object", args: map[string]any{"query": "Greet", "limit": map[string]any{}}, want: `argument "limit" for tool kern_search: expected an integer, got an object`},
		{tool: "kern_search", name: "integer null", args: map[string]any{"query": "Greet", "limit": nil}, want: `argument "limit" for tool kern_search: expected an integer, got null`},
		{tool: "kern_search", name: "integer array", args: map[string]any{"query": "Greet", "limit": []any{"3"}}, want: `argument "limit" for tool kern_search: expected an integer, got an array`},
		{tool: "kern_search", name: "integer non-numeric string", args: map[string]any{"query": "Greet", "limit": "abc"}, want: `argument "limit" for tool kern_search: expected an integer, got "abc"`},
		{tool: "kern_search", name: "integer float", args: map[string]any{"query": "Greet", "limit": 3.5}, want: `argument "limit" for tool kern_search: expected an integer, got 3.5`},
		{tool: "kern_search", name: "integer bool", args: map[string]any{"query": "Greet", "limit": true}, want: `argument "limit" for tool kern_search: expected an integer, got bool`},
		{tool: "kern_search", name: "boolean junk string", args: map[string]any{"query": "Greet", "semantic": "yes"}, want: `argument "semantic" for tool kern_search: expected a boolean, got "yes"`},
		{tool: "kern_search", name: "boolean number", args: map[string]any{"query": "Greet", "semantic": 42.0}, want: `argument "semantic" for tool kern_search: expected a boolean, got a number`},
		{tool: "kern_search", name: "boolean object", args: map[string]any{"query": "Greet", "semantic": map[string]any{}}, want: `argument "semantic" for tool kern_search: expected a boolean, got an object`},
		{tool: "kern_search", name: "boolean null", args: map[string]any{"query": "Greet", "semantic": nil}, want: `argument "semantic" for tool kern_search: expected a boolean, got null`},
		{tool: "kern_memory", name: "number junk string", args: map[string]any{"action": "ranked", "half_life_days": "fast"}, want: `argument "half_life_days" for tool kern_memory: expected a number, got "fast"`},
	}
	for _, tc := range reject {
		if err := validateArgTypes(tc.tool, tc.args); err == nil {
			t.Errorf("%s: validateArgTypes(%s, %v) = nil, want error containing %q", tc.name, tc.tool, tc.args, tc.want)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not contain %q", tc.name, err, tc.want)
		}
	}

	accept := []struct {
		name string
		args map[string]any
	}{
		{name: "integer JSON number", args: map[string]any{"query": "Greet", "limit": 3.0}},
		{name: "integer Go int", args: map[string]any{"query": "Greet", "limit": 3}},
		{name: "integer numeric string", args: map[string]any{"query": "Greet", "limit": "3"}},
		{name: "integer empty string is absent-equivalent", args: map[string]any{"query": "Greet", "limit": ""}},
		{name: "boolean false", args: map[string]any{"query": "Greet", "semantic": false}},
		{name: "boolean string false", args: map[string]any{"query": "Greet", "semantic": "false"}},
		{name: "boolean 0/1 numbers", args: map[string]any{"query": "Greet", "semantic": 1.0}},
		{name: "boolean empty string is absent-equivalent", args: map[string]any{"query": "Greet", "semantic": ""}},
	}
	for _, tc := range accept {
		if err := validateArgTypes("kern_search", tc.args); err != nil {
			t.Errorf("%s: validateArgTypes(kern_search, %v) = %v, want nil", tc.name, tc.args, err)
		}
	}
	// number-typed props (kern_memory half_life_days): floats and numeric
	// strings both pass.
	if err := validateArgTypes("kern_memory", map[string]any{"action": "ranked", "half_life_days": 7.5}); err != nil {
		t.Errorf("number float: validateArgTypes = %v, want nil", err)
	}
	if err := validateArgTypes("kern_memory", map[string]any{"action": "ranked", "half_life_days": "7.5"}); err != nil {
		t.Errorf("number numeric string: validateArgTypes = %v, want nil", err)
	}
}

// TestCapProvenanceSymbolsToFit pins the budget-fit trimmer. It lived in
// internal/mcp/provenance until that leaf crossed its LOC cap; the
// function operates only on exported provenance fields, so it moved to
// internal/mcp next to its single caller (the server budget machinery).
func TestCapProvenanceSymbolsToFit(t *testing.T) {
	syms := make([]provenance.SymbolProvenance, 128)
	for i := range syms {
		syms[i] = provenance.SymbolProvenance{
			Name:      fmt.Sprintf("Sym%03d", i),
			Qualified: fmt.Sprintf("pkg.Sym%03d", i),
			File:      "internal/pkg/file.go",
			Line:      i + 1,
		}
	}

	// A budget comfortably above the serialized payload leaves it untouched.
	p := &provenance.Provenance{SchemaVersion: provenance.SchemaVersion, Mode: provenance.ProvenanceModeRaw, Symbols: syms}
	full, _ := json.Marshal(p)
	capProvenanceSymbolsToFit(p, len(full)+1024)
	if len(p.Symbols) != 128 || p.SymbolsTruncated {
		t.Fatalf("large budget must not trim: %d symbols, truncated=%v", len(p.Symbols), p.SymbolsTruncated)
	}

	// A budget that cannot fit even one entry drops the listing entirely —
	// the fixed attestation header survives — and flags truncation. The
	// threshold is derived from the actual header encoding, so the assertion
	// holds for any future header shape.
	headerOnly := *p
	headerOnly.Symbols = nil
	headerOnly.SymbolsTruncated = true
	hb, _ := json.Marshal(headerOnly)
	tiny := len(hb) + 3 // header + array wrapper + one stray byte
	capProvenanceSymbolsToFit(p, tiny)
	if len(p.Symbols) != 0 || !p.SymbolsTruncated {
		t.Fatalf("tiny budget must empty the listing: %d symbols, truncated=%v", len(p.Symbols), p.SymbolsTruncated)
	}
	if b, _ := json.Marshal(p); len(b) > tiny {
		t.Fatalf("serialized provenance %d bytes exceeds tiny budget %d", len(b), tiny)
	}

	// A mid budget keeps a prefix, stays within budget after serialization,
	// and preserves entry order.
	p2 := &provenance.Provenance{SchemaVersion: provenance.SchemaVersion, Mode: provenance.ProvenanceModeRaw, Symbols: syms}
	capProvenanceSymbolsToFit(p2, 2000)
	b, _ := json.Marshal(p2)
	if len(b) > 2000 {
		t.Fatalf("serialized provenance %d bytes exceeds budget 2000", len(b))
	}
	if len(p2.Symbols) == 0 || !p2.SymbolsTruncated {
		t.Fatalf("mid budget should keep a prefix: %d symbols, truncated=%v", len(p2.Symbols), p2.SymbolsTruncated)
	}
	for i, s := range p2.Symbols {
		if s.Name != syms[i].Name {
			t.Fatalf("symbol %d reordered: %s", i, s.Name)
		}
	}

	// Nil-safety and the disabled budget (max_output=0) are no-ops.
	capProvenanceSymbolsToFit(nil, 100)
	capProvenanceSymbolsToFit(p2, 0)
}

// TestRootFallbackGuard pins the M#18 guarded cwd fallback (Phase 4.3):
// tools declaring a "root" argument serve root-less calls (or explicit cwd
// forms "", ".", "./") only when the fallback target is a plausible
// project; a scratch cwd fails with a correctable error naming the tool
// and the missing project. validateRootFallback is factored to take the
// cwd so the guard is testable without chdir (parallel tests resolve
// against the real process cwd).
func TestRootFallbackGuard(t *testing.T) {
	t.Parallel()
	props := map[string]any{"root": map[string]any{"type": "string"}}
	scratch := t.TempDir() // no .git, no .kern index

	// Omitted root in a scratch cwd: correctable error.
	if err := validateRootFallback("kern_search", props, nil, scratch); err == nil {
		t.Fatal("expected root-required error for omitted root in scratch cwd")
	}
	// Explicit cwd forms are the same fallback and must be guarded too.
	for _, v := range []string{"", ".", "./", "  .  "} {
		if err := validateRootFallback("kern_search", props, map[string]any{"root": v}, scratch); err == nil {
			t.Fatalf("expected error for root %q in scratch cwd", v)
		}
	}
	// An explicit project path never hits the guard (confinement gate's job).
	if err := validateRootFallback("kern_search", props, map[string]any{"root": "/some/project"}, scratch); err != nil {
		t.Fatalf("explicit root must not hit the guard: %v", err)
	}
	// A git checkout cwd serves the fallback (.git must be a real repo —
	// directory + HEAD file, LOW-7).
	git := t.TempDir()
	if err := os.MkdirAll(filepath.Join(git, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(git, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateRootFallback("kern_search", props, nil, git); err != nil {
		t.Fatalf("git cwd must serve: %v", err)
	}
	// An already-indexed kern workspace serves the fallback.
	idx := t.TempDir()
	if err := os.MkdirAll(filepath.Join(idx, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idx, ".kern", "index.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateRootFallback("kern_search", props, nil, idx); err != nil {
		t.Fatalf("indexed cwd must serve: %v", err)
	}
	// A server-created cache artifact alone (audit/D1 store init writes
	// .kern/index.sqlite into whatever cwd the server starts in) must NOT
	// make the dir plausible — otherwise one rejected call bootstraps the
	// fallback for the next (the Phase 4.3 live probe caught exactly this).
	sql := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sql, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sql, ".kern", "index.sqlite"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateRootFallback("kern_search", props, nil, sql); err == nil {
		t.Fatal("index.sqlite alone must not make a scratch cwd plausible")
	}
	// A tool that does not declare root is never guarded.
	if err := validateRootFallback("kern_mask_pii", map[string]any{"text": map[string]any{"type": "string"}}, nil, scratch); err != nil {
		t.Fatalf("tool without root prop must not be guarded: %v", err)
	}
	// The error is correctable: names the argument, the tool, and the fix.
	err := validateRootFallback("kern_search", props, nil, scratch)
	if err == nil || !strings.Contains(err.Error(), "root is required") || !strings.Contains(err.Error(), "kern_search") {
		t.Fatalf("error should name root, tool, and fix: %v", err)
	}
}
