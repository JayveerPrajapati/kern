package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestVerdictOrderInvariance locks in the allow-dominates semantics: for the
// same (from, to) pair the verdict must be identical regardless of rule slice
// order. forbid-then-allow and allow-then-forbid must both resolve to ALLOW.
func TestVerdictOrderInvariance(t *testing.T) {
	forbidThenAllow := []domain.BoundaryRule{
		{From: "web", To: "db", Action: "forbid"},
		{From: "web", To: "db", Action: "allow"},
	}
	allowThenForbid := []domain.BoundaryRule{
		{From: "web", To: "db", Action: "allow"},
		{From: "web", To: "db", Action: "forbid"},
	}
	if got := verdict(forbidThenAllow, "web", "db"); got != nil {
		t.Errorf("forbid-then-allow: expected ALLOW (nil), got %+v", got)
	}
	if got := verdict(allowThenForbid, "web", "db"); got != nil {
		t.Errorf("allow-then-forbid: expected ALLOW (nil), got %+v", got)
	}
}

// TestVerdictForbidWhenNoAllow verifies a lone forbid rule rejects the pair.
func TestVerdictForbidWhenNoAllow(t *testing.T) {
	rules := []domain.BoundaryRule{{From: "web", To: "db", Action: "forbid"}}
	got := verdict(rules, "web", "db")
	if got == nil {
		t.Fatal("lone forbid rule: expected FORBID, got nil (permitted)")
	}
	if got.From != "web" || got.To != "db" || got.Action != "forbid" {
		t.Errorf("wrong rule returned: %+v", got)
	}
}

// TestVerdictDefaultPermit verifies unconfigured (from, to) pairs remain
// permitted (default-permit), unchanged by the fix.
func TestVerdictDefaultPermit(t *testing.T) {
	rules := []domain.BoundaryRule{{From: "web", To: "db", Action: "forbid"}}
	if got := verdict(rules, "api", "db"); got != nil {
		t.Errorf("unconfigured pair should default to permitted, got %+v", got)
	}
}

// TestLoadBoundariesMissingFile: an absent .kern/boundaries.json is the
// acceptable zero-config state — nil error AND nil ruleset, never a hard
// failure. The absence is surfaced by a warning log here and as a
// "boundaries-not-configured" skip by CheckBoundariesPrecise, so the check
// layer never turns it into a silent PASS.
func TestLoadBoundariesMissingFile(t *testing.T) {
	dir := writeTree(t, map[string]string{})
	b, err := LoadBoundaries(dir)
	if err != nil {
		t.Fatalf("missing boundaries file must not be an error, got %v", err)
	}
	if b != nil {
		t.Errorf("missing boundaries file should yield a nil ruleset, got %+v", b)
	}
}

// TestLoadBoundariesMalformedFile: a present but invalid boundaries.json must
// fail closed with an error — never silently permit everything.
func TestLoadBoundariesMalformedFile(t *testing.T) {
	dir := writeTree(t, map[string]string{
		".kern/boundaries.json": `{"rules": [`,
	})
	if b, err := LoadBoundaries(dir); err == nil {
		t.Fatalf("malformed boundaries file must return an error, got ruleset %+v", b)
	}
}

// TestLoadBoundariesWellFormed: a valid file loads its rules without error.
func TestLoadBoundariesWellFormed(t *testing.T) {
	dir := writeTree(t, map[string]string{
		".kern/boundaries.json": `{"description":"d","rules":[{"from":"web","to":"db","action":"forbid"}]}`,
	})
	b, err := LoadBoundaries(dir)
	if err != nil {
		t.Fatalf("well-formed boundaries file must load: %v", err)
	}
	if b == nil || len(b.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %+v", b)
	}
	if b.Rules[0].Action != "forbid" || b.Rules[0].From != "web" || b.Rules[0].To != "db" {
		t.Errorf("rule decoded incorrectly: %+v", b.Rules[0])
	}
}

// TestLoadBoundariesEmptyRules: an explicitly empty rule set ("rules": []) is
// valid configuration — no error, nothing to enforce.
func TestLoadBoundariesEmptyRules(t *testing.T) {
	dir := writeTree(t, map[string]string{
		".kern/boundaries.json": `{"rules": []}`,
	})
	b, err := LoadBoundaries(dir)
	if err != nil {
		t.Fatalf("empty rules file must not be an error, got %v", err)
	}
	if b == nil || len(b.Rules) != 0 {
		t.Fatalf("expected empty ruleset, got %+v", b)
	}
}

// TestCheckBoundariesPrecise_MissingBoundariesWarns: a nil boundaries ruleset
// (no .kern/boundaries.json) with a non-empty check scope must not pass
// silently. The gap is surfaced as a skipped entry keyed
// "boundaries-not-configured" (a warning), never as a fabricated violation.
func TestCheckBoundariesPrecise_MissingBoundariesWarns(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"web/handler.go": `package web

func Handler() {}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	violations, skipped := CheckBoundariesPrecise(ix, nil, []string{"web/handler.go"}, false)
	if len(violations) != 0 {
		t.Fatalf("missing boundaries must not fabricate violations, got %+v", violations)
	}
	key := "boundaries-not-configured"
	if got := skipped[key]; got != 1 {
		t.Errorf("skipped[%q] = %d; want 1 (missing boundaries must be surfaced, not silent)", key, got)
	}
}

// TestCheckBoundariesPrecise_EmptyFilesNoWarn: an empty check scope is a clean
// skip — nothing to check, nothing to warn about — even when the boundaries
// ruleset is nil.
func TestCheckBoundariesPrecise_EmptyFilesNoWarn(t *testing.T) {
	violations, skipped := CheckBoundariesPrecise(nil, nil, nil, false)
	if len(violations) != 0 {
		t.Fatalf("empty scope must yield no violations, got %+v", violations)
	}
	if len(skipped) != 0 {
		t.Errorf("empty scope must yield no skipped entries, got %+v", skipped)
	}
}

// TestCheckBoundariesPrecise_ExplicitEmptyRulesNoWarn: an explicitly empty rule
// list ("rules": []) in a present file is deliberate user intent — nothing to
// enforce — so it is a clean skip, not a warn.
func TestCheckBoundariesPrecise_ExplicitEmptyRulesNoWarn(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"web/handler.go": `package web

func Handler() {}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := &Boundaries{Rules: []domain.BoundaryRule{}}
	violations, skipped := CheckBoundariesPrecise(ix, b, []string{"web/handler.go"}, false)
	if len(violations) != 0 {
		t.Fatalf("empty rules must yield no violations, got %+v", violations)
	}
	if len(skipped) != 0 {
		t.Errorf("explicit empty rules must yield no skipped entries, got %+v", skipped)
	}
}

// TestCheckBoundariesPrecise_SkipsTestdataFixtures: files under a testdata/
// directory are fixture/demo code, not production source — architecture
// enforcement must not flag their crossings (the indexer does not exclude
// them, so the skip must live in the check). A production file with the same
// crossing must still be flagged.
func TestCheckBoundariesPrecise_SkipsTestdataFixtures(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module m\n\ngo 1.20\n",
		"web/web.go": `package web

func W() {}
`,
		"api/api.go": `package api

import "m/web"

func A() { web.W() }
`,
		"testdata/fixture/main.go": `package main

import "m/web"

func main() { web.W() }
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := &Boundaries{Rules: []domain.BoundaryRule{
		{From: "api", To: "web", Action: "forbid"},
		{From: "fixture", To: "web", Action: "forbid"},
	}}
	violations, _ := CheckBoundariesPrecise(ix, b, []string{
		"api/api.go", "web/web.go", "testdata/fixture/main.go",
	}, false)
	if len(violations) != 1 {
		t.Fatalf("expected exactly the api->web violation (fixture skipped), got %+v", violations)
	}
	if violations[0].CallerFile != "api/api.go" {
		t.Errorf("expected the api/api.go crossing to be the only violation, got %+v", violations)
	}
}

// TestCheckBoundariesPrecise_BareNameCollision: the index merges call edges
// for same-named package-level funcs across packages (bare-name keys — every
// package has a "New"). A same-named function in a DIFFERENT package must not
// inherit those edges: only the file that owns the name may be held to them,
// otherwise "svc" would be falsely flagged for web.New's loadTemplates call.
func TestCheckBoundariesPrecise_BareNameCollision(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module m\n\ngo 1.20\n",
		"web/web.go": `package web

type App struct{}

func New() *App {
	a := &App{}
	a.loadTemplates()
	return a
}

func (a *App) loadTemplates() {}
`,
		"svc/svc.go": `package svc

type S struct{}

func New() *S { return &S{} }
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := &Boundaries{Rules: []domain.BoundaryRule{{From: "svc", To: "web", Action: "forbid"}}}
	violations, _ := CheckBoundariesPrecise(ix, b, []string{"web/web.go", "svc/svc.go"}, false)
	if len(violations) != 0 {
		t.Fatalf("svc.New must not inherit web.New's call edges (bare-name collision), got %+v", violations)
	}
}

// TestCheckBoundariesPrecise_CalleeBareNameCollision: two files in different
// layers define the same bare function name, and a method in the service file
// calls its OWN local definition. The callee must not be attributed to the
// api file's same-named def — that fabrication would reverse the real
// dependency (dogfood finding: TradingApp nse_service.py -> api/stocks.py
// "get_quote", where api actually CALLS the service).
func TestCheckBoundariesPrecise_CalleeBareNameCollision(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"api/stocks.py": `async def get_quote(symbol: str):
    return {"symbol": symbol}
`,
		"services/nse_service.py": `async def get_quote(symbol: str):
    return {"symbol": symbol}


class Scanner:
    async def scan(self, symbol: str):
        quote = await get_quote(symbol)
        return quote
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := &Boundaries{Rules: []domain.BoundaryRule{{From: "services", To: "api", Action: "forbid"}}}
	violations, _ := CheckBoundariesPrecise(ix, b, []string{"api/stocks.py", "services/nse_service.py"}, false)
	if len(violations) != 0 {
		t.Fatalf("services.Scanner.scan must resolve get_quote to its own file, not api/stocks.py (bare-name callee collision), got %+v", violations)
	}
}

// TestInferBoundariesJavaApiModulesNotL1: Maven "*-api" modules are contract
// interfaces that *-service modules implement (service -> api is the CORRECT
// direction). For Java projects, "api" must NOT be treated as a presentation
// layer, or every Maven monorepo gets mass false violations (dogfood finding:
// 197 spurious service->api findings on slice-adaptors).
func TestInferBoundariesJavaApiModulesNotL1(t *testing.T) {
	ix := &index.Index{
		FileHashes: map[string]string{
			"x-service/src/main/java/com/x/Service.java":  "h1",
			"x-api/src/main/java/com/x/api/Contract.java": "h2",
			// JAX-RS interfaces inside the contract module must also not be
			// L1 — the "rest" keyword alone would re-flag them (the second
			// dogfood finding on slice-adaptors).
			"x-api/src/main/java/com/x/api/rest/ContractResource.java": "h3",
		},
	}
	b := InferBoundaries(ix)
	for _, r := range b.Rules {
		if strings.Contains(r.From, "x-api") || strings.Contains(r.To, "x-api") {
			t.Fatalf("java project must not classify contract-module dirs as any layer, got rule %s -> %s", r.From, r.To)
		}
	}
}

// TestInferBoundariesNonJavaApiIsL1: for Go/Python/JS, an "api" directory is
// the HTTP surface (presentation). The service -> api forbid must exist so
// enforcement still catches genuine layering breaks (e.g. TradingApp's
// services/nse_service.py -> api/stocks.py).
func TestInferBoundariesNonJavaApiIsL1(t *testing.T) {
	ix := &index.Index{
		FileHashes: map[string]string{
			"app/api/stocks.py":     "h1",
			"app/services/quote.py": "h2",
		},
	}
	b := InferBoundaries(ix)
	found := false
	for _, r := range b.Rules {
		if r.Action == "forbid" && strings.Contains(r.From, "service") && strings.Contains(r.To, "api") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("non-java project must forbid service -> api, got rules: %+v", b.Rules)
	}
}

// TestInferBoundariesIntraModuleSubpackagesNotLayered: a dir's layer comes
// from its OWN name, not ancestor segments. "*-service/.../dao/impl" must not
// be classified L2 (service) because an ancestor module is named *-service —
// that fabricated intra-module dao->service violations (dogfood finding on
// slice-adaptors).
func TestInferBoundariesIntraModuleSubpackagesNotLayered(t *testing.T) {
	ix := &index.Index{
		FileHashes: map[string]string{
			"sub/sub-service/src/main/java/com/x/dao/impl/Impl.java": "h1",
			"sub/sub-service/src/main/java/com/x/dao/Base.java":      "h2",
		},
	}
	b := InferBoundaries(ix)
	for _, r := range b.Rules {
		if strings.Contains(r.From, "dao/impl") || strings.Contains(r.To, "dao/impl") {
			t.Fatalf("dao/impl subpackage must not be a layer, got rule %s -> %s", r.From, r.To)
		}
		// Nested ancestor-descendant layer pairs (dao dir inside its own
		// *-service module) must not produce rules — they fabricate
		// intra-module dao->service findings.
		if strings.Contains(r.From, "/dao") && strings.Contains(r.To, "sub-service") {
			t.Fatalf("nested same-module layer pair must not produce a rule, got %s -> %s", r.From, r.To)
		}
		if strings.Contains(r.To, "/dao") && strings.Contains(r.From, "sub-service") {
			t.Fatalf("nested same-module layer pair must not produce a rule, got %s -> %s", r.From, r.To)
		}
	}
}

// TestImportCheckWarnsOnMissingImportsByFile: an index that carries
// package-level imports (Pkgs[dir].Imports) but lacks per-file attribution
// (ImportsByFile is nil — indexes written by older kern) must not pass the
// import-level boundary check silently. The gap is surfaced as a
// skipped-precision warning keyed "imports-by-file-missing:<file>", never as a
// fabricated violation (package-aggregated fallback was the false-positive bug)
// and never as a silent pass.
func TestImportCheckWarnsOnMissingImportsByFile(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"web/handler.go": `package web

import "fmt"

func Handler() {
	fmt.Println("hi")
}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: Build populated package-level imports for the directory.
	if pkg := ix.Pkgs["web"]; pkg == nil || len(pkg.Imports) == 0 {
		t.Fatalf("expected Pkgs[web] to carry imports, got %+v", pkg)
	}
	// Simulate an older index: package data present, per-file attribution gone.
	ix.ImportsByFile = nil
	b := &Boundaries{Rules: []domain.BoundaryRule{{From: "web", To: "db", Action: "forbid"}}}
	files := []string{"web/handler.go"}
	violations, skipped := CheckBoundariesPrecise(ix, b, files, false)
	if len(violations) != 0 {
		t.Fatalf("missing per-file data must not fabricate violations, got %+v", violations)
	}
	key := "imports-by-file-missing:web/handler.go"
	if got := skipped[key]; got != 1 {
		t.Errorf("skipped[%q] = %d; want 1 (missing imports_by_file must be surfaced, not silent)", key, got)
	}
}

// TestImportCheckNoWarnWhenIndexHasImportsByFile: a current index with
// ImportsByFile populated must not emit the missing-data warning — the normal,
// fully-covered path.
func TestImportCheckNoWarnWhenIndexHasImportsByFile(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"web/handler.go": `package web

import "fmt"

func Handler() {
	fmt.Println("hi")
}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ix.ImportsByFile["web/handler.go"]; !ok {
		t.Fatal("expected ImportsByFile populated by Build")
	}
	b := &Boundaries{Rules: []domain.BoundaryRule{{From: "web", To: "db", Action: "forbid"}}}
	_, skipped := CheckBoundariesPrecise(ix, b, []string{"web/handler.go"}, false)
	for k := range skipped {
		if strings.HasPrefix(k, "imports-by-file-missing:") {
			t.Errorf("unexpected imports-by-file-missing skip on a current index: %q", k)
		}
	}
}

// TestImportCheckNoWarnWhenPackageHasNoImports: a file in a package with no
// imports at all has nothing to check, so even an index without per-file data
// must not warn — the skip is only warranted when package-level data shows the
// file's package DOES import something.
func TestImportCheckNoWarnWhenPackageHasNoImports(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"web/handler.go": `package web

func Handler() {}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	ix.ImportsByFile = nil // old index, but the package has no imports either
	b := &Boundaries{Rules: []domain.BoundaryRule{{From: "web", To: "db", Action: "forbid"}}}
	_, skipped := CheckBoundariesPrecise(ix, b, []string{"web/handler.go"}, false)
	for k := range skipped {
		if strings.HasPrefix(k, "imports-by-file-missing:") {
			t.Errorf("unexpected skip for a package with no imports: %q", k)
		}
	}
}

func TestInferBoundaries(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"controller/user.go": `package controller
func HandleUser() {}
`,
		"service/user.go": `package service
func GetUser() {}
`,
		"repository/user.go": `package repository
func FindUser() {}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}

	b := InferBoundaries(ix)
	if b == nil {
		t.Fatal("expected inferred boundaries, got nil")
	}
	if len(b.Rules) == 0 {
		t.Fatal("expected inferred rules, got 0")
	}

	// Verify that repository -> controller or service -> controller is forbidden
	foundRepoRule := false
	for _, r := range b.Rules {
		if strings.Contains(r.From, "repo") && strings.Contains(r.To, "controller") && r.Action == "forbid" {
			foundRepoRule = true
		}
	}
	if !foundRepoRule {
		t.Errorf("expected inferred rule forbidding repository -> controller, got rules: %+v", b.Rules)
	}
}
