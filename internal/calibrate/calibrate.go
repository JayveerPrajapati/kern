// Package calibrate measures how well kern's review model predicts the files
// a commit actually changed — the same F1-style protocol code-review-graph
// uses to publish its calibration numbers.
//
// It reports two complementary tables:
// 1. Threshold sweep. kern_changes assigns every changed file a risk score.
// "Flagging" a file means the reviewer should look at it; the sweep shows
// recall (fraction of changed files the model still flags) and the mean
// review load (flagged files per commit) at each threshold. This is the
// honest calibration of the risk scale: there is no precision here,
// because the analysis is diff-driven and can never flag a file the
// commit did not touch.
// 2. Impact F1 (CRG protocol). Given the symbols a commit touched, the graph
// predicts which files are affected (their blast radius). The ground
// truth is the set of files the commit actually edited. Precision =
// predicted files that were really edited; recall = edited files the
// graph predicted. This measures how well the call graph anticipates
// ripple edits.
// The root defaults to this repository (self-calibration on kern's own PR
// history). Requires a git checkout with a populated history.
package calibrate

import (
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// Run executes the calibration harness and writes its report to w.
// root must be a git checkout with populated history. commitsN caps how many
// recent commits are scored; thresholds is the risk-threshold sweep. The
// output format is stable — calibration numbers must stay comparable across
// versions.
func Run(root string, commitsN int, thresholds []float64, w io.Writer) error {
	thr := append([]float64(nil), thresholds...)
	sort.Float64s(thr)

	ix, err := index.Load(root)
	if err != nil || ix == nil {
		ix, err = index.Build(root)
		if err != nil {
			return fmt.Errorf("index: %v", err)
		}
		_ = ix.Save()
	}

	out, err := exec.Command("git", append([]string{"-C", root}, "rev-list", "--max-count", fmt.Sprint(commitsN), "HEAD")...).Output()
	if err != nil {
		return fmt.Errorf("rev-list: %v", err)
	}
	commits := nonEmptyLines(string(out))

	flagged := make([]int, len(thr))  // files flagged at each threshold
	recalled := make([]int, len(thr)) // flagged files that were actually changed
	scored := 0
	skipped := 0
	changedTotal := 0
	var risks []float64

	var tp, fp, fn int // impact-F1 aggregates
	var f1Commits int  // commits with a nonzero predicted set

	for _, c := range commits {
		fc, err := intel.FilesForRangeL(root, c+"^", c)
		if err != nil {
			skipped++
			continue
		}
		report := intel.AnalyzeChangesRanged(ix, fc)
		if len(report.Changes) == 0 {
			skipped++
			continue
		}
		scored++

		ground := map[string]bool{}
		affected := map[string]bool{}
		for _, ch := range report.Changes {
			ground[ch.File] = true
			changedTotal++
			risks = append(risks, ch.Risk)
			for i, t := range thr {
				if ch.Risk >= t {
					flagged[i]++
					recalled[i]++
				}
			}
			_, blast := intel.BlastRadius(ix, ch.Symbols)
			for s := range blast {
				for _, f := range intel.AffectedFiles(ix, []string{s}) {
					affected[f] = true
				}
			}
		}
		for _, f := range report.Files {
			affected[f] = true
		}

		ctp, cfp, cfn := 0, 0, 0
		for f := range ground {
			if affected[f] {
				ctp++
			} else {
				cfn++
			}
		}
		for f := range affected {
			if !ground[f] {
				cfp++
			}
		}
		tp, fp, fn = tp+ctp, fp+cfp, fn+cfn
		if len(affected) > 0 {
			f1Commits++
		}
	}

	fmt.Fprintf(w, "root:        %s\n", root)
	fmt.Fprintf(w, "commits:     %d scored, %d skipped (no indexable changes)\n", scored, skipped)

	fmt.Fprintf(w, "\n1) risk-threshold calibration (review load vs recall)\n")
	fmt.Fprintf(w, "%-10s %-12s %-16s\n", "threshold", "recall", "mean flagged/commit")
	for i, t := range thr {
		fmt.Fprintf(w, "%-10.1f %-12.3f %-16.2f\n", t, prec(recalled[i], changedTotal), float64(flagged[i])/float64(scored))
	}

	p := prec(tp, tp+fp)
	r := prec(tp, tp+fn)
	f1 := 0.0
	if p+r > 0 {
		f1 = 2 * p * r / (p + r)
	}
	fmt.Fprintf(w, "\n2) impact F1 (blast radius vs files actually edited)\n")
	fmt.Fprintf(w, "precision=%.3f recall=%.3f F1=%.3f (commits with nonzero predicted set: %d/%d)\n", p, r, f1, f1Commits, scored)

	fmt.Fprintln(w, "\nrisk distribution (across all scored changes):")
	for _, b := range histogram(risks) {
		fmt.Fprintf(w, "  [%4.1f, %4.1f) %5d\n", b.lo, b.hi, b.n)
	}
	fmt.Fprintln(w, `
The threshold with the best recall-vs-load tradeoff on this repo's history is
the calibration point for the risk scale. Pick the knee, or keep the default
4.0 (base 1.0 + log2 callers + log2 transitive blast + 1.5 cross-pkg + 2.0
untested + hub bonuses). Impact F1 is the graph's own error budget: it is what
a review would miss (unpredicted files) and what it would over-flag (predicted
but unedited).`)
	return nil
}

func prec(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func nonEmptyLines(s string) []string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

type bin struct {
	lo, hi float64
	n      int
}

func histogram(risks []float64) []bin {
	if len(risks) == 0 {
		return nil
	}
	sort.Float64s(risks)
	max := risks[len(risks)-1]
	width := 2.0
	var out []bin
	for lo := 0.0; lo < max+width; lo += width {
		hi := lo + width
		n := 0
		for _, r := range risks {
			if r >= lo && r < hi {
				n++
			}
		}
		if n > 0 || len(out) > 0 {
			out = append(out, bin{lo, hi, n})
		}
	}
	return out
}
