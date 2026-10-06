package brief

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

func write(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildIncludesSections(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "main.go", `package main

func helper() int { return 1 }

func main() {
	_ = helper()
}
`)
	if err := Warm(dir); err != nil {
		t.Fatal(err)
	}
	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# kern buddy briefing",
		"## Project map",
		"## Index",
		"Languages: go",
		"## kern savings",
		"## How to use kern",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("briefing missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "{{") {
		t.Fatalf("unfilled placeholders:\n%s", out)
	}
}

func TestBuildEntryPoints(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "app.go", `package main

func init() {}

func Run() {}
`)
	if err := Warm(dir); err != nil {
		t.Fatal(err)
	}
	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Entry points:") || !strings.Contains(out, "Run") {
		t.Fatalf("entry points missing:\n%s", out)
	}
}

func TestBuildColdSkipsIndexSections(t *testing.T) {
	// A cold cache must never trigger a synchronous full index build; the
	// digest degrades to a hint instead (the kern_buddy ~85s cold-start fix).
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "main.go", `package main

func main() {}
`)
	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not built yet") {
		t.Fatalf("expected cold-cache hint, got:\n%s", out)
	}
	if strings.Contains(out, "Languages:") || strings.Contains(out, "Symbols:") {
		t.Fatalf("cold build must not render index sections:\n%s", out)
	}
	// The warm path turns the next build into the full digest.
	if err := Warm(dir); err != nil {
		t.Fatal(err)
	}
	out, err = Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Languages: go") || !strings.Contains(out, "Symbols:") {
		t.Fatalf("warm build must render index sections:\n%s", out)
	}
}

// TestBuildDefaultDigestOmitsPerFileMap pins the V8 contract: the default
// `kern buddy` output is a CONCISE digest — useful sections first, then a
// compact project overview (file counts + entry points) with NO per-file
// listing. The full per-file map renders only on request (Options.Map, the
// `kern buddy --map` flag), and the digest sections survive it.
func TestBuildDefaultDigestOmitsPerFileMap(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "go.mod", "module demo\n\ngo 1.22\n")
	for i := 0; i < 400; i++ {
		write(t, dir, fmt.Sprintf("f%03d.go",
			i), fmt.Sprintf("package main\n\n// file %d with unique filler words for the summary.\n\nfunc F%d() int { return %d }\n", i, i, i))
	}
	if err := Warm(dir); err != nil {
		t.Fatal(err)
	}
	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The useful digest sections render FIRST and survive.
	for _, want := range []string{"## Index", "Symbols:", "## Project map", "## How to use kern"} {
		if !strings.Contains(out, want) {
			t.Fatalf("digest missing %q:\n%s", want, out)
		}
	}
	// The trailing section is the COMPACT overview: file counts, no
	// per-file listing. (go.mod + 400 .go files = 401.)
	if !strings.Contains(out, "401 files") {
		t.Fatalf("expected compact file count, got:\n%s", out)
	}
	if strings.Contains(out, "f000.go") {
		t.Fatalf("default digest must not contain per-file map entries:\n%s", out)
	}
	// The short digest stays comfortably inside the MCP output sandbox
	// (24KB default) — target well under 10KB.
	if len(out) > 10<<10 {
		t.Fatalf("default digest %d bytes exceeds the short-digest target", len(out))
	}

	// The full per-file map is available on request (--map).
	full, err := BuildWithOptions(dir, Options{Map: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full, "f000.go") || !strings.Contains(full, "func F0") {
		t.Fatalf("--map output must contain the full per-file map:\n%s", full)
	}
	if !strings.Contains(full, "## Index") || !strings.Contains(full, "Symbols:") {
		t.Fatalf("digest sections must survive the full map:\n%s", full)
	}
}

func TestWarmBuildsAndPersists(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "go.mod", "module demo\n\ngo 1.22\n")
	write(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	if err := Warm(dir); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Load(dir)
	if err != nil {
		t.Fatalf("Warm must persist the index: %v", err)
	}
	if ix.Stale() {
		t.Fatal("Warm must produce a fresh index")
	}
	if len(ix.Symbols) == 0 {
		t.Fatal("expected symbols in warmed index")
	}
}

func TestBuildNoGo(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "notes.txt", "just notes\n")
	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "# kern buddy briefing") {
		t.Fatalf("briefing header missing:\n%s", out)
	}
}

// TestBuildProjectMemoryReadsTypedStore pins P2-14: the buddy digest's
// "Project memory" section reads the TYPED store — the one the learn path
// writes (loop learn, learning extractor). Previously the digest read only
// the v1 lesson store, which holds just auto captures for a repo, so
// loop-learned lessons never surfaced. Auto captures must stay in "Recent
// session activity", never in "Project memory".
func TestBuildProjectMemoryReadsTypedStore(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	if err := Warm(dir); err != nil {
		t.Fatal(err)
	}

	// A real loop-learned lesson lands in the typed store (Source "loop").
	store := memory.NewMemoryStore(dir)
	if _, err := store.Add(domain.Memory{
		Type:    domain.MemoryLesson,
		Content: "Redis keys must contain the tenant id",
		Source:  "loop",
		Scope:   "project",
	}); err != nil {
		t.Fatal(err)
	}

	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pm := section(out, "## Project memory (from past sessions)"); !strings.Contains(pm, "Redis keys must contain the tenant id") {
		t.Fatalf("typed-store lesson missing from project memory:\n%s", out)
	}

	// Auto captures (v1 store) render only as Recent session activity and
	// never leak into Project memory.
	if err := memory.AddAuto(dir, "User: give me a summary of dispatch"); err != nil {
		t.Fatal(err)
	}
	out, err = Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pm := section(out, "## Project memory (from past sessions)"); strings.Contains(pm, "give me a summary of dispatch") {
		t.Fatalf("auto capture leaked into project memory:\n%s", pm)
	}
	if rsa := section(out, "## Recent session activity (auto captures, newest first)"); !strings.Contains(rsa, "give me a summary of dispatch") {
		t.Fatalf("auto capture missing from recent session activity:\n%s", out)
	}
}

// TestExplicitUserLessonSurfacesInProjectMemory pins the P2-14 follow-up for
// the shared explicit write path: a lesson recorded via memory.AddExplicit
// (the body behind `kern memory add` / kern_memory action=add) must render
// in buddy's "Project memory", while an auto capture (hook AddAuto) must not
// — it stays in "Recent session activity".
func TestExplicitUserLessonSurfacesInProjectMemory(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	if err := Warm(dir); err != nil {
		t.Fatal(err)
	}

	if err := memory.AddExplicit(dir, "Tenant ids must always be namespaced in Redis keys"); err != nil {
		t.Fatal(err)
	}
	if err := memory.AddAuto(dir, "User: give me a summary of dispatch"); err != nil {
		t.Fatal(err)
	}

	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	pm := section(out, "## Project memory (from past sessions)")
	if !strings.Contains(pm, "Tenant ids must always be namespaced in Redis keys") {
		t.Fatalf("explicit user lesson missing from project memory:\n%s", out)
	}
	if strings.Contains(pm, "give me a summary of dispatch") {
		t.Fatalf("auto capture leaked into project memory:\n%s", pm)
	}
	if rsa := section(out, "## Recent session activity (auto captures, newest first)"); !strings.Contains(rsa, "give me a summary of dispatch") {
		t.Fatalf("auto capture missing from recent session activity:\n%s", out)
	}
}

// section returns the digest text between header and the next "## " header.
func section(out, header string) string {
	i := strings.Index(out, header)
	if i < 0 {
		return ""
	}
	rest := out[i+len(header):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// TestArchitectureCompactFallbackOverGate pins the big-repo digest fix: when
// the community-analysis gate is exceeded, the Architecture section must
// render the CHEAP compact fallback (package/subsystem count + top packages
// by symbol count) instead of the "(skipped …)" marker, keeping the
// `kern graph --html` pointer for the interactive explorer.
func TestArchitectureCompactFallbackOverGate(t *testing.T) {
	syms := make([]index.Symbol, 0, archGateSymbols+3)
	for i := 0; i < archGateSymbols; i++ {
		syms = append(syms, index.Symbol{Name: fmt.Sprintf("f%d", i), Kind: "func", Lang: "go", File: "internal/api/handlers.go"})
	}
	syms = append(syms,
		index.Symbol{Name: "main", Kind: "func", Lang: "go", File: "cmd/kern/main.go"},
		index.Symbol{Name: "run", Kind: "func", Lang: "go", File: "cmd/kern/main.go"},
		index.Symbol{Name: "NewServer", Kind: "func", Lang: "go", File: "internal/web/server.go"},
	)
	// One call edge so the section does not early-return on an empty graph.
	ix := &index.Index{
		Symbols: syms,
		Calls:   map[string][]index.CallEdge{"main": {index.CallEdge{Target: "helper", Confidence: index.ConfidenceHigh}}},
	}

	out := architectureSection(ix)
	if !strings.Contains(out, "## Architecture") {
		t.Fatalf("architecture section missing:\n%s", out)
	}
	if strings.Contains(out, "(skipped") {
		t.Fatalf("compact fallback must not render the skipped marker:\n%s", out)
	}
	for _, want := range []string{"internal/api", "cmd/kern", "internal/web", "kern graph --html", "packages/subsystems"} {
		if !strings.Contains(out, want) {
			t.Fatalf("compact fallback missing %q:\n%s", want, out)
		}
	}
	// Top packages are ordered by symbol count: internal/api (the gate-sized
	// package) must be listed before the smaller ones.
	if strings.Index(out, "internal/api") > strings.Index(out, "cmd/kern") {
		t.Fatalf("top packages not ordered by symbol count:\n%s", out)
	}
}

// TestGoPrimaryRepoNoJsRouterLabels pins the framework-endpoint mislabel
// fix: on a Go-primary repo the digest must NOT claim "js-router" for
// routes (the indexer's heuristic can label Go http-mux routes and fixtures
// mirroring them as a JS framework). The section renders generically with
// "http" labels. On a non-Go-primary repo the heuristic framework ids stay.
func TestGoPrimaryRepoNoJsRouterLabels(t *testing.T) {
	entry := index.Symbol{
		Kind: "entry", Name: "agentsHandler", Entry: true,
		Framework: "js-router", Route: "/v1/agents",
		Lang: "typescript", File: "internal/index/testfixture/fixtures.ts",
	}
	goSyms := make([]index.Symbol, 0, 50)
	for i := 0; i < 50; i++ {
		goSyms = append(goSyms, index.Symbol{Name: fmt.Sprintf("handler%d", i), Kind: "func", Lang: "go", File: "internal/mcp/handlers.go"})
	}
	ix := &index.Index{
		Symbols:    append(goSyms, entry),
		Calls:      map[string][]index.CallEdge{},
		FileHashes: map[string]string{},
	}
	out := indexSection(ix)
	if strings.Contains(out, "js-router") {
		t.Fatalf("Go-primary repo must not claim js-router:\n%s", out)
	}
	if !strings.Contains(out, "HTTP endpoints (handler → route):") {
		t.Fatalf("generic section header missing:\n%s", out)
	}
	found := false
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "/v1/agents") {
			found = true
			if !strings.Contains(ln, "http") || strings.Contains(ln, "js-router") {
				t.Fatalf("entry must be labeled http on a Go-primary repo, got %q", ln)
			}
		}
	}
	if !found {
		t.Fatalf("entry route missing from endpoint section:\n%s", out)
	}

	// A JS-primary repo keeps the heuristic framework id (it is plausible
	// there), so the fix is Go-primary-scoped, not a blanket relabel.
	jsSyms := make([]index.Symbol, 0, 50)
	for i := 0; i < 50; i++ {
		jsSyms = append(jsSyms, index.Symbol{Name: fmt.Sprintf("c%d", i), Kind: "func", Lang: "typescript", File: "src/app.ts"})
	}
	ixJS := &index.Index{
		Symbols:    append(jsSyms, entry),
		Calls:      map[string][]index.CallEdge{},
		FileHashes: map[string]string{},
	}
	outJS := indexSection(ixJS)
	foundJS := false
	for _, ln := range strings.Split(outJS, "\n") {
		if strings.Contains(ln, "/v1/agents") {
			foundJS = true
			if !strings.Contains(ln, "js-router") {
				t.Fatalf("JS-primary repo must keep the heuristic framework id, got %q", ln)
			}
		}
	}
	if !foundJS {
		t.Fatalf("JS-primary repo endpoint section missing the entry:\n%s", outJS)
	}
}

// TestProjectMemoryDedupesAndCaps pins the near-duplicate noise fix: a
// typed store holding many copies of the same lesson (per-call captures)
// renders each normalized text once (most recent occurrence), whitespace
// variants collapse into the same entry, and the section caps at
// projectMemoryMax with an overflow pointer.
func TestProjectMemoryDedupesAndCaps(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	if err := Warm(dir); err != nil {
		t.Fatal(err)
	}
	store := memory.NewMemoryStore(dir)

	base := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	add := func(i int, content string) {
		t.Helper()
		if _, err := store.Add(domain.Memory{
			Type:      domain.MemoryLesson,
			Content:   content,
			Source:    "loop",
			Scope:     "project",
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// 12 distinct lessons (older timestamps), then a whitespace variant and
	// 15 copies of the same near-duplicate lesson (newest timestamps) →
	// 13 distinct texts, capped at 10 newest-first with 3 overflow lines.
	for i := 0; i < 12; i++ {
		add(i, fmt.Sprintf("Lesson number %d about redis keys", i))
	}
	add(12, "Edited  /tmp/kern_test.sh") // whitespace variant — collapses
	for i := 13; i <= 27; i++ {
		add(i, "Edited /tmp/kern_test.sh")
	}

	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	pm := section(out, "## Project memory (from past sessions)")
	if pm == "" {
		t.Fatalf("project memory section missing:\n%s", out)
	}
	// 16 raw copies collapse to exactly one bullet (the most recent, whose
	// canonical text renders — the whitespace variant is deduped away).
	if got := strings.Count(pm, "Edited /tmp/kern_test.sh"); got != 1 {
		t.Fatalf("near-duplicate lesson rendered %d times, want 1:\n%s", got, pm)
	}
	if strings.Contains(pm, "Edited  /tmp/kern_test.sh") {
		t.Fatalf("whitespace variant must be deduped into the same entry:\n%s", pm)
	}
	// Distinct lessons render exactly once each — the newest 9 fit inside
	// the cap next to the edited entry; the 3 oldest are truncated.
	for i := 3; i < 12; i++ {
		want := fmt.Sprintf("Lesson number %d about redis keys", i)
		if strings.Count(pm, want) != 1 {
			t.Fatalf("lesson %q rendered != 1 time:\n%s", want, pm)
		}
	}
	for i := 0; i < 3; i++ {
		want := fmt.Sprintf("Lesson number %d about redis keys", i)
		if strings.Contains(pm, want) {
			t.Fatalf("oldest lesson %q must be truncated by the cap:\n%s", want, pm)
		}
	}
	// 13 distinct texts → 10 rendered + three overflow lines worth. The
	// overflow line itself starts with "- ", so count dated bullets only.
	if !strings.Contains(pm, "and 3 more") {
		t.Fatalf("overflow line missing (13 distinct, cap 10):\n%s", pm)
	}
	if got := strings.Count(pm, "\n- 20"); got != projectMemoryMax {
		t.Fatalf("rendered %d lesson bullets, want exactly %d:\n%s", got, projectMemoryMax, pm)
	}
}
