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

// AllowsStage reports whether an autonomy level permits the given loop stage.
// Read-only stages (remember, verify, observe) are always allowed; write/act
// stages are unlocked at their assigned level (plan/learn ≥ L1, code ≥ L2,
// deploy/protect ≥ L4).
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
