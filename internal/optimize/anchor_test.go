package optimize

import (
	"strings"
	"testing"
)

func TestAnchorStoreAndFetch(t *testing.T) {
	ClearAnchors()

	rawText := "raw uncompressed stack trace line 1\nline 2\nline 3"
	id := StoreAnchor(rawText)
	if id == "" || !strings.HasPrefix(id, "anchor-") {
		t.Fatalf("expected valid anchor ID, got: %q", id)
	}

	fetched, err := FetchAnchor(id)
	if err != nil {
		t.Fatalf("FetchAnchor failed: %v", err)
	}
	if fetched != rawText {
		t.Errorf("fetched text mismatch:\ngot: %q\nwant: %q", fetched, rawText)
	}

	// Missing anchor returns error
	if _, err := FetchAnchor("anchor-nonexistent"); err == nil {
		t.Error("expected error fetching nonexistent anchor, got nil")
	}
}
