package intel

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// riskCap bounds the files risk-scored against the call graph. With vendor
// noise excluded the real source set is small; the cap is defensive so deep
// history walks on pathological repos cannot blow up ChurnContext (report A5).
const riskCap = 300

// ChurnEntry is one file with its change-frequency stats.
type ChurnEntry struct {
	File          string  `json:"file"`
	Commits       int     `json:"commits"`
	InWorkingTree bool    `json:"in_working_tree,omitempty"`
	Risk          float64 `json:"risk,omitempty"` // 0 when the file is not indexed
}

// ChurnReport is the change-frequency view: which files churn most, whether
// they are still being edited, and how risky each is in the call graph.
type ChurnReport struct {
	From    string       `json:"from,omitempty"`
	To      string       `json:"to,omitempty"`
	Commits int          `json:"commits"`
	Files   int          `json:"files"`
	Entries []ChurnEntry `json:"entries"`
}

// Churn counts how many commits touched each file in the range. from/to follow
// git log semantics: an empty range means the last 200 commits (raised from 30,
// which was too short to surface real churn signal).
func Churn(root, from, to string) (*ChurnReport, error) {
	return ChurnContext(context.Background(), root, from, to)
}

// ChurnContext counts commits with context cancellation and deadline support.
func ChurnContext(ctx context.Context, root, from, to string) (*ChurnReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	args := []string{"-C", root, "log", "--name-only", "--pretty=format:"}
	if from != "" || to != "" {
		if to == "" {
			to = "HEAD"
		}
		args = append(args, from+".."+to)
	} else {
		args = append(args, "-n", "200")
	}
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil, &GitError{Op: "git log --name-only", Err: err}
	}
	counts, commits := parseLog(string(out))
	// Exclude VCS/build/vendor-dir noise (vendor/, dist/, build/, ...) so deep
	// vendor-heavy history walks report real source churn only (report A5).
	for f := range counts {
		if code.ShouldIgnore(f) {
			delete(counts, f)
		}
	}
	entries := make([]ChurnEntry, 0, len(counts))
	for f, n := range counts {
		entries = append(entries, ChurnEntry{File: f, Commits: n})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Commits != entries[j].Commits {
			return entries[i].Commits > entries[j].Commits
		}
		return entries[i].File < entries[j].File
	})

	// Cross-reference with the working tree and the call graph.
	if wt, err := ChangedFiles(root); err == nil {
		set := map[string]bool{}
		for _, f := range wt {
			set[f] = true
		}
		for i := range entries {
			entries[i].InWorkingTree = set[entries[i].File]
		}
	}
	// Risk-score the top entries against the persisted index snapshot.
	// ReadIndex is deliberately NOT used here: it re-verifies freshness on
	// every call, and on a working tree with uncommitted edits that means a
	// git tree-OID walk plus an incremental re-index and an ~8MB index save
	// per invocation — turning a sub-second churn report into a multi-second
	// one (report A5: kern churn was ~7.8s on the kern repo). Churn is a
	// git-history metric; the call-graph risk overlay is best-effort (it is
	// skipped entirely when indexing fails), so the last persisted snapshot
	// is the right trade. When no snapshot exists at all, fall back to
	// ReadIndex so the very first run still builds one.
	ix, lerr := index.Load(root)
	if lerr != nil {
		ix, lerr = ReadIndex(root)
	}
	if lerr == nil {
		// Risk-score at most riskCap entries: the churn ranking already
		// prompted the review; scoring every historical file (which is what
		// made deep ranges hang) adds no signal (report A5).
		scored := entries
		if len(scored) > riskCap {
			scored = scored[:riskCap]
		}
		report := AnalyzeChanges(ix, filesOf(scored))
		risks := map[string]float64{}
		for _, c := range report.Changes {
			risks[c.File] = c.Risk
		}
		for i := range entries {
			entries[i].Risk = risks[entries[i].File]
		}
	}

	report := &ChurnReport{From: from, To: to, Commits: commits, Files: len(entries), Entries: entries}
	return report, nil
}

func filesOf(entries []ChurnEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.File)
	}
	return out
}

// parseLog counts per-commit file occurrences in `git log --name-only`
// output, where each commit's file list is a blank-line-separated block. It
// also returns the number of commits in the output.
func parseLog(out string) (map[string]int, int) {
	counts := map[string]int{}
	section := map[string]bool{}
	commits := 0
	open := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if open {
				commits++
				open = false
			}
			section = map[string]bool{}
			continue
		}
		open = true
		if !section[line] {
			section[line] = true
			counts[line]++
		}
	}
	if open {
		commits++
	}
	return counts, commits
}

// RenderChurn returns a compact churn report, flagging files that are both
// high-churn and still being edited.
func RenderChurn(r *ChurnReport) string {
	var b strings.Builder
	head := fmt.Sprintf("change churn over %d commits (%d files)", r.Commits, r.Files)
	if r.From != "" {
		head = fmt.Sprintf("change churn in %s..%s (%d commits, %d files)", r.From, r.To, r.Commits, r.Files)
	}
	b.WriteString(head + ":\n")
	for _, e := range r.Entries {
		flags := ""
		if e.InWorkingTree {
			flags += "  [being edited NOW]"
		}
		if e.Risk > 0 {
			flags += fmt.Sprintf("  risk %.1f", e.Risk)
		}
		fmt.Fprintf(&b, "  %3d×  %-48s%s\n", e.Commits, e.File, flags)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
