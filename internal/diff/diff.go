// Package diff computes line-level unified diffs without external tooling.
// Deterministic and dependency-free.
package diff

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Op is one line of an edit script.
type Op struct {
	Kind byte // ' ', '-', '+'
	A, B int  // 1-based line numbers in the source sides (0 = none)
	Text string
}

// DiffLines computes the edit script turning a into b (classic LCS DP).
// For very large inputs it degrades to a single replace block.
func DiffLines(a, b []string) []Op {
	n, m := len(a), len(b)
	const maxCells = 5_000_000
	if n < 0 || m < 0 || int64(n) > maxCells || int64(m) > maxCells || int64(n)+int64(m) > maxCells || int64(n)*int64(m) > maxCells {
		// Coarse fallback: whole-file replace.
		capAlloc := 0
		if total := int64(n) + int64(m); total > 0 && total <= maxCells {
			capAlloc = int(total)
		}
		ops := make([]Op, 0, capAlloc)
		for i, l := range a {
			ops = append(ops, Op{Kind: '-', A: i + 1, Text: l})
		}
		for j, l := range b {
			ops = append(ops, Op{Kind: '+', B: j + 1, Text: l})
		}
		return ops
	}

	// dp[i][j] = LCS length of a[:i], b[:j]. Rows rolled for memory, but we
	// need full table for backtrack, so keep [n+1][m+1].
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	capOps := 0
	if total := int64(n) + int64(m); total > 0 && total <= maxCells {
		capOps = int(total)
	}
	ops := make([]Op, 0, capOps)
	i, j := n, m
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			ops = append(ops, Op{Kind: ' ', A: i, B: j, Text: a[i-1]})
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			ops = append(ops, Op{Kind: '-', A: i, Text: a[i-1]})
			i--
		} else {
			ops = append(ops, Op{Kind: '+', B: j, Text: b[j-1]})
			j--
		}
	}
	for ; i > 0; i-- {
		ops = append(ops, Op{Kind: '-', A: i, Text: a[i-1]})
	}
	for ; j > 0; j-- {
		ops = append(ops, Op{Kind: '+', B: j, Text: b[j-1]})
	}
	// Reverse to ascending order.
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}
	return ops
}

// Unified renders ops as a standard unified diff with the given headers and
// 3 lines of context per hunk. Returns "" when there is no difference.
func Unified(aPath, bPath string, a, b []string) string {
	ops := DiffLines(a, b)
	aLines, bLines := countLines(a), countLines(b)
	// Drop the trailing split artifact ("a\nb\n" -> ["a","b",""]) so hunks
	// never count or emit padding past the real end of file.
	ops = trimPastEOF(ops, aLines, bLines)
	hasChange := false
	for _, op := range ops {
		if op.Kind != ' ' {
			hasChange = true
			break
		}
	}
	if !hasChange {
		return ""
	}
	var bd strings.Builder
	bd.WriteString("--- a/" + labelPath(aPath) + "\n")
	bd.WriteString("+++ b/" + labelPath(bPath) + "\n")
	const ctx = 3
	hunks := groupHunks(ops, ctx)
	for _, h := range hunks {
		aStart, aCount, bStart, bCount := hunkRange(h, aLines, bLines)
		fmt.Fprintf(&bd, "@@ -%d,%d +%d,%d @@\n", aStart, aCount, bStart, bCount)
		normalizeHunkOrder(h)
		for _, op := range h {
			bd.WriteByte(op.Kind)
			if op.Kind == ' ' {
				bd.WriteByte(' ')
			}
			bd.WriteString(op.Text)
			bd.WriteByte('\n')
		}
	}
	return bd.String()
}

// countLines reports the number of real lines a split representation has,
// ignoring the single trailing empty element that strings.Split produces for
// a trailing newline ("a\nb\n" -> ["a","b",""] has 2 real lines). An empty
// input ([""]) reports 0 lines.
func countLines(s []string) int {
	if n := len(s); n > 0 && s[n-1] == "" {
		return n - 1
	}
	return len(s)
}

// trimPastEOF drops ops that reference a line beyond the real end of either
// file (the trailing split artifact after a final newline), so hunk counts
// and content never pad past EOF.
func trimPastEOF(ops []Op, aLines, bLines int) []Op {
	filtered := make([]Op, 0, len(ops))
	for _, op := range ops {
		if op.A > aLines || op.B > bLines {
			continue
		}
		filtered = append(filtered, op)
	}
	return filtered
}

// normalizeHunkOrder reorders each contiguous change region of a hunk so all
// '-' (deletion) ops precede '+' (insertion) ops. The LCS backtrack can emit
// a replacement region as '+' then '-'; strict patch consumers expect
// deletions before insertions within a region.
func normalizeHunkOrder(h []Op) {
	i := 0
	for i < len(h) {
		if h[i].Kind == ' ' {
			i++
			continue
		}
		j := i
		for j < len(h) && h[j].Kind != ' ' {
			j++
		}
		region := make([]Op, 0, j-i)
		for k := i; k < j; k++ {
			if h[k].Kind == '-' {
				region = append(region, h[k])
			}
		}
		for k := i; k < j; k++ {
			if h[k].Kind == '+' {
				region = append(region, h[k])
			}
		}
		copy(h[i:j], region)
		i = j
	}
}

// labelPath normalizes a path for a unified-diff header so absolute paths do
// not render as "a//tmp/fa" and "./" prefixes are dropped.
func labelPath(p string) string {
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimLeft(p, "/")
	return p
}

// groupHunks splits ops into hunks of change-regions padded with context.
// Consecutive change clusters separated by <= 2*ctx unchanged lines are merged
// into one hunk (git's rule).
func groupHunks(ops []Op, ctx int) [][]Op {
	var hunks [][]Op
	var first, last int
	hasChange := false
	needStart := true
	for i, op := range ops {
		if op.Kind != ' ' {
			if needStart {
				first = i
				needStart = false
			}
			last = i
			hasChange = true
			continue
		}
		if !hasChange {
			continue
		}
		// unchanged line: does it fall within trailing context of the
		// current cluster, or start a gap big enough to close the hunk?
		if i-last > 2*ctx {
			hunks = append(hunks, hunkSpan(ops, first, last, ctx))
			needStart = true
			hasChange = false
		}
	}
	if hasChange {
		hunks = append(hunks, hunkSpan(ops, first, last, ctx))
	}
	return hunks
}

// hunkSpan returns the ops of one hunk, clamped to context lines around the
// change cluster [first,last].
func hunkSpan(ops []Op, first, last, ctx int) []Op {
	start := first - ctx
	if start < 0 {
		start = 0
	}
	end := last + ctx + 1
	if end > len(ops) {
		end = len(ops)
	}
	return ops[start:end]
}

// hunkRange computes (aStart,aCount,bStart,bCount) for a hunk. Start lines are
// the first touched line on each side; a side with no lines is reported as
// start=0 (git's "from/to empty" convention). Counts are clamped to the real
// file line counts so a hunk never claims more lines than the file has.
func hunkRange(h []Op, aLines, bLines int) (aStart, aCount, bStart, bCount int) {
	for _, op := range h {
		if op.A > 0 {
			aCount++
		}
		if op.B > 0 {
			bCount++
		}
	}
	for _, op := range h {
		if op.A > 0 {
			aStart = op.A
			break
		}
	}
	for _, op := range h {
		if op.B > 0 {
			bStart = op.B
			break
		}
	}
	if aCount > aLines {
		aCount = aLines
	}
	if bCount > bLines {
		bCount = bLines
	}
	if aCount == 0 {
		aStart = 0
	}
	if bCount == 0 {
		bStart = 0
	}
	return aStart, aCount, bStart, bCount
}

// SpanResolver returns the enclosing symbol's display name for a line of a
// file, or "" when unknown. Used by Compact to annotate collapsed context runs.
type SpanResolver func(file string, line int) string

// Compact renders a compacted, VIEW-ONLY diff: unchanged context runs longer
// than 2 lines collapse into a single annotation line, preserving every
// changed line verbatim. The output is NOT a valid patch — it exists for
// reading/agent consumption only.
func Compact(aPath, bPath string, a, b []string, resolve SpanResolver) string {
	ops := DiffLines(a, b)
	hasChange := false
	for _, op := range ops {
		if op.Kind != ' ' {
			hasChange = true
			break
		}
	}
	if !hasChange {
		return ""
	}
	if resolve == nil {
		resolve = func(string, int) string { return "" }
	}
	var bd strings.Builder
	bd.WriteString("--- a/" + labelPath(aPath) + "\n")
	bd.WriteString("+++ b/" + labelPath(bPath) + "\n")
	var run []Op
	flush := func() {
		n := len(run)
		if n <= 2 {
			for _, op := range run {
				bd.WriteString(" " + op.Text + "\n")
			}
			run = nil
			return
		}
		// Collapse the run into one annotation line. The line number used for
		// span resolution is the A value of the run's first op.
		ann := fmt.Sprintf(" ... %d lines unchanged", n)
		if span := resolve(aPath, run[0].A); span != "" {
			ann += " in " + span
		}
		bd.WriteString(ann + " ...\n")
		run = nil
	}
	for _, op := range ops {
		if op.Kind == ' ' {
			run = append(run, op)
			continue
		}
		flush()
		bd.WriteString(string(op.Kind) + op.Text + "\n")
	}
	flush()
	return bd.String()
}

// SymbolDiffEntry isolates the structural diff for a single enclosing symbol or file section.
type SymbolDiffEntry struct {
	Symbol string `json:"symbol"`  // enclosing symbol name or "(file)"
	File   string `json:"file"`    // file path
	StartA int    `json:"start_a"` // first line modified in A
	EndA   int    `json:"end_a"`   // last line modified in A
	StartB int    `json:"start_b"` // first line modified in B
	EndB   int    `json:"end_b"`   // last line modified in B
	Hunks  string `json:"hunks"`   // symbol-scoped unified diff snippet
}

// TreeDiff extracts symbol-scoped diff entries across files for surgical agent edits.
func TreeDiff(aPath, bPath string, a, b []string, resolve SpanResolver) []SymbolDiffEntry {
	ops := DiffLines(a, b)
	if len(ops) == 0 {
		return nil
	}
	if resolve == nil {
		resolve = func(string, int) string { return "" }
	}
	var entries []SymbolDiffEntry
	bySymbol := make(map[string][]Op)
	var order []string

	for _, op := range ops {
		if op.Kind == ' ' {
			continue
		}
		sym := resolve(aPath, op.A)
		if sym == "" {
			sym = resolve(bPath, op.B)
		}
		if sym == "" {
			sym = "(file)"
		}
		if _, seen := bySymbol[sym]; !seen {
			order = append(order, sym)
		}
		bySymbol[sym] = append(bySymbol[sym], op)
	}

	for _, sym := range order {
		sOps := bySymbol[sym]
		if len(sOps) == 0 {
			continue
		}
		var minA, maxA, minB, maxB int
		var bld strings.Builder
		bld.WriteString(fmt.Sprintf("@@ %s @@\n", sym))
		for _, op := range sOps {
			if op.A > 0 {
				if minA == 0 || op.A < minA {
					minA = op.A
				}
				if op.A > maxA {
					maxA = op.A
				}
			}
			if op.B > 0 {
				if minB == 0 || op.B < minB {
					minB = op.B
				}
				if op.B > maxB {
					maxB = op.B
				}
			}
			bld.WriteString(fmt.Sprintf("%c%s\n", op.Kind, op.Text))
		}
		entries = append(entries, SymbolDiffEntry{
			Symbol: sym,
			File:   aPath,
			StartA: minA,
			EndA:   maxA,
			StartB: minB,
			EndB:   maxB,
			Hunks:  bld.String(),
		})
	}
	return entries
}
