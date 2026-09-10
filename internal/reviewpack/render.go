package reviewpack

import (
	"fmt"
	"strings"
)

// RenderPack renders the pack as house-style, deterministic text. The
// token-count basis is renderBody (below): TokenCount counts every line
// except the self-referential footer, so the number is stable and exact.
func RenderPack(p *ReviewPack) string {
	var b strings.Builder
	b.WriteString(renderBody(p))
	fmt.Fprintf(&b, "=== tokens: %d total (%s) ===\n", p.TokenCount, sectionSummary(p.Sections))
	fmt.Fprintf(&b, "content hash: %s\n", p.ContentHash)
	return b.String()
}

// renderBody is the canonical token-count basis: header + all sections.
// It never references TokenCount or ContentHash, so counting it cannot
// depend on the numbers it produces.
func renderBody(p *ReviewPack) string {
	var b strings.Builder
	fmt.Fprintf(&b, "== review pack ==\n")
	fmt.Fprintf(&b, "task: %s\n", p.Task)
	if p.Lens != "" {
		fmt.Fprintf(&b, "lens: %s\n", p.Lens)
	}
	if p.Commit != "" {
		fmt.Fprintf(&b, "commit: %s (dirty %s)\n", p.Commit, shortHex(p.DirtyHash))
	}
	fmt.Fprintf(&b, "changed files (%d): %s\n", len(p.ChangedFiles), strings.Join(p.ChangedFiles, ", "))
	if p.DiffStat != "" {
		fmt.Fprintf(&b, "diff stat:\n%s\n", p.DiffStat)
	}
	if p.DiffPreview != "" {
		fmt.Fprintf(&b, "diff preview:\n%s\n", p.DiffPreview)
	}
	b.WriteString(renderEvidence(p))
	b.WriteString(renderSymbols(p))
	b.WriteString(renderTests(p))
	b.WriteString(renderConstraints(p))
	b.WriteString(renderClaims(p))
	b.WriteString(renderAssumptions(p))
	return b.String()
}

func renderEvidence(p *ReviewPack) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== evidence (%d) ===\n", len(p.Evidence))
	for i, s := range p.Evidence {
		content := strings.ReplaceAll(s.Content, "\n", " ")
		fmt.Fprintf(&b, "%d %s ~%dtok — %s — %s\n", i+1, s.Type, s.Tokens, s.Reason, content)
	}
	return b.String()
}

func renderSymbols(p *ReviewPack) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== symbols (%d) ===\n", len(p.Symbols))
	for _, s := range p.Symbols {
		fmt.Fprintf(&b, "%s (%s:%d) callers=%d callees=%d blast=%d\n",
			s.Name, s.File, s.Line, s.Callers, s.Callees, s.BlastRadius)
		if len(s.Path) > 0 {
			fmt.Fprintf(&b, "  path: %s\n", strings.Join(s.Path, " -> "))
		}
		if len(s.BlastFiles) > 0 {
			fmt.Fprintf(&b, "  blast files: %s\n", strings.Join(s.BlastFiles, ", "))
		}
	}
	return b.String()
}

func renderTests(p *ReviewPack) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== tests (%d) ===\n", len(p.Tests))
	for _, t := range p.Tests {
		if t.Kind == "gap" {
			fmt.Fprintf(&b, "gap: %s (%s) %s:%d callers=%d\n", t.Symbol, t.Kind, t.File, t.Line, t.Callers)
		} else {
			fmt.Fprintf(&b, "changed: %s\n", t.File)
		}
	}
	return b.String()
}

func renderConstraints(p *ReviewPack) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== constraints (%d) ===\n", len(p.Constraints))
	for _, c := range p.Constraints {
		if c.Rule != "" {
			fmt.Fprintf(&b, "- %s: %s\n", c.Name, c.Rule)
		} else {
			fmt.Fprintf(&b, "- %s\n", c.Name)
		}
	}
	return b.String()
}

func renderClaims(p *ReviewPack) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== claims — observed (%d) ===\n", len(p.Claims))
	for _, c := range p.Claims {
		fmt.Fprintf(&b, "[%s/%s] %s (evidence %d)\n", c.Type, c.Status, c.Statement, c.Evidence)
	}
	return b.String()
}

func renderAssumptions(p *ReviewPack) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== assumptions — unverified (%d) ===\n", len(p.Assumptions))
	for _, c := range p.Assumptions {
		fmt.Fprintf(&b, "[%s/%s] %s (evidence %d)\n", c.Type, c.Status, c.Statement, c.Evidence)
	}
	return b.String()
}

func sectionSummary(secs []Section) string {
	parts := make([]string, 0, len(secs))
	for _, s := range secs {
		parts = append(parts, fmt.Sprintf("%s %d", s.Name, s.Tokens))
	}
	return strings.Join(parts, ", ")
}

func shortHex(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}
