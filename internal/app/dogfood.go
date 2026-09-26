// Dogfooding meta-loop wiring (Self-Improvement use-cases Tier 4 #10).
//
// The app layer is the caller side of the contract:
//   - DogfoodPatterns derives the typed-claim patterns from the accumulated
//     gate-outcome log (deterministic, no LLM): a (gate, signature) pair that
//     failed at least minFailures times AND later passed becomes one
//     INFERENCE claim — "this class of gate failure recurs; it was fixed
//     before" — so the next self-refactor gets that context automatically;
//   - RecordDogfood accumulates gate outcomes into a small running log
//     (persisted best-effort at <root>/.kern/dogfood.json, mirroring the
//     context_usage.json convention) and proposes learning via typed-claim
//     memories;
//   - HasPriorDogfoodFailure / PriorDogfoodFailures let the pass-sites in
//     cmd/kern record the "fix observed" side of the meta-loop honestly: a
//     passing run records Failed:false only for (gate, signature) pairs that
//     previously failed, never for signatures with no failure history.
//
// The extractor lives here, not in internal/learning (whose LOC cap is full);
// learning is used ONLY for Remember (upsert-by-scope, idempotent).
//
// The guardrail is "learning proposes, budget approves": the output is memory
// only — nothing here changes gates, caps, or policy.

package app

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/fsutil"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// DefaultDogfoodMinFailures is the minimum number of observed FAILED records
// for a (gate, signature) pair before the learning pass may propose anything.
// Callers pass it unless they have a reason to vary the threshold.
const DefaultDogfoodMinFailures = 2

// dogfoodLogCap trims the accumulated dogfood log to the newest entries,
// mirroring the context-usage log cap.
const dogfoodLogCap = 5000

// dogfoodLogPath is the best-effort persistence location for the accumulated
// dogfood log (mirrors the context_usage.json path convention).
func dogfoodLogPath(root string) string { return filepath.Join(root, ".kern", "dogfood.json") }

// DogfoodPatterns groups dogfood records by (gate, signature). A group with
// >= minFailures FAILED records AND at least one PASSED record (Failed=false,
// same gate+signature) becomes one INFERENCE pattern:
//
//	"gate <gate> failed on <signature> <f> time(s), then passed <p> time(s)
//	 — recurring breakage class; failure+fix recorded for next refactor context"
//
// scoped "dogfood:<gate>:<signature>", with Count = f+p (failures plus
// passes) and provenance = deduped sorted observation timestamps. Groups with
// failures but no observed pass contribute nothing (the fix is not yet
// observed — honest); groups with only passes contribute nothing. Deterministic:
// sorted groups, deduped+sorted sources, patterns ordered by key then
// statement. minFailures <= 0 is treated as 1.
func DogfoodPatterns(records []domain.DogfoodRecord, minFailures int) []learning.Pattern {
	if minFailures <= 0 {
		minFailures = 1
	}
	groups := map[string][]domain.DogfoodRecord{}
	for _, r := range records {
		gate := strings.TrimSpace(r.Gate)
		sig := strings.TrimSpace(r.Signature)
		if gate == "" || sig == "" {
			continue
		}
		groups["dogfood:"+gate+":"+sig] = append(groups["dogfood:"+gate+":"+sig], r)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	patterns := make([]learning.Pattern, 0, len(keys))
	for _, key := range keys {
		g := groups[key]
		failures, passes := 0, 0
		for _, r := range g {
			if r.Failed {
				failures++
			} else {
				passes++
			}
		}
		if failures >= minFailures && passes > 0 {
			patterns = append(patterns, dogfoodPattern(key, g, failures, passes))
		}
	}
	sort.Slice(patterns, func(i, j int) bool {
		if patterns[i].Key != patterns[j].Key {
			return patterns[i].Key < patterns[j].Key
		}
		return patterns[i].Statement < patterns[j].Statement
	})
	return patterns
}

// dogfoodPattern assembles the INFERENCE pattern for one (gate, signature)
// group that shows both recurring failure and a subsequent pass. The gate and
// signature come from the group's records (homogeneous by construction), so
// colons inside the signature never break key parsing.
func dogfoodPattern(key string, records []domain.DogfoodRecord, failures, passes int) learning.Pattern {
	n := len(records)
	gate := records[0].Gate
	sig := records[0].Signature
	statement := fmt.Sprintf(
		"gate %s failed on %s %d time(s), then passed %d time(s) — recurring breakage class; failure+fix recorded for next refactor context",
		gate, sig, failures, passes)
	srcSet := map[string]bool{}
	var latest time.Time
	for _, r := range records {
		srcSet[r.At.UTC().Format(time.RFC3339)] = true
		if r.At.After(latest) {
			latest = r.At
		}
	}
	srcs := make([]string, 0, len(srcSet))
	for s := range srcSet {
		srcs = append(srcs, s)
	}
	sort.Strings(srcs)
	return learning.Pattern{
		Key:       key,
		Count:     n,
		Scopes:    []string{key},
		Sample:    []string{statement},
		Created:   latest,
		ClaimType: domain.ClaimInference,
		Provenance: learning.ClaimProvenance{
			Sources: srcs,
			Count:   n,
			Latest:  latest,
		},
		Statement: statement,
	}
}

// RecordDogfood aggregates a batch of gate-outcome records into the running
// dogfood log and proposes learning from the accumulated records: it runs
// DogfoodPatterns over the log and writes each resulting pattern via
// learning.Remember (upsert-by-scope, so repeated observations refresh one
// INFERENCE claim instead of duplicating). It returns the number of memories
// written.
//
// Nil-guarded exactly like RecordContextUsage / RecordArchitectureDrift: a
// nil memory store is a no-op (0, nil) that never panics, so unwired paths
// keep their zero behavior change. The running log is persisted best-effort
// as JSON at <root>/.kern/dogfood.json (0600 atomic write, capped to the
// newest entries); a missing or corrupt log loads as empty and starts fresh.
// Learning proposes via INFERENCE memories only — nothing here changes gates
// or policy.
func RecordDogfood(records []domain.DogfoodRecord, mem *memory.MemoryStore, minFailures int) (int, error) {
	if mem == nil {
		return 0, nil
	}
	accumulated := loadDogfoodLog(mem.Root())
	accumulated = append(accumulated, records...)
	if len(accumulated) > dogfoodLogCap {
		accumulated = accumulated[len(accumulated)-dogfoodLogCap:]
	}
	if err := saveDogfoodLog(mem.Root(), accumulated); err != nil {
		log.Printf("kern app: dogfood log NOT persisted: %v", err)
	}
	patterns := DogfoodPatterns(accumulated, minFailures)
	if len(patterns) == 0 {
		return 0, nil
	}
	ex := learning.New(mem)
	written := 0
	for _, p := range patterns {
		if _, err := ex.Remember(p); err != nil {
			log.Printf("kern app: dogfood memory NOT recorded: %v", err)
			continue
		}
		written++
	}
	return written, nil
}

// HasPriorDogfoodFailure reports whether the accumulated dogfood log for the
// store's root already contains a FAILED record for (gate, signature). It is
// the pass-site guard for the "record the fix" side of the meta-loop: a pass
// observation is only meaningful when a prior failure with the same signature
// exists. Nil-guarded: a nil store reports false.
func HasPriorDogfoodFailure(mem *memory.MemoryStore, gate, signature string) bool {
	if mem == nil {
		return false
	}
	for _, r := range loadDogfoodLog(mem.Root()) {
		if r.Gate == gate && r.Signature == signature && r.Failed {
			return true
		}
	}
	return false
}

// PriorDogfoodFailures returns the distinct (Gate, Signature) pairs in the
// accumulated dogfood log that have at least one FAILED record, sorted
// deterministically (gate, then signature). Failed/At are zero on the
// returned records — only Gate and Signature carry the pair identity.
// Nil-guarded: a nil store returns nothing. Pass-sites iterate it to record
// Failed:false observations only for signatures with a real failure history,
// so the extractor's failure→pass pairing stays honest.
func PriorDogfoodFailures(mem *memory.MemoryStore) []domain.DogfoodRecord {
	if mem == nil {
		return nil
	}
	seen := map[string]domain.DogfoodRecord{}
	for _, r := range loadDogfoodLog(mem.Root()) {
		if !r.Failed {
			continue
		}
		key := r.Gate + "\x00" + r.Signature
		if _, ok := seen[key]; !ok {
			seen[key] = domain.DogfoodRecord{Gate: r.Gate, Signature: r.Signature}
		}
	}
	out := make([]domain.DogfoodRecord, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Gate != out[j].Gate {
			return out[i].Gate < out[j].Gate
		}
		return out[i].Signature < out[j].Signature
	})
	return out
}

// loadDogfoodLog reads the accumulated dogfood log for root, returning an
// empty log when it is absent. A corrupt log is preserved as "<path>.corrupt"
// (like the memory store) and starts fresh.
func loadDogfoodLog(root string) []domain.DogfoodRecord {
	path := dogfoodLogPath(root)
	b, err := os.ReadFile(path)
	if err != nil {
		return []domain.DogfoodRecord{}
	}
	var out []domain.DogfoodRecord
	if err := json.Unmarshal(b, &out); err != nil {
		if re := os.Rename(path, path+".corrupt"); re != nil {
			log.Printf("kern app: corrupt dogfood log %s: %v (rename: %v)", path, err, re)
		} else {
			log.Printf("kern app: corrupt dogfood log %s renamed to .corrupt: %v", path, err)
		}
		return []domain.DogfoodRecord{}
	}
	if out == nil {
		out = []domain.DogfoodRecord{}
	}
	return out
}

// saveDogfoodLog persists the accumulated dogfood log for root (best-effort:
// 0600 atomic write, mirroring the context_usage.json convention).
func saveDogfoodLog(root string, records []domain.DogfoodRecord) error {
	path := dogfoodLogPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, b, 0o600)
}
