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
	"bytes"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
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
// archHeadroomFindings renders the headroom section: every subsystem under
// HeadroomWarnPct of its cap, tightest first, with its documented extraction
// candidate. Warn-level (a tight package is a scheduling signal, not a
// failure); the definite ok line keeps the section legible when clean.
func archHeadroomFindings(report *architecture.ArchDocReport) []doctor.Finding {
	rows := headroomRows(report)
	if len(rows) == 0 {
		return []doctor.Finding{{
			Check:  "arch-headroom",
			Level:  "ok",
			Detail: fmt.Sprintf("every subsystem has at least %.0f%% cap headroom", headroomWarnPct),
		}}
	}
	out := make([]doctor.Finding, 0, len(rows))
	for _, r := range rows {
		out = append(out, doctor.Finding{Check: "arch-headroom", Level: "warn", Detail: r.detail()})
	}
	return out
}

// headroomGateFindings is the advisory CI check: it fails (fail-level, exit 1
// via doctorExitCode) only when THIS change moved a subsystem from above the
// tight line to at/below it. It is advisory by wiring — CI runs it in a
// non-blocking lane — but the failure is real and names the subsystem, the
// before/after percentages, and the required action. HEAD's ledger is read
// straight from git so the comparison never depends on the working tree's
// uncommitted doc state.
func headroomGateFindings(report *architecture.ArchDocReport, root string) []doctor.Finding {
	headRows, err := headArchRows(root)
	if err != nil {
		// No git history (or an unborn HEAD): nothing to compare against, so
		// the advisory check stays silent rather than failing spuriously.
		return nil
	}
	headLOC, err := headArchLOC(root, headRows)
	if err != nil {
		// No committed Go sources to measure (unborn HEAD): stay silent.
		return nil
	}
	crossings := headroomCrossings(report, headRows, headLOC)
	out := make([]doctor.Finding, 0, 1+len(crossings))
	if len(crossings) == 0 {
		out = append(out, doctor.Finding{
			Check:  "arch-headroom-gate",
			Level:  "ok",
			Detail: "no subsystem crossed the tight-headroom line in this change",
		})
	}
	for _, c := range crossings {
		out = append(out, doctor.Finding{Check: "arch-headroom-gate", Level: "fail", Detail: c.detail()})
	}
	return append(out, staleBaselineFindings(root, report)...)
}

// headArchLOC measures every ledger row's non-test Go LOC AS OF HEAD with one
// git process: `git grep -c "" HEAD -- *.go` emits per-file line counts, which
// are summed into the row with the longest matching directory prefix (the
// ledger's subtree rule). This is a real HEAD measurement — the pre-fix gate
// derived "before" from the ledger's baseline column, so a drifted baseline
// made a clean tree look like a crossing.
func headArchLOC(root string, rows []architecture.ArchRow) (map[string]int, error) {
	cmd := exec.Command("git", "-C", root, "grep", "-c", "", "HEAD", "--", "*.go")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return map[string]int{}, nil // no matches: unborn/empty tree
		}
		return nil, err
	}
	dirs := make([]string, 0, len(rows))
	for _, r := range rows {
		dirs = append(dirs, r.Dir)
	}
	res := map[string]int{}
	for _, line := range strings.Split(string(out), "\n") {
		first := strings.IndexByte(line, ':')
		last := strings.LastIndexByte(line, ':')
		if first < 0 || last <= first {
			continue
		}
		path, count := line[first+1:last], line[last+1:]
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		n, convErr := strconv.Atoi(count)
		if convErr != nil {
			continue
		}
		best := ""
		for _, d := range dirs {
			if (path == d || strings.HasPrefix(path, d+"/")) && len(d) > len(best) {
				best = d
			}
		}
		if best != "" {
			res[best] += n
		}
	}
	return res, nil
}

// staleBaselineFindings reports a drifted ledger baseline column — but only on
// a CLEAN tree, where the baseline is supposed to equal the live measurement.
// One warn finding names the drifted rows; it never fails the gate (the
// crossing check is the enforcement, and caps are unchanged).
func staleBaselineFindings(root string, report *architecture.ArchDocReport) []doctor.Finding {
	if report == nil || !gitTreeClean(root) {
		return nil
	}
	rows, err := architecture.ParseArchDoc(root)
	if err != nil {
		return nil
	}
	base := make(map[string]int, len(rows))
	for _, r := range rows {
		base[r.Dir] = r.Baseline
	}
	var stale []string
	for _, f := range report.Findings {
		b := base[f.Subsystem]
		if f.Err != "" || f.Cap <= 0 || b <= 0 {
			continue
		}
		if math.Abs(float64(f.LOC-b))/float64(b)*100 > 2 {
			stale = append(stale, fmt.Sprintf("%s (baseline %d, live %d)", f.Subsystem, b, f.LOC))
		}
	}
	if len(stale) == 0 {
		return nil
	}
	sort.Strings(stale)
	n := len(stale)
	if n > 8 {
		stale = append(stale[:8], fmt.Sprintf("+%d more", n-8))
	}
	return []doctor.Finding{{
		Check:  "arch-baseline",
		Level:  "warn",
		Detail: fmt.Sprintf("%d ledger baseline(s) drifted >2%% from live LOC — refresh the table: %s", n, strings.Join(stale, ", ")),
	}}
}

// gitTreeClean reports whether the working tree has no tracked or untracked
// changes (the condition under which a ledger baseline must match live LOC).
func gitTreeClean(root string) bool {
	cmd := exec.Command("git", "-C", root, "status", "--porcelain")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	return err == nil && len(bytes.TrimSpace(out)) == 0
}

// headArchRows parses ARCHITECTURE.md as of HEAD (git show) so the gate
// compares against the committed ledger, not the in-progress one.
func headArchRows(root string) ([]architecture.ArchRow, error) {
	cmd := exec.Command("git", "-C", root, "show", "HEAD:ARCHITECTURE.md")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return architecture.ParseArchDocBytes(out)
}

// archDriftSection measures the ledger ONCE per doctor run and returns the
// parity findings plus the drift records, with the parity report so the
// headroom section and the advisory gate reuse the same measurement instead
// of walking the tree again.
func archDriftSection(root string) ([]doctor.Finding, []domain.ArchitectureDriftRecord, *architecture.ArchDocReport) {
	report, err := architecture.CheckArchDocParity(root)
	if err != nil {
		return []doctor.Finding{{
			Check:  "arch-drift",
			Level:  "fail",
			Detail: fmt.Sprintf("architecture parity run failed: %v", err),
		}}, nil, nil
	}
	return archDriftFindings(report), archDriftRecords(report), report
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
	for _, dir := range report.Unlisted {
		driftRows++
		out = append(out, doctor.Finding{
			Check:  "arch-drift",
			Level:  "warn",
			Detail: fmt.Sprintf("%s: Go package missing from the ledger (no own row, no ancestor row) — add rows to ARCHITECTURE.md + docs/architecture/ledger-details.md", dir),
		})
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
//		Subsystem            -> the ARCHITECTURE.md subsystem dir (f.Subsystem);
//		"loc-over-cap"        -> f.LOC exceeds f.Cap (no change ref);
//		"import-not-in-allowed-deps" -> len(f.Violations) > 0, with the offending
//		                        new imports (sorted, comma-joined) as the change ref;
//		"unlisted-package"    -> the dir appears in report.Unlisted (a Go package
//	                       with no ledger row of its own and no ancestor row);
//		"degraded-warn"       -> f.Err non-empty (missing directory or
//	                       unmeasurable source; no change ref).
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
	for _, dir := range report.Unlisted {
		records = append(records, domain.ArchitectureDriftRecord{
			Subsystem:     dir,
			ViolationKind: "unlisted-package",
			At:            now,
		})
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
