package crossrepo

import (
	"encoding/json"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/intel"
)

// Impact traces external call sites across all registered repositories in RepoRegistry.
func Impact(subject string, limit int, format string) (string, error) {
	if subject == "" {
		return "", fmt.Errorf("target_symbol, subject, or symbol is required")
	}

	if limit <= 0 {
		limit = 20
	}

	rep, err := intel.CrossRepoImpact(subject, limit)
	if err != nil {
		return "", err
	}

	if format == "json" {
		data, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	return rep.Render(), nil
}
