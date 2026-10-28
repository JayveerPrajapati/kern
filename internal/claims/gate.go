package claims

import (
	"strconv"

	"github.com/JayveerPrajapati/kern/internal/classparity"
)

const (
	gateClaimID = "guard-gate-corpus"
	gateClaim   = "destructive command forms are gated by both classifiers, benign ones by neither"
)

// claimGateCorpus proves the guard's central guarantee on the SAME corpus both
// production paths use (classparity.ParityCorpus): the bash-guard oracle
// (ClassifyCommandInRoots, enforced by the PreToolUse hook and the opencode
// plugin) and the governed-exec oracle (execclassify, enforced by kern_exec
// and `kern build`). Both halves matter — a command dangerous in only one
// oracle is a bypass, and a classifier that gates everything is unusable.
//
// This is the regression net for the 2026-10-26 finding that `find . -delete`
// classified as "serve" while `rm -rf ./internal` was allowed but `rm -rf
// internal` was gated: the claim is about agreement, not just about any single
// classifier.
func claimGateCorpus(root string) Result {
	checked, diffs := classparity.ClassifierParity(root, nil)
	if len(diffs) > 0 {
		evidence := make([]string, 0, len(diffs))
		for _, d := range diffs {
			evidence = append(evidence, d.String())
		}
		return fail(gateClaimID, gateClaim,
			"the bash-guard and governed-exec classifiers disagree", evidence...)
	}
	return pass(gateClaimID, gateClaim,
		"both classifiers agree on the whole corpus", []string{
			"checked " + strconv.Itoa(checked) + " commands: destructive forms gate in both oracles; benign forms gate in neither",
		}...)
}
