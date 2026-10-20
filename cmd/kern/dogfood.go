// Dogfooding meta-loop wiring (Self-Improvement use-cases Tier 4 #10): when a
// gate fails on kern's own repository — kern check, kern doctor --arch-drift
// — the failure AND its subsequent resolution become typed-claim INFERENCE
// memories, so the next self-refactor gets "this class of gate failure
// recurs; it was fixed before" context automatically.
//
// The recording rules are honest and deterministic:
//   - a FAILED run records the failure's deterministic signature (the first
//     failing check name for check; "arch-drift:<subsystem>:<kind>" per drift
//     record for doctor);
//   - a PASSING run records the fix (Failed:false) ONLY for (gate, signature)
//     pairs that previously failed in the log — never for signatures without
//     a failure history — so the extractor's failure→pass pairing stays
//     honest (groups with failures but no observed pass propose nothing).
//
// Best-effort by design: recording is nil-guarded, logs any failure, and
// never changes the gate's output or exit code. Learning proposes via memory
// only — nothing here changes gates, caps, or policy.

package main

import (
	"log"
	"time"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// recordDriftDogfood records the `kern doctor --arch-drift` gate outcome: a
// run that found drift records one failed record per drift condition
// (signature "arch-drift:<subsystem>:<kind>"); a clean run records the fix
// for every previously-failed arch-drift signature.
func recordDriftDogfood(driftRecords []domain.ArchitectureDriftRecord, root string) {
	if root == "" {
		return
	}
	mem := memory.NewMemoryStore(root)
	if len(driftRecords) > 0 {
		records := make([]domain.DogfoodRecord, 0, len(driftRecords))
		for _, d := range driftRecords {
			records = append(records, domain.DogfoodRecord{
				Gate:      "doctor",
				Signature: "arch-drift:" + d.Subsystem + ":" + d.ViolationKind,
				Failed:    true,
				At:        d.At,
			})
		}
		recordDogfoodGate(records, mem)
		return
	}
	recordPassesForGate(mem, "doctor")
}

// recordPassesForGate records a Failed:false observation for every (gate,
// signature) pair in the log with a prior failure — the "fix observed" side
// of the meta-loop. Deterministic: PriorDogfoodFailures returns the pairs
// sorted by gate then signature.
func recordPassesForGate(mem *memory.MemoryStore, gate string) {
	now := time.Now()
	var passes []domain.DogfoodRecord
	for _, p := range app.PriorDogfoodFailures(mem) {
		if p.Gate != gate {
			continue
		}
		passes = append(passes, domain.DogfoodRecord{
			Gate:      gate,
			Signature: p.Signature,
			Failed:    false,
			At:        now,
		})
	}
	recordDogfoodGate(passes, mem)
}

// recordDogfoodGate hands the gate-outcome records to the app-layer recorder,
// which persists the running log at <root>/.kern/dogfood.json and proposes
// typed-claim INFERENCE memories via the deterministic learning pass.
// Best-effort by design: any failure is logged and never affects the gate's
// output or exit code — learning proposes via memory only.
func recordDogfoodGate(records []domain.DogfoodRecord, mem *memory.MemoryStore) {
	if len(records) == 0 || mem == nil {
		return
	}
	if _, err := app.RecordDogfood(records, mem, app.DefaultDogfoodMinFailures); err != nil {
		log.Printf("kern: dogfood gate outcome NOT recorded: %v", err)
	}
}
