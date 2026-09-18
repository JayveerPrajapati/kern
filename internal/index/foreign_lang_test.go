package index

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
