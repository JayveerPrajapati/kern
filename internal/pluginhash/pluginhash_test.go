package pluginhash

import (
	"regexp"
	"testing"
)

func TestRegistryEntriesAreSHA256Hex(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for h := range shipped {
		if !re.MatchString(h) {
			t.Errorf("malformed registry entry %q", h)
		}
	}
}

func TestRegistryHasHistory(t *testing.T) {
	if len(shipped) < 10 {
		t.Fatalf("registry looks truncated: %d entries", len(shipped))
	}
}

func TestIsShipped(t *testing.T) {
	if IsShipped([]byte("not a shipped plugin")) {
		t.Fatal("arbitrary bytes must not be reported as shipped")
	}
	b := []byte("registered")
	shipped[Hash(b)] = true
	defer delete(shipped, Hash(b))
	if !IsShipped(b) {
		t.Fatal("registered bytes must be reported as shipped")
	}
}
