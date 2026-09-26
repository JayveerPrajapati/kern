package domain

import "time"

// DogfoodRecord is one observed gate outcome on kern's own repository — a
// failed or passed run of a self-governance gate (kern check, kern doctor
// --arch-drift). The dogfooding meta-loop (Self-Improvement use-cases Tier 4
// #10) records the gate's deterministic failure signature when it fails and
// the subsequent pass when the class is fixed, so the learning layer can
// infer "this class of gate failure recurs; it was fixed before" as a
// typed-claim INFERENCE memory the next self-refactor reads automatically.
// The guardrail is "learning proposes": the output is memory only — nothing
// here changes gates, caps, or policy.
type DogfoodRecord struct {
	// Gate is the gate that produced the outcome: "check" (kern check) or
	// "doctor" (kern doctor --arch-drift).
	Gate string
	// Signature is the deterministic failure signature of the gate run: the
	// first failing check name for kern check, or "arch-drift:<subsystem>:<kind>"
	// for kern doctor --arch-drift. The same (gate, signature) pair must be
	// used for the failed and the later passed observation so the extractor
	// can pair them.
	Signature string
	// Failed reports whether the gate run failed (true) or passed (false).
	Failed bool
	// At is when the gate outcome was observed.
	At time.Time
}
