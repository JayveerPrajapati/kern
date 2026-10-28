package main

// cmd_gen_plugin.go implements `kern gen-plugin`: synchronizes the opencode
// plugin from a single canonical source. The canonical source is the repo-root
// `.opencode/plugins/kern.ts` (the hand-edited version); the embedded asset
// `internal/agentwire/assets/plugin/kern.ts` (go:embed in
// internal/agentwire/shared.go, deployed to every host by `kern setup`) must
// stay byte-identical, and its sha256 must appear in the shipped registry
// (internal/pluginhash) so an installed copy can be told apart from a user
// edit.
//
// Before this command grew the registry step, the maintenance ritual was:
// edit the canonical file, copy it by hand, then append the hash a failing test
// printed — three opportunities to drift, and the customized-vs-shipped
// detection depends on the registry being right. Now one command does all
// three:
//
//	kern gen-plugin            # asset + registry (idempotent)
//	kern gen-plugin --check    # CI: fail if either is stale, write nothing
//
// It mirrors the gen-catalog/gen-contracts pattern: a committed generated
// artifact with a --check freshness gate, so the copies can never drift
// silently.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pluginCanonicalRel is the repo-root-relative canonical plugin source —
// the copy a contributor actually edits.
const pluginCanonicalRel = ".opencode/plugins/kern.ts"

// pluginAssetRel is the repo-root-relative embedded asset path (go:embed).
const pluginAssetRel = "internal/agentwire/assets/plugin/kern.ts"

// pluginRegistryRel is the shipped-hash registry source file.
const pluginRegistryRel = "internal/pluginhash/pluginhash.go"

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
			fmt.Println("internal/agentwire/assets/plugin/kern.ts and register its sha256 in")
			fmt.Println("internal/pluginhash; --check verifies both without writing")
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
	registry := filepath.Join(absRoot, pluginRegistryRel)
	src, err := os.ReadFile(canonical)
	if err != nil {
		fatal("gen-plugin: read canonical %s: %v", canonical, err)
	}
	sum := sha256.Sum256(src)
	hash := hex.EncodeToString(sum[:])

	if checkOnly {
		actual, err := os.ReadFile(asset)
		if err != nil {
			fatal("gen-plugin: %s is stale (unreadable: %v) — run `kern gen-plugin` and commit the regenerated asset", asset, err)
		}
		if !bytes.Equal(actual, src) {
			fatal("gen-plugin: %s is stale — run `kern gen-plugin` and commit the regenerated asset", asset)
		}
		reg, err := os.ReadFile(registry)
		if err != nil {
			fatal("gen-plugin: read registry %s: %v", registry, err)
		}
		if !strings.Contains(string(reg), `"`+hash+`"`) {
			fatal("gen-plugin: sha256 %s of the canonical plugin is not in %s — run `kern gen-plugin` and commit the registry", hash, pluginRegistryRel)
		}
		fmt.Printf("checked %s: fresh (asset byte-identical, sha256 %s registered)\n", asset, hash[:12])
		return
	}

	if err := os.MkdirAll(filepath.Dir(asset), 0o755); err != nil {
		fatal("gen-plugin: create %s: %v", filepath.Dir(asset), err)
	}
	if err := os.WriteFile(asset, src, 0o644); err != nil {
		fatal("gen-plugin: write %s: %v", asset, err)
	}
	fmt.Printf("regenerated %s from %s (byte-identical)\n", asset, canonical)

	if _, statErr := os.Stat(registry); statErr != nil {
		// Installed binary outside a source checkout: there is no registry to
		// edit. Say so instead of failing the asset sync.
		fmt.Printf("note: %s not found — registry step skipped (source checkout only)\n", pluginRegistryRel)
		return
	}
	added, err := registerPluginHash(registry, hash)
	if err != nil {
		fatal("gen-plugin: %v", err)
	}
	if added {
		fmt.Printf("registered sha256 %s in %s\n", hash, pluginRegistryRel)
	} else {
		fmt.Printf("sha256 %s already registered in %s\n", hash[:12], pluginRegistryRel)
	}
}

// registerPluginHash appends hash to the shipped registry's map literal when
// absent, returning whether it wrote. The insertion point is the opening line
// of `var shipped = map[string]bool{`, so new entries stay grouped at the top
// with their provenance comment and the historical list below is untouched.
// Idempotent: an already-present hash writes nothing.
func registerPluginHash(registryPath, hash string) (bool, error) {
	b, err := os.ReadFile(registryPath)
	if err != nil {
		return false, fmt.Errorf("read registry: %w", err)
	}
	text := string(b)
	if strings.Contains(text, `"`+hash+`"`) {
		return false, nil
	}
	marker := "var shipped = map[string]bool{"
	i := strings.Index(text, marker)
	if i < 0 {
		return false, fmt.Errorf("%s: shipped registry map literal not found", registryPath)
	}
	insertAt := i + len(marker)
	entry := "\n\t// registered by kern gen-plugin\n\t\"" + hash + "\": true,"
	updated := text[:insertAt] + entry + text[insertAt:]
	if err := os.WriteFile(registryPath, []byte(updated), 0o644); err != nil {
		return false, fmt.Errorf("write registry: %w", err)
	}
	return true, nil
}
