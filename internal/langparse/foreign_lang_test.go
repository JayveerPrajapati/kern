package langparse

import (
	"strconv"
	"strings"
	"testing"
)

// TestCalleeLongQualifiedNameNotTruncated: a cross-package qualified callee
// longer than the old 80-char cut must round-trip intact. The calls column is
// TEXT (unbounded in SQLite/JSON/gob stores), so truncating long qualified
// names silently unlinked graph edges — the recorded target matched nothing
// on the callee side.
func TestCalleeLongQualifiedNameNotTruncated(t *testing.T) {
	// Build a ~300-char qualified name from dot-separated identifiers
	// (callRe-valid: `[A-Za-z_$][A-Za-z0-9_$]*` joined by dots).
	var segs []string
	n := 0
	for i := 0; n < 300; i++ {
		seg := "seg" + strconv.Itoa(i)
		if n+len(seg)+1 > 300 {
			break
		}
		segs = append(segs, seg)
		n += len(seg) + 1
	}
	qualified := strings.Join(segs, ".")
	if len(qualified) < 295 || len(qualified) > 300 {
		t.Fatalf("test qualified name is %d chars, want ~300", len(qualified))
	}

	line := qualified + "();"
	f := &ffile{
		lines: []string{line},
		clean: []string{line},
		com:   []bool{false},
	}
	calls := map[string][]CallEdge{}
	scanCallsInner(f, 0, "owner", calls, &langSpec{}, nil)

	got := calls["owner"]
	if len(got) != 1 {
		t.Fatalf("expected 1 call edge, got %d", len(got))
	}
	if got[0].Target != qualified {
		t.Errorf("callee truncated: got %d chars %q, want the full %d-char qualified name", len(got[0].Target), got[0].Target, len(qualified))
	}
}

// TestRegexPythonNestedDefsDistinctIdentity pins the WD-3 fix on the regex
// path (-tags notreesitter builds): two same-named helpers nested in
// DIFFERENT test methods must carry distinct receivers, not the shared
// class receiver that made them one identity and blocked the index.
func TestRegexPythonNestedDefsDistinctIdentity(t *testing.T) {
	src := `class TestStreams:
    def test_one(self):
        def _helper():
            return 1
        return _helper()

    def test_two(self):
        def _helper():
            return 2
        return _helper()
`
	syms, _, _, _, err := ExtractForeignRegex("test_streams.py", []byte(src), "python")
	if err != nil {
		t.Fatal(err)
	}
	recv := map[string]bool{}
	for _, s := range syms {
		if s.Name == "_helper" {
			recv[s.Receiver] = true
		}
	}
	if !recv["test_one"] || !recv["test_two"] {
		t.Errorf("nested same-named helpers must carry their enclosing method receivers (test_one, test_two), got %v", recv)
	}
}

// TestRegexPythonOverloadStubsSkipped pins the @overload fix on the regex
// path (-tags notreesitter builds): typing @overload stubs must be skipped so
// the real implementation owns the identity — parity with the tree-sitter
// extractor. Before the fix, stub and impl shared one identity and the
// fail-loud duplicate gate refused the whole file (live repro: psf/requests
// src/requests/auth.py lines 92/94). Stacked decorators (@staticmethod
// combined with @overload, in both orders) must still be detected, and
// genuine duplicates (same identity, NO @overload) must NOT be masked: the
// extractor keeps emitting both defs so the gate can fail loud.
func TestRegexPythonOverloadStubsSkipped(t *testing.T) {
	src := `from typing import overload

class A:
    @overload
    def __init__(self, x: int) -> None: ...
    @overload
    def __init__(self, x: str) -> None: ...
    def __init__(self, x):
        self.x = x

class B:
    @staticmethod
    @overload
    def parse(x: int) -> None: ...
    @overload
    @staticmethod
    def parse(x: str) -> None: ...
    @staticmethod
    def parse(x):
        return x
`
	syms, _, _, _, err := ExtractForeignRegex("repro.py", []byte(src), "python")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Symbol{}
	for _, s := range syms {
		byName[s.Name+"\x00"+s.Receiver] = s
	}
	init, ok := byName["__init__\x00A"]
	if !ok {
		t.Fatalf("expected A.__init__ impl symbol, got %+v", syms)
	}
	if init.Line != 8 {
		t.Errorf("A.__init__ line = %d; want the IMPL line 8 (stubs at 4/6 skipped), syms: %+v", init.Line, syms)
	}
	parse, ok := byName["parse\x00B"]
	if !ok {
		t.Fatalf("expected B.parse impl symbol, got %+v", syms)
	}
	if parse.Line != 19 {
		t.Errorf("B.parse line = %d; want the IMPL line 19 (stacked-decorator stubs at 13/16 skipped), syms: %+v", parse.Line, syms)
	}
	// Genuine duplicates must NOT be suppressed: both defs are emitted and
	// the fail-loud duplicate gate (internal/index) still trips on them.
	dup := `class C:
    def run(self): ...
    def run(self): ...
`
	dsyms, _, _, _, err := ExtractForeignRegex("dup.py", []byte(dup), "python")
	if err != nil {
		t.Fatal(err)
	}
	runs := 0
	for _, s := range dsyms {
		if s.Name == "run" {
			runs++
		}
	}
	if runs != 2 {
		t.Errorf("genuine duplicate defs must BOTH be emitted (extractor must not mask them for the gate), got %d run symbols: %+v", runs, dsyms)
	}
}
