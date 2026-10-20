package main

// TestRecipesDocListsOnlyLiveCommands is the recipes.md drift gate behind the
// "All commands below exist in the dispatch table" claim at the top of
// docs/recipes.md. Every `kern <cmd>` mentioned in the doc must resolve to a
// live commandTable key — the same reverse direction as
// TestCLIReferenceDocListsOnlyLiveCommands, and for the same reason: a pivot
// that deletes commands must also delete their recipes, otherwise the doc
// advertises commands that exit 2 ("unknown command"). The forward direction
// is intentionally NOT asserted here (recipes cover tasks, not the whole
// catalog — TestCLIReferenceDocCoversCommandTable owns forward coverage).
//
// Prose-only exemptions: words that can legitimately follow "kern" in English
// prose inside this doc without being a command invocation.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recipesDocGhosts(t *testing.T) []string {
	t.Helper()
	docPath := filepath.Join("..", "..", "docs", "recipes.md")
	docB, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	doc := string(docB)

	tablePath := "dispatch_table.go"
	tableB, err := os.ReadFile(tablePath)
	if err != nil {
		t.Fatalf("read %s: %v", tablePath, err)
	}

	live := map[string]bool{}
	for _, m := range cliTableKeyRe.FindAllStringSubmatch(string(tableB), -1) {
		// mcp-mirror aliases are keyed by underscore but typed with a hyphen.
		live[strings.ReplaceAll(m[1], "_", "-")] = true
	}
	if len(live) < 30 {
		t.Fatalf("suspiciously small commandTable: %d keys parsed from %s", len(live), tablePath)
	}

	// Words that follow "kern" in prose but are not command invocations.
	proseOnly := map[string]bool{
		"from": true, // "...recipes for working with kern from the CLI."
		"adds": true, // "...kern adds the symbol-level and governance view."
	}

	var ghost []string
	seen := map[string]bool{}
	for _, m := range cliDocCommandRe.FindAllStringSubmatch(doc, -1) {
		cmd := m[1]
		// Flag-alias keys and flag values are not subcommands.
		if strings.HasPrefix(cmd, "-") {
			continue
		}
		if seen[cmd] || proseOnly[cmd] || live[cmd] {
			continue
		}
		seen[cmd] = true
		ghost = append(ghost, cmd)
	}
	return ghost
}

func TestRecipesDocListsOnlyLiveCommands(t *testing.T) {
	ghost := recipesDocGhosts(t)
	if len(ghost) > 0 {
		t.Fatalf("docs/recipes.md advertises %d commands that are not in commandTable (dispatch_table.go): %s. "+
			"Remove or rewrite the recipe — recipes must only use the shipped dispatch table "+
			"(a pivot that deletes commands must delete their recipes too).",
			len(ghost), strings.Join(ghost, ", "))
	}
}
