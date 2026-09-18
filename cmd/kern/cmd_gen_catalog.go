package main

import (
	"fmt"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/blueprint/checks/diffgate"
)

// runGenCatalog implements `kern gen-catalog`: regenerates the committed
// MCP tool catalog document (docs/tool-catalog.md) from the live tool
// registry. The catalog:doc gate (G36) blocks when the committed doc goes
// stale, so every MCP tool change must be followed by `kern gen-catalog`.
func runGenCatalog(rest []string) {
	root := "."
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--root", "-r":
			if i+1 < len(rest) {
				root = rest[i+1]
				i++
			}
		case "-h", "--help":
			fmt.Println("usage: kern gen-catalog [--root <dir>]")
			fmt.Println("regenerate docs/tool-catalog.md from the live MCP tool catalog")
			return
		default:
			fatalUsage("unknown gen-catalog flag %q (try --root <dir>)", rest[i])
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fatal("gen-catalog: resolve root: %v", err)
	}
	path, err := diffgate.WriteCatalogDoc(absRoot, diffgate.ToolInfos())
	if err != nil {
		fatal("gen-catalog: %v", err)
	}
	fmt.Printf("regenerated %s from the live MCP catalog\n", path)
}
