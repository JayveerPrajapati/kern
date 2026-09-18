package main

import (
	"fmt"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/blueprint/checks/diffgate"
)

// runGenContracts implements `kern gen-contracts`: regenerates the committed
// MCP tool contracts document (docs/mcp/tool-contracts.md) from the live
// tool registry. The contracts:doc gate blocks when the committed doc goes
// stale, so every MCP tool change must be followed by `kern gen-contracts`
// (alongside `kern gen-catalog`).
func runGenContracts(rest []string) {
	root := "."
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--root", "-r":
			if i+1 < len(rest) {
				root = rest[i+1]
				i++
			}
		case "-h", "--help":
			fmt.Println("usage: kern gen-contracts [--root <dir>]")
			fmt.Println("regenerate docs/mcp/tool-contracts.md from the live MCP tool catalog")
			return
		default:
			fatalUsage("unknown gen-contracts flag %q (try --root <dir>)", rest[i])
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fatal("gen-contracts: resolve root: %v", err)
	}
	path, err := diffgate.WriteContractsDoc(absRoot, diffgate.ToolInfos())
	if err != nil {
		fatal("gen-contracts: %v", err)
	}
	fmt.Printf("regenerated %s from the live MCP catalog\n", path)
}
