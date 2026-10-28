package mcp

// slim.go implements KERN_MCP_SLIM=1: a compact advertisement of the catalog
// for hosts with a tight context budget.
//
// The budget (8 KB) cannot be met by shortening prose alone: 32 fully
// documented tools are ~40 KB and their JSON schemas alone are ~16 KB even with
// every description stripped. Meeting it honestly means advertising FEWER
// tools, which is a product decision, so this mode is opt-in:
//
//   - only the core read/lookup surface is advertised, and
//   - each survivor keeps its full schema but loses prose (first sentence,
//     property descriptions dropped).
//
// kern_meta stays advertised and its (slim) description points at the router,
// so every other tool is still reachable — it just costs no context until the
// agent needs it.

import (
	"os"

	catalog "github.com/JayveerPrajapati/kern/internal/mcpcatalog"
)

// slimSurface is the core set advertised in slim mode. Everything else stays
// callable through kern_meta (which routes to all tools internally).
var slimSurface = []string{
	"kern_meta",
	"kern_search",
	"kern_explore",
	"kern_context",
	"kern_impact",
	"kern_graph",
	"kern_verify",
	"kern_memory",
}

// slimEnabled reports whether the compact advertisement is requested.
func slimEnabled() bool {
	return os.Getenv("KERN_MCP_SLIM") == "1"
}

// slimTools returns the compact catalog: the core surface only, descriptions
// shortened to their first sentence, schema property descriptions dropped.
func slimTools(ts []catalog.Tool) []catalog.Tool {
	keep := make(map[string]bool, len(slimSurface))
	for _, n := range slimSurface {
		keep[n] = true
	}
	out := make([]catalog.Tool, 0, len(slimSurface))
	for _, t := range ts {
		if !keep[t.Name] {
			continue
		}
		t.Description = firstSentence(t.Description, 140)
		if t.Name == "kern_meta" {
			t.Description += " Routes to every kern tool (the rest are not advertised in slim mode)."
		}
		if slimmed, ok := dropSchemaDescriptions(t.InputSchema).(map[string]any); ok {
			t.InputSchema = slimmed
		}
		out = append(out, t)
	}
	return out
}

// firstSentence returns s up to (and including) its first sentence terminator,
// clipped to limit bytes. Only a mid-sentence clip is marked with an ellipsis.
func firstSentence(s string, limit int) string {
	cut, clipped := len(s), false
	for i, r := range s {
		if r == '.' || r == '!' || r == '?' {
			if i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '\n') {
				cut = i + 1
				break
			}
		}
	}
	if cut > limit {
		cut, clipped = limit, true
	}
	if clipped {
		return trimSpaceASCII(s[:cut]) + "…"
	}
	return trimSpaceASCII(s[:cut])
}

func trimSpaceASCII(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}

// dropSchemaDescriptions returns a copy of a JSON-schema object with every
// prose key ("description", "title", "$comment") removed, recursively. Names,
// types, enum values, required lists and defaults are preserved, so the call
// contract is unchanged.
func dropSchemaDescriptions(v any) any {
	switch node := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(node))
		for k, val := range node {
			if k == "description" || k == "$comment" || k == "title" {
				continue
			}
			out[k] = dropSchemaDescriptions(val)
		}
		return out
	case []any:
		out := make([]any, len(node))
		for i, item := range node {
			out[i] = dropSchemaDescriptions(item)
		}
		return out
	default:
		return v
	}
}
