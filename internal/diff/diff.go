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
	if n*m > maxCells {
		// Coarse fallback: whole-file replace.
		ops := make([]Op, 0, n+m)
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
	ops := make([]Op, 0, n+m)
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
		aStart, aCount, bStart, bCount := hunkRange(h)
		fmt.Fprintf(&bd, "@@ -%d,%d +%d,%d @@\n", aStart, aCount, bStart, bCount)
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
// start=0 (git's "from/to empty" convention).
func hunkRange(h []Op) (aStart, aCount, bStart, bCount int) {
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
	if aCount == 0 {
		aStart = 0
	}
	if bCount == 0 {
		bStart = 0
	}
	return aStart, aCount, bStart, bCount
}
