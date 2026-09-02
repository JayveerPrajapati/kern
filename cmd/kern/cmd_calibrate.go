package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/calibrate"
)

// runCalibrate measures how well blast-radius prediction matches git history:
// it scores recent commits against a risk-threshold sweep and reports the
// impact F1 of the call graph's predictions. It is the first-class form of
// the standalone evaluate/calibration harness — same flags, identical output.
func runCalibrate(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" && len(args) > 0 && args[0] != "" {
		root = args[0]
	}
	if root == "" {
		root = "."
	}

	thr := []float64{}
	for _, s := range strings.Split(f.thresholds, ",") {
		var v float64
		if _, err := fmt.Sscanf(strings.TrimSpace(s), "%f", &v); err != nil {
			fatalUsage("bad threshold: %s", s)
		}
		thr = append(thr, v)
	}

	if err := calibrate.Run(root, f.commits, thr, os.Stdout); err != nil {
		fatal("%v", err)
	}
}
