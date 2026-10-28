package classparity

import "testing"

// TestClassifierParityCorpus pins that the bash-guard oracle (guardrule,
// enforced by the PreToolUse hook and the opencode plugin) and the
// governed-exec oracle (execclassify, enforced by kern_exec and kern build)
// agree on every corpus command. A divergence is a real bypass: the agent
// routes the command through the surface that does not gate it.
func TestClassifierParityCorpus(t *testing.T) {
	checked, diffs := ClassifierParity(t.TempDir(), nil)
	if checked != len(ParityCorpus) {
		t.Fatalf("checked %d of %d corpus commands", checked, len(ParityCorpus))
	}
	for _, d := range diffs {
		t.Errorf("DIVERGE %s", d)
	}
}

// TestParityCorpusCoversBothHalves keeps the corpus honest in both directions:
// without destructive cases the claim is vacuous, and without benign cases a
// classifier that gates everything would pass.
func TestParityCorpusCoversBothHalves(t *testing.T) {
	var harmful, benign int
	for _, c := range ParityCorpus {
		if c.Harmful {
			harmful++
		} else {
			benign++
		}
	}
	if harmful < 10 || benign < 5 {
		t.Fatalf("corpus too thin: %d harmful, %d benign", harmful, benign)
	}
}

// TestGovernedParityCorpus pins the second parity mode (P0-4): commands the
// bash oracle routes to governance must be classified dangerous by the exec
// oracle, so `kern build` mints an approval instead of running them.
func TestGovernedParityCorpus(t *testing.T) {
	checked, diffs := GovernedParity(t.TempDir(), nil)
	if checked != len(GovernedParityCorpus) {
		t.Fatalf("checked %d of %d governed corpus commands", checked, len(GovernedParityCorpus))
	}
	for _, d := range diffs {
		t.Errorf("DIVERGE %s", d)
	}
}
