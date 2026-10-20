// Package clietag holds the shared CLI conditional-fetch (etag) contract:
// the etag-eligible commands (kern context, compact/optimize, retrieve,
// explore) compute their response etags here, and the single consumed-flag
// records whether any command in this invocation actually consumed --etag.
// It exists so main()'s NIT-11 fail-loud warning ("--etag was set but this
// command does not consume it") consults ONE home regardless of which
// extracted command family consumed the flag — the pre-clietag shape
// (cmd/kern package-local etagConsumed var + a verbatim copy with its own
// state in internal/graphcli, aggregated in main()) duplicated the family
// and gated extracting further command families (tranche 6).
package clietag

import (
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/etag"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
	catalog "github.com/JayveerPrajapati/kern/internal/mcpcatalog"
)

// consumed records whether an etag-eligible command ran EtagResponse with a
// non-empty flag value in this invocation.
var consumed bool

// Consumed reports whether any command consumed the --etag flag in this
// invocation. cmd/kern's main() reads it for the NIT-11 "kern: --etag was
// set but this command does not consume it" warning; extracted command
// families report consumption through EtagResponse instead of holding
// package-local state.
func Consumed() bool {
	return consumed
}

// EtagFlagSet reports whether --etag (either spelling) appears in the
// subcommand args. main() uses it after dispatch to detect a --etag that no
// command consumed (NIT-11 fail-loud).
func EtagFlagSet(args []string) bool {
	for _, a := range args {
		if a == "--etag" || strings.HasPrefix(a, "--etag=") {
			return true
		}
	}
	return false
}

// SchemaVersionForTool resolves the catalog schema version for an
// etag-eligible MCP tool name. The MCP server hashes with the PER-TOOL
// SchemaVersion (toolByName[name].SchemaVersion), not a global constant; the
// CLI must follow the same per-tool version so a schema bump mints fresh
// etags on both surfaces together (MEDIUM-3).
func SchemaVersionForTool(name string) string {
	if t, ok := catalog.ByName(name); ok && t.SchemaVersion != "" {
		return t.SchemaVersion
	}
	return catalog.SchemaVersionV1
}

// PrintIndexStamp prints the compact index freshness stamp (age, verdict,
// commit, HEAD skew) to stderr for index-backed CLI query commands — the CLI
// mirror of the MCP provenance footer (3.2-C). Informational, like the etag
// footer: stdout stays byte-identical for existing consumers.
func PrintIndexStamp(ix *index.Index) {
	if line := provenance.IndexStamp(ix); line != "" {
		fmt.Fprintln(os.Stderr, line)
	}
}

// EtagResponse implements the CLI conditional-fetch contract (B1, ADR-0012)
// for the four commands mirroring etag-eligible MCP tools (kern context,
// kern compact, kern retrieve, kern explore). It computes the response etag
// over the raw response text combined with the tool's catalog schema
// version using the same per-tool hash inputs as the MCP server, so each
// surface is SELF-consistent — cross-surface etag equality is NOT
// guaranteed, because the CLI and the MCP server hash different text
// pipelines (ADR-0012). The `etag: <hash>` footer goes to STDERR (stdout
// stays byte-identical for existing consumers, matching the savings-footer
// convention); when --etag matches the fresh value, `unchanged (etag <E>)`
// is printed to stdout and true is returned so the caller skips the full
// output (exit 0). A non-empty flag marks the invocation as having consumed
// --etag (see Consumed).
func EtagResponse(toolName, flag, text string) bool {
	if flag != "" {
		consumed = true
	}
	e := etag.Hash(text, SchemaVersionForTool(toolName))
	if flag != "" && flag == e {
		fmt.Println(etag.UnchangedResponseText(e))
		return true
	}
	fmt.Fprintln(os.Stderr, "etag: "+e)
	return false
}
