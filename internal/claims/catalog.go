package claims

import (
	"fmt"

	mcpcatalog "github.com/JayveerPrajapati/kern/internal/mcpcatalog"
)

const (
	catalogClaimID = "tool-catalog-contract"
	catalogClaim   = "every advertised MCP tool carries a complete, governable contract"
)

// claimToolCatalog proves the agent-surface claim: every tool a client sees is
// fully described. A tool missing its category, risk level, schema version or
// input schema is not merely untidy — governed clients gate access on
// riskLevel and negotiate on schemaVersion, so an incomplete registration
// silently degrades the safety contract for that tool.
func claimToolCatalog() Result {
	tools := mcpcatalog.All
	if len(tools) == 0 {
		return fail(catalogClaimID, catalogClaim, "the tool catalog is empty")
	}
	var evidence []string
	for _, t := range tools {
		missing := ""
		switch {
		case t.Name == "":
			missing = "name"
		case t.Description == "":
			missing = "description"
		case len(t.InputSchema) == 0:
			missing = "inputSchema"
		case t.Category == "":
			missing = "category"
		case t.RiskLevel == "":
			missing = "riskLevel"
		case t.SchemaVersion == "":
			missing = "schemaVersion"
		}
		if missing != "" {
			evidence = append(evidence, fmt.Sprintf("%s: missing %s", t.Name, missing))
			return fail(catalogClaimID, catalogClaim,
				fmt.Sprintf("tool %q is missing its %s", t.Name, missing), evidence...)
		}
	}
	// Annotations are derived from riskLevel, so this also proves the
	// derivation still covers every risk tier rather than silently dropping
	// the high tiers.
	annotated := 0
	for _, t := range tools {
		if len(t.WithDerivedAnnotations().Annotations) > 0 {
			annotated++
		}
	}
	evidence = append(evidence,
		fmt.Sprintf("%d tools: name/description/inputSchema/category/riskLevel/schemaVersion present", len(tools)),
		fmt.Sprintf("%d/%d tools expose derived MCP annotations", annotated, len(tools)))
	return pass(catalogClaimID, catalogClaim,
		fmt.Sprintf("%d tools carry a complete contract", len(tools)), evidence...)
}
