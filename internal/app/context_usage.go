// Context-usage wiring (Self-Improvement use-cases Tier 2 #5 — outcome-driven
// prompt/context optimization).
//
// The app layer is the caller side of the contract:
//   - ComputeSliceUsage derives one ContextUsageRecord per named context-packet
//     slice from a task's packet and the files/symbols the task outcome
//     actually used (deterministic, no LLM);
//   - RecordContextUsage accumulates those records into a small running log
//     (persisted best-effort at <root>/.kern/context_usage.json, mirroring the
//     calibrate prediction-log convention) and proposes learning via
//     typed-claim memories.
//
// The guardrail is "learning proposes, budget approves": the output is memory
// only — nothing here touches internal/context, internal/budget,
// internal/optimize, or packet assembly.

package app

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/fsutil"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// DefaultContextUsageThreshold is the minimum number of observed outcome
// records for a slice kind before the learning pass may propose anything.
// Callers pass it unless they have a reason to vary the threshold.
const DefaultContextUsageThreshold = 5

// usageLogCap trims the accumulated context-usage log to the newest entries,
// mirroring the calibrate prediction-log cap.
const usageLogCap = 5000

// usageSlice kinds are the canonical context-packet slice names the usage
// learning measures. They mirror the ContextPacket slice fields one-to-one.
const (
	usageSliceFiles           = "files"
	usageSliceSymbols         = "symbols"
	usageSliceMemory          = "memory"
	usageSliceIncidents       = "incidents"
	usageSliceRuntimeEvidence = "runtime_evidence"
	usageSliceArchRules       = "architecture_rules"
)

// usageLogPath is the best-effort persistence location for the accumulated
// usage log (mirrors the calibrate prediction-log path convention, JSON
// instead of JSONL).
func usageLogPath(root string) string { return filepath.Join(root, ".kern", "context_usage.json") }

// ComputeSliceUsage derives one ContextUsageRecord per named context-packet
// slice by counting how many of the slice's members the outcome's used
// files/symbols cover. Slices with zero members are skipped (nothing to
// learn). A nil packet yields no records.
//
// Matching is exact and deterministic (set membership after trimming):
//
//   - files:             a File member is used when File.Path appears in
//     usedFiles;
//   - symbols:           a Symbol member is used when Symbol.Name appears in
//     usedSymbols OR Symbol.File (the symbol's defining file) appears in
//     usedFiles;
//   - memory / incidents: a Memory member is used when its Scope
//     substring-matches any used file/symbol entry (either the scope contains
//     the entry or the entry contains the scope);
//   - runtime_evidence:  an Evidence member is used when Evidence.Source —
//     the "tool/file/commit that produced this evidence" attribute — appears
//     in usedFiles;
//   - architecture_rules: a Policy member is used when Policy.ID or
//     Policy.Name appears in usedSymbols, or its Scope substring-matches like
//     memory.
//
// Outcome and At are left empty/zero: the caller stamps them with the
// outcome kind and observation time.
func ComputeSliceUsage(packet *domain.ContextPacket, usedFiles []string, usedSymbols []string) []domain.ContextUsageRecord {
	if packet == nil {
		return nil
	}
	files := cleanSet(usedFiles)
	symbols := cleanSet(usedSymbols)

	var records []domain.ContextUsageRecord
	add := func(slice string, members, used int) {
		if members == 0 {
			return // nothing to learn from a slice the packet did not carry
		}
		records = append(records, domain.ContextUsageRecord{
			Task:    packet.Task,
			Slice:   slice,
			Members: members,
			Used:    used,
		})
	}

	add(usageSliceFiles, len(packet.Files), countUsed(packet.Files, func(f domain.File) bool {
		return files[f.Path]
	}))
	add(usageSliceSymbols, len(packet.Symbols), countUsed(packet.Symbols, func(sym domain.Symbol) bool {
		return symbols[sym.Name] || files[sym.File]
	}))
	add(usageSliceMemory, len(packet.Memory), countUsed(packet.Memory, func(m domain.Memory) bool {
		return scopeSubstring(m.Scope, files, symbols)
	}))
	add(usageSliceIncidents, len(packet.Incidents), countUsed(packet.Incidents, func(m domain.Memory) bool {
		return scopeSubstring(m.Scope, files, symbols)
	}))
	add(usageSliceRuntimeEvidence, len(packet.RuntimeEvidence), countUsed(packet.RuntimeEvidence, func(ev domain.Evidence) bool {
		return files[ev.Source]
	}))
	add(usageSliceArchRules, len(packet.ArchitectureRules), countUsed(packet.ArchitectureRules, func(p domain.Policy) bool {
		return symbols[p.ID] || symbols[p.Name] || scopeSubstring(p.Scope, files, symbols)
	}))
	return records
}

// cleanSet normalizes a string slice into a trimmed, de-duplicated membership
// set, dropping empty entries. Order-independent, so input ordering never
// affects matching.
func cleanSet(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out[s] = true
		}
	}
	return out
}

// countUsed counts how many members match the used predicate.
func countUsed[T any](members []T, used func(T) bool) int {
	n := 0
	for _, m := range members {
		if used(m) {
			n++
		}
	}
	return n
}

// scopeSubstring reports whether the member's scope substring-matches any
// used file or symbol entry: either the scope contains the entry, or the
// entry contains the scope (both directions, non-empty guard only — the
// caller's scope is already non-empty).
func scopeSubstring(scope string, files, symbols map[string]bool) bool {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return false
	}
	for s := range files {
		if s != "" && (strings.Contains(scope, s) || strings.Contains(s, scope)) {
			return true
		}
	}
	for s := range symbols {
		if s != "" && (strings.Contains(scope, s) || strings.Contains(s, scope)) {
			return true
		}
	}
	return false
}

// RecordContextUsage aggregates a batch of usage records into the running
// per-slice-kind usage log and proposes learning from the accumulated
// records: it runs learning.ContextUsagePatterns over the log and writes each
// resulting pattern via learning.Remember (upsert-by-scope, so repeated
// observations refresh one constraint instead of duplicating). It returns the
// number of memories written.
//
// Nil-guarded exactly like recordCalibrationClaims: a nil memory store is a
// no-op (0, nil) that never panics, so unwired paths keep their zero behavior
// change. The running log is persisted best-effort as JSON at
// <root>/.kern/context_usage.json (mirroring the calibrate prediction-log
// convention: 0600 atomic write, capped to the newest entries); a missing or
// corrupt log loads as empty and starts fresh. Learning proposes via
// RECOMMENDATION/INFERENCE memories only — nothing here changes packet
// assembly or budgets.
func RecordContextUsage(records []domain.ContextUsageRecord, mem *memory.MemoryStore, threshold int) (int, error) {
	if mem == nil {
		return 0, nil
	}
	accumulated := loadUsageLog(mem.Root())
	accumulated = append(accumulated, records...)
	if len(accumulated) > usageLogCap {
		accumulated = accumulated[len(accumulated)-usageLogCap:]
	}
	if err := saveUsageLog(mem.Root(), accumulated); err != nil {
		log.Printf("kern app: context usage log NOT persisted: %v", err)
	}
	patterns := learning.ContextUsagePatterns(accumulated, threshold)
	if len(patterns) == 0 {
		return 0, nil
	}
	ex := learning.New(mem)
	written := 0
	for _, p := range patterns {
		if _, err := ex.Remember(p); err != nil {
			log.Printf("kern app: context usage memory NOT recorded: %v", err)
			continue
		}
		written++
	}
	return written, nil
}

// loadUsageLog reads the accumulated usage log for root, returning an empty
// log when it is absent. A corrupt log is preserved as "<path>.corrupt" (like
// the memory store) and starts fresh.
func loadUsageLog(root string) []domain.ContextUsageRecord {
	path := usageLogPath(root)
	b, err := os.ReadFile(path)
	if err != nil {
		return []domain.ContextUsageRecord{}
	}
	var out []domain.ContextUsageRecord
	if err := json.Unmarshal(b, &out); err != nil {
		if re := os.Rename(path, path+".corrupt"); re != nil {
			log.Printf("kern app: corrupt context usage log %s: %v (rename: %v)", path, err, re)
		} else {
			log.Printf("kern app: corrupt context usage log %s renamed to .corrupt: %v", path, err)
		}
		return []domain.ContextUsageRecord{}
	}
	if out == nil {
		out = []domain.ContextUsageRecord{}
	}
	return out
}

// saveUsageLog persists the accumulated usage log for root (best-effort:
// 0600 atomic write, mirroring the calibrate prediction-log convention).
func saveUsageLog(root string, records []domain.ContextUsageRecord) error {
	path := usageLogPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, b, 0o600)
}

// recordContextUsage is the choke-point helper: it stamps the outcome kind
// and observation time on the packet-derived records and hands them to
// RecordContextUsage. Best-effort by design — errors are logged and never
// block the task flow (same style as recordCalibrationClaims). A nil packet,
// unwired memory store, or unresolvable target is a silent no-op.
func (s *TaskService) recordContextUsage(t *agent.Task, change string) {
	if t == nil || t.ContextPacket == nil || s.platform == nil || s.platform.Memory() == nil {
		return
	}
	target, _, err := s.platform.resolveSymbol(change)
	if err != nil || strings.TrimSpace(target) == "" {
		return
	}
	usedSymbols := []string{target}
	var usedFiles []string
	if f := s.platform.graphNodeFile(target); f != "" {
		usedFiles = []string{f}
	}
	records := ComputeSliceUsage(t.ContextPacket, usedFiles, usedSymbols)
	if len(records) == 0 {
		return
	}
	now := time.Now()
	for i := range records {
		records[i].Outcome = "analyze"
		records[i].At = now
	}
	if _, err := RecordContextUsage(records, s.platform.Memory(), DefaultContextUsageThreshold); err != nil {
		log.Printf("kern app: context usage NOT recorded: %v", err)
	}
}
