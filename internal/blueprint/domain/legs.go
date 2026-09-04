package domain

// LegKind classifies a validation leg by whether it can produce a BLOCK.
//
// This is the honesty signal behind the per-leg verdict section (P2.3): a
// "blocking" leg can be the source of a BLOCK aggregate; an "advisory" leg
// reports WARN/INFO only and cannot gate a change on its own. The aggregate
// Status alone overpromises when an advisory leg is hidden inside a PASS or
// WARN — the per-leg kinds make the block-versus-advisory split explicit.
type LegKind string

const (
	// LegKindBlocking marks a leg whose findings can produce a BLOCK
	// (architecture boundaries — incl. projected-import violations — secret
	// detection).
	LegKindBlocking LegKind = "blocking"
	// LegKindAdvisory marks a leg whose findings are advisory-only — WARN or
	// INFO, never a gate. Duplication is advisory per the P1.1 two-pass
	// triage (only duplication:confirmed-block is block-eligible); resilience
	// is advisory because it is opt-in and NOT_RUN by default (P2.2).
	LegKindAdvisory LegKind = "advisory"
)

// checkLegKinds is the single source of truth mapping a check name to its leg
// kind. Every adapter (MCP, CLI, CI) derives the blocking/advisory split from
// this map — never from scattered conditionals. Keys are the stable check
// Name() values plus the user-facing short names recorded in
// ValidationResult.ChecksSkipped (e.g. "resilience" for resilience:scenarios).
var checkLegKinds = map[string]LegKind{
	"architecture:guard": LegKindBlocking,
	// P0.4: the authz gate rides the architecture check (its findings are
	// emitted by ArchitectureCheck under Name() "architecture:guard"), but
	// the leg is classified separately so an authz:unauthorized BLOCK is
	// never mistaken for an advisory signal in per-leg verdicts.
	"authz:guard":          LegKindBlocking,
	"secret:scan":          LegKindBlocking,
	"secret:gitleaks":      LegKindBlocking,
	"duplication:jscpd":    LegKindAdvisory,
	"duplication:advisory": LegKindAdvisory,
	"resilience:scenarios": LegKindAdvisory,
	"resilience":           LegKindAdvisory,
	// The two-person approval gate (P1.3) is a blocking leg: an unapproved
	// high-risk change must gate the file pipeline.
	"approval:gate": LegKindBlocking,
}

// CheckLegKind returns whether the named check leg is blocking or advisory.
// Unknown names default to blocking: claiming a leg is advisory when it might
// block would understate the risk, so an unclassified check is assumed able to
// block until it is proven advisory-only.
func CheckLegKind(checkName string) LegKind {
	if k, ok := checkLegKinds[checkName]; ok {
		return k
	}
	return LegKindBlocking
}
