// Command kern brief prints the repo onboarding brief: index summary, hub
// symbols, entry points, architecture, savings, memory and a compact project
// overview. It is the CLI twin of `kern buddy` — internal/brief.Build renders
// the digest. Build handles index loading internally (index.Load) and reports
// cold-index sections with a hint instead of failing. --map appends the full
// per-file project map.

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
// precache .` once. By default the brief is a concise digest; --map appends
// the full per-file project map.
func runBrief(rest []string) {
	root := "."
	mapRequested := false
	for i := 0; i < len(rest); i++ {
		switch {
		case rest[i] == "--map":
			mapRequested = true
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
	out, err := brief.BuildWithOptions(root, brief.Options{Map: mapRequested})
	if err != nil {
		fatal("brief: %v", err)
	}
	fmt.Println(out)
}

// runProjectMap implements `kern project_map [root]`: the full per-file
// project map through the same renderer `kern buddy --map` uses — the
// command the digest's "… N more files" truncation pointer recommends.
// Read-only.
func runProjectMap(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if len(args) > 0 {
		root = args[0]
	}
	if _, err := os.Stat(root); err != nil {
		fatal("project_map: %v", err)
	}
	out, err := brief.ProjectMap(root)
	if err != nil {
		fatal("project_map: %v", err)
	}
	fmt.Println(out)
}
