package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
	"github.com/JayveerPrajapati/kern/internal/mcp/etag"
)

// etagConsumed reports whether the executed command actually consumed the
// --etag flag (one of the four etag-eligible mirror commands ran
// cliEtagResponse with a non-empty value). main() warns on stderr when
// --etag was set but nothing consumed it, so a silently-ignored --etag is
// loud, not quiet (NIT-11).
var etagConsumed bool

// etagFlagSet reports whether --etag (either spelling) appears in the
// subcommand args. main() uses it after dispatch to detect a --etag that no
// command consumed (NIT-11 fail-loud).
func etagFlagSet(args []string) bool {
	for _, a := range args {
		if a == "--etag" || strings.HasPrefix(a, "--etag=") {
			return true
		}
	}
	return false
}

// schemaVersionForTool resolves the catalog schema version for an
// etag-eligible MCP tool name. The MCP server hashes with the PER-TOOL
// SchemaVersion (toolByName[name].SchemaVersion), not a global constant; the
// CLI must follow the same per-tool version so a schema bump mints fresh
// etags on both surfaces together (MEDIUM-3).
func schemaVersionForTool(name string) string {
	if t, ok := catalog.ByName(name); ok && t.SchemaVersion != "" {
		return t.SchemaVersion
	}
	return catalog.SchemaVersionV1
}

// cliEtagResponse implements the CLI conditional-fetch contract (B1,
// ADR-0012) for the four commands mirroring etag-eligible MCP tools (kern
// context, kern compact, kern retrieve, kern explore). It computes the
// response etag over the raw response text combined with the tool's catalog
// schema version using the same per-tool hash inputs as the MCP server, so
// each surface is SELF-consistent — cross-surface etag equality is NOT
// guaranteed, because the CLI and the MCP server hash different text
// pipelines (ADR-0012). The `etag: <hash>` footer goes to STDERR (stdout
// stays byte-identical for existing consumers, matching the savings-footer
// convention); when --etag matches the fresh value, `unchanged (etag <E>)`
// is printed to stdout and true is returned so the caller skips the full
// output (exit 0).
func cliEtagResponse(toolName, flag, text string) bool {
	if flag != "" {
		etagConsumed = true
	}
	e := etag.Hash(text, schemaVersionForTool(toolName))
	if flag != "" && flag == e {
		fmt.Println(etag.UnchangedResponseText(e))
		return true
	}
	fmt.Fprintln(os.Stderr, "etag: "+e)
	return false
}
