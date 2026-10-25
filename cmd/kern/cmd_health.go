package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/JayveerPrajapati/kern/internal/index"
	mcpcatalog "github.com/JayveerPrajapati/kern/internal/mcpcatalog"
)

// runHealth implements `kern health`: a read-only JSON health snapshot of
// the current project — binary version, the tool catalog size, and the
// persisted index's freshness/scale. The opencode plugin exempts any `kern`
// invocation from governed routing (first-token rule) as a content-free
// diagnostic surface, so it must stay read-only and side-effect-free. It
// always emits JSON (the flags parser's documented contract: "health output
// is always JSON").
func runHealth(rest []string) {
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v\nusage: kern health [--root ROOT] [--json]", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	indexBlock := map[string]any{"root": root}
	if disk := index.DiskIndexView(root); disk != nil {
		indexBlock = disk
	}
	host, herr := os.Hostname()
	if herr != nil {
		host = "unknown"
	}
	report := map[string]any{
		"status":  "ok",
		"version": version,
		"host":    host,
		"root":    root,
		"index":   indexBlock,
		"tools":   len(mcpcatalog.ToolNames()),
	}
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal("health: %v", err)
	}
	fmt.Println(string(out))
}
