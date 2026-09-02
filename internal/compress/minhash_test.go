package compress

import (
	"fmt"
	"strings"
	"testing"
)

// rep6 renders a 6× repeated family letter: distinct families differ in all
// six positions, so short template lines keep a pairwise ratio well under
// clusterMergeRatio and never cross-merge.
func rep6(f int) string {
	var c byte
	switch {
	case f < 26:
		c = byte('a' + f)
	default:
		c = byte('A' + f - 26)
	}
	return strings.Repeat(string(c), 6)
}

// tag3 renders i as a 3-char base-26 tag: distinct within a family while
// keeping the pairwise ratio above clusterMergeRatio so the fuzzy pass
// merges them.
func tag3(i int) string {
	b := make([]byte, 3)
	for j := 2; j >= 0; j-- {
		b[j] = byte('a' + i%26)
		i /= 26
	}
	return string(b)
}

// TestBandedPathClustersLargeLog drives a log far above the MinHash
// threshold through the banded path: 50 exact families (numeric variants
// normalize identically), 3 fuzzy families (distinct normalized forms, ratio
// above the merge threshold) and two noise lines that must survive.
func TestBandedPathClustersLargeLog(t *testing.T) {
	var lines []string
	for f := 0; f < 50; f++ {
		for i := 0; i < 100; i++ {
			lines = append(lines, fmt.Sprintf("ERROR sync %s record %d", rep6(f), i))
		}
	}
	words := []string{"disk", "cache", "index"}
	for f, word := range words {
		for i := 0; i < 30; i++ {
			lines = append(lines, fmt.Sprintf("ERROR %s block %s slow", word, tag3(1000*f+i)))
		}
	}
	lines = append(lines,
		"ERROR unexpected framing on socket foxtrot",
		"ERROR handshake mismatch tango",
	)
	log := strings.Join(lines, "\n")

	got := CompressLog(log, Options{Cluster: true, MaxLines: 100000})
	if got == "" {
		t.Fatal("empty output")
	}
	if n := strings.Count(got, "(repeated 100x)"); n != 50 {
		t.Fatalf("expected 50 exact-family markers, got %d; output head:\n%s", n, clipOut(got))
	}
	if n := strings.Count(got, "(repeated 30x)"); n != 3 {
		t.Fatalf("expected 3 fuzzy-family markers, got %d; output head:\n%s", n, clipOut(got))
	}
	for _, noise := range []string{"framing on socket foxtrot", "handshake mismatch tango"} {
		if !strings.Contains(got, noise) {
			t.Fatalf("noise line lost: %q; output head:\n%s", noise, clipOut(got))
		}
	}
}

// TestBandedPathDeterministic: the banded path must be byte-stable.
func TestBandedPathDeterministic(t *testing.T) {
	var lines []string
	for f := 0; f < 20; f++ {
		for i := 0; i < 40; i++ {
			lines = append(lines, fmt.Sprintf("ERROR sync %s record %d", rep6(f), i))
		}
	}
	log := strings.Join(lines, "\n")
	a := CompressLog(log, Options{Cluster: true, MaxLines: 100000})
	b := CompressLog(log, Options{Cluster: true, MaxLines: 100000})
	if a != b {
		t.Fatalf("banded clustering is not deterministic:\n%s\n---\n%s", clipOut(a), clipOut(b))
	}
}

// TestBandedThresholdBoundary: the same input must produce identical output
// on both sides of the threshold — pairwise at or below it, banded above it.
func TestBandedThresholdBoundary(t *testing.T) {
	restore := minHashClusterThreshold
	defer func() { minHashClusterThreshold = restore }()

	var lines []string
	for f := 0; f < 40; f++ {
		for i := 0; i < 10; i++ {
			lines = append(lines, fmt.Sprintf("ERROR sync %s record %d", rep6(f), i))
		}
	}
	for i := 0; i < 8; i++ {
		lines = append(lines, fmt.Sprintf("ERROR disk block %s slow", tag3(i)))
	}
	log := strings.Join(lines, "\n") // 408 cluster lines

	minHashClusterThreshold = 409 // pairwise
	pairwise := CompressLog(log, Options{Cluster: true, MaxLines: 100000})
	minHashClusterThreshold = 407 // banded
	banded := CompressLog(log, Options{Cluster: true, MaxLines: 100000})
	if pairwise != banded {
		t.Fatalf("banded output diverged from pairwise:\npairwise:\n%s\nbanded:\n%s", clipOut(pairwise), clipOut(banded))
	}
	if !strings.Contains(banded, "(repeated 8x)") {
		t.Fatalf("fuzzy family did not merge in both paths; output head:\n%s", clipOut(banded))
	}
}

// TestBandedDegenerateFamilies: a huge exact family plus a 500-member fuzzy
// family — the degenerate buckets this shape produces must not hang the
// banded pass or split the family.
func TestBandedDegenerateFamilies(t *testing.T) {
	restore := minHashClusterThreshold
	defer func() { minHashClusterThreshold = restore }()
	minHashClusterThreshold = 16 // force the banded path on a small input

	var lines []string
	for i := 0; i < 2000; i++ {
		lines = append(lines, fmt.Sprintf("ERROR sync %s record %d", rep6(0), i))
	}
	for i := 0; i < 500; i++ {
		lines = append(lines, fmt.Sprintf("ERROR disk block %s slow", tag3(i)))
	}
	log := strings.Join(lines, "\n")

	got := CompressLog(log, Options{Cluster: true, MaxLines: 100000})
	if n := strings.Count(got, "(repeated 2000x)"); n != 1 {
		t.Fatalf("expected the 2000-member exact family to stay whole, got %d markers; output head:\n%s", n, clipOut(got))
	}
	if n := strings.Count(got, "(repeated 500x)"); n != 1 {
		t.Fatalf("expected the 500-member fuzzy family to merge whole, got %d markers; output head:\n%s", n, clipOut(got))
	}
}

// TestMinHashSignatureProperties: signatures are deterministic, distinct
// lines do not collide on every value, and — the property the whole banded
// path rests on — near-identical lines share at least one signature value,
// so LSH pairs them.
func TestMinHashSignatureProperties(t *testing.T) {
	a := minHashSignature("ERROR disk block aaa slow")
	b := minHashSignature("ERROR disk block aaa slow")
	if a != b {
		t.Fatal("minhash signature is not deterministic")
	}
	c := minHashSignature("ERROR cache block zzz slow")
	same := 0
	for i := range a {
		if a[i] == c[i] {
			same++
		}
	}
	if same == len(a) {
		t.Fatal("distinct lines produced identical signatures")
	}
	near := minHashSignature("ERROR disk block aab slow")
	shared := 0
	for i := range a {
		if a[i] == near[i] {
			shared++
		}
	}
	if shared == 0 {
		t.Fatal("near-identical lines share no minhash value — LSH would never pair them")
	}
}

func clipOut(s string) string {
	if len(s) > 900 {
		return s[:900] + "…"
	}
	return s
}
