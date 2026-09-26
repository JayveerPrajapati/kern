// Cost/latency policy learning (Self-Improvement use-cases Tier 2 #6): learns
// per task kind which (model, settings) combo hits the verify-pass rate
// threshold cheapest, and proposes that learning as typed-claim memories —
// RECOMMENDATION for the cheapest within-threshold model ("consider
// defaulting"), INFERENCE for below-threshold models ("review selection").
//
// The app layer is the caller side of the contract (the extractor lives here,
// not in internal/learning, whose LOC cap is full):
//   - ModelPolicyPatterns derives the typed-claim patterns from accumulated
//     verify-outcome records (deterministic, no LLM);
//   - RecordModelOutcomes accumulates those records into a small running log
//     (persisted best-effort at <root>/.kern/model_policy.json, mirroring the
//     context_usage.json convention) and proposes learning via
//     learning.Remember (upsert-by-scope, idempotent).
//
// The guardrail is "learning proposes, provider selection approves": the
// output is memory only — nothing here touches internal/llm, provider
// selection, or budgets.

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

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/agents"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/fsutil"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// DefaultModelMinSamples is the minimum number of observed verify outcomes
// for a (kind, model) pair before the learning pass may propose anything.
// Callers pass it unless they have a reason to vary the threshold.
const DefaultModelMinSamples = 5

// DefaultModelPassThreshold is the integer verify-pass percentage a (kind,
// model) group must reach before the model counts as "within threshold".
const DefaultModelPassThreshold = 80

// modelPolicyLogCap trims the accumulated model-policy log to the newest
// entries, mirroring the context-usage log cap.
const modelPolicyLogCap = 5000

// modelPolicyLogPath is the best-effort persistence location for the
// accumulated model-policy log (mirrors the context_usage.json path
// convention).
func modelPolicyLogPath(root string) string { return filepath.Join(root, ".kern", "model_policy.json") }

// modelOutcomeGroup accumulates the observed verify outcomes for one
// (kind, model) pair.
type modelOutcomeGroup struct {
	records []domain.ModelOutcomeRecord
}

// scoredGroup is a (kind, model) group with >= minSamples records, scored
// deterministically: key order (kind then model) fixes the processing order,
// pct is the integer pass rate (passed*100/n rounded down), and cost is the
// task-ID-ordered EstCost total.
type scoredGroup struct {
	key    string
	group  *modelOutcomeGroup
	passed int
	pct    int
	cost   float64
}

// ModelPolicyPatterns derives cost/latency-policy signals from observed
// verify-outcome records. Records are grouped by (kind, model); each group
// with at least minSamples records is scored by its integer pass rate —
// passed*100/n rounded DOWN, computed with integer arithmetic so there is no
// floating-point nondeterminism:
//
//   - pass rate >= passThreshold% → the model is "within threshold" for its
//     kind; when several models of the same kind clear the threshold, only
//     the LOWEST total EstCost one becomes a RECOMMENDATION ("cheapest
//     within threshold — consider defaulting"; ties broken by model name
//     ascending). Within-threshold models that are NOT the cheapest
//     contribute nothing (they are fine, just not the cheapest).
//   - pass rate < passThreshold% → INFERENCE "below the <t>% threshold —
//     review selection".
//   - fewer than minSamples records → nothing.
//
// Deterministic: groups are processed in kind-then-model order, provenance
// sources are deduped + sorted task IDs with the newest observed timestamp,
// EstCost totals fold in task-ID order (so the sum is independent of input
// ordering), and the returned patterns are ordered by scope then statement.
// minSamples <= 0 is treated as 1; passThreshold <= 0 is treated as
// DefaultModelPassThreshold.
func ModelPolicyPatterns(records []domain.ModelOutcomeRecord, minSamples, passThreshold int) []learning.Pattern {
	if minSamples <= 0 {
		minSamples = 1
	}
	if passThreshold <= 0 {
		passThreshold = DefaultModelPassThreshold
	}
	groups := map[string]*modelOutcomeGroup{}
	for _, r := range records {
		kind := strings.TrimSpace(r.Kind)
		model := strings.TrimSpace(r.Model)
		if kind == "" || model == "" {
			continue
		}
		key := "model:" + kind + ":" + model
		g, ok := groups[key]
		if !ok {
			g = &modelOutcomeGroup{}
			groups[key] = g
		}
		g.records = append(g.records, r)
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// eligible holds every (kind, model) group with >= minSamples records.
	eligible := make([]scoredGroup, 0, len(keys))
	for _, key := range keys {
		g := groups[key]
		if len(g.records) < minSamples {
			continue
		}
		var passed int
		for _, r := range g.records {
			if r.Passed {
				passed++
			}
		}
		eligible = append(eligible, scoredGroup{
			key:    key,
			group:  g,
			passed: passed,
			pct:    passed * 100 / len(g.records), // integer percent, rounded down
			cost:   modelGroupCost(g),
		})
	}

	// Cheapest within-threshold model per kind: lowest total EstCost, ties
	// broken by model name ascending. Only this model becomes a
	// RECOMMENDATION; other within-threshold models of the same kind get
	// nothing.
	winner := map[string]scoredGroup{} // kind -> winning group
	for _, sg := range eligible {
		if sg.pct < passThreshold {
			continue
		}
		kind := strings.TrimPrefix(sg.key, "model:")
		kind = kind[:strings.Index(kind, ":")]
		w, ok := winner[kind]
		if !ok || sg.cost < w.cost || (sg.cost == w.cost && groupModel(sg) < groupModel(w)) {
			winner[kind] = sg
		}
	}

	patterns := make([]learning.Pattern, 0, len(eligible))
	for _, sg := range eligible {
		kind := groupKind(sg)
		model := groupModel(sg)
		n := len(sg.group.records)
		switch {
		case sg.pct < passThreshold:
			statement := fmt.Sprintf(
				"model %s for %s tasks passes verify %d%% (%d runs) — below the %d%% threshold; review selection",
				model, kind, sg.pct, n, passThreshold)
			patterns = append(patterns, modelPolicyPattern(sg.key, sg.group, statement, domain.ClaimInference))
		case winner[kind].key == sg.key:
			statement := fmt.Sprintf(
				"for %s tasks, model %s passes verify %d%% (%d runs, est $%.4f) — cheapest within threshold; consider defaulting",
				kind, model, sg.pct, n, sg.cost)
			patterns = append(patterns, modelPolicyPattern(sg.key, sg.group, statement, domain.ClaimRecommendation))
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

// groupKind returns the kind component of a scored group's "model:<kind>:<model>" key.
func groupKind(sg scoredGroup) string {
	rest := strings.TrimPrefix(sg.key, "model:")
	return rest[:strings.Index(rest, ":")]
}

// groupModel returns the model component of a scored group's key.
func groupModel(sg scoredGroup) string {
	rest := strings.TrimPrefix(sg.key, "model:")
	return rest[strings.Index(rest, ":")+1:]
}

// modelGroupCost sums a group's estimated costs in deterministic (task-ID)
// order, so the total is independent of input ordering — no floating-point
// nondeterminism.
func modelGroupCost(g *modelOutcomeGroup) float64 {
	sorted := append([]domain.ModelOutcomeRecord(nil), g.records...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Task < sorted[j].Task })
	var cost float64
	for _, r := range sorted {
		cost += r.EstCost
	}
	return cost
}

// modelPolicyPattern builds the common Pattern shape: the deterministic
// "model:<kind>:<model>" key as scope, the statement as the sample/content
// source, and provenance = the contributing task IDs (deduped + sorted) with
// the newest observed timestamp.
func modelPolicyPattern(key string, g *modelOutcomeGroup, statement string, ct domain.ClaimType) learning.Pattern {
	n := len(g.records)
	srcSet := map[string]bool{}
	var latest time.Time
	for _, r := range g.records {
		srcSet["task "+r.Task] = true
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
		ClaimType: ct,
		Provenance: learning.ClaimProvenance{
			Sources: srcs,
			Count:   n,
			Latest:  latest,
		},
		Statement: statement,
	}
}

// RecordModelOutcomes aggregates a batch of verify-outcome records into the
// running per-(kind, model) policy log and proposes learning from the
// accumulated records: it runs ModelPolicyPatterns over the log and writes
// each resulting pattern via learning.Remember (upsert-by-scope, so repeated
// observations refresh one claim instead of duplicating). It returns the
// number of memories written.
//
// Nil-guarded exactly like RecordContextUsage / RecordArchitectureDrift: a
// nil memory store is a no-op (0, nil) that never panics, so unwired paths
// keep their zero behavior change. The running log is persisted best-effort
// as JSON at <root>/.kern/model_policy.json (0600 atomic write, capped to the
// newest entries); a missing or corrupt log loads as empty and starts fresh.
// Learning proposes via RECOMMENDATION/INFERENCE memories only — nothing here
// changes internal/llm or provider selection.
func RecordModelOutcomes(records []domain.ModelOutcomeRecord, mem *memory.MemoryStore, minSamples, passThreshold int) (int, error) {
	if mem == nil {
		return 0, nil
	}
	accumulated := loadModelPolicyLog(mem.Root())
	accumulated = append(accumulated, records...)
	if len(accumulated) > modelPolicyLogCap {
		accumulated = accumulated[len(accumulated)-modelPolicyLogCap:]
	}
	if err := saveModelPolicyLog(mem.Root(), accumulated); err != nil {
		log.Printf("kern app: model policy log NOT persisted: %v", err)
	}
	patterns := ModelPolicyPatterns(accumulated, minSamples, passThreshold)
	if len(patterns) == 0 {
		return 0, nil
	}
	ex := learning.New(mem)
	written := 0
	for _, p := range patterns {
		if _, err := ex.Remember(p); err != nil {
			log.Printf("kern app: model policy memory NOT recorded: %v", err)
			continue
		}
		written++
	}
	return written, nil
}

// loadModelPolicyLog reads the accumulated model-policy log for root,
// returning an empty log when it is absent. A corrupt log is preserved as
// "<path>.corrupt" (like the memory store) and starts fresh.
func loadModelPolicyLog(root string) []domain.ModelOutcomeRecord {
	path := modelPolicyLogPath(root)
	b, err := os.ReadFile(path)
	if err != nil {
		return []domain.ModelOutcomeRecord{}
	}
	var out []domain.ModelOutcomeRecord
	if err := json.Unmarshal(b, &out); err != nil {
		if re := os.Rename(path, path+".corrupt"); re != nil {
			log.Printf("kern app: corrupt model policy log %s: %v (rename: %v)", path, err, re)
		} else {
			log.Printf("kern app: corrupt model policy log %s renamed to .corrupt: %v", path, err)
		}
		return []domain.ModelOutcomeRecord{}
	}
	if out == nil {
		out = []domain.ModelOutcomeRecord{}
	}
	return out
}

// saveModelPolicyLog persists the accumulated model-policy log for root
// (best-effort: 0600 atomic write, mirroring the context_usage.json
// convention).
func saveModelPolicyLog(root string, records []domain.ModelOutcomeRecord) error {
	path := modelPolicyLogPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, b, 0o600)
}

// recordModelOutcome is the verify choke-point helper: it stamps one verify
// outcome for the task's classified kind and the loop-configured model, and
// hands it to RecordModelOutcomes. Best-effort by design — errors are logged
// and never block the verify flow (same style as recordCalibrationClaims). A
// nil platform, unwired memory store, or nil task is a silent no-op.
//
// Resolution at the choke point (documented choice):
//   - kind: agents.ClassifyTask(t.Input, t.Type), the same call the workflow
//     router uses; a standalone Verify/ExecuteAndVerify task carries only the
//     "verify"/"execute patch" intent, so it classifies as "code" (the
//     deterministic classifier's default) — matching #5's note that these
//     tasks run fresh without a ContextPacket;
//   - model: the task has no per-task model field and the Platform carries no
//     model config, so the loop/llm configured model is used — the KERN_MODEL
//     env var the llm factory honors (internal/llm resolves model = arg →
//     KERN_MODEL → llm.model config → DefaultModel). internal/app's allowed
//     deps exclude internal/config and internal/llm, so the recorded model is
//     the env-resolved name, or "default" when KERN_MODEL is unset (the llm
//     factory's provider-default fallback at runtime);
//   - tokens/cost: the fresh verify task carries no token counts (no
//     ContextPacket), so Tokens and EstCost are recorded as 0 and the
//     learning relies on pass-rate + model recommendation.
func (s *TaskService) recordModelOutcome(t *agent.Task, passed bool) {
	if s.platform == nil || s.platform.Memory() == nil || t == nil {
		return
	}
	now := time.Now()
	if _, err := RecordModelOutcomes([]domain.ModelOutcomeRecord{{
		Task:   t.ID,
		Kind:   modelPolicyKind(agents.ClassifyTask(t.Input, t.Type)),
		Model:  modelPolicyModel(),
		Passed: passed,
		At:     now,
	}}, s.platform.Memory(), DefaultModelMinSamples, DefaultModelPassThreshold); err != nil {
		log.Printf("kern app: model outcome NOT recorded: %v", err)
	}
}

// modelPolicyKind renders a TaskKind as its canonical lowercase name for the
// model-policy records (mirrors the kind names the pipeline plan uses).
func modelPolicyKind(k agents.TaskKind) string {
	switch k {
	case agents.TaskKindDocumentation:
		return "documentation"
	case agents.TaskKindIncident:
		return "incident"
	case agents.TaskKindModernization:
		return "modernization"
	case agents.TaskKindDefault:
		return "default"
	default:
		return "code"
	}
}

// modelPolicyModel resolves the model name recorded for a verify outcome:
// the KERN_MODEL env var the llm factory honors, or "default" when unset
// (see recordModelOutcome for the full documented choice).
func modelPolicyModel() string {
	if m := strings.TrimSpace(os.Getenv("KERN_MODEL")); m != "" {
		return m
	}
	return "default"
}
