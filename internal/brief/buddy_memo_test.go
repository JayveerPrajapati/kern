package brief

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

// NEW-4 (Q3=A): the default digest body is memoized per root, invalidated
// by cheap stat probes of the stores it reads. These tests pin the
// coherence contract: warm repeats serve the memo, and a memory write
// (the store the probe watches) refreshes the digest.
func buddyMemoDigest(t *testing.T, noMemory bool) string {
	t.Helper()
	out, err := BuildWithOptions(t.TempDir(), Options{NoMemory: noMemory})
	if err != nil {
		t.Fatalf("BuildWithOptions: %v", err)
	}
	return out
}

func digestBody(s string) string {
	// Drop the two header lines (root + Generated timestamp): the header
	// is rendered per call and would break a byte-comparison across a
	// second boundary.
	for i := 0; i < 2; i++ {
		if j := strings.IndexByte(s, '\n'); j >= 0 {
			s = s[j+1:]
		}
	}
	return s
}

func TestBuddyMemoServesWarmRepeats(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	first, err := BuildWithOptions(root, Options{})
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	second, err := BuildWithOptions(root, Options{})
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if digestBody(first) != digestBody(second) {
		t.Fatal("warm repeat changed the digest body; memo did not serve")
	}
}

func TestBuddyMemoRefreshesOnMemoryWrite(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	first, err := BuildWithOptions(root, Options{})
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	if strings.Contains(first, "memo-refresh-marker") {
		t.Fatal("precondition: marker unexpectedly present")
	}
	if _, err := memory.AddExplicit(root, "memo-refresh-marker lesson for buddy digest"); err != nil {
		t.Fatalf("AddExplicit: %v", err)
	}
	second, err := BuildWithOptions(root, Options{})
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if !strings.Contains(second, "memo-refresh-marker") {
		t.Fatal("digest body not refreshed after a memory write; probe missed the v1 store")
	}
}
