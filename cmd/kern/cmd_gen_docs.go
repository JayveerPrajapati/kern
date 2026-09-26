package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/checks/diffgate"
)

// runGenDocs implements `kern gen-docs`: regenerates a committed MCP
// documentation file from the live tool registry — docs/tool-catalog.md
// (--doc catalog, the default), docs/mcp/tool-contracts.md
// (--doc contracts), or the mechanical file-tree block of docs/index.md
// (--doc site). The catalog:doc / contracts:doc gates block when the
// committed docs go stale, so every MCP tool change must be followed by
// `kern gen-docs` (or the pinned `kern gen-catalog` / `kern gen-contracts`
// wrappers).
func runGenDocs(rest []string) {
	runGenDocsImpl(rest, "gen-docs", "", true)
}

// runGenCatalog implements `kern gen-catalog`: regenerates the committed
// MCP tool catalog document (docs/tool-catalog.md) from the live tool
// registry. The catalog:doc gate (G36) blocks when the committed doc goes
// stale, so every MCP tool change must be followed by `kern gen-catalog`.
// Thin wrapper over runGenDocsImpl with the catalog doc type pinned.
func runGenCatalog(rest []string) {
	runGenDocsImpl(rest, "gen-catalog", "catalog", false)
}

// runGenContracts implements `kern gen-contracts`: regenerates the committed
// MCP tool contracts document (docs/mcp/tool-contracts.md) from the live
// tool registry. The contracts:doc gate blocks when the committed doc goes
// stale, so every MCP tool change must be followed by `kern gen-contracts`
// (alongside `kern gen-catalog`). Thin wrapper over runGenDocsImpl with the
// contracts doc type pinned.
func runGenContracts(rest []string) {
	runGenDocsImpl(rest, "gen-contracts", "contracts", false)
}

// runGenDocsImpl is the shared doc-regeneration core. cmdName drives the
// usage/error messages; pinnedDocType fixes the doc when the command is a
// wrapper (gen-catalog/gen-contracts) and is empty for gen-docs, which reads
// --doc catalog|contracts (default catalog).
func runGenDocsImpl(rest []string, cmdName, pinnedDocType string, allowDoc bool) {
	docType := pinnedDocType
	if allowDoc {
		docType = "catalog"
	}
	root := "."
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--doc":
			if !allowDoc {
				fatalUsage("unknown %s flag %q (try --root ROOT)", cmdName, rest[i])
			}
			if i+1 < len(rest) {
				docType = rest[i+1]
				i++
			}
		case "--root", "-r":
			if i+1 < len(rest) {
				root = rest[i+1]
				i++
			}
		case "-h", "--help":
			if allowDoc {
				fmt.Println("usage: kern gen-docs --doc catalog|contracts|site [--root ROOT]")
				fmt.Println("regenerate docs/tool-catalog.md (catalog), docs/mcp/tool-contracts.md (contracts),")
				fmt.Println("or the mechanical file-tree block of docs/index.md (site) from the live tree")
			} else {
				fmt.Printf("usage: kern %s [--root ROOT]\n", cmdName)
				if cmdName == "gen-catalog" {
					fmt.Println("regenerate docs/tool-catalog.md from the live MCP tool catalog")
				} else {
					fmt.Println("regenerate docs/mcp/tool-contracts.md from the live MCP tool catalog")
				}
			}
			return
		default:
			fatalUsage("unknown %s flag %q (try --root ROOT)", cmdName, rest[i])
		}
	}
	switch docType {
	case "catalog", "contracts", "site":
	default:
		fatalUsage("%s: unknown --doc %q (try catalog, contracts or site)", cmdName, docType)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fatal("%s: resolve root: %v", cmdName, err)
	}
	var path string
	switch docType {
	case "catalog":
		path, err = diffgate.WriteCatalogDoc(absRoot, diffgate.ToolInfos())
	case "contracts":
		path, err = diffgate.WriteContractsDoc(absRoot, diffgate.ToolInfos())
	case "site":
		path, err = writeSiteIndexDoc(absRoot)
	}
	if err != nil {
		fatal("%s: %v", cmdName, err)
	}
	fmt.Printf("regenerated %s from the live MCP catalog\n", path)
}

// siteIndexMarkers delimit the mechanically regenerated block of
// docs/index.md. Everything between them is owned by `kern gen-docs --doc
// site`; everything outside is hand-curated and left untouched.
const (
	siteIndexStart = "<!-- AUTO-GENERATED FILE TREE: docs (managed by `kern gen-docs --doc site`) -->"
	siteIndexEnd   = "<!-- END AUTO-GENERATED -->"
)

// writeSiteIndexDoc regenerates the mechanical file-tree block of
// docs/index.md: a sorted, linked listing of every markdown file under
// docs/ (the site index itself excluded). Descriptions live outside the
// markers and stay hand-curated — this only fixes the drift trap of links
// rotting when docs files move or new ones land. It is a no-op (and not an
// error) when docs/index.md is missing, mirroring the catalog/contracts
// writers' tolerance.
func writeSiteIndexDoc(absRoot string) (string, error) {
	indexPath := filepath.Join(absRoot, "docs", "index.md")
	docB, err := os.ReadFile(indexPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", indexPath, err)
	}
	doc := string(docB)
	start := strings.Index(doc, siteIndexStart)
	end := strings.Index(doc, siteIndexEnd)
	if start < 0 || end < 0 || end <= start {
		return "", fmt.Errorf("%s: site-index markers not found (need %q ... %q)", indexPath, siteIndexStart, siteIndexEnd)
	}
	docsDir := filepath.Join(absRoot, "docs")
	entries, err := listDocsFiles(docsDir)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(siteIndexStart)
	b.WriteString("\n")
	for _, e := range entries {
		b.WriteString("- [")
		b.WriteString(e)
		b.WriteString("](")
		b.WriteString(e)
		b.WriteString(")\n")
	}
	b.WriteString(siteIndexEnd)
	rebuilt := doc[:start] + b.String() + doc[end+len(siteIndexEnd):]
	if err := os.WriteFile(indexPath, []byte(rebuilt), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", indexPath, err)
	}
	return indexPath, nil
}

// listDocsFiles returns every .md file under docs/ (recursively, excluding
// docs/index.md itself), as slash-separated paths relative to docs/ and
// sorted for determinism.
func listDocsFiles(docsDir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(docsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, err := filepath.Rel(docsDir, p)
		if err != nil {
			return err
		}
		if rel == "index.md" {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", docsDir, err)
	}
	sort.Strings(out)
	return out, nil
}
