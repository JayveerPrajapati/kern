package domain

// ImpactRisk is the deterministic transitive impact of a proposed change — the
// shared input to RiskFromImpact. Every risk surface (kern risk, kern
// simulate/what-if, the plan workflow) derives its tier from the same
// classifier so the same change string yields the same verdict everywhere
// (D3: risk and simulate previously disagreed because they counted different
// things).
type ImpactRisk struct {
	// AffectedCount is the number of transitively affected symbols: the
	// dependents of the change target plus the target itself. 0 = isolated.
	AffectedCount int
	// ServicesAffected is the number of affected services (module nodes whose
	// package contains an affected entry point).
	ServicesAffected int
	// CreatesCycle is true when the change introduces a dependency cycle
	// (e.g. a new dependency edge that closes a loop).
	CreatesCycle bool
	// SecuritySensitive is true when the change touches a security-governed
	// resource (auth, credentials, secrets, TLS, ...). The escalation is
	// inherent to the resource and is never downgraded by a small scope.
	SecuritySensitive bool
	// Severity is the governance ceiling from the firewall (RiskLow when no
	// firewall verdict exists). It is consulted only for security-sensitive
	// changes, where a stricter firewall level (CRITICAL) survives the
	// classifier.
	Severity RiskLevel
}

// RiskFromImpact maps a change's transitive impact to a deterministic risk
// tier and its blast-radius factor. It is the ONE classifier behind kern risk,
// kern simulate/what-if, and every other risk surface, so a change string
// cannot yield incompatible verdicts across commands.
//
// Tier table (HIGH is the human-approval threshold — every surface that
// requires human sign-off keys off tier >= HIGH):
//
//	createsCycle                                -> HIGH   ("risk:cycle")
//	servicesAffected > 0 or affectedCount > 10  -> HIGH   ("blast-radius:large")
//	affectedCount 1..10                         -> MEDIUM ("blast-radius:moderate")
//	affectedCount 0 (isolated)                  -> LOW    ("blast-radius:isolated")
//
// A security-sensitive change is never downgraded by scope: its tier is at
// least HIGH (or the firewall's CRITICAL severity when stricter), with the
// blast-radius factor still reported alongside.
func RiskFromImpact(imp ImpactRisk) (RiskLevel, string) {
	if imp.CreatesCycle {
		return RiskHigh, "risk:cycle"
	}
	factor := blastRadiusFactor(imp.AffectedCount)
	if imp.SecuritySensitive {
		if imp.Severity == RiskCritical {
			return RiskCritical, factor
		}
		return RiskHigh, factor
	}
	switch {
	case imp.ServicesAffected > 0 || imp.AffectedCount > 10:
		return RiskHigh, "blast-radius:large"
	case imp.AffectedCount > 0:
		return RiskMedium, "blast-radius:moderate"
	default:
		return RiskLow, "blast-radius:isolated"
	}
}

// blastRadiusFactor labels a change's blast radius from its affected count.
func blastRadiusFactor(affected int) string {
	switch {
	case affected > 10:
		return "blast-radius:large"
	case affected > 0:
		return "blast-radius:moderate"
	default:
		return "blast-radius:isolated"
	}
}
