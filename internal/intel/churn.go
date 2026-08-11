package intel

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// ChurnEntry is one file with its change-frequency stats.
type ChurnEntry struct {
	File          string  `json:"file"`
	Commits       int     `json:"commits"`
	InWorkingTree bool    `json:"in_working_tree,omitempty"`
	Risk          float64 `json:"risk,omitempty"` // 0 when the file is not indexed
}

// ChurnReport is the change-frequency view: which files churn most, whether
// they are still being edited right now, and how risky each one is in the
// call graph. Frequently-changed files inside a risky blast radius are the
// ones most likely to break next.
type ChurnReport struct {
	From    string       `json:"from,omitempty"`
	To      string       `json:"to,omitempty"`
	Commits int          `json:"commits"`
	Files   int          `json:"files"`
	Entries []ChurnEntry `json:"entries"`
}

// Churn counts how many commits touched each file in the range. from/to follow
// git log semantics: an empty range means the last 30 commits.
func Churn(root, from, to string) (*ChurnReport, error) {
	args := []string{"-C", root, "log", "--name-only", "--pretty=format:"}
	if from != "" || to != "" {
		if to == "" {
			to = "HEAD"
		}
		args = append(args, from+".."+to)
	} else {
		args = append(args, "-n", "30")
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, &GitError{Op: "git log --name-only", Err: err}
	}
	counts, commits := parseLog(string(out))
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
	if ix, err := ReadIndex(root); err == nil {
		report := AnalyzeChanges(ix, filesOf(entries))
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
// output, where each commit's file list is a contiguous block separated by
// blank lines. It also returns the number of commits in the output (one per
// non-empty block), so the report's commit count is accurate rather than a
// count of distinct files.
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
