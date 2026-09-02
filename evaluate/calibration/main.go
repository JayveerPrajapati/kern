// Command calibration measures how well kern's review model predicts the files
// a commit actually changed — the same F1-style protocol code-review-graph
// uses to publish its calibration numbers.
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
// Usage:
// go run ./evaluate/calibration [root] [--commits N] [--thresholds a,b,c]
// The root defaults to this repository (self-calibration on kern's own PR
// history). Requires a git checkout with a populated history.
//
// `kern calibrate` is the first-class form of this harness; it exposes the
// same flags and produces identical output.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/calibrate"
)

func main() {
	commitsN := flag.Int("commits", 60, "number of recent commits to score")
	thresholds := flag.String("thresholds", "2.0,4.0,6.0,8.0", "comma-separated risk thresholds to sweep")
	flag.Parse()
	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	thr := []float64{}
	for _, s := range strings.Split(*thresholds, ",") {
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(s), "%f", &f); err != nil {
			fmt.Fprintln(os.Stderr, "bad threshold:", s)
			os.Exit(1)
		}
		thr = append(thr, f)
	}

	if err := calibrate.Run(root, *commitsN, thr, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
