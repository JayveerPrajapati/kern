package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestVerdictOrderInvariance locks in the allow-dominates semantics: for the
// same (from, to) pair the verdict must be identical regardless of rule slice
// order. forbid-then-allow and allow-then-forbid must both resolve to ALLOW.
func TestVerdictOrderInvariance(t *testing.T) {
	forbidThenAllow := []BoundaryRule{
		{From: "web", To: "db", Action: "forbid"},
		{From: "web", To: "db", Action: "allow"},
	}
	allowThenForbid := []BoundaryRule{
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
	rules := []BoundaryRule{{From: "web", To: "db", Action: "forbid"}}
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
	rules := []BoundaryRule{{From: "web", To: "db", Action: "forbid"}}
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
	b := &Boundaries{Rules: []BoundaryRule{}}
	violations, skipped := CheckBoundariesPrecise(ix, b, []string{"web/handler.go"}, false)
	if len(violations) != 0 {
		t.Fatalf("empty rules must yield no violations, got %+v", violations)
	}
	if len(skipped) != 0 {
		t.Errorf("explicit empty rules must yield no skipped entries, got %+v", skipped)
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
	b := &Boundaries{Rules: []BoundaryRule{{From: "web", To: "db", Action: "forbid"}}}
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
	b := &Boundaries{Rules: []BoundaryRule{{From: "web", To: "db", Action: "forbid"}}}
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
	b := &Boundaries{Rules: []BoundaryRule{{From: "web", To: "db", Action: "forbid"}}}
	_, skipped := CheckBoundariesPrecise(ix, b, []string{"web/handler.go"}, false)
	for k := range skipped {
		if strings.HasPrefix(k, "imports-by-file-missing:") {
			t.Errorf("unexpected skip for a package with no imports: %q", k)
		}
	}
}
