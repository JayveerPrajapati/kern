package index

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/langparse"
)

// pythonMultilineNestedDefs pins the regex-extractor fix: a nested def under a
// multi-line method signature must resolve its receiver to the enclosing
// METHOD, not the class. Before the fix bodyEndFor() ended the method at its
// closing `) -> None:` line, so receiver inference rejected the method and two
// same-named nested helpers collapsed onto Class.stream.
const pythonMultilineNestedDefs = `class TestExecLiveTailPatternFallback:
    async def test_client_side_filter_when_probe_fails(
        self, mock_ssh, profile
    ) -> None:
        async def stream(cmd, max_bytes):
            yield b"a\n"
        assert stream is not None

    async def test_server_side_filter_kept_when_probe_ok(
        self, mock_ssh, profile
    ) -> None:
        async def stream(cmd, max_bytes):
            yield b"b\n"
        assert stream is not None
`

func TestExtractForeignRegexPythonMultilineNestedDefs(t *testing.T) {
	syms, _, _, _, err := langparse.ExtractForeignRegex("test_repro.py", []byte(pythonMultilineNestedDefs), "python")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if err := checkFileSymbolConflicts("test_repro.py", syms); err != nil {
		t.Fatalf("same-named nested defs under different multi-line methods must not conflict: %v", err)
	}
	var streams []Symbol
	for _, s := range syms {
		if s.Name == "stream" {
			streams = append(streams, s)
		}
	}
	if len(streams) != 2 {
		t.Fatalf("got %d 'stream' symbols, want 2: %+v", len(streams), syms)
	}
	want := map[string]bool{
		"test_client_side_filter_when_probe_fails":   false,
		"test_server_side_filter_kept_when_probe_ok": false,
	}
	for _, s := range streams {
		if _, ok := want[s.Receiver]; !ok {
			t.Errorf("stream receiver = %q; want an enclosing method name", s.Receiver)
			continue
		}
		want[s.Receiver] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("no 'stream' resolved to enclosing method %q; symbols: %+v", name, syms)
		}
	}
}

// rustThreeFromImpls is three impl blocks for ONE type. They share (kind, name,
// receiver, params) and used to trip the fail-loud duplicate gate.
const rustThreeFromImpls = `pub enum Failure {
    Core,
    Engine,
    Io,
}

impl From<String> for Failure {
    fn from(_e: String) -> Self {
        Self::Core
    }
}

impl From<u8> for Failure {
    fn from(_e: u8) -> Self {
        Self::Engine
    }
}

impl From<usize> for Failure {
    fn from(_e: usize) -> Self {
        Self::Io
    }
}
`

func assertRustImplIdentity(t *testing.T, syms []Symbol) {
	t.Helper()
	if err := checkFileSymbolConflicts("repro.rs", syms); err != nil {
		t.Fatalf("several impls for one type must not conflict: %v", err)
	}
	var impls []Symbol
	var froms []Symbol
	for _, s := range syms {
		switch {
		case s.Kind == "impl":
			impls = append(impls, s)
		case s.Name == "from":
			froms = append(froms, s)
		}
	}
	if len(impls) != 3 {
		t.Fatalf("got %d impl symbols, want 3: %+v", len(impls), syms)
	}
	outers := map[string]bool{}
	for _, s := range impls {
		if s.Name != "Failure" {
			t.Errorf("impl name = %q; want the implemented type %q", s.Name, "Failure")
		}
		if s.Outer == "" {
			t.Errorf("impl %q has empty Outer; want the normalized header discriminator", s.Name)
		}
		outers[s.Outer] = true
	}
	if len(outers) != 3 {
		t.Errorf("impl Outer values = %v; want 3 distinct discriminators", outers)
	}
	if len(froms) != 3 {
		t.Fatalf("got %d 'from' methods, want 3: %+v", len(froms), syms)
	}
	for _, s := range froms {
		if s.Receiver != "Failure" {
			t.Errorf("from receiver = %q; want %q", s.Receiver, "Failure")
		}
	}
}

func TestExtractForeignRegexRustImplIdentity(t *testing.T) {
	syms, _, _, _, err := langparse.ExtractForeignRegex("repro.rs", []byte(rustThreeFromImpls), "rust")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	assertRustImplIdentity(t, syms)
}

// pythonTwoLevelNestedDefs is the shape that surfaced after the first fix: a
// helper nested two levels deep (`gen` inside `_factory` inside a test method).
// The regex extractor used the NEAREST enclosing function, so both `gen` defs
// keyed on `_factory` and collided; the tree-sitter extractor keys on the
// outermost function (the method).
const pythonTwoLevelNestedDefs = `class TestShell:
    async def test_a(
        self, profile
    ) -> None:
        async def _factory(pod):
            async def gen():
                yield "a"
            return gen()
        await run(_factory)

    async def test_b(
        self, profile
    ) -> None:
        async def _factory(pod):
            async def gen():
                yield "b"
            return gen()
        await run(_factory)
`

func TestExtractForeignRegexPythonTwoLevelNestedDefs(t *testing.T) {
	syms, _, _, _, err := langparse.ExtractForeignRegex("test_shell.py", []byte(pythonTwoLevelNestedDefs), "python")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if err := checkFileSymbolConflicts("test_shell.py", syms); err != nil {
		t.Fatalf("two-level nested defs under different methods must not conflict: %v", err)
	}
	var gens []Symbol
	for _, s := range syms {
		if s.Name == "gen" {
			gens = append(gens, s)
		}
	}
	if len(gens) != 2 {
		t.Fatalf("got %d 'gen' symbols, want 2: %+v", len(gens), syms)
	}
	seen := map[string]bool{}
	for _, s := range gens {
		seen[s.Receiver] = true
	}
	if !seen["test_a"] || !seen["test_b"] {
		t.Errorf("gen receivers = %v; want the outermost methods test_a and test_b", seen)
	}
}

// rustRawStringImpls embeds Rust source inside a raw string (the shape used by
// parser test fixtures). Raw-string bodies are data: the regex extractor must
// not lift `struct A` / `impl A` out of them.
const rustRawStringImpls = `struct A;

impl A {
    fn go(&self) {}
}

fn fixture() -> &'static str {
    r#"
struct A;
impl A {
    fn go(&self) {}
}
struct A;
impl A {
    fn go(&self) {}
}
"#
}
`

func TestExtractForeignRegexRustRawStringIgnored(t *testing.T) {
	syms, _, _, _, err := langparse.ExtractForeignRegex("fixture.rs", []byte(rustRawStringImpls), "rust")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if err := checkFileSymbolConflicts("fixture.rs", syms); err != nil {
		t.Fatalf("source embedded in a raw string must not be extracted: %v", err)
	}
	var structs, impls, gos int
	for _, s := range syms {
		switch {
		case s.Kind == "struct" && s.Name == "A":
			structs++
		case s.Kind == "impl":
			impls++
		case s.Name == "go":
			gos++
		}
	}
	if structs != 1 || impls != 1 || gos != 1 {
		t.Errorf("got structs=%d impls=%d gos=%d; want 1/1/1 (raw-string bodies ignored): %+v",
			structs, impls, gos, syms)
	}
}

// rustThreeSameHeaderImpls is the densic blocker (crates/densic-mcp/src/
// server.rs: three inherent `impl DensicServer {}` blocks). Multiple
// inherent impl blocks for one type are legal Rust, but their normalized
// headers — the discriminator — are IDENTICAL, so the discriminator alone
// cannot keep them apart: disambiguateSameHeaderImpls suffixes the repeats
// (#2, #3, ...) before the fail-loud duplicate gate judges the file.
const rustThreeSameHeaderImpls = `impl DensicServer {
    fn new() -> Self {
        Self { count: 0 }
    }
}

impl DensicServer {
    fn shutdown(&mut self) {
        drop(self.handle.take());
    }
}

impl DensicServer {
    fn ping(&self) -> bool {
        true
    }
}
`

func TestExtractForeignRegexRustSameHeaderImpls(t *testing.T) {
	syms, _, _, _, err := langparse.ExtractForeignRegex("server.rs", []byte(rustThreeSameHeaderImpls), "rust")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	disambiguateSameHeaderImpls(syms)
	if err := checkFileSymbolConflicts("server.rs", syms); err != nil {
		t.Fatalf("same-header inherent impls are legal Rust and must not conflict: %v", err)
	}
	var impls []Symbol
	for _, s := range syms {
		if s.Kind == "impl" {
			impls = append(impls, s)
		}
	}
	if len(impls) != 3 {
		t.Fatalf("got %d impl symbols, want 3: %+v", len(impls), syms)
	}
	want := map[string]bool{
		"impl DensicServer":   false,
		"impl DensicServer#2": false,
		"impl DensicServer#3": false,
	}
	for _, s := range impls {
		if _, ok := want[s.Outer]; !ok {
			t.Errorf("unexpected impl Outer %q; want one of %v", s.Outer, want)
			continue
		}
		want[s.Outer] = true
	}
	for o, seen := range want {
		if !seen {
			t.Errorf("missing impl Outer %q after suffixing; impls: %+v", o, impls)
		}
	}
}

func TestDisambiguateSameHeaderImplsOnlyTouchesImplKind(t *testing.T) {
	syms := []Symbol{
		{Kind: "impl", Name: "Foo", Outer: "impl Foo", Line: 1},
		{Kind: "impl", Name: "Foo", Outer: "impl Foo", Line: 10},
		{Kind: "impl", Name: "Bar", Outer: "impl Bar", Line: 20},
		{Kind: "impl", Name: "Bar", Outer: "impl Bar", Line: 30},
		{Kind: "impl", Name: "Bar", Outer: "impl Bar", Line: 40},
		{Kind: "struct", Name: "Baz", Outer: "", Line: 50},
		{Kind: "impl", Name: "Qux", Outer: "impl Qux for Zed", Line: 60},
	}
	disambiguateSameHeaderImpls(syms)
	if syms[0].Outer != "impl Foo" {
		t.Errorf("first impl must keep its header; got Outer %q", syms[0].Outer)
	}
	if syms[1].Outer != "impl Foo#2" {
		t.Errorf("second same-header impl must be suffixed; got Outer %q", syms[1].Outer)
	}
	if syms[3].Outer != "impl Bar#2" || syms[4].Outer != "impl Bar#3" {
		t.Errorf("Bar impls must be #2/#3; got %q / %q", syms[3].Outer, syms[4].Outer)
	}
	if syms[5].Outer != "" {
		t.Errorf("non-impl kinds must be untouched; got struct Outer %q", syms[5].Outer)
	}
	if syms[6].Outer != "impl Qux for Zed" {
		t.Errorf("singleton impl must be untouched; got Outer %q", syms[6].Outer)
	}
}
