package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAllowOnceRegistryLifecycle pins D4-D's core semantics: an entry is
// minted by AppendAllowOnce, consumed EXACTLY ONCE by ConsumeAllowOnce on
// the identical subject, refuses a different subject, and stays inert after
// consumption. Foreign/corrupt lines in the registry are preserved, never
// dropped by the consume rewrite.
func TestAllowOnceRegistryLifecycle(t *testing.T) {
	p := filepath.Join(t.TempDir(), "allow-once.jsonl")
	t.Setenv("KERN_GUARD_ALLOW_ONCE", p)

	subject := "grep -rn TODO internal/"
	if ConsumeAllowOnce(subject) {
		t.Fatal("consume on empty registry must be false")
	}
	if err := AppendAllowOnce(HashSubject(subject), "probe subject"); err != nil {
		t.Fatalf("append: %v", err)
	}
	// A different subject must not consume the entry.
	if ConsumeAllowOnce("cat /etc/passwd") {
		t.Fatal("different subject consumed the entry")
	}
	if !ConsumeAllowOnce(subject) {
		t.Fatal("identical subject with an unused entry must be allowed")
	}
	if ConsumeAllowOnce(subject) {
		t.Fatal("allowance must be single-use — second consume denied")
	}
}

// TestAllowOnceForeignLinesPreserved: the consume rewrite must carry
// unparseable lines through untouched (a hand-edited or truncated registry
// line is evidence, not garbage to silently drop).
func TestAllowOnceForeignLinesPreserved(t *testing.T) {
	p := filepath.Join(t.TempDir(), "allow-once.jsonl")
	t.Setenv("KERN_GUARD_ALLOW_ONCE", p)
	subject := "head -5 README.md"
	if err := AppendAllowOnce(HashSubject(subject), ""); err != nil {
		t.Fatalf("append: %v", err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	if _, err := f.WriteString("{not json\n"); err != nil {
		t.Fatalf("seed foreign line: %v", err)
	}
	f.Close()
	if !ConsumeAllowOnce(subject) {
		t.Fatal("consume must find the entry below the foreign line")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	if !strings.Contains(string(data), "{not json") {
		t.Fatalf("foreign line was dropped by the consume rewrite: %q", string(data))
	}
}
