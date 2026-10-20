package clietag

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/etag"
	catalog "github.com/JayveerPrajapati/kern/internal/mcpcatalog"
)

// TestEtagFlagSet pins both spellings of the flag detection main() relies on
// for the NIT-11 warning.
func TestEtagFlagSet(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"--etag"}, true},
		{[]string{"--etag=abc"}, true},
		{[]string{"prompt", "--etag=abc"}, true},
		{[]string{"prompt", "--etags"}, false},
		{[]string{"--verbose"}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := EtagFlagSet(c.args); got != c.want {
			t.Errorf("EtagFlagSet(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}

// TestEtagResponseConsumed pins the consumed-signal contract: a non-empty
// flag marks the invocation as consumed (Consumed() true), an empty flag
// does not (NIT-11's "silently-ignored --etag" detection depends on it),
// and a matching flag returns the skip-output true.
func TestEtagResponseConsumed(t *testing.T) {
	consumed = false
	if EtagResponse("kern_context", "", "body") {
		t.Fatal("empty flag must not skip output")
	}
	if Consumed() {
		t.Fatal("empty flag must not mark consumed")
	}

	consumed = false
	if EtagResponse("kern_context", "stale", "body") {
		t.Fatal("non-matching flag must not skip output")
	}
	if !Consumed() {
		t.Fatal("non-empty flag must mark consumed")
	}

	// Matching flag: skip-output true, still consumed. Compute the fresh
	// etag the same way the production path does and feed it back.
	consumed = false
	fresh := etag.Hash("body", SchemaVersionForTool("kern_context"))
	if !EtagResponse("kern_context", fresh, "body") {
		t.Fatal("matching flag must skip output")
	}
	if !Consumed() {
		t.Fatal("matching flag must mark consumed")
	}
}

// TestSchemaVersionForTool pins the per-tool resolution: a catalog-listed
// tool resolves to its schema version; unknown tools fall back to
// SchemaVersionV1 (MEDIUM-3: CLI and MCP server must hash with the same
// per-tool version).
func TestSchemaVersionForTool(t *testing.T) {
	if got := SchemaVersionForTool("kern_context"); got == "" {
		t.Fatal("kern_context must resolve to a non-empty schema version")
	}
	if got := SchemaVersionForTool("definitely-not-a-tool"); got != catalog.SchemaVersionV1 {
		t.Fatalf("unknown tool: got %q, want SchemaVersionV1 (%q)", got, catalog.SchemaVersionV1)
	}
}

// TestConsumedFlagSharedInPackage verifies the flag is plain package state
// (no per-caller copies), which is the entire point of the extraction.
func TestConsumedFlagSharedInPackage(t *testing.T) {
	consumed = false
	defer func() { consumed = false }()
	if Consumed() {
		t.Fatal("reset flag must read false")
	}
	consumed = true
	if !Consumed() {
		t.Fatal("set flag must read true")
	}
}
