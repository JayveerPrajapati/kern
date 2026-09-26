package domain

import "time"

// ArchitectureDriftRecord is one observed drift sample for a subsystem in the
// ARCHITECTURE.md subsystem ledger — the LOC-cap / allowed-deps parity gate
// TestArchitectureDocParity enforces. The `kern doctor --arch-drift` path
// converts the parity report's per-subsystem findings into these records; the
// learning layer aggregates records per (subsystem, violation kind) and
// proposes pre-flag RECOMMENDATION claims ("changes touching this subsystem
// are prone to this kind of violation") as typed-claim memories. The guardrail
// is "learning proposes, governance approves": the output is memory only —
// nothing here changes gates, caps, or policy.
type ArchitectureDriftRecord struct {
	// Subsystem is the ARCHITECTURE.md subsystem dir the drift was observed
	// in, e.g. "internal/web".
	Subsystem string
	// ViolationKind is the drift condition that fired. Canonical kinds
	// (the parity checker's own drift conditions):
	//   "loc-over-cap"               — measured LOC exceeds the documented cap;
	//   "import-not-in-allowed-deps" — new internal imports outside the row's
	//                                  allowed deps;
	//   "degraded-warn"              — structural problem (missing directory
	//                                  or unmeasurable source).
	ViolationKind string
	// Change is the optional change/commit ref that introduced the drift:
	// for deps drift, the offending new imports (comma-joined); empty when
	// there is no change ref (LOC/structural drift).
	Change string
	// At is when the drift was observed.
	At time.Time
}
