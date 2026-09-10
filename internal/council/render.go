package council

import (
	"fmt"
	"strings"
)

// RenderReport renders the council report as house-style deterministic text.
func RenderReport(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "== council report ==\n")
	fmt.Fprintf(&b, "packs (%d): %s\n", len(r.Packs), strings.Join(r.Packs, ", "))

	fmt.Fprintf(&b, "=== consensus (%d) ===\n", len(r.Consensus))
	for _, c := range r.Consensus {
		fmt.Fprintf(&b, "[%s/%s] %s (evidence %d, packs %s)\n",
			c.Type, c.Status, c.Statement, c.Evidence, strings.Join(c.Packs, ", "))
	}

	fmt.Fprintf(&b, "=== divergence (%d) ===\n", len(r.Divergence))
	for _, d := range r.Divergence {
		fmt.Fprintf(&b, "%s — classified %s across %s\n",
			d.Statement, strings.Join(d.Kinds, ", "), strings.Join(d.Packs, ", "))
	}

	fmt.Fprintf(&b, "=== minority positions (%d) ===\n", len(r.Minority))
	for _, m := range r.Minority {
		fmt.Fprintf(&b, "[%s/%s] %s (evidence %d, pack %s)\n",
			m.Type, m.Status, m.Statement, m.Evidence, m.Pack)
	}

	fmt.Fprintf(&b, "=== supporting evidence (%d) ===\n", len(r.Supporting))
	for _, s := range r.Supporting {
		fmt.Fprintf(&b, "%s (evidence %d across %d packs)\n", s.Statement, s.Evidence, s.Packs)
	}

	fmt.Fprintf(&b, "=== unsupported claims (%d) ===\n", len(r.Unsupported))
	for _, u := range r.Unsupported {
		fmt.Fprintf(&b, "[%s/%s] %s (packs %s)\n",
			u.Type, u.Status, u.Statement, strings.Join(u.Packs, ", "))
	}

	fmt.Fprintf(&b, "=== assumptions (%d) ===\n", len(r.Assumptions))
	for _, a := range r.Assumptions {
		fmt.Fprintf(&b, "[%s/%s] %s (packs %s)\n",
			a.Type, a.Status, a.Statement, strings.Join(a.Packs, ", "))
	}

	fmt.Fprintf(&b, "=== decision drivers (%d) ===\n", len(r.Drivers))
	for i, d := range r.Drivers {
		fmt.Fprintf(&b, "%d %s (evidence %d, packs %d)\n", i+1, d.Statement, d.Evidence, d.Packs)
	}

	fmt.Fprintf(&b, "=== next verification (%d) ===\n", len(r.NextVerification))
	for _, n := range r.NextVerification {
		fmt.Fprintf(&b, "%s: %s (%s)\n", n.Action, n.Target, n.Why)
	}
	return b.String()
}
