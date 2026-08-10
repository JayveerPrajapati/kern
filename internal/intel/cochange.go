package intel

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// CoChangeEntry is one file with its co-change partners: files that changed in
// the same commits. High overlap means editing this file usually requires
// editing its partners too.
type CoChangeEntry struct {
	File          string         `json:"file"`
	Commits       int            `json:"commits"`  // commits touching this file
	Partners      []string       `json:"partners"` // files co-changed with it, ranked by co-change count
	PartnerCounts map[string]int `json:"partner_counts"`
}

// CoChangeReport is the commit-history coupling view: which files actually
// change together, independent of the call graph. Used to grade change risk —
// a file whose partners are being edited NOW is the next one to break.
type CoChangeReport struct {
	From    string          `json:"from,omitempty"`
	To      string          `json:"to,omitempty"`
	Commits int             `json:"commits"`
	Files   int             `json:"files"`
	Entries []CoChangeEntry `json:"entries"`
	// WorkingTree marks files currently modified so co-change mode can say
	// "these partners are also dirty right now".
	WorkingTree map[string]bool `json:"-"`
}

// CoChange computes co-change coupling from git history. from/to follow git log
// semantics; an empty range means the last 30 commits. The report is built from
// commit metadata only (no call graph), so it works even where indexing fails.
func CoChange(root, from, to string) (*CoChangeReport, error) {
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
	commitFiles := parseCommitBlocks(string(out))

	// Per-file co-change partner counts.
	counts := map[string]int{}              // commits per file
	partners := map[string]map[string]int{} // file -> partner -> co-change count
	for _, files := range commitFiles {
		// Unique files in this commit (a rename appears once).
		uniq := make([]string, 0, len(files))
		uset := map[string]bool{}
		for _, f := range files {
			f = strings.TrimSpace(f)
			if f == "" || uset[f] {
				continue
			}
			uset[f] = true
			uniq = append(uniq, f)
		}
		for _, f := range uniq {
			counts[f]++
			if partners[f] == nil {
				partners[f] = map[string]int{}
			}
			for _, g := range uniq {
				if g != f {
					partners[f][g]++
				}
			}
		}
	}

	entries := make([]CoChangeEntry, 0, len(counts))
	for f, n := range counts {
		ps := partners[f]
		ranked := make([]string, 0, len(ps))
		for g := range ps {
			ranked = append(ranked, g)
		}
		sort.Slice(ranked, func(i, j int) bool {
			if ps[ranked[i]] != ps[ranked[j]] {
				return ps[ranked[i]] > ps[ranked[j]]
			}
			return ranked[i] < ranked[j]
		})
		entries = append(entries, CoChangeEntry{
			File:          f,
			Commits:       n,
			Partners:      ranked,
			PartnerCounts: ps,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Commits != entries[j].Commits {
			return entries[i].Commits > entries[j].Commits
		}
		return entries[i].File < entries[j].File
	})

	report := &CoChangeReport{
		From:    from,
		To:      to,
		Commits: len(commitFiles),
		Files:   len(entries),
		Entries: entries,
	}
	// Mark files dirty in the working tree.
	if wt, err := ChangedFiles(root); err == nil {
		report.WorkingTree = map[string]bool{}
		for _, f := range wt {
			report.WorkingTree[f] = true
		}
	}
	return report, nil
}

// parseCommitBlocks splits `git log --name-only` output into per-commit file
// lists (blank-line separated blocks, like parseLog but returning the blocks).
func parseCommitBlocks(out string) [][]string {
	var blocks [][]string
	var cur []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(cur) > 0 {
				blocks = append(blocks, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, line)
	}
	if len(cur) > 0 {
		blocks = append(blocks, cur)
	}
	return blocks
}

// RenderCoChange renders the co-change report, flagging the partners that are
// also modified in the working tree right now (the ones to change in lockstep).
func RenderCoChange(r *CoChangeReport, limit int) string {
	var b strings.Builder
	head := fmt.Sprintf("co-change coupling over %d commits (%d files)", r.Commits, r.Files)
	if r.From != "" {
		head = fmt.Sprintf("co-change coupling in %s..%s (%d commits, %d files)", r.From, r.To, r.Commits, r.Files)
	}
	b.WriteString(head + "\n\n")
	shown := 0
	for _, e := range r.Entries {
		if limit > 0 && shown >= limit {
			break
		}
		shown++
		now := ""
		if r.WorkingTree[e.File] {
			now = "  [modified NOW]"
		}
		fmt.Fprintf(&b, "  %3d×  %-48s%s\n", e.Commits, e.File, now)
		for i, p := range e.Partners {
			if i >= 5 {
				fmt.Fprintf(&b, "       + %d more partners\n", len(e.Partners)-5)
				break
			}
			tag := ""
			if r.WorkingTree[p] {
				tag = "  [modified NOW]"
			}
			fmt.Fprintf(&b, "        with %-44s %d×%s\n", p, e.PartnerCounts[p], tag)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}
