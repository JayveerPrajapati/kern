package mcp

import (
	"encoding/json"
	"testing"
)

// mcpCallSpec is one tools/call request for mcpBatch: a tool name and its
// arguments (nil means no arguments).
type mcpCallSpec struct {
	name string
	args map[string]any
}

// mcpBatch runs several tools/call requests sequentially through ONE Server
// and returns the decoded responses in request order. Sharing one server
// across a batch lets index-backed tools reuse the session's cached index —
// one index build per root instead of one build per call, which is the
// dominant cost in the dispatch-sweep tests (each fresh-server call rebuilds
// the root index from scratch). Sequential execution preserves the exact call
// order of the original fresh-server harness, so tools that mutate the root
// (e.g. kern_rename apply) or rely on prior calls' effects behave identically.
//
// Requests use integer ids (starting at 1) so serveSequential's
// waitForResponse can match them: id 0 would collide with id-less progress
// notifications, which unmarshal to 0. Slow tools that emit progress
// notifications are handled the same way serveSequential already handles
// them (notifications are skipped).
func mcpBatch(t *testing.T, calls []mcpCallSpec) []map[string]any {
	t.Helper()
	reqs := make([]string, len(calls))
	for i, c := range calls {
		pa, err := json.Marshal(c.args)
		if err != nil {
			t.Fatalf("marshal args for %s: %v", c.name, err)
		}
		reqs[i] = writeReq("tools/call", i+1, `{"name":"`+c.name+`","arguments":`+string(pa)+`}`)
	}
	return serveSequential(t, reqs...)
}
