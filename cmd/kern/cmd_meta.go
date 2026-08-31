package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp"
)

func runVersion(rest []string) {
	fmt.Printf("kern %s\n", version)
}

func runGuide(rest []string) {
	fmt.Println(mcp.Guide())
}

// runMeta implements the `kern meta` CLI subcommand — the CLI mirror of the
// kern_meta MCP meta-tool. It takes a natural-language request, classifies it
// (via the same internal classifier the MCP server uses), and dispatches to
// the appropriate CLI subcommand. This lets shell users get the same
// "describe what you want, kern picks the tool" experience as agents.
// Usage: kern meta "<request>" [--root DIR]
// Example: kern meta "show me the architecture"
// kern meta "how does dispatch work"
// kern meta "find the NewServer function"
func runMeta(rest []string) {
	var request string
	root := "."
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--root":
			i++
			if i < len(rest) {
				root = rest[i]
			}
		case "--help", "-h":
			fmt.Println(`kern meta "<request>" [--root DIR]

Single entry point: describe what you need in natural language and kern
classifies the request and runs the right tool internally. Examples:
  kern meta "show me the architecture"
  kern meta "how does dispatch work"
  kern meta "find the NewServer function"
  kern meta "what breaks if I change dispatch"
  kern meta "compress this log: ERROR: foo"`)
			return
		default:
			if request == "" {
				request = rest[i]
			} else {
				request += " " + rest[i]
			}
		}
	}
	if strings.TrimSpace(request) == "" {
		fmt.Fprintln(os.Stderr, `usage: kern meta "<request>" [--root DIR]
describe what you need and kern picks the right tool. Example:
  kern meta "show me the architecture"`)
		panic(exitError{code: 2})
	}

	// Re-dispatch via the MCP server's internal classifier. We build a
	// minimal args map and call the same handleMeta the MCP server uses, so
	// CLI and MCP stay perfectly in sync.
	srv := mcp.NewServer(os.Stdin, os.Stdout)
	args := map[string]any{"request": request, "root": root}
	out, err := srv.HandleMeta(context.Background(), args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kern meta:", err)
		panic(exitError{code: 1})
	}
	fmt.Println(out)
}
