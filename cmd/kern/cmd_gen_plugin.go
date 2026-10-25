package main

// cmd_gen_plugin.go implements `kern gen-plugin`: synchronizes the opencode
// plugin's two committed copies from a single canonical source. The canonical
// source is the repo-root `.opencode/plugins/kern.ts` (the hand-edited
// version); the embedded asset `internal/agentwire/assets/plugin/kern.ts`
// (go:embed in internal/agentwire/shared.go, deployed to every host by
// `kern setup`) must stay byte-identical — the setup parity tests
// (TestPluginMatchesMCPCatalog) and the hash registry
// (TestShippedPluginHashRegistryCurrent) fail on divergence.
//
// The plugin is maintained by editing ONE file — the canonical repo-root
// copy — then running `kern gen-plugin` (or the Makefile's `make plugin-sync`)
// to regenerate the embedded asset, and appending the new sha256 to
// internal/pluginhash's shipped registry (the registry test prints it).
// `--check` verifies byte-parity without writing, for CI and pre-commit.
//
// This mirrors the gen-catalog/gen-contracts pattern: a committed generated
// artifact with a --check freshness gate, so the two plugin copies can never
// drift silently.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// pluginCanonicalRel is the repo-root-relative canonical plugin source —
// the copy a contributor actually edits.
const pluginCanonicalRel = ".opencode/plugins/kern.ts"

// pluginAssetRel is the repo-root-relative embedded asset path (go:embed).
const pluginAssetRel = "internal/agentwire/assets/plugin/kern.ts"

// runGenPlugin implements `kern gen-plugin [--check] [--root ROOT]`.
func runGenPlugin(rest []string) {
	root := "."
	checkOnly := false
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--check":
			checkOnly = true
		case "--root", "-r":
			if i+1 < len(rest) {
				root = rest[i+1]
				i++
			}
		case "-h", "--help":
			fmt.Println("usage: kern gen-plugin [--root ROOT] [--check]")
			fmt.Println("copy .opencode/plugins/kern.ts (canonical) into the embedded")
			fmt.Println("internal/agentwire/assets/plugin/kern.ts; --check verifies parity without writing")
			return
		default:
			fatalUsage("unknown gen-plugin flag %q (try --root ROOT)", rest[i])
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fatal("gen-plugin: resolve root: %v", err)
	}
	canonical := filepath.Join(absRoot, pluginCanonicalRel)
	asset := filepath.Join(absRoot, pluginAssetRel)
	src, err := os.ReadFile(canonical)
	if err != nil {
		fatal("gen-plugin: read canonical %s: %v", canonical, err)
	}

	if checkOnly {
		actual, err := os.ReadFile(asset)
		if err != nil {
			fatal("gen-plugin: %s is stale (unreadable: %v) — run `kern gen-plugin` and commit the regenerated asset", asset, err)
		}
		if !bytes.Equal(actual, src) {
			fatal("gen-plugin: %s is stale — run `kern gen-plugin` and commit the regenerated asset (then append the new sha256 to the shipped registry)", asset)
		}
		fmt.Printf("checked %s: fresh\n", asset)
		return
	}

	if err := os.MkdirAll(filepath.Dir(asset), 0o755); err != nil {
		fatal("gen-plugin: create %s: %v", filepath.Dir(asset), err)
	}
	if err := os.WriteFile(asset, src, 0o644); err != nil {
		fatal("gen-plugin: write %s: %v", asset, err)
	}
	fmt.Printf("regenerated %s from %s (byte-identical; append the new sha256 to the shipped registry)\n", asset, canonical)
}
