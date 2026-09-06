package intel

import (
	"fmt"
	"sort"
	"strings"
)

// CrossRepoImpactReport summarizes cross-repository callers and blast radius.
type CrossRepoImpactReport struct {
	Subject       string                `json:"subject"`
	HomeRepo      string                `json:"home_repo"`
	TotalHits     int                   `json:"total_hits"`
	RepoBreakdown map[string][]RepoCall `json:"repo_breakdown"`
	Summary       string                `json:"summary"`
}

// RepoCall captures an external repo symbol calling or referencing the subject.
type RepoCall struct {
	Repo   string `json:"repo"`
	Root   string `json:"root"`
	Caller string `json:"caller"`
	File   string `json:"file"`
	Line   int    `json:"line"`
}

// CrossRepoImpact traces external call sites across all registered repositories in RepoRegistry.
func CrossRepoImpact(subject string, limit int) (*CrossRepoImpactReport, error) {
	if subject == "" {
		return nil, fmt.Errorf("subject is required")
	}
	if limit <= 0 {
		limit = 20
	}

	reg, err := LoadRepos()
	if err != nil || reg == nil || len(reg.Repos) == 0 {
		return &CrossRepoImpactReport{
			Subject:       subject,
			TotalHits:     0,
			RepoBreakdown: map[string][]RepoCall{},
			Summary:       "No external repositories registered in kern repo registry.",
		}, nil
	}

	breakdown := make(map[string][]RepoCall)
	total := 0

	for _, repo := range reg.Repos {
		ix, err := ReadIndex(repo.Root)
		if err != nil || ix == nil {
			continue
		}

		// Check if subject is called directly
		callers := ix.CallersOf(subject)
		for _, c := range callers {
			sym, ok := ix.ResolveName(c)
			file, line := "", 0
			if ok {
				file, line = sym.File, sym.Line
			}
			breakdown[repo.Name] = append(breakdown[repo.Name], RepoCall{
				Repo:   repo.Name,
				Root:   repo.Root,
				Caller: c,
				File:   file,
				Line:   line,
			})
			total++
		}

		// Also check calls map for qualified forms (e.g. pkg.Subject)
		for callerName, calleeList := range ix.Calls {
			for _, callee := range calleeList {
				if callee == subject || strings.HasSuffix(callee, "."+subject) {
					// Check if already in breakdown
					already := false
					for _, existing := range breakdown[repo.Name] {
						if existing.Caller == callerName {
							already = true
							break
						}
					}
					if !already {
						sym, ok := ix.ResolveName(callerName)
						file, line := "", 0
						if ok {
							file, line = sym.File, sym.Line
						}
						breakdown[repo.Name] = append(breakdown[repo.Name], RepoCall{
							Repo:   repo.Name,
							Root:   repo.Root,
							Caller: callerName,
							File:   file,
							Line:   line,
						})
						total++
					}
				}
			}
		}
	}

	report := &CrossRepoImpactReport{
		Subject:       subject,
		TotalHits:     total,
		RepoBreakdown: breakdown,
		Summary:       fmt.Sprintf("Found %d cross-repo references across %d registered repositories.", total, len(breakdown)),
	}

	return report, nil
}

// Render formats the CrossRepoImpactReport for text display.
func (r *CrossRepoImpactReport) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "CROSS-REPO IMPACT REPORT: %s\n", r.Subject)
	fmt.Fprintf(&b, "========================================================\n")
	fmt.Fprintf(&b, "%s\n\n", r.Summary)

	if len(r.RepoBreakdown) == 0 {
		fmt.Fprintln(&b, "No cross-repo callers found.")
		return strings.TrimSpace(b.String())
	}

	var repoNames []string
	for name := range r.RepoBreakdown {
		repoNames = append(repoNames, name)
	}
	sort.Strings(repoNames)

	for _, name := range repoNames {
		calls := r.RepoBreakdown[name]
		fmt.Fprintf(&b, "Repo [%s] (%d callers):\n", name, len(calls))
		for i, c := range calls {
			if i >= 10 {
				fmt.Fprintf(&b, "  … and %d more callers in %s\n", len(calls)-10, name)
				break
			}
			loc := ""
			if c.File != "" {
				loc = fmt.Sprintf(" (%s:%d)", c.File, c.Line)
			}
			fmt.Fprintf(&b, "  ← %s%s\n", c.Caller, loc)
		}
		fmt.Fprintln(&b)
	}

	return strings.TrimSpace(b.String())
}
