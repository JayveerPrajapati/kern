package context

import (
	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Snapshot builds a compact ContextSnapshot from a ContextPacket. The snapshot
// is a minimal representation of the task's current state, suitable for
// resume/replay. Strict Plan Phase 5 P1.
func Snapshot(pkt domain.ContextPacket, taskState, nextAction string) domain.ContextSnapshot {
	snap := domain.ContextSnapshot{
		Goal:       pkt.Intent.RawText,
		State:      taskState,
		NextAction: nextAction,
	}

	// Extract decisions from facts that are high-confidence.
	for _, f := range pkt.Facts {
		if f.Type == domain.ClaimFact && f.Confidence >= 0.8 {
			snap.Decisions = append(snap.Decisions, f.Statement)
		}
	}

	// Extract constraints from architecture rules.
	for _, r := range pkt.ArchitectureRules {
		snap.Constraints = append(snap.Constraints, r.ID)
	}

	// Extract files.
	for _, f := range pkt.Files {
		snap.Files = append(snap.Files, f.Path)
	}

	// Extract risks (use Factors + Mitigation as the description).
	for _, r := range pkt.Risks {
		desc := r.Mitigation
		if desc == "" && len(r.Factors) > 0 {
			desc = r.Factors[0]
		}
		snap.Risks = append(snap.Risks, desc)
	}

	// Tests from RequiredValidation.
	snap.Tests = append(snap.Tests, pkt.RequiredValidation...)

	return snap
}
