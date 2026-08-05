// Command kern-mcp runs the kern MCP server over stdio (default) or HTTP
// (--http ADDR). The HTTP transport is Streamable HTTP style: POST JSON-RPC
// messages to /mcp and read the response body.
package main

import (
	"flag"
	"os"

	"github.com/JayveerPrajapati/kern/internal/mcp"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/stats"
)

func main() {
	httpAddr := flag.String("http", "", "serve MCP over HTTP on this address (e.g. :8080) instead of stdio")
	flag.Parse()
	rec, err := stats.NewRecorder()
	if err == nil {
		optimize.Recorder = rec
	}
	if *httpAddr != "" {
		if err := mcp.ServeHTTP(*httpAddr); err != nil {
			os.Exit(1)
		}
		return
	}
	srv := mcp.NewServer(os.Stdin, os.Stdout)
	if err := srv.Serve(); err != nil {
		os.Exit(1)
	}
}
