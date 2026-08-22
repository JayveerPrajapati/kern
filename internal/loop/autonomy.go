package loop

import (
	"fmt"
	"strconv"
	"strings"
)

// Autonomy is a config-gated autonomy level. Higher levels unlock more stages
// of the closed loop; levels are explicit and never jumped.
type Autonomy int

const (
	L0 Autonomy = iota // read-only analysis
	L1                 // recommendations
	L2                 // sandbox modifications
	L3                 // PR creation
	L4                 // deployment with human approval
	L5                 // autonomous low-risk changes (explicit config only)
)

// stage identifiers for the closed loop. The nine stages mirror the spec's
// north-star form: intent → remember → plan → code → verify → protect →
// deploy → observe → learn.
const (
	stageIntent   = "intent"
	stageRemember = "remember"
	stagePlan     = "plan"
	stageCode     = "code"
	stageVerify   = "verify"
	stageProtect  = "protect"
	stageDeploy   = "deploy"
	stageObserve  = "observe"
	stageLearn    = "learn"
)

// String returns the level label, e.g. "L3".
func (a Autonomy) String() string {
	return "L" + strconv.Itoa(int(a))
}

// L5ProofRequirement names a capability that must be proven before L5 autonomy
// (autonomous low-risk changes) is permitted. The spec requires: policy,
// verification, rollback, monitoring, audit, confidence.
type L5ProofRequirement string

const (
	ProofPolicy       L5ProofRequirement = "policy"       // governance policy evaluated and passing
	ProofVerification L5ProofRequirement = "verification" // verification suite passing
	ProofRollback     L5ProofRequirement = "rollback"     // rollback path available and tested
	ProofMonitoring   L5ProofRequirement = "monitoring"   // production monitoring in place
	ProofAudit        L5ProofRequirement = "audit"        // audit trail active
	ProofConfidence   L5ProofRequirement = "confidence"   // model/agent confidence above threshold
)

// L5Proofs is the set of proofs that must all hold before L5 autonomy is
// exercised. Callers populate it with the outcomes of each proof check.
type L5Proofs map[L5ProofRequirement]bool

// Satisfied reports whether every required proof has been provided and is true.
// A nil or empty map is NOT satisfied — L5 is fail-closed until every proof is
// explicitly confirmed.
func (p L5Proofs) Satisfied() bool {
	if len(p) == 0 {
		return false
	}
	for _, req := range []L5ProofRequirement{ProofPolicy, ProofVerification, ProofRollback, ProofMonitoring, ProofAudit, ProofConfidence} {
		if !p[req] {
			return false
		}
	}
	return true
}

// RequiredL5Proofs returns the ordered list of proofs L5 autonomy demands.
func RequiredL5Proofs() []L5ProofRequirement {
	return []L5ProofRequirement{ProofPolicy, ProofVerification, ProofRollback, ProofMonitoring, ProofAudit, ProofConfidence}
}

// AllowsStage reports whether an autonomy level permits the given loop stage.
// Read-only stages (remember, verify, observe) are always allowed; write/act
// stages are unlocked at their assigned level (plan/learn ≥ L1, code ≥ L2,
// deploy/protect ≥ L4). L5 is additionally gated by L5Proofs — see
// AllowsStageWithProofs.
func (a Autonomy) AllowsStage(stage string) bool {
	switch stage {
	case stagePlan, stageLearn:
		return a >= L1
	case stageCode:
		return a >= L2
	case stageDeploy, stageProtect:
		return a >= L4
	case stageRemember:
		return true // read-only memory recall
	default: // intent, verify, observe — read-only
		return true
	}
}

// AllowsStageWithProofs is like AllowsStage but enforces the L5 proof gate:
// at L5, write/act stages (code, deploy, protect) are only permitted when the
// supplied proofs are all satisfied. Read-only stages (intent, remember,
// verify, observe) are never gated by proofs — L5 may always analyze.
// A nil proofs map fails closed (write stages denied) until every proof is
// explicitly confirmed.
func (a Autonomy) AllowsStageWithProofs(stage string, proofs L5Proofs) bool {
	if a < L5 {
		return a.AllowsStage(stage)
	}
	// L5: gate write/act stages on proofs; read-only stages always allowed.
	switch stage {
	case stageCode, stageDeploy, stageProtect:
		return proofs.Satisfied()
	default:
		return a.AllowsStage(stage)
	}
}

// ParseLevel parses a level string: "L0"…"L5", "0"…"5", or case-insensitive
// "l4". Any other value is an error (fail-closed).
func ParseLevel(s string) (Autonomy, error) {
	t := strings.TrimSpace(strings.ToUpper(s))
	t = strings.TrimPrefix(t, "L")
	if t == "" {
		return 0, fmt.Errorf("loop: empty autonomy level")
	}
	n, err := strconv.Atoi(t)
	if err != nil {
		return 0, fmt.Errorf("loop: invalid autonomy level %q", s)
	}
	if n < 0 || n > int(L5) {
		return 0, fmt.Errorf("loop: autonomy level %d out of range L0-L5", n)
	}
	return Autonomy(n), nil
}
