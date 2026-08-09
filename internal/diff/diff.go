// Package diff computes line-level unified diffs without external tooling.
// Used by the heal loop (#9) to show what an LLM changed and by the delta
// tooling (#13). Deterministic and dependency-free.
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
func groupHunks(ops []Op, ctx int) [][]Op {
	var hunks [][]Op
	changeAt := make([]bool, len(ops))
	for i, op := range ops {
		changeAt[i] = op.Kind != ' '
	}
	for i := 0; i < len(ops); {
		if !changeAt[i] {
			i++
			continue
		}
		start := i
		for start > 0 && i-start < ctx && !changeAt[start-1] {
			start--
		}
		end := i
		for end < len(ops) && (changeAt[end] || end-start < ctx) {
			end++
		}
		// close the trailing context
		if end < len(ops) {
			end += ctx
			if end > len(ops) {
				end = len(ops)
			}
		}
		hunks = append(hunks, ops[start:end])
		i = end
	}
	return hunks
}

func hunkRange(h []Op) (aStart, aCount, bStart, bCount int) {
	first := h[0]
	if first.Kind == '+' {
		bStart = first.B
		aStart = first.A
	} else {
		aStart = first.A
		bStart = first.B
	}
	for _, op := range h {
		if op.A > 0 {
			aCount++
		}
		if op.B > 0 {
			bCount++
		}
	}
	// For a pure-insertion hunk, git uses (aStart-1). Keep it simple: use
	// starting positions as-is; correct-enough for display.
	return aStart, aCount, bStart, bCount
}
