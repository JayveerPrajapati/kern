package prose

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Lookup implements prose-word -> symbol candidate lookup via the index's build-time inverted vocab.
func Lookup(ix *index.Index, query string, limit int) (string, error) {
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	if limit <= 0 {
		limit = 20
	}

	hits := ix.LookupProse(query, limit)
	if len(hits) == 0 {
		return "no prose matches: " + query, nil
	}

	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "%s (%d words matched)\n", h.Symbol, h.Matched)
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}
