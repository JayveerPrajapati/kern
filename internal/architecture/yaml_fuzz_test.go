package architecture

import (
	"strings"
	"testing"
)

// FuzzParseYAML asserts the fail-closed contract of the hand-rolled,
// stdlib-only YAML decoder that consumes untrusted .kern/architecture.yaml:
//
//   - it must never panic (a panic fails the fuzz target), even on deeply
//     nested flow lists / block nesting, invalid UTF-8, or NUL bytes;
//   - anything outside the fixed schema is a clean error, never a crash;
//   - on success the returned tree is made only of the value shapes the
//     decoder is allowed to emit (nil, bool, string, int64, []any,
//     map[string]any) — any other type is a decoder bug.
func FuzzParseYAML(f *testing.F) {
	seeds := []string{
		"",
		"\n",
		"# comment only\n",
		"a: b",
		"version: \"1\"",
		"rules:\n- id: r1\n  from: web\n  to: db\n  action: forbid\n",
		"- a\n- b\n- c\n",
		"- key: value\n  other: 2\n",
		"a:\n  b:\n    c:\n      d: 1\n",
		"[a, b, c]",
		"[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[1]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]",
		"key: \"\\n\\t\\r\\\"\\\\\"",
		"key: 'single quoted'",
		"key: true\nkey2: false\nkey3: null\nkey4: ~\nkey5: 12345\n",
		"- - - nested\n",
		"a: [1, [2, [3, [4]]]]",
		"a:",
		"- ",
		"a: b\n c: d\n",
		"a: b\n\tb: c\n",
		"trailing garbage: ]]]]",
		"unterminated: [1, 2",
		"a:\n - b\n  - c\n",
		"nested: \"value\"\n",
		"\x00\x00\x00",
		"\xff\xfe\xfd invalid utf8",
	}
	// Deep block nesting via indentation (70 levels) — exercises the
	// maxFlowDepth recursion guard on the block path.
	deep := ""
	for i := 0; i < 70; i++ {
		deep += strings.Repeat("  ", i) + "k" + string(rune('0'+i%10)) + ":\n"
	}
	seeds = append(seeds, deep, "a: "+deep)
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := parseYAML(data)
		if err != nil {
			// Fail-closed contract: garbage is a clean error, never a panic
			// (a panic would fail the fuzz target itself).
			return
		}
		// Success contract: the tree must be made only of the value shapes
		// the decoder emits; anything else is a decoder bug.
		assertYAMLValue(t, v)
	})
}

// assertYAMLValue walks a decoded YAML tree asserting every node is one of
// the value types parseYAML is allowed to emit.
func assertYAMLValue(t *testing.T, v any) {
	t.Helper()
	switch x := v.(type) {
	case nil, bool, string, int64, float64:
		return
	case []any:
		for _, e := range x {
			assertYAMLValue(t, e)
		}
	case map[string]any:
		for _, e := range x {
			assertYAMLValue(t, e)
		}
	default:
		t.Fatalf("parseYAML produced unexpected value type %T", v)
	}
}
