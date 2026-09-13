// Command echo-server is a minimal MCP stdio server used by the mcpclient
// tests: it speaks newline-delimited JSON-RPC over stdin/stdout, answers the
// initialize handshake, exposes one "echo" tool, and echoes the text argument.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			respond(req.ID, map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "echo", "version": "1"},
			})
		case "notifications/initialized":
			// handshake acknowledged; nothing to do
		case "tools/list":
			respond(req.ID, map[string]any{"tools": []map[string]any{{
				"name":        "echo",
				"description": "Echoes the text argument back.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{"type": "string"},
					},
				},
			}}})
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			text, _ := p.Arguments["text"].(string)
			respond(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"reply":   text,
			})
		}
	}
}

func respond(id int, result any) {
	resp := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
	b, _ := json.Marshal(resp)
	fmt.Println(string(b))
}
