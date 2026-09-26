// Architecture-drift wiring (Self-Improvement use-cases Tier 3 #9 —
// digital-twin drift learning).
//
// The app layer is the caller side of the contract:
//   - RecordArchitectureDrift accumulates per-subsystem drift records (the
//     ARCHITECTURE.md ledger's LOC-cap / allowed-deps drift conditions
//     surfaced by `kern doctor --arch-drift`) into a small running log
//     (persisted best-effort at <root>/.kern/arch_drift.json, mirroring the
//     context_usage.json convention) and proposes learning via typed-claim
//     memories: which change classes systematically break the architecture
//     gates get RECOMMENDATION "pre-flag at plan time" claims.
//
// The guardrail is "learning proposes, governance approves": the output is
// memory only — nothing here changes gates, caps, or policy.

package app

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/fsutil"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// DefaultArchitectureDriftThreshold is the minimum number of observed drift
// records for a (subsystem, violation kind) pair before the learning pass may
// propose anything. Callers pass it unless they have a reason to vary the
// threshold.
const DefaultArchitectureDriftThreshold = 3

// archDriftLogCap trims the accumulated drift log to the newest entries,
// mirroring the context-usage log cap.
const archDriftLogCap = 5000

// archDriftLogPath is the best-effort persistence location for the
// accumulated drift log (mirrors the context_usage.json path convention).
func archDriftLogPath(root string) string { return filepath.Join(root, ".kern", "arch_drift.json") }

// RecordArchitectureDrift aggregates a batch of drift records into the
// running drift log and proposes learning from the accumulated records: it
// runs learning.ArchitectureDriftPatterns over the log and writes each
// resulting pattern via learning.Remember (upsert-by-scope, so repeated
// observations refresh one constraint instead of duplicating). It returns the
// number of memories written.
//
// Nil-guarded exactly like RecordContextUsage: a nil memory store is a no-op
// (0, nil) that never panics, so unwired paths keep their zero behavior
// change. The running log is persisted best-effort as JSON at
// <root>/.kern/arch_drift.json (0600 atomic write, capped to the newest
// entries); a missing or corrupt log loads as empty and starts fresh.
// Learning proposes via RECOMMENDATION memories only — nothing here changes
// gates, caps, or policy.
func RecordArchitectureDrift(records []domain.ArchitectureDriftRecord, mem *memory.MemoryStore, threshold int) (int, error) {
	if mem == nil {
		return 0, nil
	}
	accumulated := loadArchDriftLog(mem.Root())
	accumulated = append(accumulated, records...)
	if len(accumulated) > archDriftLogCap {
		accumulated = accumulated[len(accumulated)-archDriftLogCap:]
	}
	if err := saveArchDriftLog(mem.Root(), accumulated); err != nil {
		log.Printf("kern app: architecture drift log NOT persisted: %v", err)
	}
	patterns := learning.ArchitectureDriftPatterns(accumulated, threshold)
	if len(patterns) == 0 {
		return 0, nil
	}
	ex := learning.New(mem)
	written := 0
	for _, p := range patterns {
		if _, err := ex.Remember(p); err != nil {
			log.Printf("kern app: architecture drift memory NOT recorded: %v", err)
			continue
		}
		written++
	}
	return written, nil
}

// loadArchDriftLog reads the accumulated drift log for root, returning an
// empty log when it is absent. A corrupt log is preserved as "<path>.corrupt"
// (like the memory store) and starts fresh.
func loadArchDriftLog(root string) []domain.ArchitectureDriftRecord {
	path := archDriftLogPath(root)
	b, err := os.ReadFile(path)
	if err != nil {
		return []domain.ArchitectureDriftRecord{}
	}
	var out []domain.ArchitectureDriftRecord
	if err := json.Unmarshal(b, &out); err != nil {
		if re := os.Rename(path, path+".corrupt"); re != nil {
			log.Printf("kern app: corrupt architecture drift log %s: %v (rename: %v)", path, err, re)
		} else {
			log.Printf("kern app: corrupt architecture drift log %s renamed to .corrupt: %v", path, err)
		}
		return []domain.ArchitectureDriftRecord{}
	}
	if out == nil {
		out = []domain.ArchitectureDriftRecord{}
	}
	return out
}

// saveArchDriftLog persists the accumulated drift log for root (best-effort:
// 0600 atomic write, mirroring the context_usage.json convention).
func saveArchDriftLog(root string, records []domain.ArchitectureDriftRecord) error {
	path := archDriftLogPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, b, 0o600)
}
