// Package auditdiag owns the .kern/audit diagnostics and retention:
// verification run-directory pruning, the doctor findings for audit growth,
// and the dropped-entry sidecar check. It is separate from internal/auditrot
// (chain rotation policy) and internal/doctor (the diagnostic runner) to keep
// both under their ARCHITECTURE.md LOC caps.
package auditdiag

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Finding is one audit diagnostic result (mirrors doctor.Finding).
type Finding struct {
	Check  string `json:"check"`
	Level  string `json:"level"` // ok, warn, fail
	Detail string `json:"detail"`
}

// runDirRe matches verification run directories: YYYYMMDD-HHMMSS.NNNNNNNNN
// (internal/verification's runID). Only these are pruned, so an operator's own
// file dropped in .kern/audit is never deleted.
var runDirRe = regexp.MustCompile(`^\d{8}-\d{6}\.\d{9}$`)

// VerifyLogKeepDefault is how many of the newest verification run directories
// are retained. Each run holds the full build/test/security logs, so a repo
// with many CI runs otherwise accumulates thousands of directories (1,921
// observed 2026-10-28).
const VerifyLogKeepDefault = 50

func envInt(name string, def int64) int64 {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

// IsRunDir reports whether name is a verification run directory.
func IsRunDir(name string) bool { return runDirRe.MatchString(name) }

// VerifyLogKeep returns the run-directory retention count
// (KERN_VERIFY_LOG_KEEP).
func VerifyLogKeep() int { return int(envInt("KERN_VERIFY_LOG_KEEP", VerifyLogKeepDefault)) }

// PruneRunDirs deletes the oldest verification run directories beyond keep
// (keep <= 0 keeps all). It never touches archives, .lock, or unknown
// directories. It is best-effort: an unreadable directory removes nothing.
func PruneRunDirs(auditDir string, keep int) (removed int) {
	if keep <= 0 {
		return 0
	}
	entries, err := os.ReadDir(auditDir)
	if err != nil {
		return 0
	}
	var runs []string
	for _, e := range entries {
		if e.IsDir() && runDirRe.MatchString(e.Name()) {
			runs = append(runs, e.Name())
		}
	}
	sort.Strings(runs)
	for len(runs) > keep {
		if err := os.RemoveAll(filepath.Join(auditDir, runs[0])); err != nil {
			return removed
		}
		removed++
		runs = runs[1:]
	}
	return removed
}

// dirSize totals the regular files under dir (best-effort).
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, ierr := d.Info(); ierr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// Findings returns every .kern/audit diagnostic for root.
func Findings(root string) []Finding {
	return []Finding{AuditDropped(), AuditChainSize(root), auditRetention(root)}
}

// AuditDropped surfaces audit entries lost before they reached the store. The
// sidecar is checked in the user's home (not the project store) on purpose: a
// store that cannot be written cannot record its own loss.
func AuditDropped() Finding {
	home, err := os.UserHomeDir()
	if err != nil {
		return Finding{Check: "audit-dropped", Level: "ok", Detail: "no home dir — dropped-audit sidecar not checked"}
	}
	data, err := os.ReadFile(filepath.Join(home, ".kern", "audit", "dropped.jsonl"))
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return Finding{Check: "audit-dropped", Level: "ok", Detail: "no dropped audit entries (sidecar absent or empty)"}
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	noun := "entries"
	if len(lines) == 1 {
		noun = "entry"
	}
	detail := fmt.Sprintf("%d audit %s dropped — tool calls were unaffected, but the evidence chain has gaps (lost at write time, not tampered); review, then truncate the sidecar to re-arm", len(lines), noun)
	var last struct {
		Ts string `json:"ts"`
	}
	if json.Unmarshal([]byte(lines[len(lines)-1]), &last) == nil && last.Ts != "" {
		detail += ", last at " + last.Ts
	}
	return Finding{Check: "audit-dropped", Level: "warn", Detail: detail}
}

// AuditChainSize reports the governance audit chain's footprint. The chain is
// append-only by design — each record's hash covers its predecessor, so
// in-place trimming would break tamper-evidence — and kern never truncates it.
func AuditChainSize(root string) Finding {
	path := filepath.Join(root, ".kern", "audit", "chain.jsonl")
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		return Finding{Check: "audit-chain-size", Level: "ok", Detail: "no governance audit chain yet"}
	}
	const warnBytes = 50 << 20
	size := fi.Size()
	detail := fmt.Sprintf("governance audit chain: %.1f MB", float64(size)/(1<<20))
	if size >= warnBytes {
		return Finding{Check: "audit-chain-size", Level: "warn", Detail: detail + " — append-only by design (records are hash-chained, kern never truncates); archive it by moving chain.jsonl aside (a fresh chain starts on the next append) and keep the archive for audit history"}
	}
	return Finding{Check: "audit-chain-size", Level: "ok", Detail: detail}
}

// auditRetention reports .kern/audit growth: a warning when the directory
// crosses the size or run-count budget, otherwise ok. Thresholds are
// env-overridable for tests (KERN_AUDIT_WARN_BYTES, KERN_AUDIT_WARN_RUNS).
func auditRetention(root string) Finding {
	auditDir := filepath.Join(root, ".kern", "audit")
	if st, err := os.Stat(auditDir); err != nil || !st.IsDir() {
		return Finding{Check: "audit retention", Level: "ok", Detail: "no audit directory"}
	}
	size := dirSize(auditDir)
	runs := 0
	if entries, err := os.ReadDir(auditDir); err == nil {
		for _, e := range entries {
			if e.IsDir() && runDirRe.MatchString(e.Name()) {
				runs++
			}
		}
	}
	maxBytes := envInt("KERN_AUDIT_WARN_BYTES", 100<<20)
	maxRuns := envInt("KERN_AUDIT_WARN_RUNS", 500)
	if (maxBytes > 0 && size >= maxBytes) || (maxRuns > 0 && int64(runs) >= maxRuns) {
		return Finding{Check: "audit retention", Level: "warn",
			Detail: fmt.Sprintf(".kern/audit is %.1f MB across %d run(s); prune with `kern audit rotate --keep 3`", float64(size)/(1<<20), runs)}
	}
	return Finding{Check: "audit retention", Level: "ok",
		Detail: fmt.Sprintf(".kern/audit is %.1f MB across %d run(s)", float64(size)/(1<<20), runs)}
}
