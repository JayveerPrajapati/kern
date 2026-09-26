package tokenize

import (
	"testing"
)

// FuzzCountBPE asserts the byte-level BPE counter's contract over arbitrary
// input (including invalid UTF-8, NUL bytes, and pathological byte runs):
//
//   - Count must never panic (a panic fails the fuzz target);
//   - the token count is always in [0, len(s)]: every output token consumes
//     at least one input byte, so a count above len(s) would indicate a
//     counting/merge bug.
func FuzzCountBPE(f *testing.F) {
	b := NewBPECounter()
	seeds := []string{
		"",
		"hello world",
		`func main() { fmt.Println("hi") }`,
		"ERROR 2026-08-04T10:15:30Z [worker-3] failed",
		"The quick brown fox jumps over the lazy dog",
		"日本\n語のテキスト",
		"sk-1234567890abcdefghij",
		"\x00\x00\x00",
		"\xff\xfe\xfd",
		"aaaaaaaabbbbbbbbccccccccdddddddd",
		" \t\n\r ",
		"123 456 789 0123456789012345678901234567890123456789",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		n := b.Count(s)
		if n < 0 || n > len(s) {
			t.Fatalf("Count(%q) = %d (want 0 <= n <= %d)", s, n, len(s))
		}
	})
}
