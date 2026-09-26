package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// TestSurfaceTouchesFromGit proves the git-history parser against a real
// fixture repository: each parity surface maps from the actual paths the
// drift gates check, rename entries map to the new path, records come back
// sorted by commit hash, and a non-git directory is a silent skip (empty
// slice, nil error).
func TestSurfaceTouchesFromGit(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) string {
		t.Helper()
		git("add", "-A")
		git("commit", "-q", "-m", msg)
		return git("rev-parse", "HEAD")
	}

	git("init", "-q", "-b", "main")
	git("config", "user.email", "drift@example.com")
	git("config", "user.name", "Drift Test")

	// c1: catalog only (internal/mcp/catalog/).
	write("internal/mcp/catalog/tools.go", "catalog\n")
	c1 := commit("catalog only")

	// c2: plugin only (live .opencode copy).
	write(".opencode/plugins/kern.ts", "plugin\n")
	c2 := commit("plugin only")

	// c3: docs only + an unrelated file (README must NOT map to any surface).
	write("docs/mcp/tool-contracts.md", "contracts\n")
	write("README.md", "readme\n")
	c3 := commit("docs only")

	// c4: all three surfaces together (embedded plugin asset + tool-catalog.md).
	write("internal/mcp/catalog/catalog.go", "catalog2\n")
	write("internal/setup/assets/plugin/kern.ts", "plugin2\n")
	write("docs/tool-catalog.md", "catalog doc\n")
	c4 := commit("all surfaces")

	// c5: rename inside the catalog (--name-status emits R100 old new); the
	// new path must still map to the catalog surface.
	git("mv", "internal/mcp/catalog/catalog.go", "internal/mcp/catalog/manifest.go")
	c5 := commit("rename catalog file")

	records, err := SurfaceTouchesFromGit(root, 50)
	if err != nil {
		t.Fatalf("SurfaceTouchesFromGit: %v", err)
	}
	if len(records) != 5 {
		t.Fatalf("records = %d, want 5", len(records))
	}
	// Deterministic ordering: sorted by commit hash.
	for i := 1; i < len(records); i++ {
		if records[i].Commit < records[i-1].Commit {
			t.Errorf("records not sorted by commit hash: %s then %s", records[i-1].Commit, records[i].Commit)
		}
	}
	byHash := map[string]domain.SurfaceTouchRecord{}
	for _, r := range records {
		byHash[r.Commit] = r
		if r.At.IsZero() {
			t.Errorf("commit %s has zero timestamp", r.Commit)
		}
	}
	check := func(hash, label string, wantCat, wantPlug, wantDocs bool) {
		t.Helper()
		r, ok := byHash[hash]
		if !ok {
			t.Errorf("commit %s (%s) not parsed", hash, label)
			return
		}
		if r.CatalogTouched != wantCat || r.PluginTouched != wantPlug || r.DocsTouched != wantDocs {
			t.Errorf("%s: touches = (cat=%v, plug=%v, docs=%v), want (cat=%v, plug=%v, docs=%v)",
				label, r.CatalogTouched, r.PluginTouched, r.DocsTouched, wantCat, wantPlug, wantDocs)
		}
	}
	check(c1, "catalog only", true, false, false)
	check(c2, "plugin only", false, true, false)
	check(c3, "docs only", false, false, true)
	check(c4, "all surfaces", true, true, true)
	check(c5, "rename in catalog", true, false, false)

	// maxCommits limits the scan to the newest commits.
	limited, err := SurfaceTouchesFromGit(root, 3)
	if err != nil {
		t.Fatalf("SurfaceTouchesFromGit(-n 3): %v", err)
	}
	if len(limited) != 3 {
		t.Fatalf("limited records = %d, want 3 (newest commits)", len(limited))
	}

	// Non-git directory: silent skip — empty slice, nil error.
	nonGit := t.TempDir()
	r2, err2 := SurfaceTouchesFromGit(nonGit, 50)
	if err2 != nil {
		t.Errorf("non-git dir returned error: %v, want nil (best-effort skip)", err2)
	}
	if len(r2) != 0 {
		t.Errorf("non-git dir records = %d, want 0", len(r2))
	}
}

// TestSurfaceDriftPatternsCatalogWithoutPluginInfers proves the core drift
// inference: three commits that touched the catalog without the plugin
// become exactly one INFERENCE pattern with the deterministic statement,
// "surface:" scope, count and provenance — the pre-flag the next verify/plan
// run reads before the parity tests fail.
func TestSurfaceDriftPatternsCatalogWithoutPluginInfers(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.SurfaceTouchRecord{
		{Commit: "a1111111111111111111111111111111111111111", CatalogTouched: true, At: base},
		{Commit: "b2222222222222222222222222222222222222222", CatalogTouched: true, At: base.Add(time.Hour)},
		{Commit: "c3333333333333333333333333333333333333333", CatalogTouched: true, At: base.Add(2 * time.Hour)},
	}
	patterns := SurfaceDriftPatterns(records, DefaultSurfaceDriftMinRisk)
	if len(patterns) != 1 {
		t.Fatalf("patterns = %d, want 1", len(patterns))
	}
	p := patterns[0]
	if p.Key != "surface:catalog-without-plugin" {
		t.Errorf("Key = %q, want surface:catalog-without-plugin", p.Key)
	}
	if p.ClaimType != domain.ClaimInference {
		t.Errorf("ClaimType = %q, want INFERENCE", p.ClaimType)
	}
	if p.Count != 3 {
		t.Errorf("Count = %d, want 3", p.Count)
	}
	want := "3 commits touched the catalog surface without the plugin — parity drift risk; run parity tests before merge"
	if p.Statement != want {
		t.Errorf("Statement = %q, want %q", p.Statement, want)
	}
	if len(p.Scopes) != 1 || p.Scopes[0] != "surface:catalog-without-plugin" {
		t.Errorf("Scopes = %v, want [surface:catalog-without-plugin]", p.Scopes)
	}
	if len(p.Provenance.Sources) != 3 {
		t.Errorf("Provenance.Sources = %v, want 3 deduped commit hashes", p.Provenance.Sources)
	}
	if !p.Provenance.Latest.Equal(base.Add(2 * time.Hour)) {
		t.Errorf("Provenance.Latest = %v, want newest event time", p.Provenance.Latest)
	}
	// Provenance sources are the deduped sorted commit hashes.
	if p.Provenance.Sources[0] != records[0].Commit {
		t.Errorf("Provenance.Sources[0] = %q, want %q (sorted)", p.Provenance.Sources[0], records[0].Commit)
	}
}

// TestSurfaceDriftPatternsBelowMinRiskNothing proves the minRisk guard: a
// kind with fewer than 3 risk events contributes nothing, and commits that
// keep all surfaces in sync are never risk events.
func TestSurfaceDriftPatternsBelowMinRiskNothing(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.SurfaceTouchRecord{
		// 2 catalog-without-plugin events: below threshold 3.
		{Commit: "a1111111111111111111111111111111111111111", CatalogTouched: true, At: base},
		{Commit: "b2222222222222222222222222222222222222222", CatalogTouched: true, At: base.Add(time.Hour)},
		// 1 plugin-without-docs event: below threshold 3.
		{Commit: "c3333333333333333333333333333333333333333", PluginTouched: true, At: base.Add(2 * time.Hour)},
		// Sync-clean commits: never risk events.
		{Commit: "d4444444444444444444444444444444444444444", CatalogTouched: true, PluginTouched: true, DocsTouched: true, At: base.Add(3 * time.Hour)},
	}
	if patterns := SurfaceDriftPatterns(records, DefaultSurfaceDriftMinRisk); len(patterns) != 0 {
		t.Fatalf("patterns = %d, want 0 (all kinds below threshold)", len(patterns))
	}
	// minRisk <= 0 behaves as 1: a single event then infers.
	if patterns := SurfaceDriftPatterns(records[:1], 0); len(patterns) != 1 {
		t.Fatalf("patterns with minRisk 0 = %d, want 1 (treated as 1)", len(patterns))
	}
}

// TestSurfaceDriftPatternsDeterministic proves both kinds infer at
// threshold and that input order never affects the result: shuffled input
// yields identical patterns, ordered deterministically by kind then
// statement.
func TestSurfaceDriftPatternsDeterministic(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.SurfaceTouchRecord{
		{Commit: "a1111111111111111111111111111111111111111", CatalogTouched: true, At: base},
		{Commit: "b2222222222222222222222222222222222222222", PluginTouched: true, At: base.Add(time.Hour)},
		{Commit: "c3333333333333333333333333333333333333333", CatalogTouched: true, At: base.Add(2 * time.Hour)},
		// catalog+plugin without docs: catalog-without-plugin is NOT a risk
		// (plugin touched), but plugin-without-docs IS a risk (docs missing).
		{Commit: "d4444444444444444444444444444444444444444", CatalogTouched: true, PluginTouched: true, At: base.Add(3 * time.Hour)},
		{Commit: "e5555555555555555555555555555555555555555", PluginTouched: true, At: base.Add(4 * time.Hour)},
		{Commit: "f6666666666666666666666666666666666666666", CatalogTouched: true, At: base.Add(5 * time.Hour)},
	}
	want := SurfaceDriftPatterns(records, DefaultSurfaceDriftMinRisk)
	if len(want) != 2 {
		t.Fatalf("patterns = %d, want 2 (both kinds at threshold 3)", len(want))
	}
	if want[0].Key != "surface:catalog-without-plugin" || want[1].Key != "surface:plugin-without-docs" {
		t.Errorf("pattern order = [%s, %s], want catalog-without-plugin then plugin-without-docs",
			want[0].Key, want[1].Key)
	}
	wantPlug := "3 commits touched the plugin surface without the docs — parity drift risk; run parity tests before merge"
	if want[1].Statement != wantPlug {
		t.Errorf("plugin statement = %q, want %q", want[1].Statement, wantPlug)
	}

	shuffled := []domain.SurfaceTouchRecord{
		records[5], records[2], records[0], records[4], records[1], records[3],
	}
	got := SurfaceDriftPatterns(shuffled, DefaultSurfaceDriftMinRisk)
	if len(got) != len(want) {
		t.Fatalf("shuffled patterns = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Key != want[i].Key || got[i].Statement != want[i].Statement || got[i].Count != want[i].Count {
			t.Errorf("pattern %d differs after shuffle: got %+v, want %+v", i, got[i], want[i])
		}
		if len(got[i].Provenance.Sources) != len(want[i].Provenance.Sources) {
			t.Errorf("pattern %d provenance differs after shuffle", i)
		}
	}
}

// TestRecordSurfaceDriftWritesInference proves the full learning pass: three
// catalog-without-plugin commits write exactly one INFERENCE typed-claim
// memory through the learning path (deterministic statement, "surface:..."
// scope), and a second identical batch dedupes by commit hash — the log does
// not grow and the memory stays idempotent (upsert).
func TestRecordSurfaceDriftWritesInference(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.SurfaceTouchRecord{
		{Commit: "a1111111111111111111111111111111111111111", CatalogTouched: true, At: base},
		{Commit: "b2222222222222222222222222222222222222222", CatalogTouched: true, At: base.Add(time.Hour)},
		{Commit: "c3333333333333333333333333333333333333333", CatalogTouched: true, At: base.Add(2 * time.Hour)},
	}
	root := t.TempDir()
	mem := memory.NewMemoryStore(root)

	n, err := RecordSurfaceDrift(records, mem, DefaultSurfaceDriftMinRisk)
	if err != nil {
		t.Fatalf("RecordSurfaceDrift: %v", err)
	}
	if n != 1 {
		t.Fatalf("written = %d, want 1", n)
	}
	mems, err := mem.List(domain.MemoryConstraint)
	if err != nil {
		t.Fatalf("memory List: %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("memories = %d, want 1", len(mems))
	}
	m := mems[0]
	if m.ClaimType != domain.ClaimInference {
		t.Errorf("ClaimType = %q, want INFERENCE", m.ClaimType)
	}
	if m.Scope != "surface:catalog-without-plugin" {
		t.Errorf("Scope = %q, want surface:catalog-without-plugin", m.Scope)
	}
	if !strings.Contains(m.Content, "3 commits touched the catalog surface without the plugin") {
		t.Errorf("Content = %q, want the drift-risk statement", m.Content)
	}
	if logLen := len(loadSurfaceDriftLog(root)); logLen != 3 {
		t.Fatalf("surface drift log = %d entries, want 3", logLen)
	}

	// Second identical batch: dedupe by commit hash — no log growth, and the
	// same scope is upserted so memory stays at one entry.
	n2, err := RecordSurfaceDrift(records, mem, DefaultSurfaceDriftMinRisk)
	if err != nil {
		t.Fatalf("RecordSurfaceDrift (2nd): %v", err)
	}
	if n2 != 1 {
		t.Fatalf("written (2nd) = %d, want 1 (upsert)", n2)
	}
	if logLen := len(loadSurfaceDriftLog(root)); logLen != 3 {
		t.Errorf("surface drift log after repeat = %d entries, want 3 (dedupe by commit)", logLen)
	}
	mems, err = mem.List(domain.MemoryConstraint)
	if err != nil {
		t.Fatalf("memory List (2nd): %v", err)
	}
	if len(mems) != 1 {
		t.Errorf("memories (2nd) = %d, want 1 (idempotent upsert, no duplicate)", len(mems))
	}

	// A new commit appended grows the log, and the accumulated history still
	// yields the same single inference.
	records2 := append([]domain.SurfaceTouchRecord{
		{Commit: "d4444444444444444444444444444444444444444", CatalogTouched: true, At: base.Add(3 * time.Hour)},
	}, records...)
	n3, err := RecordSurfaceDrift(records2, mem, DefaultSurfaceDriftMinRisk)
	if err != nil {
		t.Fatalf("RecordSurfaceDrift (new commit): %v", err)
	}
	if n3 != 1 {
		t.Fatalf("written (new commit) = %d, want 1", n3)
	}
	if logLen := len(loadSurfaceDriftLog(root)); logLen != 4 {
		t.Errorf("surface drift log after new commit = %d entries, want 4", logLen)
	}
}

// TestRecordSurfaceDriftNilGuard proves a nil memory store is a no-op that
// never panics (0, nil) — unwired paths keep their zero behavior change.
func TestRecordSurfaceDriftNilGuard(t *testing.T) {
	records := []domain.SurfaceTouchRecord{
		{Commit: "a1111111111111111111111111111111111111111", CatalogTouched: true, At: time.Now()},
	}
	n, err := RecordSurfaceDrift(records, nil, DefaultSurfaceDriftMinRisk)
	if err != nil {
		t.Fatalf("nil mem returned error: %v", err)
	}
	if n != 0 {
		t.Fatalf("written = %d, want 0", n)
	}
}
