package mcp

import (
	"os"
	"strings"
	"testing"
)

// The opencode plugin (.opencode/plugins/kern.ts) advertises tools at agent
// startup the same way the MCP server's tools/list does: every tool({...})
// definition carries a `description:` string the agent reads before any tool
// is called. Unlike the catalog (drift-gated by TestCatalogAdvertisementTokenBudget
// in catalog_ad_tokens_test.go), the plugin's advertisement cost was UNGATED —
// this test is that gate.

const pluginAdFile = "../../.opencode/plugins/kern.ts"

// maxPluginAdTotalBytes is the drift gate for the total byte cost of the
// plugin's tool advertisements (the concatenated description: string
// literals).
//
// Measured 2026-09-29: 143 descriptions totalling 34,021 bytes (~8,505
// tokens at the ~bytes/4 heuristic). That is already well over the 16,000-byte
// target for slim ads, so per policy the cap is set to measured + 10%
// (34,021 x 1.10 = 37,423 -> 37,500) so the gate passes today but catches
// further growth while the descriptions are slimmed toward the target.
const maxPluginAdTotalBytes = 37500

// maxPluginDescBytes guards against a single bloated description: any one
// tool's description must stay under 400 bytes (the plugin's ads are the
// agent-facing surface; the catalog's per-tool cap is 300 and the plugin
// should stay in the same order, not per-tool unbounded).
const maxPluginDescBytes = 400

// minPluginDescCount is a parser-drift guard: the plugin currently defines
// 143 descriptions. If extraction suddenly finds far fewer, the parser (not
// the plugin) broke, and a silent zero would make the budget gate vacuous.
const minPluginDescCount = 100

// TestPluginAdTokensBudget loads .opencode/plugins/kern.ts, extracts every
// `description:` string literal from the tool({...}) definitions, sums their
// byte lengths, and asserts the total stays under the drift-gate cap. It also
// enforces the per-description byte ceiling. The measured total is printed on
// every run.
func TestPluginAdTokensBudget(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(pluginAdFile)
	if err != nil {
		t.Fatalf("read %s: %v", pluginAdFile, err)
	}
	descs := extractPluginDescriptions(string(raw))
	if len(descs) < minPluginDescCount {
		t.Fatalf("extracted only %d descriptions from %s (expected >= %d) — parser drift or the plugin was restructured; update the extractor",
			len(descs), pluginAdFile, minPluginDescCount)
	}

	total := 0
	maxBytes := 0
	for _, d := range descs {
		n := len(d)
		total += n
		if n > maxBytes {
			maxBytes = n
		}
		if n > maxPluginDescBytes {
			t.Errorf("plugin tool description = %d bytes, exceeds per-description cap %d: %q...",
				n, maxPluginDescBytes, clipPluginDesc(d, 80))
		}
	}

	t.Logf("plugin advertisement total: %d bytes across %d descriptions (~%d tokens at bytes/4); largest single description: %d bytes",
		total, len(descs), total/4, maxBytes)
	if total > maxPluginAdTotalBytes {
		t.Errorf("plugin advertisement = %d bytes (%d descriptions), exceeds drift-gate cap %d — every opencode agent pays this at startup",
			total, len(descs), maxPluginAdTotalBytes)
	}
}

// extractPluginDescriptions pulls the string literal following every
// `description:` key in the plugin source. Literals are double-quoted (may
// span lines, may be followed on the same line by more code) or backtick
// template strings; the raw content between the delimiters is returned with
// escapes left as-is. The scan is character-based, not line-based, because
// the plugin has description literals whose closing quote is followed on the
// same line by `args: {`.
func extractPluginDescriptions(src string) []string {
	var descs []string
	rest := src
	for {
		idx := strings.Index(rest, "description:")
		if idx < 0 {
			return descs
		}
		i := idx + len("description:")
		for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t' || rest[i] == '\r' || rest[i] == '\n') {
			i++
		}
		if i >= len(rest) {
			return descs
		}
		switch rest[i] {
		case '"':
			j := i + 1
			for j < len(rest) {
				if rest[j] == '\\' {
					j += 2
					continue
				}
				if rest[j] == '"' {
					break
				}
				j++
			}
			if j >= len(rest) {
				return descs
			}
			descs = append(descs, rest[i+1:j])
			rest = rest[j+1:]
		case '`':
			j := strings.IndexByte(rest[i+1:], '`')
			if j < 0 {
				return descs
			}
			j += i + 1
			descs = append(descs, rest[i+1:j])
			rest = rest[j+1:]
		default:
			// Not a string literal (e.g. a computed value); skip past the key.
			rest = rest[i:]
		}
	}
}

// clipPluginDesc truncates s to at most max bytes for error messages.
func clipPluginDesc(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
