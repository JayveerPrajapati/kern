package tokenize

import "testing"

func TestCountNonEmpty(t *testing.T) {
	if got := Count("hello world"); got <= 0 {
		t.Fatalf("expected positive count, got %d", got)
	}
}

func TestCountEmpty(t *testing.T) {
	if got := Count(""); got != 0 {
		t.Fatalf("expected 0 for empty, got %d", got)
	}
}

func TestCountMonotonic(t *testing.T) {
	short := Count(stringsRepeat("word ", 10))
	long := Count(stringsRepeat("word ", 100))
	if long <= short {
		t.Fatalf("expected longer text to have more tokens: short=%d long=%d", short, long)
	}
}

func TestKindDensity(t *testing.T) {
	code := `func main() { println("hello") }`
	if CountKind(code, KindCode) < CountKind(code, KindGeneric) {
		t.Fatalf("expected code to be denser than generic text")
	}
}

func stringsRepeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
