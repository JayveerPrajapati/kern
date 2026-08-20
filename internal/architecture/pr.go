package architecture

import "github.com/JayveerPrajapati/kern/internal/index"

// PRValidation is the outcome of validating a pull request against governance.
type PRValidation struct {
	Branch       string
	Base         string
	ChangedFiles []string
	Violations   []Violation
	Approved     bool // true when there are no error-severity violations
	Summary      string
}

// ValidatePR validates a proposed change set against the architecture rules.
// files = changed file paths (relative to root). Requires no network; this is
// the local gate an agent or CI step runs before the PR is opened.
func ValidatePR(root, branch, base string, files []string) (*PRValidation, error) {
	cfg, err := Load(root)
	if err != nil {
		return nil, err
	}
	ix, err := index.Build(root)
	if err != nil {
		return nil, err
	}
	vs := NewEngine(cfg).Check(ix, files)
	rep := buildReport(vs)
	return &PRValidation{
		Branch:       branch,
		Base:         base,
		ChangedFiles: append([]string{}, files...),
		Violations:   vs,
		Approved:     rep.OK,
		Summary:      Render(rep),
	}, nil
}
