package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolveRoot / withinRoot / validateRoot are the root-confinement primitives
// that guard index-using tools. This file tests them directly (package mcp =
// internal test package) so the unexported functions are exercised without
// going through tool dispatch.

func TestResolveRootEmptyFallsBackToCwd(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := resolveRoot(""); got != cwd {
		t.Fatalf("resolveRoot(\"\") = %q, want cwd %q", got, cwd)
	}
}

func TestResolveRootCleansToAbsolute(t *testing.T) {
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
	// resolveRoot performs no existence check; a nonexistent root still
	// resolves to a cleaned absolute path (validateRoot allows nonexistent
	// roots because several tools create the directory before indexing).
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if got := resolveRoot(missing); got != missing {
		t.Fatalf("resolveRoot(missing) = %q, want %q", got, missing)
	}
}

func TestWithinRoot(t *testing.T) {
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
	// kern_search declares query/root/limit/semantic as strings.
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
	}
	for _, tc := range rejectCases {
		if err := validateStringArgs("kern_search", tc.args); err == nil {
			t.Errorf("%s: validateStringArgs(kern_search, %v) = nil, want error containing %q", tc.name, tc.args, tc.want)
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
		if err := validateStringArgs("kern_search", tc.args); err != nil {
			t.Errorf("%s: validateStringArgs(kern_search, %v) = %v, want nil", tc.name, tc.args, err)
		}
	}

	// Non-string-typed props are out of scope: kern_retrieve's integer/boolean
	// props must pass through even with non-scalar values (their handlers fail
	// loud via atoiArg instead).
	if err := validateStringArgs("kern_retrieve", map[string]any{"limit": map[string]any{}}); err != nil {
		t.Errorf("non-string prop with object value should pass through, got %v", err)
	}
	if err := validateStringArgs("kern_retrieve", map[string]any{"with_freshness": []any{"x"}}); err != nil {
		t.Errorf("non-string prop with array value should pass through, got %v", err)
	}

	// Unknown tool names validate nothing.
	if err := validateStringArgs("kern_no_such_tool", map[string]any{"query": map[string]any{}}); err != nil {
		t.Errorf("unknown tool should validate nothing, got %v", err)
	}
}
