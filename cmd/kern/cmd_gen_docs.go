package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/diffgate"
)

// runGenDocs implements `kern gen-docs`: regenerates a committed MCP
// documentation file from the live tool registry — docs/tool-catalog.md
// (--doc catalog, the default), docs/mcp/tool-contracts.md
// (--doc contracts), docs/mcp/tool-schemas.json (--doc schemas),
// docs/tool-selection.md (--doc selection), or the mechanical file-tree block
// of docs/index.md (--doc site). The
// catalog:doc / contracts:doc gates block when the committed docs go stale,
// so every MCP tool change must be followed by `kern gen-docs` (or the
// pinned `kern gen-catalog` / `kern gen-contracts` wrappers). --check
// verifies freshness without writing, for CI.
func runGenDocs(rest []string) {
	runGenDocsImpl(rest, "gen-docs", "", true)
}

// runGenCatalog implements `kern gen-catalog`: regenerates the committed
// MCP tool catalog document (docs/tool-catalog.md) from the live tool
// registry. Thin wrapper over runGenDocsImpl with the catalog doc type
// pinned.
func runGenCatalog(rest []string) {
	runGenDocsImpl(rest, "gen-catalog", "catalog", false)
}

// runGenContracts implements `kern gen-contracts`: regenerates the committed
// MCP tool contracts document (docs/mcp/tool-contracts.md) from the live
// tool registry. Thin wrapper over runGenDocsImpl with the contracts doc
// type pinned.
func runGenContracts(rest []string) {
	runGenDocsImpl(rest, "gen-contracts", "contracts", false)
}

// runGenDocsImpl is the shared doc-regeneration core. cmdName drives the
// usage/error messages; pinnedDocType fixes the doc when the command is a
// wrapper (gen-catalog/gen-contracts) and is empty for gen-docs, which reads
// --doc catalog|contracts|schemas|site|selection (default catalog).
func runGenDocsImpl(rest []string, cmdName, pinnedDocType string, allowDoc bool) {
	docType := pinnedDocType
	if allowDoc {
		docType = "catalog"
	}
	root := "."
	checkOnly := false
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--check":
			checkOnly = true
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
				fmt.Println("usage: kern gen-docs --doc catalog|contracts|schemas|site|selection [--root ROOT] [--check]")
				fmt.Println("regenerate (or, with --check, verify without writing) docs/tool-catalog.md")
				fmt.Println("(catalog), docs/mcp/tool-contracts.md (contracts), docs/mcp/tool-schemas.json")
				fmt.Println("(schemas), docs/tool-selection.md (selection), or the mechanical file-tree block of docs/index.md (site)")
			} else {
				fmt.Printf("usage: kern %s [--root ROOT] [--check]\n", cmdName)
				if cmdName == "gen-catalog" {
					fmt.Println("regenerate (or, with --check, verify without writing) docs/tool-catalog.md")
				} else {
					fmt.Println("regenerate (or, with --check, verify without writing) docs/mcp/tool-contracts.md")
				}
			}
			return
		default:
			fatalUsage("unknown %s flag %q (try --root ROOT)", cmdName, rest[i])
		}
	}
	switch docType {
	case "catalog", "contracts", "schemas", "site", "selection":
	default:
		fatalUsage("%s: unknown --doc %q (try catalog, contracts, schemas, site or selection)", cmdName, docType)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fatal("%s: resolve root: %v", cmdName, err)
	}
	path, expected, err := generatedDoc(absRoot, docType)
	if err != nil {
		fatal("%s: %v", cmdName, err)
	}

	if checkOnly {
		actual, err := os.ReadFile(path)
		if err != nil {
			fatal("%s: %v", cmdName, err)
		}
		if !bytes.Equal(actual, expected) {
			fatal("%s: %s is stale — run %s and commit the regenerated file", cmdName, path, regenerateCommand(docType))
		}
		fmt.Printf("checked %s: fresh\n", path)
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fatal("%s: create %s: %v", cmdName, filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, expected, 0o644); err != nil {
		fatal("%s: write %s: %v", cmdName, path, err)
	}
	fmt.Printf("regenerated %s from the live MCP catalog\n", path)
}

// generatedDoc renders the expected bytes for one generated artifact
// without touching the filesystem. Keeping generation and writing separate
// is what makes --check side-effect free.
func generatedDoc(absRoot, docType string) (string, []byte, error) {
	switch docType {
	case "catalog":
		tools := diffgate.ToolInfos()
		if len(tools) == 0 {
			return "", nil, fmt.Errorf("no MCP tool catalog injected")
		}
		return filepath.Join(absRoot, filepath.FromSlash("docs/tool-catalog.md")), diffgate.GenerateCatalogDoc(tools), nil
	case "contracts":
		tools := diffgate.ToolInfos()
		if len(tools) == 0 {
			return "", nil, fmt.Errorf("no MCP tool catalog injected")
		}
		return filepath.Join(absRoot, filepath.FromSlash("docs/mcp/tool-contracts.md")), diffgate.GenerateContractsDoc(tools), nil
	case "schemas":
		data, err := diffgate.GenerateSchemaBaseline(diffgate.ToolInfos())
		if err != nil {
			return "", nil, err
		}
		return filepath.Join(absRoot, filepath.FromSlash("docs/mcp/tool-schemas.json")), data, nil
	case "selection":
		data, err := generateToolSelectionDoc(diffgate.ToolInfos())
		if err != nil {
			return "", nil, err
		}
		return filepath.Join(absRoot, filepath.FromSlash("docs/tool-selection.md")), data, nil
	case "site":
		indexPath, content, err := buildSiteIndexDoc(absRoot)
		if err != nil {
			return "", nil, err
		}
		return indexPath, []byte(content), nil
	default:
		return "", nil, fmt.Errorf("unknown doc type %q", docType)
	}
}

// regenerateCommand returns the focused regeneration command for a stale doc.
func regenerateCommand(docType string) string {
	switch docType {
	case "catalog":
		return "kern gen-catalog"
	case "contracts":
		return "kern gen-contracts"
	default:
		return fmt.Sprintf("kern gen-docs --doc %s", docType)
	}
}

// siteIndexMarkers delimit the mechanically regenerated block of
// docs/index.md. Everything between them is owned by `kern gen-docs --doc
// site`; everything outside is hand-curated and left untouched.
const (
	siteIndexStart = "<!-- AUTO-GENERATED FILE TREE: docs (managed by `kern gen-docs --doc site`) -->"
	siteIndexEnd   = "<!-- END AUTO-GENERATED -->"
)

// buildSiteIndexDoc computes the fresh docs/index.md content without writing
// it. gen-docs uses it directly for --check and for regeneration.
func buildSiteIndexDoc(absRoot string) (string, string, error) {
	indexPath := filepath.Join(absRoot, "docs", "index.md")
	docB, err := os.ReadFile(indexPath)
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", indexPath, err)
	}
	doc := string(docB)
	start := strings.Index(doc, siteIndexStart)
	end := strings.Index(doc, siteIndexEnd)
	if start < 0 || end < 0 || end <= start {
		return "", "", fmt.Errorf("%s: site-index markers not found (need %q ... %q)", indexPath, siteIndexStart, siteIndexEnd)
	}
	entries, err := listDocsFiles(filepath.Join(absRoot, "docs"))
	if err != nil {
		return "", "", err
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
	return indexPath, rebuilt, nil
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
