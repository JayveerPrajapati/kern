package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// runSnapshot implements `kern snapshot [root] [--out FILE] [--symbol X]
// [--limit N]`: load-or-build the index, render a canonical versioned graph
// snapshot (whole-repo, or the neighbourhood of --symbol), print a one-line
// summary, then emit the snapshot JSON to stdout or --out.
func runSnapshot(rest []string) int {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) > 0 {
		root = args[0]
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("Snapshot: %v", err)
	}
	mode := "whole"
	if f.symbol != "" {
		mode = "subgraph"
	}
	snap, err := ix.Snapshot(mode, f.symbol, f.limit)
	if err != nil {
		fatal("Snapshot: %v", err)
	}
	identity := snap.Identity.ContentRoot
	if len(identity) > 12 {
		identity = identity[:12]
	}
	fmt.Printf("snapshot: mode=%s symbols=%d edges=%d identity=%s\n",
		snap.Mode, len(snap.Graph.Nodes), len(snap.Graph.Edges), identity)
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		fatal("Snapshot: %v", err)
	}
	if f.out != "" {
		if err := os.WriteFile(f.out, b, 0o644); err != nil {
			fatal("Snapshot: %v", err)
		}
		fmt.Printf("wrote %s (%d bytes)\n", f.out, len(b))
		return 0
	}
	fmt.Println(string(b))
	return 0
}

// runSnapshotVerify implements `kern snapshot verify <file> [root]
// [--strict]`: load the snapshot, verify it against root, print the verdict,
// and exit 0 fresh / 1 stale / 2 unknown or error.
func runSnapshotVerify(rest []string) int {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	if len(args) < 1 {
		fatalUsage("usage: kern snapshot verify <file> [root] [--strict]")
	}
	file := args[0]
	root := f.root
	if root == "" {
		root = "."
	}
	if len(args) > 1 {
		root = args[1]
	}
	snap, err := index.LoadSnapshot(file)
	if err != nil {
		fatal2("Snapshot verify: %v", err)
	}
	verdict, err := index.VerifySnapshot(root, snap, f.strict)
	if err != nil {
		fatal2("Snapshot verify: %v", err)
	}
	fmt.Printf("verdict: %s (content_root=%s, built_at=%s)\n",
		verdict, snap.Identity.ContentRoot, snap.Identity.BuiltAt.Format(time.RFC3339))
	switch verdict {
	case index.FreshnessFresh:
		return 0
	case index.FreshnessStale:
		return 1
	default:
		return 2
	}
}
