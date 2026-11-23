package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestE2E_MCP_initialize_tools_list_tools_call exercises the exact transport
// sequence a real MCP client uses, not just the individual methods in
// isolation. It keeps the protocol layer honest even when every per-tool body
// already has its own e2e test.
func TestE2E_MCP_initialize_tools_list_tools_call(t *testing.T) {
	root := fixtureRoot(t)
	s := e2eDefaultServer(t, root)

	init := doHTTP(t, s, "POST", "application/json",
		`{"jsonrpc":"2.0","id":4001,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e-client","version":"1"}}}`,
		nil)
	var initResp struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(init.Body.Bytes(), &initResp); err != nil {
		t.Fatalf("decode initialize response: %v\n%s", err, init.Body.String())
	}
	if initResp.Result.ProtocolVersion != "2025-06-18" {
		t.Fatalf("initialize protocol version = %q", initResp.Result.ProtocolVersion)
	}
	if !strings.Contains(initResp.Result.ServerInfo.Name, "kern") {
		t.Fatalf("initialize serverInfo does not identify kern: %+v", initResp.Result.ServerInfo)
	}

	list := doHTTP(t, s, "POST", "application/json",
		`{"jsonrpc":"2.0","id":4002,"method":"tools/list"}`, nil)
	var listResp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode tools/list response: %v\n%s", err, list.Body.String())
	}
	found := false
	for _, tool := range listResp.Result.Tools {
		if tool.Name == "kern_search" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("tools/list did not advertise kern_search")
	}

	call := e2eCall(t, s, 4003, "kern_search", map[string]any{
		"root":  root,
		"query": "NewServer",
		"limit": 5,
	})
	assertE2EContract(t, "mcp-protocol/tools-call", call, e2eContract{
		Success:        true,
		MustContain:    []string{"NewServer"},
		WantProvenance: true,
	})
}
