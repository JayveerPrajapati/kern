// reasons.go holds the deny reason strings, byte-parity with the script's
// (the parity battery pins them). They are the ONLY user-facing deny texts
// the Go hook emits; the script's copies stay frozen until it is retired.
package guardhook

import (
	"fmt"
	"strings"
)

func readDenyReason(subjHash string) string {
	return "Use kern_explore with the file path as symbol (symbolic summary; add tier=full for the verbatim file, or start_line/end_line for a line window) instead of the built-in read; kern_compact_file does the same when KERN_MCP_FULL=1. Call kern_explore with {\"symbol\":\"<filepath>\"}. Set KERN_ENFORCE=0 to disable this guard. Operator allow-once: kern guard allow " + subjHash + " (tty-gated), then retry."
}

func grepDenyReason(subjHash string) string {
	return "Use kern_search (code symbols — patterns are SYMBOL queries, not regex) instead of the built-in grep: call it with a symbol-name pattern like {\"pattern\":\"funcName\"}. For true regex search run bash: kern ast <pattern> (CLI; wildcards + kind prefixes) — a plain bash grep is denied by the same guard, not an escape (operator break-glass only: host env KERN_BYPASS=1). For docs use kern_doc (action=search; KERN_MCP_FULL=1). Set KERN_ENFORCE=0 to disable this guard. Operator allow-once: kern guard allow " + subjHash + " (tty-gated), then retry."
}

func globDenyReason() string {
	return "Use kern_buddy (repo layout + entry points digest; on the default MCP surface) instead of the built-in glob, or kern_project_map (compressed symbol map) when KERN_MCP_FULL=1. To locate a file by name ask kern_meta (request=find file <name>). Escapes: KERN_BYPASS=1 / KERN_ENFORCE=0."
}

func gateDenyReason(blockedCmd, subjHash, root string) string {
	return fmt.Sprintf("kern guard: blocked '%s' — GATE command (write/destructive/network/installer). Ask the operator, then retry byte-identical: run `kern guard grant '<the exact blocked command>'` (mints approval appr-<id>), the operator answers `kern approval ask appr-<id> --root %s` with [1] accept once / [2] accept prefix this session / [3] reject, await the decision with `kern approval wait appr-<id> --timeout 30m`, then retry the exact command. Escapes: KERN_BYPASS=1 / KERN_ENFORCE=0. Operator allow-once: kern guard allow %s (tty-gated), then retry.", blockedCmd, root, subjHash)
}

func governedDenyReason(blockedCmd, subjHash, repl string) string {
	return fmt.Sprintf("kern guard: blocked '%s' — not in the passthrough set; use %s instead (build/test: kern_verify, unsure: kern_meta). To RUN it governed (verification + audit), re-run as: kern build '<the exact command>' (build/test loops: kern verify --command '<the exact command>' --output summary). Compounds/pipes: split into single commands — each segment is classified separately. Escapes: KERN_BYPASS=1 / KERN_ENFORCE=0. Operator allow-once: kern guard allow %s (tty-gated), then retry.", blockedCmd, repl, subjHash)
}

// replacementFor maps a governed command's first token to the kern tool
// replacement named in the deny reason.
func replacementFor(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return "kern_meta (request=<what you need>)"
	}
	switch fields[0] {
	case "grep", "rg", "ag":
		return "kern_search (query=<symbol>)"
	case "cat", "head", "tail", "sed", "awk", "less", "more":
		return "kern_explore (symbol=<file path>, tier=full, start_line/end_line)"
	case "find":
		return "kern_buddy"
	case "go", "make":
		return "kern_verify (command=<cmd>, output=summary)"
	}
	return "kern_meta (request=<what you need>)"
}
