// Policy-signal wiring (Self-Improvement use-cases Tier 3 #7 — approval-log
// → policy refinement).
//
// The app layer is the caller side of the contract: after every human
// approve/reject decision it learns from the governance approval log which
// proposed actions ALWAYS get approved (pre-approval recommendation) vs which
// are risky (policy-review inference), writing the signals as typed-claim
// memories. The guardrail is "learning proposes, policy change approves":
// the output is memory only — nothing here changes the firewall, gates, or
// policy store.

package app

import (
	"log"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// DefaultPolicySignalThreshold is the minimum number of approved decisions
// for an action signature before the learning pass proposes a pre-approval
// recommendation. Callers pass it unless they have a reason to vary the
// threshold.
const DefaultPolicySignalThreshold = 3

// RecordPolicySignals learns from the governance approval log: it reads every
// decided approval from the store, runs the deterministic policy-signal
// extractor, and writes each signal as a typed-claim memory
// (RECOMMENDATION for always-approved actions, INFERENCE for risky ones) via
// learning.Remember, which upserts by scope so repeated runs refresh one
// constraint instead of duplicating. It returns the number of memories
// written.
//
// Nil-guarded exactly like recordCalibrationClaims: a nil store or nil memory
// store is a no-op (0, nil) that never panics, so unwired paths keep their
// zero behavior change. Best-effort by design — callers log and ignore
// errors; learning never blocks the approval decision it runs after.
func RecordPolicySignals(store *governance.FileStore, mem *memory.MemoryStore, threshold int) (int, error) {
	if store == nil || mem == nil {
		return 0, nil
	}
	decisions, err := store.Decisions()
	if err != nil {
		return 0, err
	}
	patterns := learning.PolicyPatterns(decisions, threshold)
	if len(patterns) == 0 {
		return 0, nil
	}
	ex := learning.New(mem)
	written := 0
	for _, p := range patterns {
		if _, err := ex.Remember(p); err != nil {
			log.Printf("kern app: policy signal memory NOT recorded: %v", err)
			continue
		}
		written++
	}
	return written, nil
}
