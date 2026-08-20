// Package ids provides helpers for building collision-safe node IDs from
// untrusted external strings (service names, API paths, topics, etc.).
package ids

import "strings"

// Escape escapes ':' and '\' in s so it can be embedded in a colon-delimited
// node ID without aliasing distinct entities. A crafted value containing ':'
// can otherwise merge two distinct nodes into one ID. Escaping '\' first keeps
// the encoding reversible and unambiguous.
func Escape(s string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		`:`, `\:`,
	).Replace(s)
}