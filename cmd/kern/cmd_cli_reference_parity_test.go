package main

// TestCLIReferenceDocCoversCommandTable is the drift gate behind the "this
// list mirrors the shipped dispatch table" claim in docs/cli-reference.md.
// Every key in the commandTable map (cmd/kern/dispatch_table.go) must be
// mentioned in the doc as `kern <key>` — or as a token in a "kern a / b / c"
// grouped line. Underscore keys (mcp-mirror aliases such as "doc_fetch") are
// matched by their hyphenated spelling ("doc-fetch"), which is what a user
// actually types; flag-alias keys ("--version", "-v") are exempt because they
// are not subcommands. When the CLI grows, the doc must grow with it — this
// test fails closed and lists exactly which commands are missing.
//
// Pattern follows the other doc-parity gates (internal/setup/setup_test.go's
// cliTableEntryRe, internal/architecture/agents_doc_parity_test.go).

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// cliTableKeyRe recovers each `"name": {` entry key from the commandTable
// map literal in dispatch_table.go — the same key extraction the setup
// package's plugin-parity test uses, so the two gates can never disagree
// about what a "command" is.
var cliTableKeyRe = regexp.MustCompile(`(?m)^\s*"([^"]+)":\s*\{`)

func TestCLIReferenceDocCoversCommandTable(t *testing.T) {
	// Tests run with the package dir as CWD (cmd/kern); the doc lives at the
	// repo root, two levels up — same pattern as the setup parity test.
	docPath := filepath.Join("..", "..", "docs", "cli-reference.md")
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
	table := string(tableB)

	keys := cliTableKeyRe.FindAllStringSubmatch(table, -1)
	if len(keys) < 100 {
		t.Fatalf("suspiciously small commandTable: %d keys parsed from %s", len(keys), tablePath)
	}

	// mentioned reports whether the doc references the command, either as
	// `kern <key>` (adjacent) or as a token inside a "kern a / b / c" group.
	mentioned := func(key string) bool {
		if regexp.MustCompile(`kern\s+` + regexp.QuoteMeta(key) + `([ \t/|]|$)`).MatchString(doc) {
			return true
		}
		return regexp.MustCompile(`/\s*` + regexp.QuoteMeta(key) + `([ \t/|]|$)`).MatchString(doc)
	}

	var missing []string
	for _, m := range keys {
		key := m[1]
		// Flag-alias keys ("--version", "-v") are not subcommands.
		if !regexp.MustCompile(`^[a-z0-9]`).MatchString(key) {
			continue
		}
		// mcp-mirror aliases are keyed by underscore but typed with a hyphen.
		spelling := strings.ReplaceAll(key, "_", "-")
		if !mentioned(spelling) {
			missing = append(missing, spelling)
		}
	}

	if len(missing) > 0 {
		t.Fatalf("docs/cli-reference.md does not cover %d commands from commandTable (%s). "+
			"Add a one-liner for each (format: `kern <cmd> <usage>   <help>`) to the "+
			"\"More commands\" section, or `kern gen-docs`-style regenerate if one exists.\nmissing:\n  %s",
			len(missing), tablePath, strings.Join(missing, "\n  "))
	}
}
