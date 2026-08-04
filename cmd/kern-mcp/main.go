// Command kern-mcp runs the kern MCP server over stdio.
package main

import (
	"os"

	"github.com/JayveerPrajapati/kern/internal/mcp"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/stats"
)

func main() {
	rec, err := stats.NewRecorder()
	if err == nil {
		optimize.Recorder = rec
	}
	srv := mcp.NewServer(os.Stdin, os.Stdout)
	if err := srv.Serve(); err != nil {
		os.Exit(1)
	}
}
