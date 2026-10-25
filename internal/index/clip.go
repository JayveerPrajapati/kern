package index

import "strings"

// ClipReflect bounds and sanitizes a value echoed into an operator-facing
// stderr line or response header: control runes are stripped and the result
// clipped, so an adversarial root path (or an error embedding one) cannot
// flood or smudge the log/response (payload-reflection hygiene; no execution
// path exists — the value is only ever printed). It lives in index (not
// strutil, whose LOC cap is at its ratchet) because every consumer sits on
// this package: the index persist-fallback line, the buddy digest's project
// header (internal/brief), and the buddy warm stderr line
// (internal/mcp/context). Exported for exactly those downstream sites.
func ClipReflect(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= max {
			b.WriteString("…")
			break
		}
	}
	return b.String()
}
