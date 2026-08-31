package loop

import "github.com/JayveerPrajapati/kern/internal/domain"

// AutonomyScore is a deterministic, multi-dimension autonomy score in [0.0, 1.0]
// plus a recommended level. It is advisory only: the configured level remains
// the HARD ceiling (AllowsStage / AllowsStageWithProofs). The score informs and
// recommends but never grants more autonomy than the config permits.
// It combines five base dimensions, each in [0.0, 1.0]:
// - Confidence:         how confident the run is in its outcome.
// - RiskTolerance:      how much risk is tolerated (higher = more tolerant).
// - PolicyCoverage:     how much of the relevant policy surface was evaluated.
// - VerificationCoverage: how many verification gates passed.
// - SafetyBudgetRatio:  remaining budget headroom (1.0 = unused, 0.0 = exhausted).
// It also carries the three context dimensions the spec requires
// (reversibility, environment, permissions). These are advisory and, unlike
// the five base dimensions, are only blended into the score when explicitly set
// (> 0): a zero value means "not evaluated" and leaves the score unchanged, so
// existing callers that only populate the five base dimensions are fully
// backward compatible. When any of the three is set, the base weights are
// renormalized to keep the total at 1.0.
// When HistoricalSuccess > 0 it is blended into Score() as a lightly-weighted
// evidence dimension (see Score), representing evidence-based success from
// recorded runs.
type AutonomyScore struct {
	Confidence           float64
	RiskTolerance        float64
	PolicyCoverage       float64
	VerificationCoverage float64
	SafetyBudgetRatio    float64
	// Reversibility is how reversible the operation is (1.0 = fully
	// reversible / rollback-able, 0.0 = irreversible).. Only
	// blended into the score when > 0 (backward compatible).
	Reversibility float64
	// Environment is how safe the target environment is (1.0 = development /
	// low-risk, 0.0 = production / high-risk).. Only blended in
	// when > 0.
	Environment float64
	// Permissions is the fraction of required permissions the agent holds
	// (1.0 = fully authorized, 0.0 = none).. Only blended in
	// when > 0.
	Permissions float64
	// HistoricalSuccess is an evidence-based success rate in [0.0, 1.0] from
	// recorded past runs (0 = no history recorded). It is advisory only: when
	// set it may RAISE the RECOMMENDED level (RecommendedLevel), but the
	// configured level remains the HARD ceiling via AllowedByScore — recorded
	// evidence never widens what the config permits, exactly as
	// requires. JSON-serializable; 0 means no history.
	HistoricalSuccess float64
}

// Autonomy score weights (documented, deterministic, sum to 1.0). Higher weights
// pull the score toward the corresponding dimension.
const (
	scoreWeightConfidence           = 0.30
	scoreWeightRiskTolerance        = 0.20
	scoreWeightPolicyCoverage       = 0.15
	scoreWeightVerificationCoverage = 0.15
	scoreWeightSafetyBudgetRatio    = 0.20
	// scoreWeightReversibility / Environment / Permissions are the
	// context dimensions. They are only consumed when the corresponding field
	// is set (> 0); when consumed, the base weights are renormalized so the
	// total still sums to 1.0.
	scoreWeightReversibility = 0.10
	scoreWeightEnvironment   = 0.05
	scoreWeightPermissions   = 0.05
	// scoreWeightHistoricalSuccess is the weight given to the evidence-based
	// historical success dimension when it is present (HistoricalSuccess > 0).
	// It is the 6th dimension: when active, the original weights are
	// renormalized to 1 - scoreWeightHistoricalSuccess = 0.90 so the total still
	// sums to 1.0.
	scoreWeightHistoricalSuccess = 0.10
)

// Score computes the weighted combination of the dimensions, clamped to
// [0.0, 1.0]. It is deterministic. When HistoricalSuccess is zero the result is
// the plain weighted sum of the base dimensions (backward compatible). When
// HistoricalSuccess > 0 it is blended in as an evidence dimension with weight
// scoreWeightHistoricalSuccess, and the other contributions are scaled down so
// the total still sums to 1.0.
// The context dimensions (Reversibility, Environment, Permissions)
// are only consumed when set (> 0); when any is set the base weights are
// renormalized by (1 - sum(set context weights)) so the total stays 1.0. This
// keeps callers that only populate the five base dimensions fully backward
// compatible.
func (a AutonomyScore) Score() float64 {
	// Determine which context dimensions are present.
	contextWeight := 0.0
	if a.Reversibility > 0 {
		contextWeight += scoreWeightReversibility
	}
	if a.Environment > 0 {
		contextWeight += scoreWeightEnvironment
	}
	if a.Permissions > 0 {
		contextWeight += scoreWeightPermissions
	}
	// Base contributions (before history blend), renormalized by the consumed
	// context weight so the base total = 1 - contextWeight.
	baseScale := 1.0 - contextWeight
	s := (a.Confidence*scoreWeightConfidence +
		a.RiskTolerance*scoreWeightRiskTolerance +
		a.PolicyCoverage*scoreWeightPolicyCoverage +
		a.VerificationCoverage*scoreWeightVerificationCoverage +
		a.SafetyBudgetRatio*scoreWeightSafetyBudgetRatio) * baseScale
	if a.Reversibility > 0 {
		s += a.Reversibility * scoreWeightReversibility
	}
	if a.Environment > 0 {
		s += a.Environment * scoreWeightEnvironment
	}
	if a.Permissions > 0 {
		s += a.Permissions * scoreWeightPermissions
	}
	if a.HistoricalSuccess > 0 {
		// Blend the evidence dimension in, renormalizing the rest to 0.90.
		s = s*(1-scoreWeightHistoricalSuccess) + a.HistoricalSuccess*scoreWeightHistoricalSuccess
	}
	if s < 0 {
		return 0
	}
	if s > 1 {
		return 1
	}
	return s
}

// WithHistoricalSuccess returns a copy of a with the evidence-based historical
// success rate recorded and blended into the score. The rate is
// clamped to [0.0, 1.0]; window records how many past runs the evidence covers
// (advisory metadata, not used in the score). The returned score may raise the
// RECOMMENDED level but never raises the configured level, which stays the hard
// ceiling via AllowedByScore. It is deterministic and offline.
func (a AutonomyScore) WithHistoricalSuccess(successRate float64, window int) AutonomyScore {
	if successRate < 0 {
		successRate = 0
	}
	if successRate > 1 {
		successRate = 1
	}
	a.HistoricalSuccess = successRate
	return a
}

// RecommendedLevel maps the score onto L0..L5 deterministically. Higher scores
// recommend higher autonomy; the level never exceeds L5.
func (a AutonomyScore) RecommendedLevel() Autonomy {
	switch s := a.Score(); {
	case s < 0.2:
		return L0
	case s < 0.4:
		return L1
	case s < 0.6:
		return L2
	case s < 0.8:
		return L3
	case s < 0.95:
		return L4
	default:
		return L5
	}
}

// AutonomyScoreFromRisk derives an AutonomyScore from a risk level. A HIGH-risk
// change lowers confidence/verification coverage and raises the needed approval,
// so it recommends a LOWER autonomy level than a low-risk change. It also seeds
// the context dimensions from the risk level: higher-risk changes
// are less reversible, run in a more restricted environment, and need fuller
// permission coverage before higher autonomy is recommended.
func AutonomyScoreFromRisk(level domain.RiskLevel) AutonomyScore {
	switch level {
	case domain.RiskCritical:
		return AutonomyScore{Confidence: 0.15, RiskTolerance: 0.15, PolicyCoverage: 0.15, VerificationCoverage: 0.15, SafetyBudgetRatio: 0.15, Reversibility: 0.2, Environment: 0.2, Permissions: 0.3}
	case domain.RiskHigh:
		return AutonomyScore{Confidence: 0.45, RiskTolerance: 0.45, PolicyCoverage: 0.50, VerificationCoverage: 0.50, SafetyBudgetRatio: 0.50, Reversibility: 0.5, Environment: 0.5, Permissions: 0.6}
	case domain.RiskMedium:
		return AutonomyScore{Confidence: 0.70, RiskTolerance: 0.70, PolicyCoverage: 0.70, VerificationCoverage: 0.70, SafetyBudgetRatio: 0.70, Reversibility: 0.7, Environment: 0.7, Permissions: 0.7}
	default: // RiskLow
		return AutonomyScore{Confidence: 0.90, RiskTolerance: 0.90, PolicyCoverage: 0.90, VerificationCoverage: 0.90, SafetyBudgetRatio: 0.90, Reversibility: 0.9, Environment: 0.9, Permissions: 0.9}
	}
}

// AllowedByScore reports whether the configured autonomy level is permitted given
// the score's recommended level: the config level must be LESS THAN OR EQUAL to
// the recommended level, i.e. you may not run at a higher autonomy than the score
// supports. This is a SOFT gate that complements the hard config gate
// (AllowsStage / AllowsStageWithProofs): config remains the ceiling, the score
// only narrows (never widens) what is permitted.
func (a Autonomy) AllowedByScore(score AutonomyScore) bool {
	return a <= score.RecommendedLevel()
}
