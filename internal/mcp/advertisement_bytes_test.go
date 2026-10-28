package mcp

import (
	"encoding/json"
	"testing"

	mcpcatalog "github.com/JayveerPrajapati/kern/internal/mcpcatalog"
)

// maxToolsListBytes pins the serialized size of the default-surface tools/list
// response. Every MCP client (and every subagent) pays this payload at session
// start, before any tool is called; the catalog description token gate covers
// the text but not the wrapper/schema bytes on the wire. Measured
// 2026-10-27 at 36,695 bytes after the P4.1 wording diet (was 38,343);
// the cap is measurement + ~3.5% headroom.
const maxToolsListBytes = 38000

func TestToolsListAdvertisedBytes(t *testing.T) {
	resp := serveOne(t, writeReq("tools/list", 99, ""))
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("default tools/list response: %d bytes", len(raw))
	// Composition breakdown so the next trim is aimed, not guessed.
	descBytes, schemaBytes := 0, 0
	for _, tool := range mcpcatalog.All {
		descBytes += len(tool.Description)
		if b, merr := json.Marshal(tool.InputSchema); merr == nil {
			schemaBytes += len(b)
		}
	}
	t.Logf("composition: %d tool descriptions (%d avg), %d schema bytes (%d avg)",
		descBytes, descBytes/len(mcpcatalog.All), schemaBytes, schemaBytes/len(mcpcatalog.All))
	if maxToolsListBytes > 0 && len(raw) > maxToolsListBytes {
		t.Errorf("tools/list = %d bytes, exceeds cap %d — trim schema/parameter prose", len(raw), maxToolsListBytes)
	}
}
