package mcpargs

import (
	"errors"
	"testing"
)

// The mcp handler suite exercises these helpers indirectly through every
// adapter; these tests pin the leaf's own lenient-parsing contract directly
// — the semantics extracted handler families depend on.

func TestArgString(t *testing.T) {
	args := map[string]any{
		"plain":  "value",
		"num":    42,
		"pad":    "  spaced  ",
		"nil":    nil,
		"absent": "x", // key deleted below
	}
	delete(args, "absent")
	if got := ArgString(args, "plain"); got != "value" {
		t.Fatalf("plain = %q", got)
	}
	if got := ArgString(args, "num"); got != "42" {
		t.Fatalf("num coerced = %q, want 42", got)
	}
	if got := ArgString(args, "pad"); got != "spaced" {
		t.Fatalf("trimmed = %q, want spaced", got)
	}
	if got := ArgString(args, "nil"); got != "" {
		t.Fatalf("nil = %q, want empty", got)
	}
	if got := ArgString(args, "absent"); got != "" {
		t.Fatalf("missing = %q, want empty", got)
	}
}

func TestArgBool(t *testing.T) {
	args := map[string]any{
		"native":  true,
		"strTrue": "true",
		"strOne":  "1",
		"strNo":   "yes",
		"float1":  1.0,
		"float0":  0.0,
		"nil":     nil,
	}
	for key, want := range map[string]bool{
		"native": true, "strTrue": true, "strOne": true, "strNo": false,
		"float1": true, "float0": false, "nil": false, "missing": false,
	} {
		if got := ArgBool(args, key); got != want {
			t.Fatalf("ArgBool(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestAtoiArg(t *testing.T) {
	if n, err := AtoiArg("", 20); err != nil || n != 20 {
		t.Fatalf("empty fallback = (%d, %v), want (20, nil)", n, err)
	}
	if n, err := AtoiArg("7", 20); err != nil || n != 7 {
		t.Fatalf("valid = (%d, %v), want (7, nil)", n, err)
	}
	if _, err := AtoiArg("seven", 20); err == nil {
		t.Fatal("malformed must be an error, not a silent default")
	} else if !errors.Is(err, err) {
		t.Fatal("unreachable")
	}
}

func TestArgStrings(t *testing.T) {
	args := map[string]any{
		"native": []string{"a", " b ", ""},
		"any":    []any{"x", 7, nil},
		"string": "a, b\nc",
		"empty":  " , ,",
	}
	if got := ArgStrings(args, "native"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("native = %#v, want [a b] (trimmed, empties dropped)", got)
	}
	// A nil element inside []any stringifies to "<nil>" and survives — the
	// current lenient behavior (JSON null in an array), pinned as-is.
	if got := ArgStrings(args, "any"); len(got) != 3 || got[0] != "x" || got[1] != "7" || got[2] != "<nil>" {
		t.Fatalf("any = %#v, want [x 7 <nil>]", got)
	}
	if got := ArgStrings(args, "string"); len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("string = %#v, want [a b c]", got)
	}
	if got := ArgStrings(args, "empty"); got != nil && len(got) != 0 {
		t.Fatalf("empty = %#v, want nil/empty", got)
	}
	if got := ArgStrings(args, "missing"); got != nil {
		t.Fatalf("missing = %#v, want nil", got)
	}
}

func TestArgAliases(t *testing.T) {
	args := map[string]any{
		"symbolName": "MyFunc",
		"file_list":  "a.go, b.go",
		"count":      "15",
		"limit":      100.0,
	}
	if got := ArgStringWithAliases(args, "symbol", "symbolName", "name"); got != "MyFunc" {
		t.Fatalf("ArgStringWithAliases = %q, want MyFunc", got)
	}
	if got := ArgStringWithAliases(args, "nonexistent", "other"); got != "" {
		t.Fatalf("ArgStringWithAliases absent = %q, want empty", got)
	}
	if got := ArgStringsWithAliases(args, "files", "file_list"); len(got) != 2 || got[0] != "a.go" {
		t.Fatalf("ArgStringsWithAliases = %v, want [a.go b.go]", got)
	}
	if n, err := ArgInt(args, "count", 5); err != nil || n != 15 {
		t.Fatalf("ArgInt string = (%d, %v), want (15, nil)", n, err)
	}
	if n, err := ArgInt(args, "limit", 10); err != nil || n != 100 {
		t.Fatalf("ArgInt float64 = (%d, %v), want (100, nil)", n, err)
	}
	if n, err := ArgInt(args, "missing", 42); err != nil || n != 42 {
		t.Fatalf("ArgInt default = (%d, %v), want (42, nil)", n, err)
	}
}
