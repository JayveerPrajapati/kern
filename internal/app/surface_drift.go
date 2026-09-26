// Tool-catalog drift prediction (Self-Improvement use-cases Tier 4 #11).
//
// The app layer is the caller side of the contract:
//   - SurfaceTouchesFromGit scans the recent git history of a repository and
//     maps each commit's changed paths onto the tool-catalog parity surfaces
//     (deterministic, stdlib os/exec only, no LLM);
//   - SurfaceDriftPatterns derives the typed-claim patterns from the
//     accumulated touch log: a risk event is a commit that touched the
//     catalog without the plugin, or the plugin without the docs — the exact
//     drift-prone pattern the parity gates (TestPluginMatchesMCPCatalog,
//     catalog:drift, the G36/G37 doc checks) only catch AFTER the fact. A
//     kind with >= minRisk events becomes one INFERENCE claim that pre-flags
//     the drift BEFORE the next verify/plan run;
//   - RecordSurfaceDrift accumulates touch records into a small running log
//     (persisted best-effort at <root>/.kern/surface_drift.json, mirroring
//     the dogfood.json convention) and proposes learning via typed-claim
//     memories.
//
// The extractor lives here, not in internal/learning (whose LOC cap is full);
// learning is used ONLY for Remember (upsert-by-scope, idempotent).
//
// The guardrail is "learning proposes, budget approves": the output is memory
// only — nothing here changes the catalog, the plugin, the docs, or any gate.

package app

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/fsutil"
	"github.com/JayveerPrajapati/kern/internal/learning"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// DefaultSurfaceDriftMinRisk is the minimum number of observed risk events
// for a touch kind before the learning pass may propose anything. Callers
// pass it unless they have a reason to vary the threshold.
const DefaultSurfaceDriftMinRisk = 3

// SurfaceDriftMaxCommits is the default number of commits scanned per run
// (callers pass SurfaceTouchesFromGit's maxCommits unless they vary it).
const SurfaceDriftMaxCommits = 50

// surfaceDriftLogCap trims the accumulated surface-drift log to the newest
// entries, mirroring the dogfood log cap.
const surfaceDriftLogCap = 5000

// surfaceDriftLogPath is the best-effort persistence location for the
// accumulated surface-drift log (mirrors the dogfood.json path convention).
func surfaceDriftLogPath(root string) string {
	return filepath.Join(root, ".kern", "surface_drift.json")
}

// SurfaceTouchesFromGit scans the repository's recent commit history and
// maps each commit's changed paths onto the tool-catalog parity surfaces.
// It runs `git -C <root> log -n <maxCommits> --name-status
// --pretty=format:%H%x09%cI` (hash + committer timestamp per commit block,
// then one "<status>\t<path>" line per changed path), stdlib os/exec only.
//
// Path mapping (matches exactly the parity surfaces the drift gates check):
//
//	CatalogTouched: paths under "internal/mcp/catalog/" — the catalog
//	    manifest (catalog.ToolNames() is the source of truth behind
//	    mcp.ToolNames(), which TestPluginMatchesMCPCatalog compares against
//	    the plugin; tool-registration edits elsewhere in internal/mcp —
//	    handlers, transports — do not change the catalog surface).
//	PluginTouched: ".opencode/plugins/kern.ts" or
//	    "internal/setup/assets/plugin/kern.ts" — the plugin pair that
//	    TestPluginMatchesMCPCatalog requires to stay byte-identical.
//	DocsTouched: "docs/tool-catalog.md" or "docs/mcp/tool-contracts.md" —
//	    the regenerated tool docs the diffgate doc checks (G36/G37) verify
//	    against the live catalog.
//
// Records are returned sorted deterministically by commit hash. Commits that
// touched none of the parity surfaces are dropped (nothing to learn). A
// non-git root, a git error, or an empty history is a best-effort skip: it
// returns a nil error and an empty slice, never blocking the host flow.
func SurfaceTouchesFromGit(root string, maxCommits int) ([]domain.SurfaceTouchRecord, error) {
	if strings.TrimSpace(root) == "" {
		return []domain.SurfaceTouchRecord{}, nil
	}
	if maxCommits <= 0 {
		maxCommits = SurfaceDriftMaxCommits
	}
	cmd := exec.Command("git", "-C", root, "log", "-n", strconv.Itoa(maxCommits),
		"--name-status", "--pretty=format:%H%x09%cI")
	out, err := cmd.Output()
	if err != nil {
		return []domain.SurfaceTouchRecord{}, nil // non-git dir or git error: skip
	}
	records := parseSurfaceTouches(string(out))
	sort.Slice(records, func(i, j int) bool { return records[i].Commit < records[j].Commit })
	return records, nil
}

// parseSurfaceTouches parses the `git log --name-status --pretty=format:%H
// %x09%cI` output: each commit block starts with "<hash>\t<timestamp>",
// followed by "<status>\t<path>" lines (renames/copies are
// "<status>\t<old>\t<new>", the changed path being the last field). A commit
// is kept only when it touched at least one parity surface.
func parseSurfaceTouches(out string) []domain.SurfaceTouchRecord {
	var records []domain.SurfaceTouchRecord
	var cur *domain.SurfaceTouchRecord
	flush := func() {
		if cur != nil && (cur.CatalogTouched || cur.PluginTouched || cur.DocsTouched) {
			records = append(records, *cur)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) == 2 && isCommitHash(fields[0]) {
			if at, err := time.Parse(time.RFC3339, fields[1]); err == nil {
				flush() // previous commit block ends here
				cur = &domain.SurfaceTouchRecord{Commit: fields[0], At: at}
				continue
			}
		}
		if cur == nil {
			continue
		}
		switch path := changedPath(fields); {
		case isCatalogSurface(path):
			cur.CatalogTouched = true
		case isPluginSurface(path):
			cur.PluginTouched = true
		case isDocsSurface(path):
			cur.DocsTouched = true
		}
	}
	flush()
	return records
}

// isCommitHash reports whether s looks like a full git commit hash (%H emits
// exactly 40 lowercase hex digits).
func isCommitHash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// changedPath extracts the changed path from a `--name-status` field list.
// Plain entries are "<status>\t<path>"; renames/copies are
// "<status>\t<old>\t<new>" and the changed path is the last field.
func changedPath(fields []string) string {
	if len(fields) < 2 {
		return ""
	}
	return fields[len(fields)-1]
}

// isCatalogSurface reports whether the changed path is part of the MCP
// catalog manifest — internal/mcp/catalog/, the source of truth behind
// mcp.ToolNames().
func isCatalogSurface(path string) bool {
	return strings.HasPrefix(path, "internal/mcp/catalog/")
}

// isPluginSurface reports whether the changed path is either copy of the
// opencode plugin that must stay byte-identical.
func isPluginSurface(path string) bool {
	return path == ".opencode/plugins/kern.ts" ||
		path == "internal/setup/assets/plugin/kern.ts"
}

// isDocsSurface reports whether the changed path is one of the regenerated
// tool docs the diffgate doc checks verify against the live catalog.
func isDocsSurface(path string) bool {
	return path == "docs/tool-catalog.md" ||
		path == "docs/mcp/tool-contracts.md"
}

// SurfaceDriftPatterns groups surface-touch records into drift-risk kinds. A
// RISK EVENT is a commit record with (CatalogTouched && !PluginTouched) —
// kind "catalog-without-plugin" — or (PluginTouched && !DocsTouched) — kind
// "plugin-without-docs". A kind with >= minRisk events becomes one INFERENCE
// pattern:
//
//	"<n> commits touched the <a> surface without the <b> — parity drift
//	 risk; run parity tests before merge"
//
// scoped "surface:<kind>", with Count = n and provenance = deduped sorted
// commit hashes plus the latest timestamp. Below-threshold kinds contribute
// nothing. Deterministic: kinds sorted, sources deduped+sorted, patterns
// ordered by key then statement. minRisk <= 0 is treated as 1.
func SurfaceDriftPatterns(records []domain.SurfaceTouchRecord, minRisk int) []learning.Pattern {
	if minRisk <= 0 {
		minRisk = 1
	}
	byKind := map[string][]domain.SurfaceTouchRecord{}
	for _, r := range records {
		if r.CatalogTouched && !r.PluginTouched {
			byKind["catalog-without-plugin"] = append(byKind["catalog-without-plugin"], r)
		}
		if r.PluginTouched && !r.DocsTouched {
			byKind["plugin-without-docs"] = append(byKind["plugin-without-docs"], r)
		}
	}
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	patterns := make([]learning.Pattern, 0, len(kinds))
	for _, kind := range kinds {
		if group := byKind[kind]; len(group) >= minRisk {
			patterns = append(patterns, surfaceDriftPattern(kind, group))
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

// surfaceDriftPattern assembles the INFERENCE pattern for one drift-risk kind
// group (homogeneous by construction). The statement names the touched
// surface and the peer surface the commit failed to keep in sync.
func surfaceDriftPattern(kind string, records []domain.SurfaceTouchRecord) learning.Pattern {
	n := len(records)
	key := "surface:" + kind
	a, b := surfacePair(kind)
	statement := fmt.Sprintf(
		"%d commits touched the %s surface without the %s — parity drift risk; run parity tests before merge",
		n, a, b)
	srcSet := map[string]bool{}
	var latest time.Time
	for _, r := range records {
		if c := strings.TrimSpace(r.Commit); c != "" {
			srcSet[c] = true
		}
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

// surfacePair names the touched surface (a) and the peer it must stay in
// sync with (b) for a drift-risk kind, for the statement wording.
func surfacePair(kind string) (a, b string) {
	switch kind {
	case "catalog-without-plugin":
		return "catalog", "plugin"
	case "plugin-without-docs":
		return "plugin", "docs"
	}
	return kind, "peer"
}

// RecordSurfaceDrift aggregates a batch of surface-touch records into the
// running drift log and proposes learning from the accumulated records: it
// runs SurfaceDriftPatterns over the log and writes each resulting pattern
// via learning.Remember (upsert-by-scope, so repeated observations refresh
// one INFERENCE claim instead of duplicating). It returns the number of
// memories written.
//
// Nil-guarded exactly like RecordDogfood / RecordContextUsage: a nil memory
// store is a no-op (0, nil) that never panics, so unwired paths keep their
// zero behavior change. The running log is persisted best-effort as JSON at
// <root>/.kern/surface_drift.json (0600 atomic write, capped to the newest
// entries); a missing or corrupt log loads as empty and starts fresh.
//
// DELIBERATE DEVIATION from the dogfood/context-usage recorders: records are
// deduped by Commit hash — a commit already present in the log is not
// appended again. The touch log is append-only per commit (a commit's surface
// touches never change after it lands), so re-scanning the same history on
// every `kern check` run would otherwise grow the log without adding
// information; dedupe keeps repeated runs idempotent without growing the log.
// The extractor runs over the accumulated deduped log.
func RecordSurfaceDrift(records []domain.SurfaceTouchRecord, mem *memory.MemoryStore, minRisk int) (int, error) {
	if mem == nil {
		return 0, nil
	}
	accumulated := loadSurfaceDriftLog(mem.Root())
	seen := map[string]bool{}
	for _, r := range accumulated {
		seen[r.Commit] = true
	}
	for _, r := range records {
		if r.Commit == "" || seen[r.Commit] {
			continue // already recorded (dedupe by commit hash — see above)
		}
		accumulated = append(accumulated, r)
		seen[r.Commit] = true
	}
	if len(accumulated) > surfaceDriftLogCap {
		accumulated = accumulated[len(accumulated)-surfaceDriftLogCap:]
	}
	if err := saveSurfaceDriftLog(mem.Root(), accumulated); err != nil {
		log.Printf("kern app: surface drift log NOT persisted: %v", err)
	}
	patterns := SurfaceDriftPatterns(accumulated, minRisk)
	if len(patterns) == 0 {
		return 0, nil
	}
	ex := learning.New(mem)
	written := 0
	for _, p := range patterns {
		if _, err := ex.Remember(p); err != nil {
			log.Printf("kern app: surface drift memory NOT recorded: %v", err)
			continue
		}
		written++
	}
	return written, nil
}

// loadSurfaceDriftLog reads the accumulated surface-drift log for root,
// returning an empty log when it is absent. A corrupt log is preserved as
// "<path>.corrupt" (like the memory store) and starts fresh.
func loadSurfaceDriftLog(root string) []domain.SurfaceTouchRecord {
	path := surfaceDriftLogPath(root)
	b, err := os.ReadFile(path)
	if err != nil {
		return []domain.SurfaceTouchRecord{}
	}
	var out []domain.SurfaceTouchRecord
	if err := json.Unmarshal(b, &out); err != nil {
		if re := os.Rename(path, path+".corrupt"); re != nil {
			log.Printf("kern app: corrupt surface drift log %s: %v (rename: %v)", path, err, re)
		} else {
			log.Printf("kern app: corrupt surface drift log %s renamed to .corrupt: %v", path, err)
		}
		return []domain.SurfaceTouchRecord{}
	}
	if out == nil {
		out = []domain.SurfaceTouchRecord{}
	}
	return out
}

// saveSurfaceDriftLog persists the accumulated surface-drift log for root
// (best-effort: 0600 atomic write, mirroring the dogfood.json convention).
func saveSurfaceDriftLog(root string, records []domain.SurfaceTouchRecord) error {
	path := surfaceDriftLogPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, b, 0o600)
}
