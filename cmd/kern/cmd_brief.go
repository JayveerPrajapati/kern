// Command kern brief prints the repo onboarding brief: project map, index
// summary, hub symbols, entry points, architecture and recent kern savings.
// It is the CLI twin of `kern buddy` — internal/brief.Build renders the
// digest. Build handles index loading internally (index.Load) and reports
// cold-index sections with a hint instead of failing.

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/brief"
)

// runBrief implements `kern brief [root]`: builds and prints the repo brief
// for root (default "."). A cold index produces the brief minus the
// index/architecture sections plus a hint to run `kern index .` / `kern
// precache .` once.
func runBrief(rest []string) {
	root := "."
	for i := 0; i < len(rest); i++ {
		switch {
		case rest[i] == "--root" || rest[i] == "-r":
			i++
			if i < len(rest) {
				root = rest[i]
			}
		case strings.HasPrefix(rest[i], "-"):
			fatalUsage("brief: unknown flag %q\nusage: kern brief [root]", rest[i])
		default:
			root = rest[i]
		}
	}
	if _, err := os.Stat(root); err != nil {
		fatal("brief: %v", err)
	}
	out, err := brief.Build(root)
	if err != nil {
		fatal("brief: %v", err)
	}
	fmt.Println(out)
}
