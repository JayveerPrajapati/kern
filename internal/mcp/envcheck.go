package mcp

// Surface env validation (I8). The MCP surface switches are read lazily by
// filteredTools on the first tools/list; a misspelled value used to degrade
// silently into "no filtering" — KERN_MCP_CATEGORY=search looked like a
// filter for kern_search but is not a category at all (kern_search carries
// CategoryGraph), so operators lost the filter without any signal. The
// binaries validate the env at startup and fail loudly; the lazy reader
// keeps its lenient back-compat fallback for library embedding.

import (
	"fmt"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
)

// ValidateMCPSurfaceEnv fails loudly at server startup when KERN_MCP_CATEGORY
// names something that is not a registered tool category. The valid set is
// derived from the catalog (catalog.Categories) — never a duplicated literal
// — so the error message stays honest as the catalog grows. Empty and
// valid values pass; only unknown values error.
func ValidateMCPSurfaceEnv() error {
	raw := strings.TrimSpace(os.Getenv("KERN_MCP_CATEGORY"))
	if raw == "" {
		return nil
	}
	c := strings.ToLower(raw)
	if catalog.ValidCategory(c) {
		return nil
	}
	return fmt.Errorf("KERN_MCP_CATEGORY=%q is not a valid tool category (valid categories: %s)",
		raw, strings.Join(catalog.Categories(), ", "))
}
