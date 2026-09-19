package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
)

var (
	streamMu           sync.Mutex
	registeredChannels = map[string]string{
		"graph_walk":    "Streaming call graph and AST traversal nodes",
		"file_diff":     "Streaming file-by-file patch blocks",
		"log_stream":    "Real-time log optimization and incident triage chunks",
		"tool_progress": "Progress bar percentage and status messages for long pipelines",
	}
	recentEvents = []map[string]any{}
)

// ChunkDescriptor holds metadata for a single payload chunk.
type ChunkDescriptor struct {
	Index int    `json:"index"`
	Total int    `json:"total"`
	Size  int    `json:"size"`
	Data  string `json:"data"`
}

// Handle executes streaming and chunking actions.
func Handle(ctx context.Context, transport string, args map[string]any) (string, error) {
	action := strings.ToLower(strings.TrimSpace(mcpargs.ArgString(args, "action")))
	if action == "" {
		action = "status"
	}

	streamMu.Lock()
	defer streamMu.Unlock()

	format := strings.ToLower(mcpargs.ArgString(args, "format"))

	switch action {
	case "channels":
		if format == "json" {
			data, _ := json.MarshalIndent(registeredChannels, "", "  ")
			return string(data), nil
		}
		var sb strings.Builder
		sb.WriteString("## Registered Streaming Channels\n\n")
		for ch, desc := range registeredChannels {
			sb.WriteString(fmt.Sprintf("- **`%s`**: %s\n", ch, desc))
		}
		return sb.String(), nil

	case "chunk":
		payload := mcpargs.ArgString(args, "payload")
		if payload == "" {
			return "", fmt.Errorf("kern_stream: 'payload' is required to chunk")
		}
		chunkSize := 1000
		if csStr := mcpargs.ArgString(args, "chunk_size"); csStr != "" {
			if n, err := strconv.Atoi(csStr); err == nil && n > 0 {
				chunkSize = n
			}
		} else if n, ok := args["chunk_size"].(float64); ok && n > 0 {
			chunkSize = int(n)
		}

		var chunks []string
		runes := []rune(payload)
		for i := 0; i < len(runes); i += chunkSize {
			end := i + chunkSize
			if end > len(runes) {
				end = len(runes)
			}
			chunks = append(chunks, string(runes[i:end]))
		}

		desc := make([]ChunkDescriptor, len(chunks))
		for i, c := range chunks {
			desc[i] = ChunkDescriptor{
				Index: i + 1,
				Total: len(chunks),
				Size:  len(c),
				Data:  c,
			}
		}

		// D4: compact text summary by default; full chunk descriptors behind format=json.
		if strings.ToLower(mcpargs.ArgString(args, "format")) == "json" {
			data, _ := json.MarshalIndent(map[string]any{
				"total_chunks": len(chunks),
				"total_chars":  len(runes),
				"chunk_size":   chunkSize,
				"chunks":       desc,
			}, "", "  ")
			return string(data), nil
		}
		return fmt.Sprintf("sent %d chunks (%d chars, chunk size %d)", len(chunks), len(runes), chunkSize), nil

	case "emit":
		channel := mcpargs.ArgString(args, "channel")
		progressToken := mcpargs.ArgString(args, "progress_token")
		message := mcpargs.ArgString(args, "message")
		percent := 0
		if pStr := mcpargs.ArgString(args, "percent"); pStr != "" {
			if n, err := strconv.Atoi(pStr); err == nil {
				percent = n
			}
		} else if n, ok := args["percent"].(float64); ok {
			percent = int(n)
		}

		evt := map[string]any{
			"channel":        channel,
			"progress_token": progressToken,
			"percent":        percent,
			"message":        message,
			"timestamp":      time.Now().UTC().Format(time.RFC3339),
		}
		recentEvents = append(recentEvents, evt)
		if len(recentEvents) > 50 {
			recentEvents = recentEvents[len(recentEvents)-50:]
		}

		if format == "json" {
			data, _ := json.MarshalIndent(evt, "", "  ")
			return string(data), nil
		}
		return fmt.Sprintf("Stream event published to channel %q (%d%% - %s)", channel, percent, message), nil

	case "status":
		fallthrough
	default:
		if format == "json" {
			data, _ := json.MarshalIndent(map[string]any{
				"transport":     transport,
				"channels":      registeredChannels,
				"recent_events": len(recentEvents),
			}, "", "  ")
			return string(data), nil
		}

		var sb strings.Builder
		sb.WriteString("## Streaming & Progress Transport Status\n\n")
		sb.WriteString(fmt.Sprintf("- **Transport Type**: `%s`\n", transport))
		sb.WriteString(fmt.Sprintf("- **Registered Channels**: %d\n", len(registeredChannels)))
		sb.WriteString(fmt.Sprintf("- **Recent Events Emitted**: %d\n", len(recentEvents)))
		sb.WriteString("- **Supported Capabilities**: `notifications/progress`, `chunked_transport`, `jsonrpc_batch`\n")

		return sb.String(), nil
	}
}
