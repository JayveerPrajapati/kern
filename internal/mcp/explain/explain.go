package explain

import (
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// Explain produces an end-to-end architecture explanation for a symbol or subsystem.
// Combines declaration location, direct callers, callees, and downstream execution chain into
// a single cohesive response.
func Explain(ix *index.Index, subject string) (string, error) {
	if subject == "" {
		return "", fmt.Errorf("subject is required")
	}

	exp, err := intel.Explain(ix, subject)
	if err != nil {
		return "", err
	}

	return exp.Render(), nil
}
