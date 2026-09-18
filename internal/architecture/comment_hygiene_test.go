package architecture

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// comment_hygiene_test.go is the regrowth guard for the 2026-09-18 comment
// sweep: internal tracker and campaign IDs must not appear in Go comments.
// Provenance lives in git history, not in source prose. When this test
// fails, reword the comment to state the rationale without the tracking
// reference — or, in tests, delete the comment line entirely.
//
// The gate-registry identifiers G0..G36 (internal/gates) are real in-repo
// code identifiers, not tracker IDs, and are deliberately allowed: the
// pattern requires the dash-separated forms used by the retired trackers.

// trackerIDRe matches the retired tracker/campaign ID families in comment
// prose. It is intentionally anchored on word boundaries; new families get
// added here when a new tracking system is retired.
var trackerIDRe = regexp.MustCompile(`\b(?:` +
	`P0\.[0-9]+` + // authorized-context / shared-state primitive IDs
	`|AUD-[0-9]+` + // audit-finding IDs
	`|G-P0-[0-9]+` + // governance-primitive gap IDs
	`|G-[0-9]+` + // plan-gap IDs
	`|W[12]-[0-9]+` + // weekly-workstream IDs
	`|P1-[0-9]+` + // fix-queue recommendation IDs
	`|KERN-P2-0[0-9]+` + // deferred-queue IDs
	`|report A[0-9]+` + // evaluation-report finding IDs
	`|F-0[0-9]+[a-z]?` + // QA-sweep finding IDs (incl. letter suffixes)
	`|CG-P1-[0-9]+` + // campaign-recommendation IDs
	`)`)

// hygieneSkipDirs are never scanned: kern's own data stores, VCS metadata,
// vendored/generated trees, and the sandbox snapshots under .kern.
var hygieneSkipDirs = map[string]bool{
	".kern": true, ".git": true, "node_modules": true, "vendor": true,
	".opencode": true, ".claude": true, ".cursor": true, ".gemini": true,
	".kiro": true, "graphify-out": true, "bin": true, "testfixture": true,
	"sdk": true, "python": true, "dist": true, "docs": true, "homebrew": true,
}

// TestNoTrackerIDsInComments fails on any Go comment line carrying a retired
// tracker/campaign ID, so the class cannot regrow after a sweep.
func TestNoTrackerIDsInComments(t *testing.T) {
	root := repoRoot(t)
	var violations []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if hygieneSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		lines := strings.Split(string(b), "\n")
		for i, line := range lines {
			if !strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if m := trackerIDRe.FindString(line); m != "" {
				rel, _ := filepath.Rel(root, p)
				violations = append(violations, rel+":"+strconv.Itoa(i+1)+" (\""+m+"\")")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Errorf("tracker IDs in comments (provenance belongs in git history, not prose) — strip the ID and keep only the rationale:\n%s",
			strings.Join(violations, "\n"))
	}
}
