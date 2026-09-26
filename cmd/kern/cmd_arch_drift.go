package main

// archDriftFindings renders the ARCHITECTURE.md parity check (the same
// per-subsystem LOC caps + allowed-deps validation TestArchitectureDocParity
// enforces) as doctor findings. Drift is reported as warnings — never fatal:
// `kern doctor --arch-drift` exits non-zero only when the parity run itself
// errors (unreadable ARCHITECTURE.md, broken table, unmeasurable source).
//
// The check lives here (cmd/kern) rather than inside internal/doctor so the
// architecture dependency stays off internal/doctor's documented allowed-deps
// set — cmd/kern is not a row in the ARCHITECTURE.md table, so no table
// change is required. The validation logic itself is shared with the drift
// test via internal/architecture.CheckArchDocParity; nothing is re-implemented.
//
// The same parity report also feeds the digital-twin drift learner
// (Self-Improvement use-cases Tier 3 #9): archDriftRecords maps each
// per-subsystem drift condition to a domain.ArchitectureDriftRecord and
// recordArchDrift hands them to app.RecordArchitectureDrift, which proposes
// typed-claim RECOMMENDATION memories ("changes touching subsystem X are
// prone to kind-Y violations — pre-flag at plan time"). Learning proposes via
// memory only — nothing here changes gates, caps, or policy.

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/architecture"
	"github.com/JayveerPrajapati/kern/internal/doctor"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// archDriftSection runs the parity check exactly once and derives both the
// doctor findings to render and the drift records to learn from from the
// SAME report, so `kern doctor --arch-drift` measures the ledger once per
// invocation. On a hard parity error (unreadable ARCHITECTURE.md, broken
// table) it returns the fail finding and no records — nothing is learned from
// a broken measure.
func archDriftSection(root string) ([]doctor.Finding, []domain.ArchitectureDriftRecord) {
	report, err := architecture.CheckArchDocParity(root)
	if err != nil {
		return []doctor.Finding{{
			Check:  "arch-drift",
			Level:  "fail",
			Detail: fmt.Sprintf("architecture parity run failed: %v", err),
		}}, nil
	}
	return archDriftFindings(report), archDriftRecords(report)
}

// archDriftFindings renders the parity report as doctor findings (warn-level
// per drifted row, ok-level when nothing drifts).
func archDriftFindings(report *architecture.ArchDocReport) []doctor.Finding {
	var out []doctor.Finding
	driftRows := 0
	for _, f := range report.Findings {
		lvl := "ok"
		detail := fmt.Sprintf("%s: %d/%d LOC", f.Subsystem, f.LOC, f.Cap)
		switch {
		case f.Err != "":
			lvl = "warn"
			driftRows++
			detail += " — " + f.Err
		case f.LOC > f.Cap:
			lvl = "warn"
			driftRows++
			detail += fmt.Sprintf(" (over cap; suggested cap %d)", int(float64(f.LOC)*1.5/100)*100+100)
		}
		if len(f.Violations) > 0 {
			lvl = "warn"
			driftRows++
			detail += fmt.Sprintf("; %d new import(s) outside allowed deps: %s", len(f.Violations), strings.Join(f.Violations, ", "))
		}
		out = append(out, doctor.Finding{Check: "arch-drift", Level: lvl, Detail: detail})
	}
	if driftRows == 0 {
		out = append(out, doctor.Finding{
			Check:  "arch-drift",
			Level:  "ok",
			Detail: fmt.Sprintf("%d subsystem(s) within documented LOC caps and allowed deps", report.Rows),
		})
	}
	return out
}

// archDriftRecords maps the parity report's per-subsystem drift findings to
// the record shape the drift learner consumes. The mapping is deterministic
// and uses the parity checker's own drift conditions (the validator's kind
// strings):
//
//	Subsystem            -> the ARCHITECTURE.md subsystem dir (f.Subsystem);
//	"loc-over-cap"        -> f.LOC exceeds f.Cap (no change ref);
//	"import-not-in-allowed-deps" -> len(f.Violations) > 0, with the offending
//	                        new imports (sorted, comma-joined) as the change ref;
//	"degraded-warn"       -> f.Err non-empty (missing directory or
//	                        unmeasurable source; no change ref).
//
// A subsystem can contribute up to three records (one per drift condition) in
// a single run; all carry the same observation timestamp.
func archDriftRecords(report *architecture.ArchDocReport) []domain.ArchitectureDriftRecord {
	if report == nil {
		return nil
	}
	now := time.Now()
	var records []domain.ArchitectureDriftRecord
	for _, f := range report.Findings {
		if f.Err != "" {
			records = append(records, domain.ArchitectureDriftRecord{
				Subsystem:     f.Subsystem,
				ViolationKind: "degraded-warn",
				At:            now,
			})
		}
		if f.LOC > f.Cap {
			records = append(records, domain.ArchitectureDriftRecord{
				Subsystem:     f.Subsystem,
				ViolationKind: "loc-over-cap",
				At:            now,
			})
		}
		if len(f.Violations) > 0 {
			records = append(records, domain.ArchitectureDriftRecord{
				Subsystem:     f.Subsystem,
				ViolationKind: "import-not-in-allowed-deps",
				Change:        strings.Join(f.Violations, ", "),
				At:            now,
			})
		}
	}
	return records
}

// recordArchDrift hands the drift records to the app-layer recorder, which
// persists the running drift log at <root>/.kern/arch_drift.json and proposes
// typed-claim RECOMMENDATION memories via the deterministic learning pass.
// Best-effort by design: the memory store is opened on the same root the rest
// of the system uses (so plan/verify read the proposed constraints), and any
// failure is logged and never affects the doctor findings or exit code —
// learning proposes via memory only.
func recordArchDrift(records []domain.ArchitectureDriftRecord, root string) {
	if len(records) == 0 {
		return
	}
	if _, err := app.RecordArchitectureDrift(records, memory.NewMemoryStore(root), app.DefaultArchitectureDriftThreshold); err != nil {
		log.Printf("kern: architecture drift NOT recorded: %v", err)
	}
}
