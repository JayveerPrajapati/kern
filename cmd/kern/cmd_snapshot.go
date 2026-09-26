package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/pack"
)

// runSnapshot implements `kern snapshot [root] [--out FILE] [--symbol X]
// [--limit N] [--verify FILE]`: load-or-build the index, render a canonical
// versioned graph snapshot (whole-repo, or the neighbourhood of --symbol),
// print a one-line summary, then emit the snapshot JSON to stdout or --out.
// `--verify FILE` loads a previously written snapshot and checks it
// against the current repo, mirroring the MCP kern_snapshot action=verify
// handler instead of silently treating the snapshot file as a repo root.
func runSnapshot(rest []string) int {
	// --verify is a value-carrying flag here (the global parser treats it as a
	// bool), so intercept it before parseFlags and route to the verify path,
	// preserving any surrounding flags (--strict, --root ...).
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--verify" {
			if i+1 >= len(rest) || strings.HasPrefix(rest[i+1], "-") {
				fatalUsage("usage: kern snapshot --verify <file> [--strict]")
			}
			vrest := make([]string, 0, len(rest))
			vrest = append(vrest, rest[i+1])     // snapshot file (positional 0)
			vrest = append(vrest, rest[:i]...)   // flags before --verify
			vrest = append(vrest, rest[i+2:]...) // flags after the file
			return runSnapshotVerify(vrest)
		}
	}
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if len(args) > 0 {
		root = args[0]
	}
	// Guard: a positional path that names a file (not a directory) is
	// almost certainly a snapshot JSON meant for --verify. Refuse it with a
	// clear error instead of building an empty fresh snapshot over it.
	if fi, statErr := os.Stat(root); statErr == nil && !fi.IsDir() {
		fatalUsage("usage: kern snapshot [root] [--out FILE] [--symbol X] [--limit N] [--verify <file>]\n  %q is a file, not a repo root. To verify a snapshot against the current repo, run:\n  kern snapshot --verify %s", root, root)
	}
	ix, err := loadOrBuild(root)
	if err != nil {
		fatal("Snapshot: %v", err)
	}
	// --format pack renders the graph-snapshot pack text (pack.BuildGraph —
	// the `kern pack --graph` render, whole graph or --symbol neighbourhood)
	// instead of the versioned snapshot JSON. --max-tokens caps the
	// signature section deterministically (surface consolidation T2b).
	if f.format == "pack" {
		gb, err := pack.BuildGraph(root, pack.Options{
			MaxTokens:   f.maxTokens,
			GraphSymbol: f.symbol,
		})
		if err != nil {
			fatal("Snapshot: %v", err)
		}
		out := gb.Render()
		if f.out != "" {
			if werr := os.WriteFile(f.out, []byte(out), 0o644); werr != nil {
				fatal("Snapshot: %v", werr)
			}
			fmt.Printf("wrote %s (%d bytes)\n", f.out, len(out))
			return 0
		}
		fmt.Print(out)
		return 0
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
	f, args := parseFlagsOrDie(rest)
	if len(args) < 1 {
		fatalUsage("usage: kern snapshot verify <file> [root] [--strict]")
	}
	file := args[0]
	root := projectRoot(f)
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
	fmt.Printf("verdict: %s (content_root=%s, built_at=%s, files=%d, tree_oid=%s, schema=%d, strict=%v)\n",
		verdict, snap.Identity.ContentRoot, snap.Identity.BuiltAt.Format(time.RFC3339),
		len(snap.Files), snap.Identity.TreeOID, snap.SchemaVersion, f.strict)
	switch verdict {
	case index.FreshnessFresh:
		return 0
	case index.FreshnessStale:
		return 1
	default:
		return 2
	}
}
